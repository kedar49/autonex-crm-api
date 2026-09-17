BEGIN;

ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_priority_check;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS priority;

COMMIT;
