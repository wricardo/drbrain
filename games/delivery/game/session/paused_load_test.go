package session

import (
	"testing"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// A reloaded session must come back PAUSED even if it was saved running.
//
// The old behaviour restored the saved speed, so opening a save at 2x advanced
// the world while the player was still reading it. That was surprising on its
// own; once fleet upkeep exists it is a money drain on a session nobody is
// playing, which is the worse failure. The test asserts both halves: the clock
// is stopped, and the tick count does not move while it sits open.
func TestReloadStartsPausedEvenIfSavedRunning(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("paused-on-load", 11)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(Speed2x); err != nil {
		t.Fatal(err)
	}
	// Advance deterministically rather than trusting the ticker's wall clock.
	sess.Do(func(w *engine.World) {
		w.AdvanceNoPause()
		w.AdvanceNoPause()
	})
	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	before := sess.Snapshot().Tick

	mgr.Evict("paused-on-load")
	re, err := mgr.GetOrLoad("paused-on-load")
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	if got := re.Speed(); got != SpeedPaused {
		t.Fatalf("reloaded speed = %d, want %d (paused) even though it was saved at 2x", got, SpeedPaused)
	}
	snap := re.Snapshot()
	if !snap.Paused {
		t.Fatal("reloaded snapshot reports paused=false")
	}
	if snap.Tick != before {
		t.Fatalf("tick advanced across the reload: %d -> %d", before, snap.Tick)
	}

	// The clock must still be usable: pausing on load is not a lockout.
	if err := re.SetSpeed(Speed1x); err != nil {
		t.Fatalf("resume after load: %v", err)
	}
	if got := re.Speed(); got != Speed1x {
		t.Fatalf("speed after resume = %d, want 1", got)
	}
}
