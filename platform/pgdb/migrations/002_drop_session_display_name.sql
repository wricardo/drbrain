-- Motor sessions are identified by their account (or shown as a guest); the
-- per-session display name is gone from the schema and the API.
ALTER TABLE sessions DROP COLUMN IF EXISTS display_name;
