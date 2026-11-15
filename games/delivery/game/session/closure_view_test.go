package session

import (
	"testing"

	"github.com/wricardo/drbrain-delivery/game/engine"
)

// TestSnapshotCarriesClosures proves street works reach the client.
//
// A closure that the engine enforces but the snapshot omits would be invisible
// drama: the player's car stops at an apparently open street with no
// explanation. This checks the glyph rows, the structured list and the event
// feed all agree.
func TestSnapshotCarriesClosures(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("closures", 1)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	// Reveal the map and push past the chapter gate, then let the clock run
	// until a closure is scheduled. This is a persistence/view test, so it
	// forces the world into the state rather than playing 900 ticks by hand.
	sess.Do(func(w *engine.World) {
		w.City.RevealAll()
		w.Ledger.BuildsCompleted = 4
	})

	var found bool
	for i := 0; i < 3000 && !found; i++ {
		sess.Do(func(w *engine.World) {
			w.Advance()
			if len(w.ActiveClosures()) > 0 {
				found = true
			}
		})
	}
	if !found {
		t.Fatal("no closure started in 3000 ticks with the chapter gate open")
	}

	snap := sess.Snapshot()
	if len(snap.Closures) == 0 {
		t.Fatal("engine has an active closure but the snapshot reports none")
	}
	c := snap.Closures[0]
	if c.Reason == "" {
		t.Error("closure has no reason for the player")
	}
	if len(c.Tiles) == 0 {
		t.Fatal("closure has no tiles")
	}

	// Every closed tile must render as 'X' on the map the client receives.
	for _, p := range c.Tiles {
		if p.Y < 0 || p.Y >= len(snap.City.Rows) {
			t.Fatalf("closure tile %v is off the rendered grid", p)
		}
		row := snap.City.Rows[p.Y]
		if p.X < 0 || p.X >= len(row) {
			t.Fatalf("closure tile %v is off the rendered grid", p)
		}
		if got := row[p.X]; got != 'X' {
			t.Fatalf("closed tile (%d,%d) rendered %q, want 'X'", p.X, p.Y, string(got))
		}
	}

	// The bounding box must contain every tile, or a client that pans to the
	// closure will miss part of it.
	for _, p := range c.Tiles {
		if p.X < c.MinX || p.X > c.MaxX || p.Y < c.MinY || p.Y > c.MaxY {
			t.Fatalf("tile %v outside bounding box [%d,%d]-[%d,%d]",
				p, c.MinX, c.MinY, c.MaxX, c.MaxY)
		}
	}

	// And the feed must have said so.
	sawEvent := false
	for _, e := range snap.Events {
		if e.Type == engine.EventClosure {
			sawEvent = true
		}
	}
	if !sawEvent {
		t.Error("no closure event in the feed")
	}
}

