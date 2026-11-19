package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// newTestManager builds a manager over a temp dir and the repo city.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	city, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := NewManager(city, t.TempDir())
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func TestSaveLoadRoundTripPreservesState(t *testing.T) {
	mgr := newTestManager(t)

	sess, err := mgr.Create("rt", 7)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// Stop the ticker so the test controls the clock exactly.
	if err := sess.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}

	// A little history: one tick (which spawns the forced first order), then a
	// queued and applied move.
	sess.Do(func(w *engine.World) {
		w.Advance()
	})
	sess.Do(func(w *engine.World) {
		_ = w.QueueMove(w.Cars[0].ID, engine.South)
		w.Advance()
	})

	var before engine.Position
	var beforeTick int64
	var beforeBattery int
	var beforeOrders int
	sess.Do(func(w *engine.World) {
		before = w.Cars[0].Pos
		beforeTick = w.Tick
		beforeBattery = w.Cars[0].Battery
		beforeOrders = len(w.Orders)
	})

	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mgr.dir, "rt.json")); err != nil {
		t.Fatalf("save file missing: %v", err)
	}

	// Evict and reload from disk.
	mgr.Evict("rt")
	reloaded, err := mgr.GetOrLoad("rt")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	var after engine.Position
	var afterTick int64
	var afterBattery int
	var afterOrders int
	reloaded.Do(func(w *engine.World) {
		after = w.Cars[0].Pos
		afterTick = w.Tick
		afterBattery = w.Cars[0].Battery
		afterOrders = len(w.Orders)
	})

	if after != before {
		t.Errorf("car position %v -> %v", before, after)
	}
	if afterTick != beforeTick {
		t.Errorf("tick %d -> %d", beforeTick, afterTick)
	}
	if afterBattery != beforeBattery {
		t.Errorf("battery %d -> %d", beforeBattery, afterBattery)
	}
	if afterOrders != beforeOrders {
		t.Errorf("orders %d -> %d", beforeOrders, afterOrders)
	}
}

func TestClassNameSurvivesPersistence(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("cls", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}

	// The van is money-only (requires_grant is false), which is what keeps a
	// second car reachable without a build.
	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 5000
	})
	sess.Do(func(w *engine.World) {
		if _, err := w.BuyCar("van", "d_main"); err != nil {
			t.Errorf("buy van: %v", err)
		}
	})

	snap := sess.Snapshot()
	if len(snap.Cars) != 2 {
		t.Fatalf("cars = %d, want 2", len(snap.Cars))
	}
	var vanClass string
	for _, c := range snap.Cars {
		if c.Name != "Scooter One" {
			vanClass = c.Class
		}
	}
	if vanClass != "van" {
		t.Fatalf("van class = %q, want \"van\"", vanClass)
	}

	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("cls")

	reloaded, err := mgr.GetOrLoad("cls")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	after := reloaded.Snapshot()
	for _, c := range after.Cars {
		if c.Name == "Scooter One" {
			continue
		}
		if c.Class != "van" {
			t.Errorf("van class after reload = %q, want \"van\"", c.Class)
		}
	}
}

// TestBuildTerrainSurvivesPersistence checks the TILES a build rewrote, which
// is a different question from the entities it added (see
// TestBuildAddedEntitiesSurviveReload). Both come back because the save replays
// completed builds rather than storing either list.
func TestBuildTerrainSurvivesPersistence(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("deltas", 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}

	// Build the east bridge by granting the unlock and the money directly, so
	// the test isolates the persistence question from the progression grind.
	const buildID = "b02_east_bridge"
	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 5000
		w.Grant([]string{buildID}, nil)
	})
	sess.Do(func(w *engine.World) {
		if _, err := w.Build(buildID); err != nil {
			t.Fatalf("build: %v", err)
		}
	})

	// Record the actual TERRAIN at the bridge tiles before saving. RenderRows
	// overlays entity glyphs, and the build also adds a charger on this row, so
	// comparing rendered rows would fail for a reason unrelated to terrain.
	// Read the grid directly, at the coordinates the build itself declares.
	terrainAt := func(w *engine.World, x, y int) engine.CellType {
		t, _ := w.City.At(engine.Position{X: x, Y: y})
		return t
	}
	// The bridge tiles come from the shipped city config, not from a literal:
	// the map is generated and its coordinates move when the layout changes.
	bridgeTiles := engine.BridgeTilesForTest(mgr.CityFile(), buildID)
	if len(bridgeTiles) == 0 {
		t.Fatalf("build %s declares no bridge tiles", buildID)
	}
	got := make([]engine.CellType, len(bridgeTiles))
	sess.Do(func(w *engine.World) {
		for i, tw := range bridgeTiles {
			got[i] = terrainAt(w, tw.X, tw.Y)
		}
	})
	for i, tw := range bridgeTiles {
		if got[i] != engine.RoadTile {
			t.Fatalf("bridge not built in memory: (%d,%d)=%s", tw.X, tw.Y, got[i])
		}
	}

	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("deltas")

	reloaded, err := mgr.GetOrLoad("deltas")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var after []engine.CellType
	var completed bool
	reloaded.Do(func(w *engine.World) {
		for _, tw := range bridgeTiles {
			after = append(after, terrainAt(w, tw.X, tw.Y))
		}
		for _, b := range w.Builds() {
			if b.ID == buildID {
				completed = b.Completed
			}
		}
	})

	for i, tw := range bridgeTiles {
		if after[i] != got[i] {
			t.Errorf("terrain not restored at (%d,%d): %s -> %s", tw.X, tw.Y, got[i], after[i])
		}
	}
	if !completed {
		t.Error("build not marked completed after reload")
	}
}

