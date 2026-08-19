DROP INDEX IF EXISTS contacts_org_email_idx;
DROP INDEX IF EXISTS activities_org_created_idx;
DROP INDEX IF EXISTS deals_org_created_idx;
DROP INDEX IF EXISTS contacts_org_created_idx;
DROP INDEX IF EXISTS accounts_org_created_idx;

ALTER TABLE activities DROP COLUMN IF EXISTS org_id;
ALTER TABLE deals      DROP COLUMN IF EXISTS org_id;
ALTER TABLE contacts   DROP COLUMN IF EXISTS org_id;
ALTER TABLE accounts   DROP COLUMN IF EXISTS org_id;
ALTER TABLE users      DROP COLUMN IF EXISTS org_id;

-- Organizations created by the backfill are dropped with the table.
DROP TABLE IF EXISTS organizations;
