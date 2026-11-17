-- Runtime state of Dr Brain - Motor Programming. Built-in maps are not stored
-- here: they are files (maps/*.json). JSON bodies mirror the file formats.

CREATE TABLE accounts (
    id                text        PRIMARY KEY,
    provider          text        NOT NULL,
    provider_user_id  text        NOT NULL,
    handle            text        NOT NULL,
    display_name      text        NOT NULL,
    profile_public    boolean     NOT NULL DEFAULT false,
    created_at        timestamptz NOT NULL,
    handle_changed_at timestamptz,
    password_hash     text        NOT NULL DEFAULT ''
);
-- Handles are unique ignoring case; a provider identity belongs to one account.
CREATE UNIQUE INDEX accounts_handle_key   ON accounts (lower(handle));
CREATE UNIQUE INDEX accounts_identity_key ON accounts (provider, provider_user_id);

-- One row per signed-in browser, keyed by the SHA-256 of the cookie token (never the token).
CREATE TABLE logins (
    token_hash text        PRIMARY KEY,
    account_id text        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);
CREATE INDEX logins_account_idx ON logins (account_id, created_at);
CREATE INDEX logins_expires_idx ON logins (expires_at);

CREATE TABLE sessions (
    id              text        PRIMARY KEY,
    display_name    text        NOT NULL,
    map_id          text        NOT NULL,
    map             jsonb       NOT NULL,
    program         jsonb       NOT NULL,
    vm              jsonb       NOT NULL,
    speed_ms        integer     NOT NULL,
    seq             bigint      NOT NULL,
    created_at      timestamptz NOT NULL,
    last_action_at  timestamptz NOT NULL,
    attempts        integer     NOT NULL,
    best_steps      integer     NOT NULL,
    fewest_slots    jsonb,
    fewest_steps    jsonb,
    -- A deleted account leaves its sessions behind as guest rows.
    account_id      text        REFERENCES accounts (id) ON DELETE SET NULL,
    owner_hash      text        NOT NULL DEFAULT ''
);
CREATE INDEX sessions_last_action_idx ON sessions (last_action_at DESC);
CREATE INDEX sessions_created_idx     ON sessions (created_at DESC);
CREATE INDEX sessions_map_idx         ON sessions (map_id);
CREATE INDEX sessions_account_idx     ON sessions (account_id);

-- Player-created maps only; the shipped maps stay files.
CREATE TABLE maps (
    id         text        PRIMARY KEY,
    config     jsonb       NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
