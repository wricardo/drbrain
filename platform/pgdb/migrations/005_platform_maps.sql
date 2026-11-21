-- Custom player maps for every game. Builtin maps stay on disk: only
-- user-created rows live here. Rows are drafts until published; edit tokens are
-- stored as SHA-256 hex so a leaked table never yields a usable token.
CREATE TABLE IF NOT EXISTS platform_maps (
    game            text        NOT NULL,
    id              text        NOT NULL,
    config          jsonb       NOT NULL,
    owner_id        text        NOT NULL DEFAULT '',
    status          text        NOT NULL DEFAULT 'PUBLISHED' CHECK (status IN ('DRAFT', 'PUBLISHED')),
    edit_token_hash text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (game, id)
);

CREATE INDEX IF NOT EXISTS platform_maps_game_owner_idx ON platform_maps (game, owner_id);
CREATE INDEX IF NOT EXISTS platform_maps_game_status_idx ON platform_maps (game, status);
