BEGIN;

-- 1. Ensure all existing users have a profile row so follow_ups FK (assigned_to -> profiles.id) and RBAC claims remain valid
INSERT INTO profiles (id, full_name, role)
SELECT u.id, coalesce(u.name, split_part(u.email, '@', 1), 'User'), 'sales'
FROM users u
LEFT JOIN profiles p ON p.id = u.id
WHERE p.id IS NULL
ON CONFLICT (id) DO NOTHING;

-- 2. Add columns required for Actions dashboard if missing
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS title TEXT NOT NULL DEFAULT '';
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS due_at TIMESTAMPTZ NOT NULL DEFAULT now();
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS assigned_to UUID REFERENCES profiles(id) ON DELETE SET NULL;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS lead_id UUID REFERENCES leads(id) ON DELETE SET NULL;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS deal_id UUID REFERENCES deals(id) ON DELETE SET NULL;
ALTER TABLE follow_ups ADD COLUMN IF NOT EXISTS completed_at TIMESTAMPTZ;

-- 3. Make sure the dormant reminder-sequence columns are nullable. 000001_init
--    already declares them so; these are defensive no-ops for any database that
--    tightened them by hand.
ALTER TABLE follow_ups ALTER COLUMN invoice_id DROP NOT NULL;
ALTER TABLE follow_ups ALTER COLUMN scheduled_for DROP NOT NULL;
ALTER TABLE follow_ups ALTER COLUMN channel DROP NOT NULL;

-- 4. Safely normalize status constraint without risking errors on unexpected status values
ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;

UPDATE follow_ups SET status = 'open' WHERE status IS NULL OR status NOT IN ('open', 'in_progress', 'done', 'sent', 'cancelled');
UPDATE follow_ups SET status = 'open' WHERE status = 'pending';
UPDATE follow_ups SET status = 'done' WHERE status IN ('sent', 'cancelled');

ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'open';
ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['open', 'in_progress', 'done']));

-- 5. Indexes for fast org-scoped listing and filtering
CREATE INDEX IF NOT EXISTS idx_follow_ups_org_id ON follow_ups(org_id);
CREATE INDEX IF NOT EXISTS idx_follow_ups_account_id ON follow_ups(account_id);
CREATE INDEX IF NOT EXISTS idx_follow_ups_org_due ON follow_ups(org_id, due_at ASC);

COMMIT;
