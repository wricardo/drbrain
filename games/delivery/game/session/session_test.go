package session

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

func testCity(t *testing.T) (*engine.City, *engine.CityConfig) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "maps", "city.json"))
	if err != nil {
		t.Fatal(err)
	}
	city, cfg, err := engine.CityFromFile(path)
	if err != nil {
		t.Fatalf("load city: %v", err)
	}
	return city, cfg
}

func TestSessionTickerAdvancesWorld(t *testing.T) {
	city, cfg := testCity(t)
	s, err := New("race1", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()

	// Wait for a few ticks. The tick interval is 300ms at 1x, so 4x gets us
	// there quickly.
	if err := s.SetSpeed(Speed4x); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var tick int64
		s.Do(func(w *engine.World) { tick = w.Tick })
		return tick >= 3
	})

	snap := s.Snapshot()
	if snap.Tick < 3 {
		t.Fatalf("tick = %d, want >= 3", snap.Tick)
	}
}

func TestPausedSessionDoesNotTick(t *testing.T) {
	city, cfg := testCity(t)
	s, err := New("pause1", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()

	if err := s.SetSpeed(Speed4x); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		var tick int64
		s.Do(func(w *engine.World) { tick = w.Tick })
		return tick >= 2
	})

	if err := s.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}
	// Give the goroutine a chance to observe the pause, then confirm it froze.
	waitFor(t, func() bool { return s.Paused() })

	var frozen int64
	s.Do(func(w *engine.World) { frozen = w.Tick })

	// The loop may be mid-flight; a short settle then a re-read is enough.
	for range 3 {
		var now int64
		s.Do(func(w *engine.World) { now = w.Tick })
		if now != frozen {
			t.Fatalf("paused session ticked: %d -> %d", frozen, now)
		}
	}
}

// TestConcurrentAccessIsRaceFree is the important one: it exercises the
// ticker goroutine against concurrent readers and writers. Run with -race.
func TestConcurrentAccessIsRaceFree(t *testing.T) {
	city, cfg := testCity(t)
	s, err := New("race2", city, cfg, 42, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()

	if err := s.SetSpeed(Speed4x); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	// Readers.
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 30 {
				_ = s.Snapshot()
				_ = s.Speed()
			}
		}()
	}

	// Writers: queue moves and flip the speed.
	wg.Add(1)
	go func() {
		defer wg.Done()
		dirs := []engine.Direction{engine.South, engine.East, engine.West, engine.North}
		for i := range 60 {
			s.Do(func(w *engine.World) {
				if len(w.Cars) > 0 {
					_ = w.QueueMove(w.Cars[0].ID, dirs[i%len(dirs)])
				}
			})
			if i%20 == 0 {
				if s.Speed() == SpeedPaused {
					_ = s.SetSpeed(Speed4x)
				} else {
					_ = s.SetSpeed(SpeedPaused)
				}
			}
		}
	}()

	wg.Wait()
}

func TestSetSpeedRejectsUnknownValues(t *testing.T) {
	city, cfg := testCity(t)
	s, err := New("spd", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []int{-1, 3, 10, 100} {
		if err := s.SetSpeed(bad); err == nil {
			t.Errorf("speed %d was accepted", bad)
		}
	}
}

func TestInvalidSessionIDRejected(t *testing.T) {
	city, cfg := testCity(t)
	for _, bad := range []string{"", "../x", "a/b", "x.json", "with space"} {
		if _, err := New(bad, city, cfg, 1, ""); err == nil {
			t.Errorf("id %q was accepted", bad)
		}
	}
}

// waitFor polls cond for up to a second.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for range 200 {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestNewSessionStartsPaused(t *testing.T) {
	// A new session must not tick on its own. This game is manual driving: if
	// the clock ran, orders would expire while the player was still reading the
	// screen, and every deadline would be a reaction-time test rather than a
	// planning one.
	city, cfg := testCity(t)
	s, err := New("paused", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	defer s.Close()

	// Give the ticker goroutine a chance to misbehave.
	time.Sleep(400 * time.Millisecond)

	var tick int64
	s.Do(func(w *engine.World) { tick = w.Tick })
	if tick != 0 {
		t.Fatalf("new session advanced to tick %d without being unpaused", tick)
	}
	if !s.Paused() {
		t.Fatal("new session should report paused")
	}
}

func TestManualTickStillWorksWhenPaused(t *testing.T) {
	city, cfg := testCity(t)
	s, err := New("manual", city, cfg, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Paused is the default, and explicit stepping must still work.
	s.Do(func(w *engine.World) {
		for range 3 {
			w.Advance()
		}
	})
	snap := s.Snapshot()
	if snap.Tick != 3 {
		t.Fatalf("manual tick produced tick %d, want 3", snap.Tick)
	}
}
