-- Reputation is an append-only series like the cash statement. The engine
-- records the actual delta after clamping, and the save projection inserts new
-- rows by seq. Existing sessions have no historical rows; their chart starts at
-- the first post-migration change (or one current summary point).

ALTER TABLE delivery_sessions
    ADD COLUMN IF NOT EXISTS last_reputation_seq integer NOT NULL DEFAULT -1;

CREATE TABLE IF NOT EXISTS delivery_reputation_entries (
    session_id text    NOT NULL REFERENCES delivery_sessions (id) ON DELETE CASCADE,
    seq        integer NOT NULL,
    tick       bigint  NOT NULL,
    delta      double precision NOT NULL,
    balance    double precision NOT NULL,
    reason     text    NOT NULL,
    order_id   text    NOT NULL DEFAULT '',
    memo       text    NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, seq)
);

CREATE INDEX IF NOT EXISTS delivery_reputation_entries_tick_idx
    ON delivery_reputation_entries (session_id, tick);

-- Cumulative delivered-item charts filter done orders and order by tick.
CREATE INDEX IF NOT EXISTS delivery_order_history_delivered_idx
    ON delivery_order_history (session_id, delivered_tick)
    WHERE state = 'done';
