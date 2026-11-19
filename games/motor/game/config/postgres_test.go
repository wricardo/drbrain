package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/wricardo/drbrain/games/motor/game/engine"
	"github.com/wricardo/drbrain/games/motor/game/service"
	"github.com/wricardo/drbrain/games/motor/internal/pgdb/pgtest"
)

func jsonIndent(c engine.MapConfig) ([]byte, error) { return json.MarshalIndent(c, "", "  ") }

// pgManager builds a Manager whose shipped maps are files in a temp dir and
// whose player maps are in PostgreSQL.
func pgManager(t *testing.T, shipped []string, max int) (*Manager, *PostgresUserMaps, string) {
	t.Helper()
	dir := t.TempDir()
	for _, id := range shipped {
		data, err := jsonIndent(cfg(id))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := NewPostgresUserMaps(pgtest.Pool(t))
	m, err := NewManager(Options{Dir: dir, UserStore: store, MaxUserMaps: max})
	if err != nil {
		t.Fatal(err)
	}
	return m, store, dir
}

func TestPostgresMapsSurviveRestart(t *testing.T) {
	m, store, dir := pgManager(t, []string{"shipped"}, 0)
	if _, err := m.Create(cfg("mine")); err != nil {
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

	m2, err := NewManager(Options{Dir: dir, UserStore: store})
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
	m, store, _ := pgManager(t, nil, 0)
	if _, err := m.Create(cfg("dup")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("dup")); err == nil {
		t.Fatal("second Create of the same id succeeded")
	}

	changed := cfg("dup")
	changed.Name = "Renamed"
	if _, err := m.Save(changed); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: t.TempDir(), UserStore: store})
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
		go func() { defer wg.Done(); _, err := m.Create(cfg("race")); results <- err }()
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
	m, store, _ := pgManager(t, []string{"shipped"}, 2)
	for _, id := range []string{"one", "two"} {
		if _, err := m.Create(cfg(id)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.Create(cfg("three"))
	var se *service.Error
	if !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("third map = %v, want LIMIT_REACHED", err)
	}
	if err := m.Delete("one"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Create(cfg("three")); err != nil {
		t.Fatalf("create after delete: %v", err)
	}
	if err := m.Delete("one"); err == nil {
		t.Fatal("deleting an absent map succeeded")
	}
	// Shipped maps do not count against the cap.
	cfgs, err := store.List()
	if err != nil || len(cfgs) != 2 {
		t.Fatalf("stored maps = %d, %v", len(cfgs), err)
	}
}

func TestPostgresPlayerMapCannotTakeAShippedId(t *testing.T) {
	m, store, dir := pgManager(t, []string{"shipped"}, 0)
	if _, err := m.Create(cfg("shipped")); err == nil {
		t.Fatal("Create over a shipped map succeeded")
	}
	// A row that already holds a shipped id is ignored on load, like a file would be.
	if err := store.Put(cfg("shipped")); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: dir, UserStore: store})
	if err != nil {
		t.Fatal(err)
	}
	if !m2.Builtin("shipped") || len(m2.List()) != 1 {
		t.Fatalf("shipped map was shadowed: builtin=%v maps=%d", m2.Builtin("shipped"), len(m2.List()))
	}
}

func TestPostgresBadRowIsSkipped(t *testing.T) {
	m, store, dir := pgManager(t, nil, 0)
	if _, err := m.Create(cfg("good")); err != nil {
		t.Fatal(err)
	}
	if err := store.Put(engine.MapConfig{ID: "broken", Name: "no layout"}); err != nil {
		t.Fatal(err)
	}
	m2, err := NewManager(Options{Dir: dir, UserStore: store})
	if err != nil {
		t.Fatalf("one bad row stopped the server: %v", err)
	}
	if _, err := m2.Get("good"); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Get("broken"); err == nil {
		t.Fatal("invalid stored map was served")
	}
}

// Editing a shipped map is still the admin path and still writes the file.
func TestPostgresAdminEditOfShippedMapStaysAFile(t *testing.T) {
	m, store, dir := pgManager(t, []string{"shipped"}, 0)
	edited := cfg("shipped")
	edited.Name = "Edited"
	if _, err := m.Save(edited); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "shipped.json"))
	if err != nil || len(raw) == 0 {
		t.Fatalf("shipped file: %v", err)
	}
	cfgs, _ := store.List()
	if len(cfgs) != 0 {
		t.Fatalf("shipped map leaked into the database: %v", fmt.Sprint(cfgs))
	}
}
