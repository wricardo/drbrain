-- Shared session rows for mazes, tesla, delivery and music: platform metadata
-- as jsonb plus each game's own encoded payload. The table used to be created
-- on demand by platform/sessionstore; IF NOT EXISTS adopts databases that
-- already have it, and the version row records it for everyone else.
CREATE TABLE IF NOT EXISTS platform_sessions (
    id         text        PRIMARY KEY,
    meta       jsonb       NOT NULL,
    payload    bytea       NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
