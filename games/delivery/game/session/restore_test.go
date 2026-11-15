package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wricardo/drbrain-delivery/game/engine"
)

// Persistence used to drop anything the restore path did not enumerate: build
// added chargers, district reveals, achievement flags and the order spawner's
// memory all vanished on reload while every individual rule looked correct.
// These tests save, evict and reload, then assert the observable state is
// identical — the property the save format exists to provide.

func reload(t *testing.T, mgr *Manager, id string) *Session {
	t.Helper()
	sess, ok := mgr.Get(id)
	if !ok {
		t.Fatalf("session %s not live", id)
	}
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatalf("save: %v", err)
	}
	mgr.Evict(id)
	re, err := mgr.GetOrLoad(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return re
}

// TestBuildAddedEntitiesSurviveReload covers the whole class of "the save only
// stored tiles" bugs: a build that adds a charger must still exist after a
// reload, because the save replays the build rather than re-listing effects.
func TestBuildAddedEntitiesSurviveReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("ents", 3)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	const buildID = "b02_east_bridge"
	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 5000
		w.Grant([]string{buildID}, nil)
		if _, err := w.Build(buildID); err != nil {
			t.Fatalf("build: %v", err)
		}
	})

	before := sess.Snapshot()
	re := reload(t, mgr, "ents")
	after := re.Snapshot()

	if len(after.City.Chargers) != len(before.City.Chargers) {
		t.Fatalf("chargers lost on reload: %d -> %d", len(before.City.Chargers), len(after.City.Chargers))
	}
	found := false
	for _, ch := range after.City.Chargers {
		if ch.ID == "ch_bridgehead" {
			found = true
		}
	}
	if !found {
		t.Error("the bridge's charger is missing after reload")
	}
}

// TestRevealedDistrictsSurviveReload: a revealed district must stay revealed.
// Losing it put the harbour back under fog and made the fish market unreachable
// again, which silently undid a purchase.
func TestRevealedDistrictsSurviveReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("rev", 3)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 9000
		w.Grant([]string{"b02_east_bridge", "b04_harbour_gate"}, nil)
		for _, id := range []string{"b02_east_bridge", "b04_harbour_gate"} {
			if _, err := w.Build(id); err != nil {
				t.Fatalf("build %s: %v", id, err)
			}
		}
	})

	before := sess.Snapshot()
	re := reload(t, mgr, "rev")
	after := re.Snapshot()

	if after.City.Hidden != before.City.Hidden {
		t.Fatalf("hidden tiles changed on reload: %d -> %d", before.City.Hidden, after.City.Hidden)
	}
	// The harbour charger is the concrete proof the district is in play.
	found := false
	for _, ch := range after.City.Chargers {
		if ch.ID == "ch_harbour_gate" {
			found = true
		}
	}
	if !found {
		t.Error("the harbour gate charger is missing after reload")
	}
}

// TestAchievementsDoNotRefireOnReload: achievements are one-shot. A reload that
// forgot their state paid their money grants a second time, which is free money
// for anyone who saves and reloads.
func TestAchievementsDoNotRefireOnReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("ach", 3)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	// Satisfy several achievements at once and let one tick fire them.
	sess.Do(func(w *engine.World) {
		w.Ledger.Tows = 1
		w.Ledger.Deliveries = 5
		w.Advance()
	})

	countDone := func(s *Session) (done, money int) {
		s.Do(func(w *engine.World) {
			money = w.Ledger.Money
			for _, a := range w.Achievements() {
				if a.Done {
					done++
				}
			}
		})
		return done, money
	}

	doneBefore, moneyBefore := countDone(sess)
	if doneBefore == 0 {
		t.Fatal("precondition: no achievement fired")
	}

	re := reload(t, mgr, "ach")
	doneAfter, _ := countDone(re)
	if doneAfter != doneBefore {
		t.Fatalf("achievements reset on reload: %d done -> %d", doneBefore, doneAfter)
	}

	// And one more tick must not re-award them.
	re.Do(func(w *engine.World) { w.Advance() })
	doneLater, moneyLater := countDone(re)
	if doneLater != doneBefore {
		t.Fatalf("achievements refired after reload: %d -> %d", doneBefore, doneLater)
	}
	if moneyLater != moneyBefore {
		t.Fatalf("achievement money granted twice: %d -> %d", moneyBefore, moneyLater)
	}
}

// TestForcedFirstOrderDoesNotRefireOnReload: the spawner's memory is part of
// the save. Without it, every reload raised the gate-bypassing first order
// again, so a save/reload loop inflated the queue past its cap.
func TestForcedFirstOrderDoesNotRefireOnReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("forced", 5)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(func(w *engine.World) {
		for range 300 {
			w.Advance()
		}
	})
	spawnedBefore := sess.Snapshot().Ledger.OrdersSpawned

	for i := range 4 {
		re := reload(t, mgr, "forced")
		re.Do(func(w *engine.World) { w.Advance() })
		spawned := re.Snapshot().Ledger.OrdersSpawned
		// At most the one order a normal tick may raise through the gates.
		if spawned > spawnedBefore+1 {
			t.Fatalf("reload #%d spawned %d orders (before %d): the forced first order refired",
				i+1, spawned, spawnedBefore)
		}
		spawnedBefore = spawned
	}
}

