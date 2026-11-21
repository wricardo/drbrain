package session

import (
	"sync"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// Watch is one live subscription to a session's state.
//
// Notifications are coalesced rather than queued: a watcher holds a single
// "something changed" slot, and a publish that finds it already full is a
// no-op. That is deliberate. The state is a full snapshot whenever it is read,
// so a slow consumer that misses five ticks does not need five snapshots — it
// needs to know that the state moved and read it once. Queueing instead would
// let a paused tab bank an unbounded backlog of snapshots, each ~1 MB, and
// delay every later notification behind them.
type Watch struct {
	// wake receives a value when the session state changes. Buffered to one.
	wake chan struct{}
	// done is closed by Unwatch so a reader blocked in Next can give up.
	done chan struct{}
	// closeOnce makes Unwatch idempotent.
	closeOnce sync.Once
}

// Next blocks until the state changes or the watch is closed.
//
// It reports false when the watch has been closed, so a caller can end its
// loop on the same channel it waits on and never touch a closed channel.
func (w *Watch) Next() bool {
	select {
	case <-w.wake:
		return true
	case <-w.done:
		// Prefer a pending change over shutdown so a final update is not lost
		// when a client disconnects right after a tick.
		select {
		case <-w.wake:
			return true
		default:
			return false
		}
	}
}

// watchHub fans one session's changes out to every attached watcher.
//
// The zero value is not usable; New initialises it.
type watchHub struct {
	mu       sync.Mutex
	watchers map[*Watch]struct{}
}

func newWatchHub() *watchHub {
	return &watchHub{watchers: map[*Watch]struct{}{}}
}

// subscribe attaches a new watcher.
func (h *watchHub) subscribe() *Watch {
	w := &Watch{wake: make(chan struct{}, 1), done: make(chan struct{})}
	h.mu.Lock()
	h.watchers[w] = struct{}{}
	h.mu.Unlock()
	return w
}

// unsubscribe detaches a watcher and unblocks any reader waiting on it.
func (h *watchHub) unsubscribe(w *Watch) {
	h.mu.Lock()
	delete(h.watchers, w)
	h.mu.Unlock()
	w.closeOnce.Do(func() { close(w.done) })
}

// publish wakes every watcher, skipping any that already has a pending wake.
//
// It never blocks on a watcher: the send is guarded by a non-blocking select
// against a one-slot buffer. A stalled subscriber therefore costs nothing here,
// which is what lets the tick loop call this while holding no locks of its own.
func (h *watchHub) publish() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.watchers {
		select {
		case w.wake <- struct{}{}:
		default:
			// Already has an unread wake; it will see the newer state anyway.
		}
	}
}

// count reports how many watchers are attached (used by tests).
func (h *watchHub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.watchers)
}

// closeAll detaches every watcher and unblocks any reader waiting on one.
//
// Used when a session goes away: the stream must end rather than block on a
// hub that will never publish again.
func (h *watchHub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for w := range h.watchers {
		w.closeOnce.Do(func() { close(w.done) })
		delete(h.watchers, w)
	}
}

// Watch subscribes to state changes on this session.
//
// The caller must call Session.Unwatch when done, or the watcher leaks.
func (s *Session) Watch() *Watch {
	return s.hub.subscribe()
}

// Unwatch detaches a watcher created by Watch.
func (s *Session) Unwatch(w *Watch) {
	s.hub.unsubscribe(w)
}

// WatchCount reports the number of live watchers on this session.
func (s *Session) WatchCount() int { return s.hub.count() }

// notify publishes a state change to every watcher.
//
// Called from the tick loop and from every mutating entry point. Sessions start
// PAUSED, so the tick loop alone is not enough: a manual step, a queued move or
// a speed change must reach subscribers too, or a paused player's screen would
// only ever update on their own mutations.
func (s *Session) notify() {
	s.hub.publish()
}

// StreamSnapshot returns the snapshot a live subscription should send.
//
// It is the same view the query returns, except that finished orders are
// trimmed to the most recent streamHistory. Order history is the dominant cost
// of a snapshot and it grows without bound: a 42k-tick session carries ~4200
// orders, of which ~96% are DONE or EXPIRED, and that is 1.3 MB of JSON to
// re-send on every tick. The UI only renders open orders plus a short
// "N completed" list, so the tail is enough for it.
//
// The query path keeps the full history on purpose: agents and the CLI treat
// `session(id:)` as the complete record, and trimming there would silently
// change what they see.
func (s *Session) StreamSnapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := BuildSnapshot(s.world)
	snap.ID = s.ID
	snap.CreatedAt = s.CreatedAt
	snap.Speed = s.speed
	snap.Paused = s.speed == SpeedPaused
	snap.Orders = trimOrders(snap.Orders, streamHistory)
	snap.Ledger.CashEntries = trimCashEntries(snap.Ledger.CashEntries, streamCashRows)
	return snap
}

// streamHistory is how many finished orders a live stream keeps. Enough for
// the "completed" list a player scrolls through, small enough to stay cheap.
const streamHistory = 20

// streamCashRows is how many cash statement rows a live stream keeps. The
// statement grows with every delivery and every upkeep interval, and the live
// frame is re-sent on every tick; the latest rows are what the panel shows, and
// the full history stays available through the session query.
const streamCashRows = 40

// trimCashEntries keeps the newest rows of a newest-first statement.
func trimCashEntries(entries []CashEntryView, keep int) []CashEntryView {
	if len(entries) <= keep {
		return entries
	}
	return entries[:keep]
}

// trimOrders keeps every unfinished order plus the newest finished ones.
//
// "Newest" is by creation tick, and the input is already in creation order, so
// the tail is the newest without sorting. Order of the result is preserved:
// open orders in place, followed by the retained history.
func trimOrders(orders []OrderView, keep int) []OrderView {
	var unfinished, finished []OrderView
	for _, o := range orders {
		if isFinishedOrderState(o.State) {
			finished = append(finished, o)
			continue
		}
		unfinished = append(unfinished, o)
	}
	if len(finished) > keep {
		finished = finished[len(finished)-keep:]
	}
	out := make([]OrderView, 0, len(unfinished)+len(finished))
	out = append(out, unfinished...)
	out = append(out, finished...)
	return out
}

// isFinishedOrderState reports whether an order is terminal.
//
// Mirrors engine.OrderState.Terminal but takes the serialised string, because
// OrderView carries the state as a string rather than the enum.
func isFinishedOrderState(state string) bool {
	return state == string(engine.OrderDone) || state == string(engine.OrderExpired)
}
