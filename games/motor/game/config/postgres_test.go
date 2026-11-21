package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// pgManager builds a Manager whose shipped maps are files in a temp dir and
// whose player maps are rows in platform_maps.
func pgManager(t *testing.T, shipped []string, max int) (*Manager, *pgxpool.Pool, string) {
	t.Helper()
	dir := t.TempDir()
	for _, id := range shipped {
		data, err := json.Marshal(cfg(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pool := pgtest.Pool(t)
	m, err := NewManager(Options{Dir: dir, Pool: pool, MaxUserMaps: max})
	if err != nil {
		t.Fatal(err)
	}
	return m, pool, dir
}

func TestPostgresMapsSurviveRestart(t *testing.T) {
	m, pool, dir := pgManager(t, []string{"shipped"}, 0)
	if _, err := m.Create(cfg("mine"), "tester"); err != nil {
		t.Fatal(err)
	}
	if m.Builtin("mine") || !m.Builtin("shipped") {
		t.Fatalf("Builtin: mine=%v shipped=%v", m.Builtin("mine"), m.Builtin("shipped"))
	}
	// Nothing was written next to the shipped maps.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("maps dir has %d entries, want only the shipped map", len(entries))
	}

	m2, err := NewManager(Options{Dir: dir, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m2.Get("mine")
	if err != nil {
		t.Fatalf("player map lost over a restart: %v", err)
	}
	if got.Name != "T" || m2.Builtin("mine") {
		t.Fatalf("restored map = %+v, builtin %v", got, m2.Builtin("mine"))
	}
	if len(m2.List()) != 2 {
		t.Fatalf("List = %d maps, want shipped + player", len(m2.List()))
	}
}

func TestPostgresMapCreateIsAtomicAndSaveReplaces(t *testing.T) {
	m, pool, _ := pgManager(t, nil, 0)
	if _, err := m.Create(cfg("dup"), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("dup"), "tester"); err == nil {
		t.Fatal("second Create of the same id succeeded")
	}

	changed := cfg("dup")
	changed.Name = "Renamed"
	if _, err := m.Save(changed, player("tester")); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: t.TempDir(), Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m2.Get("dup"); err != nil || got.Name != "Renamed" {
		t.Fatalf("saved name = %v, %v", got, err)
	}
}

func TestPostgresMapConcurrentCreateOneWinner(t *testing.T) {
	m, _, _ := pgManager(t, nil, 0)
	var wg sync.WaitGroup
	results := make(chan error, 10)
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := m.Create(cfg("race"), "tester"); results <- err }()
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent creates succeeded, want 1", wins)
	}
}

func TestPostgresMapLimitAndDelete(t *testing.T) {
	m, pool, _ := pgManager(t, []string{"shipped"}, 2)
	for _, id := range []string{"one", "two"} {
		if _, err := m.Create(cfg(id), "tester"); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.Create(cfg("three"), "tester")
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("third map = %v, want LIMIT_REACHED", err)
	}
	if err := m.Delete("one", player("tester")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("three"), "tester"); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if err := m.Delete("one", player("tester")); err == nil {
		t.Fatal("deleting an absent map succeeded")
	}
	// Shipped maps do not count against the cap.
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM platform_maps WHERE game = 'motor'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("stored motor maps = %d, want 2", n)
	}
}

func TestPostgresPlayerMapCannotTakeAShippedId(t *testing.T) {
	m, pool, dir := pgManager(t, []string{"shipped"}, 0)
	if _, err := m.Create(cfg("shipped"), "tester"); err == nil {
		t.Fatal("Create over a shipped map succeeded")
	}
	// A row that already holds a shipped id is ignored on load, like a file would be.
	if _, err := pool.Exec(t.Context(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('motor', 'shipped', $1::jsonb, 'tester', 'PUBLISHED')`,
		mustJSON(t, cfg("shipped"))); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: dir, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Builtin("shipped") || len(m2.List()) != 1 {
		t.Fatalf("shipped map was shadowed: builtin=%v maps=%d", m2.Builtin("shipped"), len(m2.List()))
	}
}

func TestPostgresBadRowIsSkipped(t *testing.T) {
	m, pool, dir := pgManager(t, nil, 0)
	if _, err := m.Create(cfg("good"), "tester"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('motor', 'broken', '{"id":"broken","name":"no layout"}'::jsonb, 'tester', 'DRAFT')`); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: dir, Pool: pool})
	if err != nil {
		t.Fatalf("one bad row stopped the server: %v", err)
	}
	if _, err := m2.Get("good"); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Get("broken"); err == nil {
		t.Fatal("invalid stored map was served")
	}
	if len(m2.List()) != 1 {
		t.Fatalf("List included the invalid row: %d maps", len(m2.List()))
	}
}

