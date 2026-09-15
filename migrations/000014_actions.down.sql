BEGIN;

DROP INDEX IF EXISTS idx_follow_ups_account_id;
DROP INDEX IF EXISTS idx_follow_ups_org_id;

ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;

ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'pending';

UPDATE follow_ups SET status = 'pending' WHERE status = 'open';
UPDATE follow_ups SET status = 'sent' WHERE status IN ('in_progress', 'done');

ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['pending', 'sent', 'cancelled']));

ALTER TABLE follow_ups DROP COLUMN IF EXISTS account_id;

COMMIT;
