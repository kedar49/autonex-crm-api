DROP INDEX IF EXISTS deals_org_contact_idx;
DROP INDEX IF EXISTS deals_org_owner_idx;
DROP INDEX IF EXISTS deals_org_stage_position_idx;

ALTER TABLE deals DROP CONSTRAINT IF EXISTS deals_stage_check;

ALTER TABLE deals DROP COLUMN IF EXISTS updated_at;
ALTER TABLE deals DROP COLUMN IF EXISTS position;
ALTER TABLE deals DROP COLUMN IF EXISTS expected_close_date;
ALTER TABLE deals DROP COLUMN IF EXISTS contact_id;
ALTER TABLE deals DROP COLUMN IF EXISTS owner_user_id;
ALTER TABLE deals DROP COLUMN IF EXISTS description;

-- Restoring NOT NULL would fail on any account-less deal, so drop those first.
DELETE FROM deals WHERE account_id IS NULL;
ALTER TABLE deals ALTER COLUMN account_id SET NOT NULL;
