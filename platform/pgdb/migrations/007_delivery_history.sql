-- Delivery history projections. platform_sessions remains the authoritative
-- snapshot; these tables are derived, indexed, and rebuildable from it.
--
-- The session row is upserted by the save/autosave path. Cash entries are
-- append-only and keyed by the engine's monotonic seq, so replaying a save is
-- idempotent. Order rows are upserted until they reach a terminal state.

CREATE TABLE IF NOT EXISTS delivery_sessions (
    id             text        PRIMARY KEY REFERENCES platform_sessions (id) ON DELETE CASCADE,
    city_id        text        NOT NULL,
    account_id     text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL,
    tick           bigint      NOT NULL DEFAULT 0,
    money          bigint      NOT NULL DEFAULT 0,
    reputation     double precision NOT NULL DEFAULT 0,
    last_cash_seq  integer     NOT NULL DEFAULT -1,
    updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS delivery_sessions_city_tick_idx
    ON delivery_sessions (city_id, tick DESC);
CREATE INDEX IF NOT EXISTS delivery_sessions_account_idx
    ON delivery_sessions (account_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS delivery_cash_entries (
    session_id text    NOT NULL REFERENCES delivery_sessions (id) ON DELETE CASCADE,
    seq        integer NOT NULL,
    tick       bigint  NOT NULL,
    kind       text    NOT NULL,
    amount     integer NOT NULL,
    balance    integer NOT NULL,
    car_id     text    NOT NULL DEFAULT '',
    car_name   text    NOT NULL DEFAULT '',
    order_id   text    NOT NULL DEFAULT '',
    memo       text    NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, seq)
);

CREATE INDEX IF NOT EXISTS delivery_cash_entries_tick_idx
    ON delivery_cash_entries (session_id, tick);
CREATE INDEX IF NOT EXISTS delivery_cash_entries_kind_idx
    ON delivery_cash_entries (kind, tick);

CREATE TABLE IF NOT EXISTS delivery_order_history (
    session_id     text    NOT NULL REFERENCES delivery_sessions (id) ON DELETE CASCADE,
    order_id       text    NOT NULL,
    item           text    NOT NULL,
    quantity       integer NOT NULL,
    kitchen_id     text    NOT NULL DEFAULT '',
    customer_id    text    NOT NULL DEFAULT '',
    assigned_car   text    NOT NULL DEFAULT '',
    state          text    NOT NULL,
    created_tick   bigint  NOT NULL,
    deadline_tick  bigint  NOT NULL,
    delivered_tick bigint  NOT NULL DEFAULT 0,
    reward         integer NOT NULL DEFAULT 0,
    penalty        integer NOT NULL DEFAULT 0,
    paid           integer NOT NULL DEFAULT 0,
    late           boolean NOT NULL DEFAULT false,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (session_id, order_id)
);

CREATE INDEX IF NOT EXISTS delivery_order_history_state_idx
    ON delivery_order_history (session_id, state);
CREATE INDEX IF NOT EXISTS delivery_order_history_created_idx
    ON delivery_order_history (created_tick);
