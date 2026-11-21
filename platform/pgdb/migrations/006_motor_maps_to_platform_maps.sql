-- Move Motor's player-created maps from the legacy maps table into the shared
-- platform_maps catalogue. Owner and status move from config fields to
-- columns, so the stored config is the playable map only. The old table stays
-- untouched for rollback and is removed in a later release.
INSERT INTO platform_maps (game, id, config, owner_id, status, created_at, updated_at)
SELECT 'motor', id, config - 'owner_id' - 'status',
       COALESCE(config->>'owner_id', ''),
       CASE WHEN config->>'status' = 'DRAFT' THEN 'DRAFT' ELSE 'PUBLISHED' END,
       created_at, updated_at
FROM maps
ON CONFLICT (game, id) DO NOTHING;
