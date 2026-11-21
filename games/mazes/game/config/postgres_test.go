package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/mazes/game/engine"
	"github.com/wricardo/drbrain/games/mazes/game/service"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
)

// unwinnableMaze is structurally valid but the finish is walled off by void.
func unwinnableMaze(id string) engine.MapConfig {
	return engine.MapConfig{
		ID: id, Name: "trapped",
		Levels: [][]string{{"So.", "o..", "..F"}},
	}
}

// solvableMaze is the same size with a clear route to the finish.
func solvableMaze(id string) engine.MapConfig {
	return engine.MapConfig{
		ID: id, Name: "open",
		Levels: [][]string{{"SoF", "ooo", "ooo"}},
	}
}

// pgManager builds a Manager whose shipped maps are files in a temp dir and
// whose player maps are in PostgreSQL.
func pgManager(t *testing.T, max int) (*Manager, string) {
	t.Helper()
	return pgManagerOn(t, pgtest.Pool(t), max)
}

func pgManagerOn(t *testing.T, pool *pgxpool.Pool, max int) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	m, err := NewManager(Options{Dir: dir, Pool: pool, MaxUserMaps: max})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, dir
}

func TestPGDraftCreateReloadAndStructuralSave(t *testing.T) {
	pool := pgtest.Pool(t)
	m, dir := pgManagerOn(t, pool, 0)

	token, err := m.CreateDraft(cfg("mine"), "owner-a")
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if token != "" {
		t.Errorf("mazes must not issue edit tokens, got %q", token)
	}

	// A fresh manager (same pool, same shipped dir) sees the durable row.
	m2, err := NewManager(Options{Dir: dir, Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := m2.GetEntry("mine")
	if err != nil {
		t.Fatalf("GetEntry: %v", err)
	}
	if entry.Builtin || entry.OwnerID != "owner-a" || entry.Status != service.MapStatusDraft {
		t.Fatalf("entry = %+v", entry)
	}

	// Drafts only need to be structurally sound; finish reachability is the
	// publication gate.
	if _, err := m2.CreateDraft(unwinnableMaze("trapped"), "owner-a"); err != nil {
		t.Fatalf("structurally valid draft rejected: %v", err)
	}
	if _, err := m2.GetEntry("trapped"); err != nil {
		t.Fatalf("unwinnable draft must load: %v", err)
	}

	// A structurally broken draft is refused with INVALID_ARGUMENT.
	broken := cfg("broken")
	broken.Levels = [][]string{{"...", "...", "..."}}
	var se *service.Error
	if _, err := m2.CreateDraft(broken, "owner-a"); !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
		t.Fatalf("structurally invalid draft = %v, want INVALID_ARGUMENT", err)
	}
}

func TestPGPublishGate(t *testing.T) {
	m, _ := pgManager(t, 0)

	if _, err := m.CreateDraft(unwinnableMaze("trapped"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	var se *service.Error
	if _, err := m.Publish("trapped", service.MapActor{AccountID: "owner-a"}); !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
		t.Fatalf("publishing an unwinnable map = %v, want INVALID_ARGUMENT", err)
	}

	if _, err := m.CreateDraft(solvableMaze("good"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	entry, err := m.Publish("good", service.MapActor{AccountID: "owner-a"})
	if err != nil {
		t.Fatalf("publishing a solvable map: %v", err)
	}
	if entry.Status != service.MapStatusPublished {
		t.Fatalf("status = %q, want PUBLISHED", entry.Status)
	}
	if _, err := m.Publish("good", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatalf("republish: %v", err)
	}
}

func TestPGOwnerGates(t *testing.T) {
	m, _ := pgManager(t, 0)
	if _, err := m.CreateDraft(solvableMaze("good"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("good", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}

	other := service.MapActor{AccountID: "owner-b"}
	var se *service.Error
	if _, err := m.SaveOwned(solvableMaze("good"), other); !errors.As(err, &se) || se.Code != service.CodeForbidden {
		t.Errorf("other owner save = %v, want FORBIDDEN", err)
	}
	if _, err := m.Publish("good", other); !errors.As(err, &se) || se.Code != service.CodeForbidden {
		t.Errorf("other owner publish = %v, want FORBIDDEN", err)
	}
	if err := m.DeleteOwned("good", other); !errors.As(err, &se) || se.Code != service.CodeForbidden {
		t.Errorf("other owner delete = %v, want FORBIDDEN", err)
	}

	// The owner editing a published map puts it back into review.
	updated := solvableMaze("good")
	updated.Name = "renamed"
	entry, err := m.SaveOwned(updated, service.MapActor{AccountID: "owner-a"})
	if err != nil {
		t.Fatalf("owner save: %v", err)
	}
	if entry.Status != service.MapStatusDraft {
		t.Errorf("owner save status = %q, want DRAFT", entry.Status)
	}
	if err := m.DeleteOwned("good", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Errorf("owner delete: %v", err)
	}
}

func TestPGQuota(t *testing.T) {
	m, _ := pgManager(t, 2)
	if _, err := m.CreateDraft(solvableMaze("one"), "o"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft(solvableMaze("two"), "o"); err != nil {
		t.Fatal(err)
	}
	var se *service.Error
	if _, err := m.CreateDraft(solvableMaze("three"), "o"); !errors.As(err, &se) || se.Code != service.CodeLimit {
		t.Fatalf("third draft = %v, want LIMIT_REACHED", err)
	}
}

func TestPGCorruptRowIsSkipped(t *testing.T) {
	pool := pgtest.Pool(t)
	m, _ := pgManagerOn(t, pool, 0)
	if _, err := m.CreateDraft(solvableMaze("good"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	// A row that decodes but fails the engine's structural checks must not
	// break listing; asking for it reports the problem.
	if _, err := pool.Exec(t.Context(), `
INSERT INTO platform_maps (game, id, config, owner_id, status)
VALUES ('mazes', 'broken', '{"id":"broken","name":"x"}'::jsonb, 'owner-a', 'PUBLISHED')`); err != nil {
		t.Fatal(err)
	}

	entries, err := m.ListEntries("owner-a")
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	for _, e := range entries {
		if e.ID == "broken" {
			t.Fatal("corrupt row was listed")
		}
	}
	if len(entries) != 1 || entries[0].ID != "good" {
		t.Fatalf("entries = %+v", entries)
	}

	var se *service.Error
	if _, err := m.GetEntry("broken"); !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
		t.Fatalf("GetEntry(corrupt) = %v, want INVALID_ARGUMENT", err)
	}
}

func TestPGCustomCannotShadowShipped(t *testing.T) {
	dir := t.TempDir()
	raw, err := json.Marshal(cfg("shipped"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "shipped.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewManager(Options{Dir: dir, Pool: pgtest.Pool(t)})
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsBuiltin("shipped") {
		t.Fatal("shipped map not reported as builtin")
	}

	var se *service.Error
	if _, err := m.CreateDraft(solvableMaze("shipped"), "owner-a"); !errors.As(err, &se) || se.Code != service.CodeInvalidArgument {
		t.Fatalf("draft shadowing a shipped map = %v, want INVALID_ARGUMENT", err)
	}
}

// PostgreSQL mode never writes player maps next to the shipped ones.
func TestPGWritesNoFiles(t *testing.T) {
	m, dir := pgManager(t, 0)
	if _, err := m.CreateDraft(solvableMaze("mine"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("shipped dir after draft = %v, want empty", names)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom")); !os.IsNotExist(err) {
		t.Fatalf("PG mode created a custom dir: %v", err)
	}
}

// Account deletion removes drafts and orphans published maps.
func TestPGAccountDeletionHooks(t *testing.T) {
	m, _ := pgManager(t, 0)
	if _, err := m.CreateDraft(solvableMaze("draft-a"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateDraft(solvableMaze("pub-a"), "owner-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Publish("pub-a", service.MapActor{AccountID: "owner-a"}); err != nil {
		t.Fatal(err)
	}

	if err := m.DeleteDraftsByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}
	if err := m.OrphanPublishedByOwner("owner-a"); err != nil {
		t.Fatal(err)
	}

	var se *service.Error
	if _, err := m.GetEntry("draft-a"); !errors.As(err, &se) || se.Code != service.CodeNotFound {
		t.Errorf("draft after account deletion = %v, want NOT_FOUND", err)
	}
	orphaned, err := m.GetEntry("pub-a")
	if err != nil {
		t.Fatalf("published map must survive: %v", err)
	}
	if orphaned.OwnerID != "" || orphaned.Status != service.MapStatusPublished {
		t.Errorf("orphaned entry = %+v", orphaned)
	}
}
