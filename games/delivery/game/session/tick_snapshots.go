package session

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wricardo/drbrain/games/delivery/game/engine"
)

// Tick snapshots are the replayable per-tick log: one row per completed tick,
// written from the engine's tick hook. platform_sessions remains the
// authoritative save; these rows are derived data, so a failed or dropped write
// never affects the game.
const (
	// snapshotWriteTimeout bounds one batch write. It is generous because the
	// writer is asynchronous and a slow flush only delays snapshots, not ticks.
	snapshotWriteTimeout = 10 * time.Second
	// snapshotQueueSize is how many frames may wait for the database. A tick is
	// never blocked by storage: when the queue is full the new frame is dropped
	// and counted.
	snapshotQueueSize = 1024
	// snapshotBatchSize caps how many rows one transaction inserts.
	snapshotBatchSize = 128
	// snapshotFlushInterval is how long the first frame of a batch waits for
	// company before it is written.
	snapshotFlushInterval = 250 * time.Millisecond
)

// ErrSnapshotsDisabled reports that the recorder is not configured. The tick
// log is optional infrastructure, so a reader distinguishes "off" from
// "empty".
var ErrSnapshotsDisabled = errors.New("delivery tick snapshots are disabled")

// TickSnapshot is one stored world frame.
type TickSnapshot struct {
	SessionID  string
	Tick       int64
	RecordedAt time.Time
	State      engine.SavedState
}

// TickSnapshotQuery is one page request against a session's tick log.
// Zero bounds mean "unset"; recorded ticks start at 1.
type TickSnapshotQuery struct {
	FromTick  int64
	ToTick    int64
	AfterTick int64
	Limit     int
}

// Tick snapshot page limits. The reader enforces them so every caller, not
// just GraphQL, gets a bounded response.
const (
	defaultTickSnapshotLimit = 100
	maxTickSnapshotLimit     = 500
)

// snapshotOp is one queued write or delete. Deletes travel through the same
// queue as frames so they cannot be overtaken by a batch that was recorded
// before the session was deleted.
type snapshotOp struct {
	sessionID string
	tick      int64
	state     []byte
	// del removes the session's rows instead of inserting a frame.
	del bool
	// ack receives the delete's result; nil for inserts.
	ack chan error
}

// tickSnapshotRecorder buffers per-tick frames and writes them in batches.
//
// It exists as a separate, asynchronous writer because the tick clock must
// never wait on PostgreSQL: a database slower than the tick rate would stall
// the simulation. The trade-off is that a full queue drops frames, which is
// acceptable for derived replay data and is reported once per recorder.
type tickSnapshotRecorder struct {
	pool      *pgxpool.Pool
	retention int64

	mu     sync.RWMutex
	closed bool
	ch     chan snapshotOp

	done      chan struct{}
	closeOnce sync.Once
	dropped   atomic.Int64
	// dropLogged makes the first drop the only one that logs; a saturated
	// recorder would otherwise flood the log at the tick rate.
	dropLogged atomic.Bool
	// err is the last write failure, read after done is closed.
	err error
}

// newTickSnapshotRecorder starts a recorder keeping the newest retention ticks
// per session. retention must be positive; callers that disable the feature
// pass no recorder at all.
func newTickSnapshotRecorder(pool *pgxpool.Pool, retention int) *tickSnapshotRecorder {
	r := &tickSnapshotRecorder{
		pool:      pool,
		retention: int64(retention),
		ch:        make(chan snapshotOp, snapshotQueueSize),
		done:      make(chan struct{}),
	}
	go r.run()
	return r
}

// Record queues one completed tick. It is called from the engine tick hook,
// with the session lock held, and never blocks: the frame is encoded here so
// the queued bytes are immutable, then dropped if the writer is behind.
func (r *tickSnapshotRecorder) Record(sessionID string, frame engine.SavedState) {
	if r == nil {
		return
	}
	blob, err := json.Marshal(frame)
	if err != nil {
		// The frame is engine-owned JSON; a marshal failure is a programming
		// error, and losing one snapshot must not take the session down.
		slog.Default().Warn("delivery tick snapshot: marshal", "session", sessionID, "tick", frame.Tick, "err", err)
		return
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return
	}
	select {
	case r.ch <- snapshotOp{sessionID: sessionID, tick: frame.Tick, state: blob}:
	default:
		if r.dropped.Add(1) == 1 && !r.dropLogged.Swap(true) {
			slog.Default().Warn("delivery tick snapshot: queue full, dropping frames",
				"session", sessionID, "tick", frame.Tick)
		}
	}
}

