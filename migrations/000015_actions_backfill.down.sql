-- The backfill only repairs data and adds indexes; the data repair is not
-- reversible (the original NULLs carried no information), so down drops just
-- the indexes this migration added.
BEGIN;

DROP INDEX IF EXISTS idx_follow_ups_assigned_to;

COMMIT;
