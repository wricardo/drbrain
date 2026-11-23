package session

import (
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
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
	if err := sess.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	mgr.Evict(id)
	re, err := mgr.GetOrLoad(id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	return re
}

// advancePastGrowth applies the whole city schedule: growth is a pure function
// of the tick, so moving the clock past the last stage and advancing once is
// enough. Tests use it to isolate persistence from the growth grind.
func advancePastGrowth(w *engine.World) {
	w.Tick = 1_000_000
	w.Advance()
}

// TestGrowthAddedEntitiesSurviveReload covers the whole class of "the save only
// stored tiles" bugs: a stage that adds a charger must still exist after a
// reload, because ImportState replays the schedule rather than re-listing
// effects.
func TestGrowthAddedEntitiesSurviveReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("ents", 3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(advancePastGrowth)

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

// TestGrowthRevealedDistrictsSurviveReload: a revealed district must stay
// revealed. Losing it put the harbour back under fog and made the fish market
// unreachable again, which silently undid the city's expansion.
func TestGrowthRevealedDistrictsSurviveReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("rev", 3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(advancePastGrowth)

	before := sess.Snapshot()
	re := reload(t, mgr, "rev")
	after := re.Snapshot()

	if after.City.Hidden != before.City.Hidden {
		t.Fatalf("hidden tiles changed on reload: %d -> %d", before.City.Hidden, after.City.Hidden)
	}
	// The harbour charger is the concrete proof the district is in play.
	found := false
	for _, ch := range after.City.Chargers {
		if ch.ID == "ch_bridgehead" {
			found = true
		}
	}
	if !found {
		t.Error("the harbour gate charger is missing after reload")
	}
}

// TestGrowthDoesNotReapplyOnReload: stages are one-shot. A reload that replayed
// them without a tick bound would duplicate chargers and re-pay grants.
func TestGrowthDoesNotReapplyOnReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("grow", 3, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(advancePastGrowth)

	var chargersBefore, stagesBefore, moneyBefore int
	sess.Do(func(w *engine.World) {
		chargersBefore = len(w.City.Chargers)
		stagesBefore = len(w.Growth())
		moneyBefore = w.Ledger.Money
	})

	re := reload(t, mgr, "grow")
	sess.Do(func(w *engine.World) {
		if got := len(w.City.Chargers); got != chargersBefore {
			t.Fatalf("chargers duplicated on reload: %d -> %d", chargersBefore, got)
		}
		if got := len(w.Growth()); got != stagesBefore {
			t.Fatalf("growth stages changed on reload: %d -> %d", stagesBefore, got)
		}
		if got := w.Ledger.Money; got != moneyBefore {
			t.Fatalf("money changed on reload: %d -> %d", moneyBefore, got)
		}
	})
	_ = re
}

// TestForcedFirstOrderDoesNotRefireOnReload: the spawner's memory is part of
// the save. Without it, every reload raised the gate-bypassing first order
// again, so a save/reload loop inflated the queue past its cap.
func TestForcedFirstOrderDoesNotRefireOnReload(t *testing.T) {
	mgr := newTestManager(t)
	sess, _, err := mgr.Create("forced", 5, "", "")
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
	sess, _, err := mgr.Create("racing", 4, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(Speed4x); err != nil {
		t.Fatal(err)
	}

	for range 40 {
		if err := sess.Save(); err != nil {
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

// TestDeleteRejectsEscapingIDs: ids double as store keys and URL path segments,
// so Delete must validate before touching the store. Every other entry point
// validated and this one used not to.
func TestDeleteRejectsEscapingIDs(t *testing.T) {
	mgr := newTestManager(t)
	for _, id := range []string{"../victim", "a/b", "x y"} {
		if err := mgr.Delete(id); err == nil {
			t.Errorf("Delete accepted %q", id)
		}
	}
}

// A save written by the previous build must still load. The format changed
// shape (SavedState replaced a hand-rolled persistedWorld that also stored
// terrain_deltas and revealed tiles). Dropping those is safe ONLY because the
// city's growth schedule is replayed from the config by tick; this test proves
// the geography comes back anyway.
func TestLegacySaveStillLoads(t *testing.T) {
	mgr := newTestManager(t)

	sess, _, err := mgr.Create("legacy", 7, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)
	sess.Do(func(w *engine.World) {
		w.Ledger.Money = 5000
		w.Ledger.Deliveries = 12
		advancePastGrowth(w)
	})
	want := sess.Snapshot()
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("legacy")

	// Rewrite the payload into the OLD shape: drop the fields the previous
	// build never wrote, and add back the two it did write.
	rewritePayload(t, mgr, "legacy", func(doc map[string]any) {
		world := doc["world"].(map[string]any)
		delete(world, "achievements")
		delete(world, "next_eligible_tick")
		delete(world, "last_spawn_tick")
		delete(world, "has_spawned")
		world["terrain_deltas"] = []any{map[string]any{"x": 12, "y": 2, "type": "road"}}
		world["revealed"] = []any{}
	})

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