// TestSaveWhileTickingIsRaceFree exercises Save against the live ticker. Save
// used to marshal the world under no lock while the ticker mutated cars, which
// the race detector reports and which can emit a corrupt save.
func TestSaveWhileTickingIsRaceFree(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("racing", 4)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(Speed4x); err != nil {
		t.Fatal(err)
	}

	for range 40 {
		if err := sess.Save(mgr.CityFileName()); err != nil {
			t.Fatalf("save: %v", err)
		}
		sess.Do(func(w *engine.World) {
			_ = w.QueueMove(w.Cars[0].ID, engine.South)
		})
	}
	_ = sess.SetSpeed(SpeedPaused)

	// The save written under contention must still load.
	mgr.Evict("racing")
	if _, err := mgr.GetOrLoad("racing"); err != nil {
		t.Fatalf("reload of a concurrently written save: %v", err)
	}
}

// TestDeleteRejectsIDs that escape the sessions directory. filepath.Join cleans
// ".." segments, so an unvalidated id removed a file outside the sessions dir;
// every other entry point validated and this one did not.
func TestDeleteRejectsEscapingIDs(t *testing.T) {
	mgr := newTestManager(t)

	root := filepath.Dir(mgr.dir)
	victim := filepath.Join(root, "victim.json")
	if err := os.WriteFile(victim, []byte("not a save"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := mgr.Delete("../victim"); err == nil {
		t.Error("Delete accepted an id containing path separators")
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("Delete removed %s, outside the sessions directory: %v", victim, err)
	}
}

// A save written by the previous build must still load. The format changed
// shape (SavedState replaced a hand-rolled persistedWorld that also stored
// terrain_deltas and revealed tiles). Dropping those is safe ONLY because the
// completed builds are replayed from the city config; this test proves the
// geography comes back anyway.
func TestLegacySaveStillLoads(t *testing.T) {
	dir := t.TempDir()
	city := filepath.Join("..", "..", "maps", "city.json")
	mgr, err := NewManager(city, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	sess, err := mgr.Create("legacy", 7)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 5000
		w.Ledger.Deliveries = 12
		w.Grant([]string{"b01_corner_charger", "b02_east_bridge"}, nil)
		for _, id := range []string{"b01_corner_charger", "b02_east_bridge"} {
			if _, err := w.Build(id); err != nil {
				t.Fatalf("build %s: %v", id, err)
			}
		}
		for range 50 {
			w.Advance()
		}
	})
	want := sess.Snapshot()
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("legacy")

	// Rewrite the save into the OLD shape: drop the fields the previous build
	// never wrote, and add back the two it did write.
	path := filepath.Join(dir, "legacy.json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(blob, &doc); err != nil {
		t.Fatal(err)
	}
	world := doc["world"].(map[string]any)
	delete(world, "achievements")
	delete(world, "next_eligible_tick")
	delete(world, "last_spawn_tick")
	delete(world, "has_spawned")
	world["terrain_deltas"] = []any{map[string]any{"x": 12, "y": 2, "type": "road"}}
	world["revealed"] = []any{}
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	re, err := mgr.GetOrLoad("legacy")
	if err != nil {
		t.Fatalf("old-format save failed to load: %v", err)
	}
	got := re.Snapshot()

	if got.Tick != want.Tick {
		t.Errorf("tick = %d, want %d", got.Tick, want.Tick)
	}
	if got.Ledger.Money != want.Ledger.Money {
		t.Errorf("money = %d, want %d", got.Ledger.Money, want.Ledger.Money)
	}
	if len(got.Cars) != len(want.Cars) {
		t.Errorf("cars = %d, want %d", len(got.Cars), len(want.Cars))
	}
	// Geography must be rebuilt from the completed builds, not from the deltas
	// the new format no longer stores.
	before := map[string]bool{}
	for _, ch := range want.City.Chargers {
		before[ch.ID] = true
	}
	after := map[string]bool{}
	for _, ch := range got.City.Chargers {
		after[ch.ID] = true
	}
	for id := range before {
		if !after[id] {
			t.Errorf("charger %q lost loading an old-format save", id)
		}
	}
	if got.City.Hidden != want.City.Hidden {
		t.Errorf("hidden tiles = %d, want %d", got.City.Hidden, want.City.Hidden)
	}
	// And the bridge tiles are road again.
	re.Do(func(w *engine.World) {
		if t12, _ := w.City.At(engine.Position{X: 12, Y: 2}); t12 != engine.RoadTile {
			t.Errorf("bridge tile (12,2) = %q, want road", t12)
		}
	})
}
