BEGIN;

ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;

UPDATE follow_ups SET status = 'open' WHERE status = 'pending';
UPDATE follow_ups SET status = 'done' WHERE status IN ('sent', 'cancelled');

ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'open';

ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['open', 'in_progress', 'done']));

CREATE INDEX IF NOT EXISTS idx_follow_ups_org_id ON follow_ups(org_id);
CREATE INDEX IF NOT EXISTS idx_follow_ups_account_id ON follow_ups(account_id);

COMMIT;
