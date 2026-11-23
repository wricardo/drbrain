-- Re-stamp sessions written by a binary that predates the game-scoped store.
--
-- 009 stamped the rows that existed when it ran. A server rolled back to an
-- older build (or an in-flight save from the process being replaced) writes a
-- meta without a game, which a scoped store then correctly hides as another
-- game's row. Running the same shape-based stamp once more after the upgrade
-- recovers those rows; it is a no-op for already stamped rows.
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
