package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	deliveryengine "github.com/wricardo/drbrain/games/delivery/game/engine"
	teslaengine "github.com/wricardo/drbrain/games/tesla/game/engine"
	"github.com/wricardo/drbrain/platform/mapstore"
	"github.com/wricardo/drbrain/platform/pgdb"
)

// legacy imports are cursor-free one-shots: ids are file names and the source
// directories are never modified.
const importMapsHelp = `usage: drbrain import-maps --game=NAME --dir=PATH [--dry-run] [--replace] [--db=URL]

Import legacy custom maps (Tesla) or cities (Delivery) into platform_maps.
Rows are inserted ownerless and PUBLISHED; Tesla edit-token sidecars are
imported as hashes, never plaintext. Existing rows are skipped unless
--replace is given. Source files are never deleted or changed.

flags:
  --game=NAME   tesla (default) or delivery
  --dir=PATH    legacy custom directory (required); pass only custom maps,
                not a release/builtin directory
  --dry-run     run every file and database check, write nothing
  --replace     update existing rows (config, owner, status, token hash)
  --db=URL      override DRBRAIN_DATABASE_URL

cutover order:
  1. stop the serving process (the import is additive, but stopping avoids a
     second writer while operators inspect it)
  2. run with --dry-run and read the counts and any file errors
  3. re-run without --dry-run
  4. start the new build and verify the maps/cities in the UI
  5. keep the legacy directory and old tables until rollback is no longer
     needed; this command never deletes either

Tesla ids use letters, numbers, hyphen or underscore; Delivery ids are
[a-z0-9_-]{1,64}.
`

var (
	teslaImportID    = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	deliveryImportID = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
)

type importOptions struct {
	game    string
	dir     string
	dryRun  bool
	replace bool
}

type importCounts struct {
	imported int
	updated  int
	skipped  int
	failed   int
}

// legacyGame describes one importable game without leaking a type parameter
// into the command dispatch.
type legacyGame[T any] struct {
	name      string
	checkID   func(string) error
	decode    func([]byte) (T, error)
	validate  func(T) error
	tokenized bool
}

func teslaLegacyGame() legacyGame[*teslaengine.GameConfig] {
	return legacyGame[*teslaengine.GameConfig]{
		name: "tesla",
		checkID: func(id string) error {
			if !teslaImportID.MatchString(id) {
				return errors.New("must match ^[A-Za-z0-9_-]{1,64}$")
			}
			return nil
		},
		decode: func(raw []byte) (*teslaengine.GameConfig, error) {
			var cfg teslaengine.GameConfig
			if err := json.Unmarshal(raw, &cfg); err != nil {
				return nil, err
			}
			return &cfg, nil
		},
		validate:  teslaengine.ValidateGameConfigStructure,
		tokenized: true,
	}
}

func deliveryLegacyGame() legacyGame[*deliveryengine.CityConfig] {
	return legacyGame[*deliveryengine.CityConfig]{
		name: "delivery",
		checkID: func(id string) error {
			if !deliveryImportID.MatchString(id) {
				return errors.New("must match ^[a-z0-9_-]{1,64}$")
			}
			return nil
		},
		decode:   deliveryengine.DecodeCityConfig,
		validate: deliveryengine.ValidateCityConfigStructural,
	}
}

func runImportMaps(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("import-maps", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, importMapsHelp)
		fs.PrintDefaults()
	}
	game := fs.String("game", "tesla", "tesla or delivery")
	dir := fs.String("dir", "", "legacy custom map/city directory")
	dryRun := fs.Bool("dry-run", false, "validate and report without writing")
	replace := fs.Bool("replace", false, "update rows that already exist")
	db := fs.String("db", "", "database URL (default DRBRAIN_DATABASE_URL)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "drbrain import-maps: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	opts := importOptions{game: strings.TrimSpace(*game), dir: strings.TrimSpace(*dir), dryRun: *dryRun, replace: *replace}
	if opts.game != "tesla" && opts.game != "delivery" {
		fmt.Fprintf(stderr, "drbrain import-maps: --game must be tesla or delivery\n")
		return 2
	}
	if opts.dir == "" {
		fmt.Fprintf(stderr, "drbrain import-maps: --dir is required\n")
		return 2
	}
	databaseURL := strings.TrimSpace(*db)
	if databaseURL == "" {
		databaseURL = strings.TrimSpace(os.Getenv("DRBRAIN_DATABASE_URL"))
	}
	if databaseURL == "" {
		fmt.Fprintf(stderr, "drbrain import-maps: DRBRAIN_DATABASE_URL is empty; pass --db\n")
		return 2
	}

	pool, err := pgdb.Open(context.Background(), databaseURL)
	if err != nil {
		fmt.Fprintf(stderr, "drbrain import-maps: database: %v\n", err)
		return 1
	}
	defer pool.Close()

	var counts importCounts
	switch opts.game {
	case "tesla":
		counts, err = runImport(teslaLegacyGame(), pool, opts, stdout, stderr)
	case "delivery":
		counts, err = runImport(deliveryLegacyGame(), pool, opts, stdout, stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "drbrain import-maps: %v\n", err)
		return 1
	}
	mode := ""
	if opts.dryRun {
		mode = " (dry run: nothing written)"
	}
	fmt.Fprintf(stdout, "import-maps: imported=%d updated=%d skipped=%d failed=%d%s\n",
		counts.imported, counts.updated, counts.skipped, counts.failed, mode)
	if counts.failed > 0 {
		return 1
	}
	return 0
}

