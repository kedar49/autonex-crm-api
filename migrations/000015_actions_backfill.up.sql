-- Re-applies the safety steps of 000014 for databases that ran an earlier
-- revision of that file. Every statement is idempotent, so this is a no-op on
-- an environment already carrying the current 000014.
BEGIN;

-- Every user needs a profile row: it is both the RBAC role source and the
-- target of follow_ups.assigned_to.
INSERT INTO profiles (id, full_name, role)
SELECT u.id, coalesce(u.name, split_part(u.email, '@', 1), 'User'), 'sales'
FROM users u
LEFT JOIN profiles p ON p.id = u.id
WHERE p.id IS NULL
ON CONFLICT (id) DO NOTHING;

-- Go scans due_at into a concrete time.Time, so a NULL here is a runtime error.
UPDATE follow_ups SET due_at = now() WHERE due_at IS NULL;
ALTER TABLE follow_ups ALTER COLUMN due_at SET DEFAULT now();
ALTER TABLE follow_ups ALTER COLUMN due_at SET NOT NULL;

UPDATE follow_ups SET title = '' WHERE title IS NULL;
ALTER TABLE follow_ups ALTER COLUMN title SET DEFAULT '';
ALTER TABLE follow_ups ALTER COLUMN title SET NOT NULL;

-- Normalise before constraining, so legacy reminder statuses can't fail the check.
ALTER TABLE follow_ups DROP CONSTRAINT IF EXISTS follow_ups_status_check;
UPDATE follow_ups SET status = 'open' WHERE status IS NULL OR status IN ('pending');
UPDATE follow_ups SET status = 'done' WHERE status IN ('sent', 'cancelled');
UPDATE follow_ups SET status = 'open' WHERE status NOT IN ('open', 'in_progress', 'done');
ALTER TABLE follow_ups ALTER COLUMN status SET DEFAULT 'open';
ALTER TABLE follow_ups ADD CONSTRAINT follow_ups_status_check CHECK (status = ANY (ARRAY['open', 'in_progress', 'done']));

CREATE INDEX IF NOT EXISTS idx_follow_ups_org_due ON follow_ups(org_id, due_at ASC);
CREATE INDEX IF NOT EXISTS idx_follow_ups_assigned_to ON follow_ups(assigned_to);

COMMIT;