func (r *tickSnapshotRecorder) run() {
	defer close(r.done)
	pending := make([]snapshotOp, 0, snapshotBatchSize)
	var flush <-chan time.Time
	for {
		select {
		case op, ok := <-r.ch:
			if !ok {
				if len(pending) > 0 {
					r.flush(pending)
				}
				return
			}
			if op.del {
				// Frames queued before the delete belong to this session and
				// must land before it, otherwise they would resurrect rows the
				// delete just removed.
				if len(pending) > 0 {
					r.flush(pending)
					pending = pending[:0]
					flush = nil
				}
				op.ack <- r.deleteRows(op.sessionID)
				continue
			}
			pending = append(pending, op)
			if len(pending) == 1 {
				flush = time.After(snapshotFlushInterval)
			}
			if len(pending) >= snapshotBatchSize {
				r.flush(pending)
				pending = pending[:0]
				flush = nil
			}
		case <-flush:
			r.flush(pending)
			pending = pending[:0]
			flush = nil
		}
	}
}

// flush writes one batch and prunes each touched session to its retention
// window. Both run in one transaction so a deleted session can never leave a
// partial batch behind.
func (r *tickSnapshotRecorder) flush(batch []snapshotOp) {
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWriteTimeout)
	defer cancel()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		r.fail("begin", err)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()

	inserts := &pgx.Batch{}
	for _, op := range batch {
		inserts.Queue(`
INSERT INTO delivery_tick_snapshots (session_id, tick, state)
VALUES ($1, $2, $3)
ON CONFLICT (session_id, tick) DO NOTHING`, op.sessionID, op.tick, string(op.state))
	}
	if err := tx.SendBatch(ctx, inserts).Close(); err != nil {
		r.fail("insert", err)
		return
	}

	maxTicks := make(map[string]int64, len(batch))
	for _, op := range batch {
		if op.tick > maxTicks[op.sessionID] {
			maxTicks[op.sessionID] = op.tick
		}
	}
	for id, maxTick := range maxTicks {
		if _, err := tx.Exec(ctx, `
DELETE FROM delivery_tick_snapshots
WHERE session_id = $1 AND tick <= $2`, id, maxTick-r.retention); err != nil {
			r.fail("prune", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		r.fail("commit", err)
	}
}

func (r *tickSnapshotRecorder) fail(op string, err error) {
	r.err = err
	slog.Default().Warn("delivery tick snapshot: write", "op", op, "err", err)
}

// deleteRows removes a session's frames. It runs on the writer goroutine, so
// it is already ordered after every frame recorded before the delete.
func (r *tickSnapshotRecorder) deleteRows(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), snapshotWriteTimeout)
	defer cancel()
	_, err := r.pool.Exec(ctx,
		`DELETE FROM delivery_tick_snapshots WHERE session_id = $1`, sessionID)
	if err != nil {
		slog.Default().Warn("delivery tick snapshot: delete", "session", sessionID, "err", err)
	}
	return err
}

// deleteSession asks the writer to remove a session's frames and waits for the
// result. The table has no foreign key to platform_sessions (a session ticks
// before its first save), so deletion is explicit and lives next to the session
// delete.
func (r *tickSnapshotRecorder) deleteSession(sessionID string) error {
	if r == nil {
		return nil
	}
	op := snapshotOp{sessionID: sessionID, del: true, ack: make(chan error, 1)}
	r.mu.RLock()
	if r.closed {
		r.mu.RUnlock()
		return nil
	}
	select {
	case r.ch <- op:
		r.mu.RUnlock()
	case <-time.After(snapshotWriteTimeout):
		r.mu.RUnlock()
		return errors.New("delivery tick snapshot: delete queue is full")
	}
	select {
	case err := <-op.ack:
		return err
	case <-time.After(snapshotWriteTimeout):
		return errors.New("delivery tick snapshot: delete timed out")
	}
}

// Load returns one page of retained frames for a session, oldest first.
//
// Bounds are inclusive; AfterTick is exclusive and wins over FromTick when it
// is higher, which is what makes keyset paging correct. The limit is normalized
// here (default 100, cap 500).
func (r *tickSnapshotRecorder) Load(ctx context.Context, sessionID string, q TickSnapshotQuery) ([]TickSnapshot, error) {
	if r == nil {
		return nil, ErrSnapshotsDisabled
	}
	limit := q.Limit
	if limit <= 0 {
		limit = defaultTickSnapshotLimit
	}
	if limit > maxTickSnapshotLimit {
		limit = maxTickSnapshotLimit
	}
	from := q.FromTick
	if q.AfterTick+1 > from {
		from = q.AfterTick + 1
	}
	ctx, cancel := context.WithTimeout(ctx, snapshotWriteTimeout)
	defer cancel()
	rows, err := r.pool.Query(ctx, `
SELECT tick, state, created_at
FROM delivery_tick_snapshots
WHERE session_id = $1
  AND ($2 = 0 OR tick >= $2)
  AND ($3 = 0 OR tick <= $3)
ORDER BY tick
LIMIT $4`, sessionID, from, q.ToTick, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TickSnapshot
	for rows.Next() {
		snap := TickSnapshot{SessionID: sessionID}
		var blob []byte
		if err := rows.Scan(&snap.Tick, &blob, &snap.RecordedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(blob, &snap.State); err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// Close stops the writer and flushes what is already queued. The session tick
// loops must be stopped first, or a late frame races the closed channel.
func (r *tickSnapshotRecorder) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.ch)
		r.mu.Unlock()
	})
	<-r.done
	return r.err
}