func TestSaveIsAtomicAcrossRepeatedWrites(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("atomic", 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	for range 5 {
		sess.Do(func(w *engine.World) { w.Advance() })
		if err := sess.Save(mgr.CityFileName()); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	// No temp files may be left behind.
	entries, err := os.ReadDir(mgr.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("leftover temp file %q", e.Name())
		}
	}
}

func TestSaveAllPersistsEveryLiveSession(t *testing.T) {
	mgr := newTestManager(t)
	for _, id := range []string{"s1", "s2", "s3"} {
		sess, err := mgr.Create(id, 1)
		if err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		_ = sess.SetSpeed(SpeedPaused)
		sess.Do(func(w *engine.World) { w.Advance() })
	}

	if err := mgr.SaveAll(); err != nil {
		t.Fatalf("save all: %v", err)
	}
	for _, id := range []string{"s1", "s2", "s3"} {
		if _, err := os.Stat(filepath.Join(mgr.dir, id+".json")); err != nil {
			t.Errorf("%s not saved: %v", id, err)
		}
	}
}

func TestListIncludesDiskOnlySessions(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("ondisk", 5)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) { w.Advance() })
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("ondisk")

	list := mgr.List()
	var found *Summary
	for i := range list {
		if list[i].ID == "ondisk" {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatal("disk-only session missing from List")
	}
	if found.Loaded {
		t.Error("disk-only session reported as loaded")
	}
	if found.Tick == 0 {
		t.Error("summary has no tick")
	}
}

func TestDeleteRemovesSaveFile(t *testing.T) {
	mgr := newTestManager(t)
	if _, err := mgr.Create("gone", 1); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete("gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mgr.dir, "gone.json")); !os.IsNotExist(err) {
		t.Error("save file still present after Delete")
	}
	if _, err := mgr.GetOrLoad("gone"); err == nil {
		t.Error("expected reload of deleted session to fail")
	}
}

func TestEvictKeepsSaveFile(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("kept", 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("kept")

	if _, err := os.Stat(filepath.Join(mgr.dir, "kept.json")); err != nil {
		t.Fatalf("evict removed the save: %v", err)
	}
	if _, err := mgr.GetOrLoad("kept"); err != nil {
		t.Fatalf("reload after evict: %v", err)
	}
}

func TestCorruptSaveIsReportedNotFatal(t *testing.T) {
	mgr := newTestManager(t)
	path := filepath.Join(mgr.dir, "broken.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.GetOrLoad("broken"); err == nil {
		t.Fatal("expected corrupt save to be reported")
	}
	// List must skip it rather than panicking.
	for _, s := range mgr.List() {
		if s.ID == "broken" {
			t.Error("corrupt save appeared in List")
		}
	}
}

func TestSaveWithoutDirIsNoOp(t *testing.T) {
	// A session created with no directory (as in unit tests) must not try to
	// write anywhere.
	city, cfg := testCity(t)
	s, err := New("nodir", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save("city.json"); err != nil {
		t.Fatalf("save without dir returned error: %v", err)
	}
}

func TestPersistedFileHasExpectedShape(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("shape", 9)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) { w.Advance() })
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}

	blob, err := os.ReadFile(filepath.Join(mgr.dir, "shape.json"))
	if err != nil {
		t.Fatal(err)
	}
	var p persisted
	if err := json.Unmarshal(blob, &p); err != nil {
		t.Fatalf("save is not valid persisted JSON: %v", err)
	}
	if p.ID != "shape" {
		t.Errorf("id = %q", p.ID)
	}
	if p.CityFile != "city.json" {
		t.Errorf("city_file = %q, want city.json (save stores the name, not a grid)", p.CityFile)
	}
	if p.World.Seed != 9 {
		t.Errorf("seed = %d, want 9", p.World.Seed)
	}
	if p.World.Ledger == nil {
		t.Error("ledger not persisted")
	}
	if len(p.World.Cars) == 0 {
		t.Error("cars not persisted")
	}
}
