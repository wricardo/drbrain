// Package mapstore owns the map rules every game reimplements: builtin vs
// custom directories, duplicate refusal, admin-only builtin writes, and edit
// tokens for custom maps. The game supplies decode/encode and a validator; the
// store never sees engine types and never authenticates.
//
// Two backends share this contract. Without a PostgreSQL pool the store is the
// original file-backed catalogue (builtin dir plus custom dir and token
// sidecars). With a pool, builtin maps stay on disk and custom maps live in
// platform_maps, gaining ownership, drafts, publication and quotas.
package mapstore

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/platform/pgdb"
)

// Errors every game maps to its own public error vocabulary.
var (
	ErrNotFound          = errors.New("mapstore: map not found")
	ErrExists            = errors.New("mapstore: map already exists")
	ErrInvalid           = errors.New("mapstore: invalid map")
	ErrReadOnly          = errors.New("mapstore: built-in map is read-only")
	ErrEditTokenRequired = errors.New("mapstore: edit token required")
	ErrInvalidToken      = errors.New("mapstore: invalid edit token")
	// ErrForbidden means the actor may not change this map.
	ErrForbidden = errors.New("mapstore: forbidden")
	// ErrLimit means the game already has the maximum number of custom maps.
	ErrLimit = errors.New("mapstore: custom map limit reached")
	// ErrNoDatabase means the operation only exists with a PostgreSQL pool.
	ErrNoDatabase = errors.New("mapstore: operation requires PostgreSQL")
)

// Status is a custom map's lifecycle state. Builtin maps are always published.
type Status string

const (
	StatusDraft     Status = "DRAFT"
	StatusPublished Status = "PUBLISHED"
)

// editTokenSuffix is the custom-map sidecar, unchanged from tesla's
// implementation so existing tokens keep working.
const editTokenSuffix = ".edit-token"

type tokenRecord struct {
	TokenHash string `json:"token_hash"`
}

// Map is one entry in the catalogue.
type Map[T any] struct {
	Name    string
	Config  T
	Builtin bool
	// OwnerID is the account id that owns a custom map; it is empty for
	// builtins and for legacy ownerless rows.
	OwnerID string
	// Status is the lifecycle state; builtins and legacy file maps are
	// StatusPublished.
	Status Status
}

// Actor is who is changing a map. Admin bypasses ownership and builtin gates.
type Actor struct {
	AccountID string
	Admin     bool
}

// Options configures a store. BuiltinDir is required.
type Options[T any] struct {
	BuiltinDir string
	// CustomDir holds user-created maps; empty means <BuiltinDir>/custom. It is
	// unused (and never created) when Pool is set.
	CustomDir string
	Decode    func([]byte) (T, error)
	Encode    func(T) ([]byte, error)
	// Validate is the game's structural map validator. It runs on every read
	// and before every write.
	Validate func(T) error
	// NoEditTokens disables token sidecars for games whose maps are
	// admin-managed and single-directory (mazes). Create then returns "".
	NoEditTokens bool

	// Game names the PostgreSQL partition for custom maps. Required with Pool.
	Game string
	// Pool enables the PostgreSQL-backed custom maps, ownership and drafts.
	Pool *pgxpool.Pool
	// PublishValidate is the stricter check Publish runs; nil falls back to
	// Validate.
	PublishValidate func(T) error
	// MaxCustomMaps caps custom rows for this game (all owners). Zero means no
	// cap.
	MaxCustomMaps int
	// IssueEditTokens makes Create and CreateDraft return a fresh edit token.
	IssueEditTokens bool
}

// Store is a map catalogue with builtin and custom directories.
type Store[T any] struct {
	builtinDir   string
	customDir    string
	decode       func([]byte) (T, error)
	encode       func(T) ([]byte, error)
	validate     func(T) error
	noEditTokens bool

	pg              *pgxpool.Pool
	game            string
	publishValidate func(T) error
	maxCustomMaps   int
	issueEditTokens bool

	mu    sync.Mutex
	cache map[string]T
}

// Open validates the directories and creates them when missing.
func Open[T any](opts Options[T]) (*Store[T], error) {
	if strings.TrimSpace(opts.BuiltinDir) == "" {
		return nil, fmt.Errorf("mapstore: builtin dir is empty")
	}
	if opts.Decode == nil || opts.Encode == nil || opts.Validate == nil {
		return nil, fmt.Errorf("mapstore: Decode, Encode and Validate are required")
	}
	s := &Store[T]{
		builtinDir:   opts.BuiltinDir,
		decode:       opts.Decode,
		encode:       opts.Encode,
		validate:     opts.Validate,
		noEditTokens: opts.NoEditTokens,
		cache:        make(map[string]T),
	}
	if opts.Pool == nil {
		custom := strings.TrimSpace(opts.CustomDir)
		if custom == "" {
			custom = filepath.Join(opts.BuiltinDir, "custom")
		}
		if err := os.MkdirAll(custom, 0o755); err != nil {
			return nil, fmt.Errorf("mapstore: create custom dir: %w", err)
		}
		s.customDir = custom
		return s, nil
	}
	game, err := cleanGame(opts.Game)
	if err != nil {
		return nil, err
	}
	s.pg = opts.Pool
	s.game = game
	s.publishValidate = opts.PublishValidate
	s.maxCustomMaps = opts.MaxCustomMaps
	s.issueEditTokens = opts.IssueEditTokens
	return s, nil
}

