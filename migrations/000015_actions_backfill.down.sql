-- The backfill repairs data and tightens two columns the Actions code relies
-- on. The data repair is not reversible (the NULLs it replaced carried no
-- information), so this reverts only the reversible half: the added index and
-- the NOT NULL on due_at, which 000001_init declared nullable.
BEGIN;

DROP INDEX IF EXISTS idx_follow_ups_assigned_to;

ALTER TABLE follow_ups ALTER COLUMN due_at DROP NOT NULL;

COMMIT;
