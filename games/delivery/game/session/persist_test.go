package session

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	cityconfig "github.com/wricardo/drbrain/games/delivery/game/config"
	"github.com/wricardo/drbrain/games/delivery/game/engine"
	"github.com/wricardo/drbrain/platform/pgdb/pgtest"
	"github.com/wricardo/drbrain/platform/sessionstore"
)

// newTestManager builds a manager over a fresh database schema and the repo
// city. The schema is isolated per test, so tests never share sessions.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	mgr, err := NewManager(newTestCatalog(t), pgtest.Pool(t))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

// newTestCatalog serves the repo's shipped city as the "city" builtin. It has
// no pool: these tests exercise sessions, not community cities.
func newTestCatalog(t *testing.T) CityCatalog {
	t.Helper()
	cat, err := cityconfig.Open(cityconfig.Options{
		BuiltinDir:    filepath.Dir(testCityPath(t)),
		DefaultCityID: "city",
	})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	return cat
}

// newMemoryManager is newTestManager without a database: saves live for the
// process only, which is enough for ownership and payload-shape tests.
func newMemoryManager(t *testing.T) *Manager {
	t.Helper()
	mgr := &Manager{
		catalog:  newTestCatalog(t),
		store:    sessionstore.NewMemory(sessionstore.Options{}),
		sessions: map[string]*Session{},
	}
	t.Cleanup(func() { _ = mgr.Close() })
	return mgr
}

func testCityPath(t *testing.T) string {
	t.Helper()
	city, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	return city
}

// storedPayload returns a session's durable payload.
func storedPayload(t *testing.T, mgr *Manager, id string) []byte {
	t.Helper()
	_, blob, err := mgr.store.Load(id)
	if err != nil {
		t.Fatalf("load stored %s: %v", id, err)
	}
	return blob
}

// rewritePayload edits a stored payload in place, standing in for a hand edit
// or for a save written by an older build.
func rewritePayload(t *testing.T, mgr *Manager, id string, fn func(map[string]any)) {
	t.Helper()
	meta, blob, err := mgr.store.Load(id)
	if err != nil {
		t.Fatalf("load stored %s: %v", id, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatalf("parse stored %s: %v", id, err)
	}
	fn(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.store.Persist(meta, out); err != nil {
		t.Fatalf("persist %s: %v", id, err)
	}
}

func TestSaveLoadRoundTripPreservesState(t *testing.T) {
	mgr := newTestManager(t)

	sess, _, err := mgr.Create("rt", 7, "", "")
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

	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	_ = storedPayload(t, mgr, "rt")

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
	sess, _, err := mgr.Create("cls", 1, "", "")
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

	if err := sess.Save(); err != nil {
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
		if c.PurchasePrice <= 0 {
			t.Errorf("van purchase price after reload = %d, want the paid price", c.PurchasePrice)
		}
		if c.ResaleValue != c.PurchasePrice*engine.CarResalePercent/100 {
			t.Errorf("van resale = %d for a %d purchase, want 70%%", c.ResaleValue, c.PurchasePrice)
		}
	}
}

// TestGrowthTerrainSurvivesPersistence checks the TILES the schedule rewrote,
// which is a different question from the entities it added. Both come back
// because ImportState replays the schedule by tick rather than storing either.
func TestGrowthTerrainSurvivesPersistence(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("deltas", 3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}

	// Apply the whole schedule: the persistence question is whether its terrain
	// writes survive, not whether the player can afford them.
	sess.Do(advancePastGrowth)

	// Record the actual TERRAIN at the bridge tiles before saving. RenderRows
	// overlays entity glyphs, and the stage also adds a charger on this row, so
	// comparing rendered rows would fail for a reason unrelated to terrain.
	// Read the grid directly, at the coordinates the stage itself declares.
	terrainAt := func(w *engine.World, x, y int) engine.CellType {
		t, _ := w.City.At(engine.Position{X: x, Y: y})
		return t
	}
	// The bridge tiles come from the shipped city config, not from a literal:
	// the map is generated and its coordinates move when the layout changes.
	bridgeTiles := engine.BridgeTilesForTest(testCityPath(t), "g02_harbour_gate")
	if len(bridgeTiles) == 0 {
		t.Fatalf("growth stage declares no bridge tiles")
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

	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("deltas")

	reloaded, err := mgr.GetOrLoad("deltas")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	var after []engine.CellType
	reloaded.Do(func(w *engine.World) {
		for _, tw := range bridgeTiles {
			after = append(after, terrainAt(w, tw.X, tw.Y))
		}
	})

	for i, tw := range bridgeTiles {
		if after[i] != got[i] {
			t.Errorf("terrain not restored at (%d,%d): %s -> %s", tw.X, tw.Y, got[i], after[i])
		}
	}
}

func TestSaveAllPersistsEveryLiveSession(t *testing.T) {
	mgr := newTestManager(t)
	for _, id := range []string{"s1", "s2", "s3"} {
		sess, _, err := mgr.Create(id, 1, "", "")
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
		if _, _, err := mgr.store.Load(id); err != nil {
			t.Errorf("%s not saved: %v", id, err)
		}
	}
}

func TestListIncludesUnloadedSessions(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("unloaded", 5, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) { w.Advance() })
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("unloaded")

	list := mgr.List(ListOptions{})
	var found *Summary
	for i := range list {
		if list[i].ID == "unloaded" {
			found = &list[i]
		}
	}
	if found == nil {
		t.Fatal("unloaded session missing from List")
	}
	if found.Loaded {
		t.Error("unloaded session reported as loaded")
	}
	if found.Tick == 0 {
		t.Error("summary has no tick")
	}
}

func TestDeleteRemovesStoredSession(t *testing.T) {
	mgr := newTestManager(t)
	if _, _, err := mgr.Create("gone", 1, "", ""); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Delete("gone"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := mgr.store.Load("gone"); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Errorf("stored session still present after Delete: %v", err)
	}
	if _, err := mgr.GetOrLoad("gone"); err == nil {
		t.Error("expected reload of deleted session to fail")
	}
}

func TestEvictKeepsStoredSession(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("kept", 1, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("kept")

	if _, _, err := mgr.store.Load("kept"); err != nil {
		t.Fatalf("evict removed the stored session: %v", err)
	}
	if _, err := mgr.GetOrLoad("kept"); err != nil {
		t.Fatalf("reload after evict: %v", err)
	}
}

func TestSessionWithoutStoreSaveIsNoOp(t *testing.T) {
	// A session built directly with New (as in unit tests) has no store; Save
	// must be a no-op rather than a nil dereference.
	city, cfg := testCity(t)
	s, err := New("nostore", city, cfg, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Save(); err != nil {
		t.Fatalf("save without a store returned error: %v", err)
	}
}

func TestPersistedPayloadHasExpectedShape(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("shape", 9, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) { w.Advance() })
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	var p persisted
	if err := json.Unmarshal(storedPayload(t, mgr, "shape"), &p); err != nil {
		t.Fatalf("save is not valid persisted JSON: %v", err)
	}
	if p.ID != "shape" {
		t.Errorf("id = %q", p.ID)
	}
	if p.CityID != "city" {
		t.Errorf("city_id = %q, want city (save stores the id, not a grid)", p.CityID)
	}
	if p.CityFile != "" {
		t.Errorf("city_file = %q, want empty (legacy field is read-only)", p.CityFile)
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