// TestSnapshotCarriesBoxLoads proves the hold is visible as boxes rather than
// as a count of order ids. A UI that shows "1/12" for a twelve-box order is
// lying about how full the van is.
func TestSnapshotCarriesBoxLoads(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("boxes", 2)
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.SetSpeed(SpeedPaused)

	sess.Do(func(w *engine.World) {
		car := w.Cars[0]
		car.Class.Capacity = 40

		// Load the hold through the real pickup path rather than writing Cargo
		// directly. Writing Cargo by hand leaves cargoBoxes at zero, which is
		// the drift this accounting exists to prevent -- and it is how the
		// first version of this test fooled itself.
		var kitchen engine.Position
		for _, k := range w.City.Kitchens {
			if k.ID == "k_pizza" {
				kitchen = k.Pos
			}
		}
		o := &engine.Order{
			ID: "ord_box", Item: "pizza", KitchenID: "k_pizza", CustomerID: "c_anna",
			Quantity: 12, State: engine.OrderAssigned, AssignedCar: car.ID,
			DeadlineTick: 100000, Reward: 100,
		}
		w.Orders = append(w.Orders, o)

		// Pickup only happens for a car that MOVED onto the kitchen, so step in
		// from a neighbour: a parked car with a fresh assignment deliberately
		// does not collect (otherwise assigning would be as good as driving).
		var from engine.Position
		var dir engine.Direction
		for _, d := range engine.AllDirections {
			if n, ok := kitchen.Neighbour(d); ok && w.City.Walkable(n) {
				// Step onto the kitchen, so the direction is the inverse.
				for _, back := range engine.AllDirections {
					if b, ok := n.Neighbour(back); ok && b == kitchen {
						from, dir = n, back
					}
				}
			}
			if dir != "" {
				break
			}
		}
		if dir == "" {
			t.Fatal("kitchen has no walkable neighbour")
		}
		car.Pos = from
		if err := w.QueueMove(car.ID, dir); err != nil {
			t.Fatal(err)
		}
		w.AdvanceNoPause()
		if o.State != engine.OrderPickedUp {
			t.Fatalf("order not picked up: state %s", o.State)
		}
	})

	snap := sess.Snapshot()
	if len(snap.Cars) == 0 {
		t.Fatal("no cars")
	}
	c := snap.Cars[0]
	if c.Load != 12 {
		t.Errorf("load = %d, want 12", c.Load)
	}
	if c.Capacity != 40 {
		t.Errorf("capacity = %d, want 40", c.Capacity)
	}
	if c.FreeBoxes != 28 {
		t.Errorf("freeBoxes = %d, want 28", c.FreeBoxes)
	}
}

// TestSaveReloadKeepsActiveClosure covers the path a player actually takes:
// save a session mid-street-works, evict it from memory, reload it from disk.
//
// The engine-level test (engine.TestReloadRestoresActiveClosures) pins
// ImportState; this pins the whole round trip through Manager, which is where a
// missing refill would reach the player as "the street quietly reopened".
func TestSaveReloadKeepsActiveClosure(t *testing.T) {
	mgr := newTestManager(t)
	sess, err := mgr.Create("closure-reload", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.SetSpeed(SpeedPaused); err != nil {
		t.Fatal(err)
	}

	var tickBefore int64
	var closedBefore, closuresBefore int
	sess.Do(func(w *engine.World) {
		w.City.RevealAll()
		w.Ledger.BuildsCompleted = 4
		for i := 0; i < 4000; i++ {
			w.Advance()
			// Wait until a closure has been running for more than one tick, so
			// the rebuild cannot pass by looking only at the current tick.
			live := w.ActiveClosures()
			if len(live) == 0 || live[0].StartTick >= w.Tick {
				continue
			}
			tickBefore = w.Tick
			closedBefore = len(w.ClosedSet())
			closuresBefore = len(live)
			return
		}
	})
	if tickBefore == 0 || closedBefore == 0 {
		t.Skip("no closure became live long enough to test with")
	}

	if err := sess.Save(mgr.CityFileName()); err != nil {
		t.Fatal(err)
	}
	mgr.Evict("closure-reload")
	reloaded, err := mgr.GetOrLoad("closure-reload")
	if err != nil {
		t.Fatal(err)
	}
	snap := reloaded.Snapshot()

	if snap.Tick != tickBefore {
		t.Fatalf("tick = %d, want %d", snap.Tick, tickBefore)
	}
	if len(snap.Closures) != closuresBefore {
		t.Fatalf("active closures %d -> %d after save/reload", closuresBefore, len(snap.Closures))
	}
	// The rows the map renders must still show the closed tiles.
	closedRows := 0
	for _, row := range snap.City.Rows {
		for _, r := range row {
			if r == 'X' {
				closedRows++
			}
		}
	}
	t.Logf("tick %d: %d closure(s), %d closed tiles, %d 'X' glyphs in the rendered rows",
		snap.Tick, len(snap.Closures), closedBefore, closedRows)
	if closedRows != closedBefore {
		t.Errorf("rendered rows show %d closed tiles, want %d", closedRows, closedBefore)
	}
}
