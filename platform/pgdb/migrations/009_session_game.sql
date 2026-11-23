-- platform_sessions is shared by mazes, music, tesla and delivery. Until now
-- the store listed every row, so each game's session picker (and delivery's
-- delete/load path) could see and overwrite another game's saves.
--
-- sessionstore.Options.Game now scopes a store to one game. Rows written
-- before this migration carry no game field; stamp each from its payload's
-- top-level shape so existing saves keep working under the scoped stores.
UPDATE platform_sessions
SET meta = meta || jsonb_build_object('game', stamped.game)
FROM (
    SELECT id, CASE
        WHEN doc ? 'world' OR doc ? 'city_id' OR doc ? 'city_file' THEN 'delivery'
        WHEN doc ? 'piece' OR doc ? 'slots'                              THEN 'music'
        WHEN doc ? 'game_state' OR doc ? 'map_name'                      THEN 'tesla'
        WHEN doc ? 'map' AND doc ? 'state'                               THEN 'mazes'
    END AS game
    FROM (
        SELECT id, convert_from(payload, 'UTF8')::jsonb AS doc
        FROM platform_sessions
    ) AS decoded
) AS stamped
WHERE platform_sessions.id = stamped.id
  AND stamped.game IS NOT NULL
  AND platform_sessions.meta->>'game' IS NULL;

CREATE INDEX IF NOT EXISTS platform_sessions_game_idx
    ON platform_sessions ((meta->>'game'));