// CustomDir is where user-created maps live; empty in PostgreSQL mode.
func (s *Store[T]) CustomDir() string { return s.customDir }

// Refresh drops the parsed-config cache, so the next read sees files changed
// outside this process.
func (s *Store[T]) Refresh() {
	s.mu.Lock()
	s.cache = make(map[string]T)
	s.mu.Unlock()
}

// List returns builtin maps then custom maps, sorted by name. In PostgreSQL
// mode every custom row is listed, drafts included, for compatibility.
func (s *Store[T]) List() ([]Map[T], error) {
	return s.list("", false)
}

// ListFor is List with a viewer: other players' drafts are hidden. Published
// rows and the viewer's own drafts stay visible.
func (s *Store[T]) ListFor(viewerID string) ([]Map[T], error) {
	return s.list(viewerID, true)
}

func (s *Store[T]) list(viewerID string, filterDrafts bool) ([]Map[T], error) {
	if s.pg == nil {
		return s.listFiles()
	}
	out, seen, err := s.listBuiltins()
	if err != nil {
		return nil, err
	}
	rows, err := s.listRows(viewerID, filterDrafts)
	if err != nil {
		return nil, err
	}
	for _, m := range rows {
		if seen[m.Name] {
			// A custom map can never shadow a builtin of the same name.
			continue
		}
		seen[m.Name] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// listFiles is the legacy catalogue: builtin files then custom files.
func (s *Store[T]) listFiles() ([]Map[T], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Map[T]
	seen := make(map[string]bool)
	for _, group := range []struct {
		dir     string
		builtin bool
	}{{s.builtinDir, true}, {s.customDir, false}} {
		names, err := mapNames(group.dir)
		if err != nil {
			return nil, err
		}
		for _, name := range names {
			if seen[name] {
				continue
			}
			cfg, err := s.loadLocked(name, group.dir)
			if errors.Is(err, ErrInvalid) {
				// A corrupt or invalid file is omitted from the catalogue; Get
				// still reports the exact problem when asked for it.
				continue
			}
			if err != nil {
				return nil, err
			}
			seen[name] = true
			out = append(out, Map[T]{
				Name: name, Config: cfg, Builtin: group.builtin, Status: StatusPublished,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// listBuiltins reads the builtin directory (files only). Caller merges.
func (s *Store[T]) listBuiltins() (out []Map[T], seen map[string]bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	names, err := mapNames(s.builtinDir)
	if err != nil {
		return nil, nil, err
	}
	seen = make(map[string]bool, len(names))
	out = make([]Map[T], 0, len(names))
	for _, name := range names {
		cfg, err := s.loadLocked(name, s.builtinDir)
		if errors.Is(err, ErrInvalid) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		seen[name] = true
		out = append(out, Map[T]{Name: name, Config: cfg, Builtin: true, Status: StatusPublished})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, seen, nil
}

// Get returns a map, preferring the builtin copy.
func (s *Store[T]) Get(name string) (Map[T], error) {
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	if s.pg == nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		if fileExists(filepath.Join(s.builtinDir, base+".json")) {
			cfg, err := s.loadLocked(base, s.builtinDir)
			return Map[T]{Name: base, Config: cfg, Builtin: true, Status: StatusPublished}, err
		}
		if fileExists(filepath.Join(s.customDir, base+".json")) {
			cfg, err := s.loadLocked(base, s.customDir)
			return Map[T]{Name: base, Config: cfg, Status: StatusPublished}, err
		}
		return Map[T]{}, fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if s.isBuiltin(base) {
		s.mu.Lock()
		cfg, err := s.loadLocked(base, s.builtinDir)
		s.mu.Unlock()
		return Map[T]{Name: base, Config: cfg, Builtin: true, Status: StatusPublished}, err
	}
	return s.dbGet(base)
}

// Entry is an alias for Get.
func (s *Store[T]) Entry(name string) (Map[T], error) { return s.Get(name) }

// IsBuiltin reports whether name is a shipped map.
func (s *Store[T]) IsBuiltin(name string) bool {
	base, err := cleanName(name)
	if err != nil {
		return false
	}
	return s.isBuiltin(base)
}

func (s *Store[T]) isBuiltin(base string) bool {
	return fileExists(filepath.Join(s.builtinDir, base+".json"))
}

// Create stores a new custom map and returns its edit token. It never
// overwrites: a builtin or custom map with the name yields ErrExists.
//
// With a PostgreSQL pool this is the legacy wrapper: it creates an ownerless
// PUBLISHED row. New code should use CreateDraft.
func (s *Store[T]) Create(name string, cfg T) (string, error) {
	if s.pg != nil {
		return s.dbCreateLegacy(name, cfg)
	}
	base, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if err := s.validate(cfg); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.checkPublish(cfg); err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) || fileExists(filepath.Join(s.customDir, base+".json")) {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	token := ""
	if !s.noEditTokens {
		var err error
		token, err = newEditToken()
		if err != nil {
			return "", err
		}
		if err := s.writeTokenLocked(base, token); err != nil {
			return "", err
		}
	}
	if err := s.writeLocked(base, cfg, s.customDir); err != nil {
		if token != "" {
			_ = os.Remove(s.tokenPath(base))
		}
		return "", err
	}
	return token, nil
}

// Save overwrites an existing custom map. admin allows overwriting a builtin.
// A custom map's token must have been verified by the caller first.
//
// With a PostgreSQL pool, ownerless rows require admin so the legacy no-token
// path cannot edit an imported map by accident.
func (s *Store[T]) Save(name string, cfg T, admin bool) error {
	if s.pg != nil {
		_, err := s.dbSaveLegacy(name, cfg, admin)
		return err
	}
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case fileExists(filepath.Join(s.builtinDir, base+".json")):
		return s.saveBuiltinLocked(base, cfg, admin)
	case fileExists(filepath.Join(s.customDir, base+".json")):
		if err := s.validate(cfg); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if err := s.checkPublish(cfg); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
		return s.writeLocked(base, cfg, s.customDir)
	default:
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
}

// SaveBuiltin overwrites a shipped map. admin must be true; the transport
// authenticates before passing it.
func (s *Store[T]) SaveBuiltin(name string, cfg T, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveBuiltinLocked(base, cfg, admin)
}

func (s *Store[T]) saveBuiltinLocked(base string, cfg T, admin bool) error {
	if !admin {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if err := s.validate(cfg); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.checkPublish(cfg); err != nil {
		return fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
	}
	return s.writeLocked(base, cfg, s.builtinDir)
}

// Delete removes a map. Builtins require admin; custom maps need a verified
// token (the caller checks it) and are deleted with their token sidecar.
func (s *Store[T]) Delete(name string, admin bool) error {
	if s.pg != nil {
		return s.dbDeleteLegacy(name, admin)
	}
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return s.deleteBuiltinLocked(base, admin)
	}
	if fileExists(filepath.Join(s.customDir, base+".json")) {
		if err := removeMap(s.customDir, base); err != nil {
			return err
		}
		delete(s.cache, base)
		return nil
	}
	return fmt.Errorf("%w: %q", ErrNotFound, base)
}

// DeleteBuiltin removes a shipped map. admin must be true.
func (s *Store[T]) DeleteBuiltin(name string, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deleteBuiltinLocked(base, admin)
}

func (s *Store[T]) deleteBuiltinLocked(base string, admin bool) error {
	if !admin {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if err := removeMap(s.builtinDir, base); err != nil {
		return err
	}
	delete(s.cache, base)
	return nil
}

// VerifyEditToken checks a custom map's token. Builtins always fail: use admin
// credentials for them.
func (s *Store[T]) VerifyEditToken(name, token string) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	if s.pg != nil {
		return s.dbVerifyEditToken(base, token)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if fileExists(filepath.Join(s.builtinDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	if !fileExists(filepath.Join(s.customDir, base+".json")) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if s.noEditTokens || strings.TrimSpace(token) == "" {
		return ErrEditTokenRequired
	}
	raw, err := os.ReadFile(s.tokenPath(base))
	if err != nil {
		// A custom map predating tokens requires admin instead.
		return ErrEditTokenRequired
	}
	var rec tokenRecord
	if err := json.Unmarshal(raw, &rec); err != nil || rec.TokenHash == "" {
		return ErrEditTokenRequired
	}
	got := hashToken(token)
	if subtle.ConstantTimeCompare([]byte(got), []byte(rec.TokenHash)) != 1 {
		return ErrInvalidToken
	}
	return nil
}

// Has reports whether a map with this id exists anywhere, without decoding it.
// PostgreSQL mode checks the database row; file mode checks the builtin and
// custom files.
func (s *Store[T]) Has(name string) (bool, error) {
	base, err := cleanName(name)
	if err != nil {
		return false, err
	}
	if s.pg == nil {
		return s.isBuiltin(base) || fileExists(filepath.Join(s.customDir, base+".json")), nil
	}
	ctx, cancel := dbCtx()
	defer cancel()
	var exists bool
	if err := s.pg.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM platform_maps WHERE game = $1 AND id = $2)`,
		s.game, base).Scan(&exists); err != nil {
		return false, fmt.Errorf("mapstore: has %s: %w", base, err)
	}
	return exists, nil
}

// ImportLegacy inserts a legacy custom map as-is, ignoring the user quota: an
// operator is restoring data that already existed, not creating player content.
// It never overwrites an existing row and reports created=false for one; the
// caller decides whether to replace. editTokenHash is the stored SHA-256 hex
// ("" when the legacy map had no token).
//
// Builtin ids are refused so an import can never shadow a shipped map.
func (s *Store[T]) ImportLegacy(name string, cfg T, ownerID string, status Status, editTokenHash string) (created bool, err error) {
	base, st, raw, err := s.legacyRow(name, cfg, status, editTokenHash)
	if err != nil {
		return false, err
	}
	if st == StatusPublished {
		if err := s.checkImportPublish(cfg); err != nil {
			return false, fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
	}
	if s.pg == nil {
		return false, fmt.Errorf("ImportLegacy: %w", ErrNoDatabase)
	}
	if s.isBuiltin(base) {
		return false, fmt.Errorf("%w: %q", ErrExists, base)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tag, err := s.pg.Exec(ctx, `
INSERT INTO platform_maps (game, id, config, owner_id, status, edit_token_hash)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (game, id) DO NOTHING`,
		s.game, base, string(raw), ownerID, string(st), editTokenHash)
	if err != nil {
		return false, fmt.Errorf("mapstore: import %s: %w", base, err)
	}
	return tag.RowsAffected() == 1, nil
}

// ReplaceLegacy updates an existing imported row in place: config, owner,
// status and edit-token hash. It is the --replace path of the importer and
// never creates a row.
func (s *Store[T]) ReplaceLegacy(name string, cfg T, ownerID string, status Status, editTokenHash string) error {
	base, st, raw, err := s.legacyRow(name, cfg, status, editTokenHash)
	if err != nil {
		return err
	}
	if st == StatusPublished {
		if err := s.checkImportPublish(cfg); err != nil {
			return fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
	}
	if s.pg == nil {
		return fmt.Errorf("ReplaceLegacy: %w", ErrNoDatabase)
	}
	if s.isBuiltin(base) {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tag, err := s.pg.Exec(ctx, `
UPDATE platform_maps
SET config = $3, owner_id = $4, status = $5, edit_token_hash = $6, updated_at = now()
WHERE game = $1 AND id = $2`,
		s.game, base, string(raw), ownerID, string(st), editTokenHash)
	if err != nil {
		return fmt.Errorf("mapstore: replace %s: %w", base, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	return nil
}

// checkPublish runs the publication validator (structural Validate when no
// stricter PublishValidate is configured).
func (s *Store[T]) checkPublish(cfg T) error {
	check := s.publishValidate
	if check == nil {
		check = s.validate
	}
	return check(cfg)
}

// checkImportPublish runs PublishValidate only when the store has one. Legacy
// importers may run in games whose PublishValidate is optional.
func (s *Store[T]) checkImportPublish(cfg T) error {
	if s.publishValidate == nil {
		return nil
	}
	return s.publishValidate(cfg)
}

// legacyRow validates the pieces every legacy import shares.
func (s *Store[T]) legacyRow(name string, cfg T, status Status, editTokenHash string) (string, Status, []byte, error) {
	base, err := cleanName(name)
	if err != nil {
		return "", "", nil, err
	}
	st, err := parseStatus(string(status))
	if err != nil {
		return "", "", nil, err
	}
	if editTokenHash != "" && !validTokenHash(editTokenHash) {
		return "", "", nil, fmt.Errorf("%w: %q: edit token hash must be 64 hex characters", ErrInvalid, base)
	}
	if err := s.validate(cfg); err != nil {
		return "", "", nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	raw, err := s.encode(cfg)
	if err != nil {
		return "", "", nil, fmt.Errorf("mapstore: encode %s: %w", base, err)
	}
	return base, st, raw, nil
}

func validTokenHash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// CreateDraft stores a new owned DRAFT map and returns its edit token ("" when
// IssueEditTokens is off). OwnerID must be non-empty: imports and the legacy
// Create use ownerless rows instead.
func (s *Store[T]) CreateDraft(name string, cfg T, ownerID string) (string, error) {
	if s.pg == nil {
		return "", fmt.Errorf("CreateDraft: %w", ErrNoDatabase)
	}
	base, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(ownerID) == "" {
		return "", fmt.Errorf("%w: %q: owner is required", ErrInvalid, base)
	}
	if err := s.validate(cfg); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if s.isBuiltin(base) {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	raw, err := s.encode(cfg)
	if err != nil {
		return "", fmt.Errorf("mapstore: encode %s: %w", base, err)
	}
	token := ""
	hash := ""
	if s.issueEditTokens {
		token, err = newEditToken()
		if err != nil {
			return "", err
		}
		hash = hashToken(token)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "mapstore:"+s.game); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM platform_maps WHERE game = $1 AND id = $2)`,
		s.game, base).Scan(&exists); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	if exists {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	if err := s.checkQuotaTx(ctx, tx); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO platform_maps (game, id, config, owner_id, status, edit_token_hash)
VALUES ($1, $2, $3, $4, $5, $6)`,
		s.game, base, string(raw), ownerID, string(StatusDraft), hash)
	if err != nil {
		if pgdb.Constraint(err) != "" {
			return "", fmt.Errorf("%w: %q", ErrExists, base)
		}
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	return token, nil
}

// SaveOwned overwrites a map on behalf of an actor. A builtin needs an admin,
// a custom row needs its owner (or an admin, who cannot change the owner).
// Editing a published map as its owner puts it back into review.
func (s *Store[T]) SaveOwned(name string, cfg T, actor Actor) (Map[T], error) {
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	if s.pg == nil {
		if !s.isBuiltin(base) && !fileExists(filepath.Join(s.customDir, base+".json")) {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrNotFound, base)
		}
		if !actor.Admin {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
		}
		if err := s.Save(base, cfg, true); err != nil {
			return Map[T]{}, err
		}
		return s.Get(base)
	}
	if err := s.validate(cfg); err != nil {
		return Map[T]{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if s.isBuiltin(base) {
		if !actor.Admin {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
		}
		s.mu.Lock()
		err := s.saveBuiltinLocked(base, cfg, true)
		s.mu.Unlock()
		if err != nil {
			return Map[T]{}, err
		}
		return Map[T]{Name: base, Config: cfg, Builtin: true, Status: StatusPublished}, nil
	}
	raw, err := s.encode(cfg)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: encode %s: %w", base, err)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, status, _, err := lockRow(ctx, tx, s.game, base)
	if err != nil {
		return Map[T]{}, rowError("save", base, err)
	}
	switch {
	case actor.Admin:
		// Admin edits keep both owner and lifecycle state.
	case owner == "":
		return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
	case owner != actor.AccountID:
		return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
	default:
		status = StatusDraft
	}
	if status == StatusPublished {
		if err := s.checkPublish(cfg); err != nil {
			return Map[T]{}, fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE platform_maps SET config = $3, owner_id = $4, status = $5, updated_at = now()
WHERE game = $1 AND id = $2`, s.game, base, string(raw), owner, string(status)); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: status}, nil
}

// Publish moves an owned draft to PUBLISHED after running the publish
// validator. Publishing an already published map the actor may publish is a
// no-op; builtins are rejected as read-only.
func (s *Store[T]) Publish(name string, actor Actor) (Map[T], error) {
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	if s.pg == nil {
		if s.isBuiltin(base) {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
		}
		return Map[T]{}, fmt.Errorf("Publish: %w", ErrNoDatabase)
	}
	if s.isBuiltin(base) {
		return Map[T]{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: publish %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, status, raw, err := lockRow(ctx, tx, s.game, base)
	if err != nil {
		return Map[T]{}, rowError("publish", base, err)
	}
	if !actor.Admin {
		if owner == "" || owner != actor.AccountID {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
		}
	}
	if status == StatusPublished {
		cfg, err := s.decodeRow(base, raw)
		if err != nil {
			return Map[T]{}, err
		}
		if err := s.checkPublish(cfg); err != nil {
			return Map[T]{}, fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
		return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: status}, nil
	}
	cfg, err := s.decodeRow(base, raw)
	if err != nil {
		return Map[T]{}, err
	}
	if err := s.checkPublish(cfg); err != nil {
		return Map[T]{}, fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE platform_maps SET status = $3, updated_at = now() WHERE game = $1 AND id = $2`,
		s.game, base, string(StatusPublished)); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: publish %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: publish %s: %w", base, err)
	}
	return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: StatusPublished}, nil
}

// DeleteOwned removes a map on behalf of an actor: builtins need an admin,
// custom rows need their owner or an admin.
func (s *Store[T]) DeleteOwned(name string, actor Actor) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	if s.pg == nil {
		if !actor.Admin {
			if s.isBuiltin(base) {
				return fmt.Errorf("%w: %q", ErrReadOnly, base)
			}
			if fileExists(filepath.Join(s.customDir, base+".json")) {
				return fmt.Errorf("%w: %q", ErrForbidden, base)
			}
		}
		return s.Delete(base, actor.Admin)
	}
	if s.isBuiltin(base) {
		if !actor.Admin {
			return fmt.Errorf("%w: %q", ErrReadOnly, base)
		}
		s.mu.Lock()
		err := s.deleteBuiltinLocked(base, true)
		s.mu.Unlock()
		return err
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, _, _, err := lockRow(ctx, tx, s.game, base)
	if err != nil {
		return rowError("delete", base, err)
	}
	if !actor.Admin {
		if owner == "" || owner != actor.AccountID {
			return fmt.Errorf("%w: %q", ErrForbidden, base)
		}
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM platform_maps WHERE game = $1 AND id = $2`, s.game, base); err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", base, err)
	}
	return nil
}

// Claim attaches an ownerless custom row to ownerID after checking its edit
// token. Claiming a row you already own is idempotent; another owner is
// forbidden. Builtins have no tokens.
func (s *Store[T]) Claim(name, ownerID, token string) (Map[T], error) {
	if s.pg == nil {
		return Map[T]{}, fmt.Errorf("Claim: %w", ErrNoDatabase)
	}
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	if strings.TrimSpace(ownerID) == "" {
		return Map[T]{}, fmt.Errorf("%w: %q: owner is required", ErrInvalid, base)
	}
	if strings.TrimSpace(token) == "" {
		return Map[T]{}, ErrEditTokenRequired
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: claim %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, status, raw, hash, err := lockClaimRow(ctx, tx, s.game, base)
	if err != nil {
		return Map[T]{}, rowError("claim", base, err)
	}
	if hash == "" {
		return Map[T]{}, ErrEditTokenRequired
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(hash)) != 1 {
		return Map[T]{}, ErrInvalidToken
	}
	if owner != "" && owner != ownerID {
		return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
	}
	if owner == "" {
		owner = ownerID
		if _, err := tx.Exec(ctx,
			`UPDATE platform_maps SET owner_id = $3, updated_at = now() WHERE game = $1 AND id = $2`,
			s.game, base, owner); err != nil {
			return Map[T]{}, fmt.Errorf("mapstore: claim %s: %w", base, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: claim %s: %w", base, err)
	}
	cfg, err := s.decodeRow(base, raw)
	if err != nil {
		return Map[T]{}, err
	}
	return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: status}, nil
}

// DeleteDraftsByOwner removes every DRAFT row owned by ownerID for this game.
// Account deletion calls it before a published map is orphaned.
func (s *Store[T]) DeleteDraftsByOwner(ownerID string) error {
	if s.pg == nil {
		return fmt.Errorf("DeleteDraftsByOwner: %w", ErrNoDatabase)
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil
	}
	ctx, cancel := dbCtx()
	defer cancel()
	if _, err := s.pg.Exec(ctx, `
DELETE FROM platform_maps WHERE game = $1 AND owner_id = $2 AND status = $3`,
		s.game, ownerID, string(StatusDraft)); err != nil {
		return fmt.Errorf("mapstore: delete drafts of %s: %w", ownerID, err)
	}
	return nil
}

// OrphanPublishedByOwner detaches published maps from a deleted account so
// they stay playable without an owner.
func (s *Store[T]) OrphanPublishedByOwner(ownerID string) error {
	if s.pg == nil {
		return fmt.Errorf("OrphanPublishedByOwner: %w", ErrNoDatabase)
	}
	if strings.TrimSpace(ownerID) == "" {
		return nil
	}
	ctx, cancel := dbCtx()
	defer cancel()
	if _, err := s.pg.Exec(ctx, `
UPDATE platform_maps SET owner_id = '', updated_at = now()
WHERE game = $1 AND owner_id = $2 AND status = $3`,
		s.game, ownerID, string(StatusPublished)); err != nil {
		return fmt.Errorf("mapstore: orphan maps of %s: %w", ownerID, err)
	}
	return nil
}

// ---- PostgreSQL rows ------------------------------------------------------

func dbCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), pgdb.QueryTimeout)
}

// checkQuotaTx counts every custom row for the game under the caller's
// advisory lock, so concurrent creates cannot overshoot MaxCustomMaps.
func (s *Store[T]) checkQuotaTx(ctx context.Context, tx pgx.Tx) error {
	if s.maxCustomMaps <= 0 {
		return nil
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM platform_maps WHERE game = $1`, s.game).Scan(&n); err != nil {
		return fmt.Errorf("mapstore: count maps: %w", err)
	}
	if n >= s.maxCustomMaps {
		return fmt.Errorf("%w: %d maps", ErrLimit, s.maxCustomMaps)
	}
	return nil
}

func (s *Store[T]) dbCreateLegacy(name string, cfg T) (string, error) {
	base, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if err := s.validate(cfg); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := s.checkPublish(cfg); err != nil {
		return "", fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
	}
	if s.isBuiltin(base) {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	raw, err := s.encode(cfg)
	if err != nil {
		return "", fmt.Errorf("mapstore: encode %s: %w", base, err)
	}
	token := ""
	hash := ""
	if s.issueEditTokens {
		token, err = newEditToken()
		if err != nil {
			return "", err
		}
		hash = hashToken(token)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, "mapstore:"+s.game); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM platform_maps WHERE game = $1 AND id = $2)`,
		s.game, base).Scan(&exists); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	if exists {
		return "", fmt.Errorf("%w: %q", ErrExists, base)
	}
	if err := s.checkQuotaTx(ctx, tx); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `
INSERT INTO platform_maps (game, id, config, owner_id, status, edit_token_hash)
VALUES ($1, $2, $3, '', $4, $5)`,
		s.game, base, string(raw), string(StatusPublished), hash)
	if err != nil {
		if pgdb.Constraint(err) != "" {
			return "", fmt.Errorf("%w: %q", ErrExists, base)
		}
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("mapstore: create %s: %w", base, err)
	}
	return token, nil
}

func (s *Store[T]) dbSaveLegacy(name string, cfg T, admin bool) (Map[T], error) {
	base, err := cleanName(name)
	if err != nil {
		return Map[T]{}, err
	}
	if s.isBuiltin(base) {
		if !admin {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrReadOnly, base)
		}
		if err := s.validate(cfg); err != nil {
			return Map[T]{}, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		s.mu.Lock()
		err := s.saveBuiltinLocked(base, cfg, true)
		s.mu.Unlock()
		if err != nil {
			return Map[T]{}, err
		}
		return Map[T]{Name: base, Config: cfg, Builtin: true, Status: StatusPublished}, nil
	}
	if err := s.validate(cfg); err != nil {
		return Map[T]{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	raw, err := s.encode(cfg)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: encode %s: %w", base, err)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tx, err := s.pg.Begin(ctx)
	if err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	owner, status, _, err := lockRow(ctx, tx, s.game, base)
	if err != nil {
		return Map[T]{}, rowError("save", base, err)
	}
	if !admin && owner == "" {
		return Map[T]{}, fmt.Errorf("%w: %q", ErrForbidden, base)
	}
	if status == StatusPublished {
		if err := s.checkPublish(cfg); err != nil {
			return Map[T]{}, fmt.Errorf("%w: %q: %v", ErrInvalid, base, err)
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE platform_maps SET config = $3, updated_at = now() WHERE game = $1 AND id = $2`,
		s.game, base, string(raw)); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Map[T]{}, fmt.Errorf("mapstore: save %s: %w", base, err)
	}
	return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: status}, nil
}

func (s *Store[T]) dbDeleteLegacy(name string, admin bool) error {
	base, err := cleanName(name)
	if err != nil {
		return err
	}
	if s.isBuiltin(base) {
		s.mu.Lock()
		err := s.deleteBuiltinLocked(base, admin)
		s.mu.Unlock()
		return err
	}
	if !admin {
		return fmt.Errorf("%w: %q", ErrForbidden, base)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	tag, err := s.pg.Exec(ctx,
		`DELETE FROM platform_maps WHERE game = $1 AND id = $2`, s.game, base)
	if err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", base, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	return nil
}

func (s *Store[T]) dbVerifyEditToken(base, token string) error {
	if s.isBuiltin(base) {
		return fmt.Errorf("%w: %q", ErrReadOnly, base)
	}
	ctx, cancel := dbCtx()
	defer cancel()
	var hash string
	err := s.pg.QueryRow(ctx,
		`SELECT edit_token_hash FROM platform_maps WHERE game = $1 AND id = $2`,
		s.game, base).Scan(&hash)
	if err != nil {
		if pgdb.IsNoRows(err) {
			return fmt.Errorf("%w: %q", ErrNotFound, base)
		}
		return fmt.Errorf("mapstore: verify token for %s: %w", base, err)
	}
	if hash == "" || strings.TrimSpace(token) == "" {
		return ErrEditTokenRequired
	}
	if subtle.ConstantTimeCompare([]byte(hashToken(token)), []byte(hash)) != 1 {
		return ErrInvalidToken
	}
	return nil
}

func (s *Store[T]) dbGet(base string) (Map[T], error) {
	ctx, cancel := dbCtx()
	defer cancel()
	var owner, status string
	var raw []byte
	err := s.pg.QueryRow(ctx,
		`SELECT config, owner_id, status FROM platform_maps WHERE game = $1 AND id = $2`,
		s.game, base).Scan(&raw, &owner, &status)
	if err != nil {
		if pgdb.IsNoRows(err) {
			return Map[T]{}, fmt.Errorf("%w: %q", ErrNotFound, base)
		}
		return Map[T]{}, fmt.Errorf("mapstore: get %s: %w", base, err)
	}
	cfg, err := s.decodeRow(base, raw)
	if err != nil {
		return Map[T]{}, err
	}
	st, err := parseStatus(status)
	if err != nil {
		return Map[T]{}, fmt.Errorf("%w: %s", err, base)
	}
	return Map[T]{Name: base, Config: cfg, OwnerID: owner, Status: st}, nil
}

func (s *Store[T]) listRows(viewerID string, filterDrafts bool) ([]Map[T], error) {
	ctx, cancel := dbCtx()
	defer cancel()
	query := `SELECT id, config, owner_id, status FROM platform_maps WHERE game = $1`
	args := []any{s.game}
	if filterDrafts {
		query += ` AND (status = $2 OR (status = $3 AND owner_id = $4 AND owner_id <> ''))`
		args = append(args, string(StatusPublished), string(StatusDraft), viewerID)
	}
	query += ` ORDER BY id`
	rows, err := s.pg.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("mapstore: list: %w", err)
	}
	defer rows.Close()
	var out []Map[T]
	for rows.Next() {
		var id, owner, status string
		var raw []byte
		if err := rows.Scan(&id, &raw, &owner, &status); err != nil {
			return nil, fmt.Errorf("mapstore: list: %w", err)
		}
		cfg, st, err := s.decodeStored(id, raw, status)
		if err != nil {
			// A corrupt or unknown row must not hide the rest of the
			// catalogue; Get still reports the exact problem.
			slog.Warn("mapstore: skipping invalid map row", "game", s.game, "id", id, "err", err)
			continue
		}
		out = append(out, Map[T]{Name: id, Config: cfg, OwnerID: owner, Status: st})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("mapstore: list: %w", err)
	}
	return out, nil
}

// decodeStored parses one database row, mapping every storage problem to
// ErrInvalid.
func (s *Store[T]) decodeStored(id string, raw []byte, status string) (T, Status, error) {
	st, err := parseStatus(status)
	if err != nil {
		var zero T
		return zero, "", err
	}
	cfg, err := s.decodeRow(id, raw)
	return cfg, st, err
}

func (s *Store[T]) decodeRow(id string, raw []byte) (T, error) {
	var zero T
	cfg, err := s.decode(raw)
	if err != nil {
		return zero, fmt.Errorf("%w: %s: %v", ErrInvalid, id, err)
	}
	if err := s.validate(cfg); err != nil {
		return zero, fmt.Errorf("%w: %s: %v", ErrInvalid, id, err)
	}
	return cfg, nil
}

func parseStatus(status string) (Status, error) {
	switch Status(status) {
	case StatusDraft:
		return StatusDraft, nil
	case StatusPublished:
		return StatusPublished, nil
	default:
		return "", fmt.Errorf("%w: unknown status %q", ErrInvalid, status)
	}
}

// lockRow reads a row FOR UPDATE and returns its owner, status and payload.
func lockRow(ctx context.Context, tx pgx.Tx, game, id string) (owner string, status Status, raw []byte, err error) {
	var statusText string
	err = tx.QueryRow(ctx,
		`SELECT owner_id, status, config FROM platform_maps WHERE game = $1 AND id = $2 FOR UPDATE`,
		game, id).Scan(&owner, &statusText, &raw)
	if err != nil {
		return "", "", nil, err
	}
	status, err = parseStatus(statusText)
	return owner, status, raw, err
}

func lockClaimRow(ctx context.Context, tx pgx.Tx, game, id string) (owner string, status Status, raw []byte, hash string, err error) {
	var statusText string
	err = tx.QueryRow(ctx,
		`SELECT owner_id, status, config, edit_token_hash FROM platform_maps
WHERE game = $1 AND id = $2 FOR UPDATE`, game, id).Scan(&owner, &statusText, &raw, &hash)
	if err != nil {
		return "", "", nil, "", err
	}
	status, err = parseStatus(statusText)
	return owner, status, raw, hash, err
}

func rowError(op, base string, err error) error {
	if pgdb.IsNoRows(err) {
		return fmt.Errorf("%w: %q", ErrNotFound, base)
	}
	if errors.Is(err, ErrInvalid) {
		return fmt.Errorf("%w: %s", err, base)
	}
	return fmt.Errorf("mapstore: %s %s: %w", op, base, err)
}

// ---- files ----------------------------------------------------------------

func (s *Store[T]) loadLocked(name, dir string) (T, error) {
	if cfg, ok := s.cache[name]; ok {
		return cfg, nil
	}
	raw, err := os.ReadFile(filepath.Join(dir, name+".json"))
	if err != nil {
		var zero T
		return zero, fmt.Errorf("mapstore: read %s: %w", name, err)
	}
	cfg, err := s.decode(raw)
	if err != nil {
		var zero T
		return zero, fmt.Errorf("mapstore: decode %s: %w", name, err)
	}
	if err := s.validate(cfg); err != nil {
		var zero T
		return zero, fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
	}
	s.cache[name] = cfg
	return cfg, nil
}

func (s *Store[T]) writeLocked(name string, cfg T, dir string) error {
	raw, err := s.encode(cfg)
	if err != nil {
		return fmt.Errorf("mapstore: encode %s: %w", name, err)
	}
	if err := writeAtomic(dir, filepath.Join(dir, name+".json"), raw); err != nil {
		return fmt.Errorf("mapstore: write %s: %w", name, err)
	}
	s.cache[name] = cfg
	return nil
}

func (s *Store[T]) writeTokenLocked(name, token string) error {
	rec := tokenRecord{TokenHash: hashToken(token)}
	raw, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("mapstore: encode token for %s: %w", name, err)
	}
	if err := os.WriteFile(s.tokenPath(name), raw, 0o600); err != nil {
		return fmt.Errorf("mapstore: write token for %s: %w", name, err)
	}
	return nil
}

func (s *Store[T]) tokenPath(name string) string {
	return filepath.Join(s.customDir, name+".json"+editTokenSuffix)
}

func mapNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("mapstore: list %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".json")
		if _, err := cleanName(name); err == nil {
			names = append(names, name)
		}
	}
	return names, nil
}

func removeMap(dir, name string) error {
	if err := os.Remove(filepath.Join(dir, name+".json")); err != nil {
		return fmt.Errorf("mapstore: delete %s: %w", name, err)
	}
	// Best effort: the token sidecar is meaningless without the map.
	_ = os.Remove(filepath.Join(dir, name+".json"+editTokenSuffix))
	return nil
}

func writeAtomic(dir, path string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// cleanName accepts "foo", "foo.json" and surrounding spaces; it rejects
// traversal so a name is always a safe file base.
func cleanName(name string) (string, error) {
	base := strings.TrimSuffix(filepath.Base(strings.TrimSpace(name)), ".json")
	if base == "" || base == "." || base == ".." || strings.ContainsAny(base, `/\`) {
		return "", fmt.Errorf("%w: name %q", ErrInvalid, name)
	}
	return base, nil
}

// cleanGame rejects a partition name that could not be a clean key.
func cleanGame(game string) (string, error) {
	g := strings.TrimSpace(game)
	if g == "" || g == "." || g == ".." || strings.ContainsAny(g, `/\`) ||
		strings.ContainsFunc(g, unicode.IsSpace) {
		return "", fmt.Errorf("mapstore: game %q is not a valid name", game)
	}
	return g, nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func newEditToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("mapstore: generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
