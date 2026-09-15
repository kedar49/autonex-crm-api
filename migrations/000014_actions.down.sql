BEGIN;

DROP INDEX IF EXISTS idx_follow_ups_org_due;
DROP INDEX IF EXISTS idx_follow_ups_account_id;
DROP INDEX IF EXISTS idx_follow_ups_org_id;

ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;

UPDATE follow_ups SET status = 'pending' WHERE status = 'open';
UPDATE follow_ups SET status = 'sent' WHERE status IN ('in_progress', 'done');

ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'pending';
ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['pending', 'sent', 'cancelled']));

-- account_id is the only column 000014 genuinely adds. org_id, title, due_at,
-- assigned_to, lead_id, deal_id and completed_at have all been on follow_ups
-- since 000001_init, so the up-script's ADD COLUMN IF NOT EXISTS lines are
-- no-ops for them and this rollback must leave them alone — dropping them here
-- would destroy data 000014 never created.
ALTER TABLE follow_ups DROP COLUMN IF EXISTS account_id;

-- The up-script's DROP NOT NULL on invoice_id, scheduled_for and channel is
-- likewise a no-op: 000001 declared all three nullable. Nothing to restore.

COMMIT;
