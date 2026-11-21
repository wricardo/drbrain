package session

import (
	"testing"
	"time"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// newTestSession builds a session that is never started, so tests control the
// clock entirely through Do. No directory, so nothing is persisted.
func newTestSession(t *testing.T) *Session {
	t.Helper()
	city, cfg := testCity(t)
	s, err := New("watchtest", city, cfg, 1)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	return s
}

// TestWatchNotifiedOnTick proves a manual step reaches a subscriber.
//
// This is the case that polling was hiding: sessions start paused, so if only
// the tick loop published, a hand-played game would look frozen to a
// subscription. Do is the path every resolver mutation takes.
func TestWatchNotifiedOnTick(t *testing.T) {
	s := newTestSession(t)
	w := s.Watch()
	defer s.Unwatch(w)

	s.Do(func(w *engine.World) { w.Advance() })

	select {
	case <-w.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("no notification after a manual tick while paused")
	}
}

// TestWatchNotifiedOnSpeedChange proves pause/resume reaches subscribers.
func TestWatchNotifiedOnSpeedChange(t *testing.T) {
	s := newTestSession(t)
	w := s.Watch()
	defer s.Unwatch(w)

	if err := s.SetSpeed(Speed2x); err != nil {
		t.Fatalf("SetSpeed: %v", err)
	}
	select {
	case <-w.wake:
	case <-time.After(2 * time.Second):
		t.Fatal("no notification after SetSpeed")
	}
}

// TestWatchCoalesces proves notifications collapse instead of queueing.
//
// A slow subscriber must not accumulate one snapshot per tick: the state is a
// full snapshot, so the next read supersedes every intermediate one. A backlog
// would also delay every later notification behind it.
func TestWatchCoalesces(t *testing.T) {
	s := newTestSession(t)
	w := s.Watch()
	defer s.Unwatch(w)

	for range 50 {
		s.Do(func(w *engine.World) { w.Advance() })
	}

	// Exactly one wake is pending: the buffer holds one value and the rest were
	// dropped by the non-blocking send.
	select {
	case <-w.wake:
	default:
		t.Fatal("expected a pending wake")
	}
	select {
	case <-w.wake:
		t.Fatal("expected notifications to be coalesced, got a second wake")
	default:
	}
}

// TestUnwatchUnblocksNext proves a closed watch ends a blocked reader.
//
// The subscription goroutine waits in Next; if Unwatch did not unblock it, a
// disconnected client would leak that goroutine.
func TestUnwatchUnblocksNext(t *testing.T) {
	s := newTestSession(t)
	w := s.Watch()

	done := make(chan bool, 1)
	go func() { done <- w.Next() }()

	time.Sleep(20 * time.Millisecond)
	s.Unwatch(w)

	select {
	case ok := <-done:
		if ok {
			t.Fatal("Next reported a change; expected a closed watch")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Next stayed blocked after Unwatch")
	}
}

// TestCloseEndsWatchers proves an evicted session ends live subscriptions.
func TestCloseEndsWatchers(t *testing.T) {
	s := newTestSession(t)
	w := s.Watch()

	done := make(chan bool, 1)
	go func() { done <- w.Next() }()

	time.Sleep(20 * time.Millisecond)
	s.Close()

	select {
	case ok := <-done:
		if ok {
			t.Fatal("Next reported a change after Close")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not unblock a waiting watcher")
	}
	if n := s.WatchCount(); n != 0 {
		t.Fatalf("watch count = %d after Close, want 0", n)
	}
}

// TestStreamSnapshotTrimsFinishedOrders proves the stream payload is bounded.
//
// Full order history is ~96% of a late-game snapshot and grows forever, which
// is why streaming it per tick would cost more than polling did.
func TestStreamSnapshotTrimsFinishedOrders(t *testing.T) {
	s := newTestSession(t)

	// 3 open orders and 40 finished ones.
	s.Do(func(w *engine.World) {
		w.Orders = nil
		for i := range 3 {
			w.Orders = append(w.Orders, &engine.Order{ID: "open" + string(rune('a'+i)), State: engine.OrderPending})
		}
		for i := range 40 {
			w.Orders = append(w.Orders, &engine.Order{ID: "done" + string(rune('a'+i)), State: engine.OrderDone})
		}
	})

	snap := s.StreamSnapshot()
	if len(snap.Orders) != 3+streamHistory {
		t.Fatalf("stream snapshot has %d orders, want %d", len(snap.Orders), 3+streamHistory)
	}

	// Every open order survives.
	open := 0
	for _, o := range snap.Orders {
		if !isFinishedOrderState(o.State) {
			open++
		}
	}
	if open != 3 {
		t.Fatalf("kept %d open orders, want 3", open)
	}

	// The query path is untouched: full history still comes back.
	if full := s.Snapshot(); len(full.Orders) != 43 {
		t.Fatalf("query snapshot has %d orders, want the full 43", len(full.Orders))
	}
}

// TestTrimOrdersKeepsNewest proves the retained history is the most recent.
func TestTrimOrdersKeepsNewest(t *testing.T) {
	var orders []OrderView
	for i := range 5 {
		orders = append(orders, OrderView{ID: "old" + string(rune('a'+i)), State: string(engine.OrderDone)})
	}
	orders = append(orders, OrderView{ID: "live", State: string(engine.OrderPending)})

	got := trimOrders(orders, 2)
	if len(got) != 3 {
		t.Fatalf("got %d orders, want 3", len(got))
	}
	// Newest finished order is kept, the older ones dropped.
	if got[1].ID != "oldd" {
		t.Fatalf("first kept history order = %q, want %q", got[1].ID, "oldd")
	}
	if got[2].ID != "olde" {
		t.Fatalf("last kept history order = %q, want %q", got[2].ID, "olde")
	}
}
