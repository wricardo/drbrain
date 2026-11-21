package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

func teslaFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../games/tesla/maps/classic.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func deliveryFixture(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../games/delivery/maps/city.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func importDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pgtest.Pool(t)
}

func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func tokenSidecar(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	raw, _ := json.Marshal(map[string]string{"token_hash": hex.EncodeToString(sum[:])})
	return raw
}

func runTeslaImport(t *testing.T, pool *pgxpool.Pool, opts importOptions) (importCounts, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	counts, err := runImport(teslaLegacyGame(), pool, opts, &out, &errOut)
	if err != nil {
		t.Fatalf("runImport: %v", err)
	}
	return counts, out.String(), errOut.String()
}

type importedRow struct {
	owner, status, token string
	config               []byte
}

func readImported(t *testing.T, pool *pgxpool.Pool, game, id string) importedRow {
	t.Helper()
	var row importedRow
	var status string
	if err := pool.QueryRow(t.Context(),
		`SELECT owner_id, status, edit_token_hash, config FROM platform_maps WHERE game = $1 AND id = $2`,
		game, id).Scan(&row.owner, &status, &row.token, &row.config); err != nil {
		t.Fatalf("read imported %s/%s: %v", game, id, err)
	}
	row.status = status
	return row
}

func platformMapCount(t *testing.T, pool *pgxpool.Pool, game string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM platform_maps WHERE game = $1`, game).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportMapsTeslaDryRunThenImportAndRerun(t *testing.T) {
	pool := importDB(t)
	dir := t.TempDir()
	mapPath := writeFile(t, dir, "legacy_tesla.json", teslaFixture(t))
	tokenPath := writeFile(t, dir, "legacy_tesla.json.edit-token", tokenSidecar("secret-token"))
	wantHash := ""
	{
		sum := sha256.Sum256([]byte("secret-token"))
		wantHash = hex.EncodeToString(sum[:])
	}

	counts, _, errOut := runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir, dryRun: true})
	if counts.imported != 1 || counts.updated != 0 || counts.skipped != 0 || counts.failed != 0 {
		t.Fatalf("dry run counts = %+v (stderr %s)", counts, errOut)
	}
	if n := platformMapCount(t, pool, "tesla"); n != 0 {
		t.Fatalf("dry run wrote %d rows", n)
	}
	for _, p := range []string{mapPath, tokenPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("dry run touched %s: %v", p, err)
		}
	}

	counts, _, errOut = runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir})
	if counts.imported != 1 || counts.failed != 0 {
		t.Fatalf("import counts = %+v (stderr %s)", counts, errOut)
	}
	row := readImported(t, pool, "tesla", "legacy_tesla")
	if row.owner != "" || row.status != "PUBLISHED" || row.token != wantHash {
		t.Fatalf("imported row = owner %q status %q token %q", row.owner, row.status, row.token)
	}
	var cfg map[string]any
	if err := json.Unmarshal(row.config, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, leaked := cfg["owner_id"]; leaked {
		t.Error("imported config carries owner_id")
	}

	// A rerun is a no-op and leaves the source files in place.
	counts, _, _ = runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir})
	if counts.imported != 0 || counts.skipped != 1 || counts.failed != 0 {
		t.Fatalf("rerun counts = %+v", counts)
	}
	if n := platformMapCount(t, pool, "tesla"); n != 1 {
		t.Fatalf("rerun changed row count: %d", n)
	}
	for _, p := range []string{mapPath, tokenPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("import deleted source %s: %v", p, err)
		}
	}
}

func TestImportMapsTeslaReplace(t *testing.T) {
	pool := importDB(t)
	dir := t.TempDir()
	raw := teslaFixture(t)
	writeFile(t, dir, "legacy_tesla.json", raw)
	writeFile(t, dir, "legacy_tesla.json.edit-token", tokenSidecar("old-token"))
	if counts, _, errOut := runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir}); counts.imported != 1 {
		t.Fatalf("first import = %+v (%s)", counts, errOut)
	}

	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg["name"] = "Renamed legacy map"
	renamed, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dir, "legacy_tesla.json", renamed)
	writeFile(t, dir, "legacy_tesla.json.edit-token", tokenSidecar("new-token"))
	sum := sha256.Sum256([]byte("new-token"))
	wantHash := hex.EncodeToString(sum[:])

	counts, _, _ := runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir})
	if counts.skipped != 1 || counts.updated != 0 {
		t.Fatalf("default rerun counts = %+v, want skip", counts)
	}
	row := readImported(t, pool, "tesla", "legacy_tesla")
	var before map[string]any
	_ = json.Unmarshal(row.config, &before)
	if before["name"] == "Renamed legacy map" {
		t.Fatal("skip overwrote the existing row")
	}

	counts, _, errOut := runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir, replace: true})
	if counts.updated != 1 || counts.failed != 0 {
		t.Fatalf("replace counts = %+v (%s)", counts, errOut)
	}
	row = readImported(t, pool, "tesla", "legacy_tesla")
	var after map[string]any
	if err := json.Unmarshal(row.config, &after); err != nil {
		t.Fatal(err)
	}
	if after["name"] != "Renamed legacy map" || row.token != wantHash {
		t.Fatalf("replaced row = name %v token %q", after["name"], row.token)
	}
}

func TestImportMapsReportsFileFailures(t *testing.T) {
	pool := importDB(t)
	dir := t.TempDir()
	good := teslaFixture(t)
	writeFile(t, dir, "good.json", good)
	writeFile(t, dir, "broken.json", []byte(`{"name":`))
	writeFile(t, dir, "bad name.json", good)
	writeFile(t, dir, "mismatch.json", append([]byte(`{"id":"other",`), good[1:]...))
	writeFile(t, dir, "bad_token.json", good)
	writeFile(t, dir, "bad_token.json.edit-token", []byte(`{"token_hash":"plaintext"}`))

	counts, _, errOut := runTeslaImport(t, pool, importOptions{game: "tesla", dir: dir})
	if counts.failed != 4 {
		t.Fatalf("failed = %d, want 4 (stderr %s)", counts.failed, errOut)
	}
	if counts.imported != 1 {
		t.Fatalf("good file not imported: %+v", counts)
	}
	for _, name := range []string{"broken.json", "bad name.json", "mismatch.json", "bad_token.json"} {
		if !strings.Contains(errOut, name) {
			t.Errorf("stderr does not name %q:\n%s", name, errOut)
		}
	}
	if n := platformMapCount(t, pool, "tesla"); n != 1 {
		t.Fatalf("rows after failures = %d, want only good", n)
	}
}

func TestImportMapsDeliveryCity(t *testing.T) {
	pool := importDB(t)
	dir := t.TempDir()
	writeFile(t, dir, "legacy_city.json", deliveryFixture(t))

	var out, errOut bytes.Buffer
	counts, err := runImport(deliveryLegacyGame(), pool,
		importOptions{game: "delivery", dir: dir}, &out, &errOut)
	if err != nil {
		t.Fatal(err)
	}
	if counts.imported != 1 || counts.failed != 0 {
		t.Fatalf("delivery counts = %+v (%s)", counts, errOut.String())
	}
	row := readImported(t, pool, "delivery", "legacy_city")
	if row.owner != "" || row.status != "PUBLISHED" || row.token != "" {
		t.Fatalf("delivery row = owner %q status %q token %q", row.owner, row.status, row.token)
	}
}

func TestImportMapsFlagValidationAndHelp(t *testing.T) {
	t.Setenv("DRBRAIN_DATABASE_URL", "")
	var out, errOut bytes.Buffer
	if code := runImportMaps([]string{"--dir=" + t.TempDir()}, &out, &errOut); code != 2 {
		t.Fatalf("missing game default should still need a database, code = %d", code)
	}
	if !strings.Contains(errOut.String(), "DRBRAIN_DATABASE_URL") {
		t.Fatalf("missing database error = %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	if code := runImportMaps([]string{"--game=music", "--dir=" + t.TempDir(), "--db=x"}, &out, &errOut); code != 2 {
		t.Fatalf("bad game code = %d", code)
	}
	if !strings.Contains(errOut.String(), "tesla or delivery") {
		t.Fatalf("bad game error = %s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	_ = runImportMaps([]string{"-h"}, &out, &errOut)
	if !strings.Contains(errOut.String(), "cutover order") {
		t.Fatalf("help does not document the cutover order: %s", errOut.String())
	}
}