// runImport is the testable core: it validates every file, checks the database
// for existing rows, and applies the requested action. A dry run performs the
// reads and validations but performs no writes.
func runImport[T any](g legacyGame[T], pool *pgxpool.Pool, opts importOptions, stdout, stderr io.Writer) (importCounts, error) {
	var counts importCounts
	entries, err := os.ReadDir(opts.dir)
	if err != nil {
		return counts, fmt.Errorf("read %s: %w", opts.dir, err)
	}
	// The import only needs a builtin catalogue for its shadow guard; the
	// operator's directory is exactly the custom data being imported.
	builtinDir, err := os.MkdirTemp("", "drbrain-import-maps-*")
	if err != nil {
		return counts, fmt.Errorf("create scratch builtin dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(builtinDir) }()

	store, err := mapstore.Open(mapstore.Options[T]{
		BuiltinDir: builtinDir,
		Game:       g.name,
		Pool:       pool,
		Decode:     g.decode,
		Encode:     func(cfg T) ([]byte, error) { return json.Marshal(cfg) },
		Validate:   g.validate,
	})
	if err != nil {
		return counts, fmt.Errorf("open %s store: %w", g.name, err)
	}

	fail := func(path, format string, args ...any) {
		counts.failed++
		fmt.Fprintf(stderr, "import-maps: %s: %s\n", path, fmt.Sprintf(format, args...))
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(opts.dir, entry.Name())
		id := strings.TrimSuffix(entry.Name(), ".json")
		if err := g.checkID(id); err != nil {
			fail(path, "invalid map id %q: %v", id, err)
			continue
		}
		if seen[id] {
			fail(path, "duplicate map id %q", id)
			continue
		}
		seen[id] = true

		raw, err := os.ReadFile(path)
		if err != nil {
			fail(path, "%v", err)
			continue
		}
		// A legacy document that names a different id would import under the
		// wrong key; the file name is the id.
		var probe struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.Unmarshal(raw, &probe); err == nil && len(probe.ID) > 0 {
			var declared string
			if err := json.Unmarshal(probe.ID, &declared); err == nil && declared != id {
				fail(path, "map id %q does not match file name %q", declared, id)
				continue
			}
		}
		cfg, err := g.decode(raw)
		if err != nil {
			fail(path, "decode: %v", err)
			continue
		}
		if err := g.validate(cfg); err != nil {
			fail(path, "invalid map: %v", err)
			continue
		}
		hash := ""
		if g.tokenized {
			hash, err = readTeslaTokenHash(opts.dir, id)
			if err != nil {
				fail(path, "%v", err)
				continue
			}
		}

		exists, err := store.Has(id)
		if err != nil {
			fail(path, "check existing row: %v", err)
			continue
		}
		switch {
		case !exists:
			if opts.dryRun {
				counts.imported++
				continue
			}
			created, err := store.ImportLegacy(id, cfg, "", mapstore.StatusPublished, hash)
			if err != nil {
				fail(path, "import: %v", err)
				continue
			}
			if created {
				counts.imported++
			} else {
				// A concurrent writer inserted it after Has.
				counts.skipped++
			}
		case opts.replace:
			if opts.dryRun {
				counts.updated++
				continue
			}
			if err := store.ReplaceLegacy(id, cfg, "", mapstore.StatusPublished, hash); err != nil {
				fail(path, "replace: %v", err)
				continue
			}
			counts.updated++
		default:
			counts.skipped++
		}
	}
	return counts, nil
}

// readTeslaTokenHash reads <id>.json.edit-token when present. Only the stored
// SHA-256 hash is read; a malformed sidecar fails the file rather than silently
// dropping a player's edit token.
func readTeslaTokenHash(dir, id string) (string, error) {
	path := filepath.Join(dir, id+".json.edit-token")
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read edit-token sidecar: %w", err)
	}
	var rec struct {
		TokenHash string `json:"token_hash"`
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return "", fmt.Errorf("malformed edit-token sidecar %s: %w", filepath.Base(path), err)
	}
	if rec.TokenHash == "" {
		return "", fmt.Errorf("edit-token sidecar %s has no token_hash", filepath.Base(path))
	}
	if len(rec.TokenHash) != 64 {
		return "", fmt.Errorf("edit-token sidecar %s: token_hash must be 64 hex characters", filepath.Base(path))
	}
	if _, err := hex.DecodeString(rec.TokenHash); err != nil {
		return "", fmt.Errorf("edit-token sidecar %s: token_hash must be hex: %w", filepath.Base(path), err)
	}
	return rec.TokenHash, nil
}