// Editing a shipped map is still the admin path and still writes the file.
func TestPostgresAdminEditOfShippedMapStaysAFile(t *testing.T) {
	m, pool, dir := pgManager(t, []string{"shipped"}, 0)
	edited := cfg("shipped")
	edited.Name = "Edited"
	if _, err := m.Save(edited, player("tester")); err == nil {
		t.Fatal("a player edited a shipped map without the admin key")
	}
	if _, err := m.Save(edited, admin()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "shipped.json"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("shipped file: %v", err)
	}
	var n int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM platform_maps WHERE game = 'motor'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("shipped map leaked into platform_maps: %d rows", n)
	}
}

// DetachAccount removes drafts and orphans published maps, which is what the
// account service runs before deleting the account row.
func TestPostgresDetachAccountRemovesDraftsAndOrphansPublished(t *testing.T) {
	m, _, _ := pgManager(t, nil, 0)
	if _, err := m.Create(cfg("draft"), "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("published"), "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("published", player("alice")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("other"), "bob"); err != nil {
		t.Fatal(err)
	}
	if err := m.DetachAccount("alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Get("draft"); err == nil {
		t.Fatal("draft survived account deletion")
	}
	e, err := m.Entry("published")
	if err != nil {
		t.Fatal(err)
	}
	if e.OwnerID != "" || e.Status != engine.MapStatusPublished {
		t.Fatalf("published map = %+v, want ownerless PUBLISHED", e)
	}
	if e, err := m.Entry("other"); err != nil || e.OwnerID != "bob" {
		t.Fatalf("other player's map = %+v, %v", e, err)
	}
}

// The migration copies legacy rows with their metadata split into columns and
// leaves the maps table in place. Re-running it changes nothing.
func TestMotorMapsMigration(t *testing.T) {
	pool := pgtest.Pool(t)
	ctx := t.Context()
	seeds := []struct {
		id     string
		config string
	}{
		{"legacy_plain", `{"id":"legacy_plain","name":"Plain","layout":["....",".>.*","...."]}`},
		{"owned_draft", `{"id":"owned_draft","name":"Draft","layout":["....",".>.*","...."],"owner_id":"alice","status":"DRAFT"}`},
		{"owned_pub", `{"id":"owned_pub","name":"Pub","layout":["....",".>.*","...."],"owner_id":"bob","status":"PUBLISHED"}`},
		{"odd_status", `{"id":"odd_status","name":"Odd","layout":["....",".>.*","...."],"owner_id":"carol","status":"ARCHIVED"}`},
	}
	for _, s := range seeds {
		if _, err := pool.Exec(ctx,
			`INSERT INTO maps (id, config) VALUES ($1, $2::jsonb)`, s.id, s.config); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile("../../../../platform/pgdb/migrations/006_motor_maps_to_platform_maps.sql")
	if err != nil {
		t.Fatal(err)
	}
	for run := 1; run <= 2; run++ {
		if _, err := pool.Exec(ctx, string(body)); err != nil {
			t.Fatalf("apply migration (run %d): %v", run, err)
		}
	}
	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM platform_maps WHERE game = 'motor'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(seeds) {
		t.Fatalf("motor rows = %d, want %d", count, len(seeds))
	}
	want := map[string]struct{ owner, status string }{
		"legacy_plain": {"", "PUBLISHED"},
		"owned_draft":  {"alice", "DRAFT"},
		"owned_pub":    {"bob", "PUBLISHED"},
		"odd_status":   {"carol", "PUBLISHED"},
	}
	rows, err := pool.Query(ctx,
		`SELECT id, config, owner_id, status FROM platform_maps WHERE game = 'motor'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, owner, status string
		var raw []byte
		if err := rows.Scan(&id, &raw, &owner, &status); err != nil {
			t.Fatal(err)
		}
		w, ok := want[id]
		if !ok || owner != w.owner || status != w.status {
			t.Errorf("%s = owner %q status %q, want %+v", id, owner, status, w)
		}
		var fields map[string]any
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
		if _, ok := fields["owner_id"]; ok {
			t.Errorf("%s config kept owner_id", id)
		}
		if _, ok := fields["status"]; ok {
			t.Errorf("%s config kept status", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	var legacy int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM maps`).Scan(&legacy); err != nil {
		t.Fatal(err)
	}
	if legacy != len(seeds) {
		t.Fatalf("legacy maps rows = %d, want %d untouched rows", legacy, len(seeds))
	}

	// ON CONFLICT DO NOTHING: a rerun leaves later edits alone.
	if _, err := pool.Exec(ctx,
		`UPDATE platform_maps SET owner_id = 'changed' WHERE game = 'motor' AND id = 'owned_pub'`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := pool.QueryRow(ctx,
		`SELECT owner_id FROM platform_maps WHERE game = 'motor' AND id = 'owned_pub'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != "changed" {
		t.Fatalf("rerun overwrote existing row: owner = %q", owner)
	}
}

func mustJSON(t *testing.T, cfg engine.MapConfig) string {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
