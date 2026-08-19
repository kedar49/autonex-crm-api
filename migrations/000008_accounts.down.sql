DROP INDEX IF EXISTS accounts_org_name_idx;
DROP INDEX IF EXISTS accounts_org_owner_idx;
DROP INDEX IF EXISTS accounts_org_created_name_idx;

ALTER TABLE accounts DROP COLUMN IF EXISTS updated_at;
ALTER TABLE accounts DROP COLUMN IF EXISTS owner_user_id;
ALTER TABLE accounts DROP COLUMN IF EXISTS notes;
ALTER TABLE accounts DROP COLUMN IF EXISTS phone;
ALTER TABLE accounts DROP COLUMN IF EXISTS industry;
ALTER TABLE accounts DROP COLUMN IF EXISTS website;
