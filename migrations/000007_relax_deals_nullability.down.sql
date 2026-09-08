BEGIN;

-- Fallback for any null values before restoring NOT NULL
UPDATE deals SET owner_id = (SELECT id FROM profiles LIMIT 1) WHERE owner_id IS NULL;
UPDATE deals SET account_id = (SELECT id FROM accounts LIMIT 1) WHERE account_id IS NULL;

ALTER TABLE deals ALTER COLUMN owner_id SET NOT NULL;
ALTER TABLE deals ALTER COLUMN account_id SET NOT NULL;

COMMIT;
