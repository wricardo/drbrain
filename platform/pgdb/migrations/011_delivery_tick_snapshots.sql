-- Per-tick world snapshots for delivery sessions.
--
-- platform_sessions stays the authoritative save: it holds the latest state
-- only. This table is the replayable tick log, written by the session's engine
-- tick hook as the world advances, independent of autosave.
--
-- state is a compact engine.SavedState frame: cars, orders, money, reputation,
-- counters, spawner memory. The append-only cash and reputation histories are
-- NOT duplicated here (they are already per-movement rows in
-- delivery_cash_entries / delivery_reputation_entries) and neither is the
-- retained event feed, so the row size stays proportional to live state rather
-- than session length.
--
-- No foreign key to platform_sessions: a session may tick before its first
-- save creates the parent row, and snapshots are best-effort derived data.
-- Manager.Delete removes a session's rows explicitly. Retention is bounded per
-- session by DELIVERY_TICK_SNAPSHOTS.
CREATE TABLE IF NOT EXISTS delivery_tick_snapshots (
    session_id text        NOT NULL,
    tick       bigint      NOT NULL,
    state      jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, tick)
);

-- The primary key already serves the only read pattern (one session, ordered
-- by tick). This index serves retention sweeps that cross sessions, e.g. an
-- orphan cleanup after rows are deleted out of band.
CREATE INDEX IF NOT EXISTS delivery_tick_snapshots_created_idx
    ON delivery_tick_snapshots (created_at);
