BEGIN;

DROP INDEX IF EXISTS idx_follow_ups_org_due;
DROP INDEX IF EXISTS idx_follow_ups_account_id;
DROP INDEX IF EXISTS idx_follow_ups_org_id;

ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;

UPDATE follow_ups SET status = 'pending' WHERE status = 'open';
UPDATE follow_ups SET status = 'sent' WHERE status IN ('in_progress', 'done');

ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['pending', 'sent', 'cancelled']));

-- Drop every column 000014 added, so a rollback leaves the legacy reminder
-- schema rather than a half-migrated hybrid the old code cannot insert into.
ALTER TABLE follow_ups DROP COLUMN IF EXISTS completed_at;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS deal_id;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS lead_id;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS account_id;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS assigned_to;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS due_at;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS title;
ALTER TABLE follow_ups DROP COLUMN IF EXISTS org_id;

-- Restore the NOT NULLs 000014 relaxed. Rows that never carried a reminder
-- payload can't satisfy them, so they go first.
DELETE FROM follow_ups WHERE invoice_id IS NULL OR scheduled_for IS NULL OR channel IS NULL;
ALTER TABLE follow_ups ALTER COLUMN invoice_id SET NOT NULL;
ALTER TABLE follow_ups ALTER COLUMN scheduled_for SET NOT NULL;
ALTER TABLE follow_ups ALTER COLUMN channel SET NOT NULL;

COMMIT;
