DROP INDEX IF EXISTS activities_org_human_idx;
DROP INDEX IF EXISTS activities_contact_idx;
DROP INDEX IF EXISTS activities_account_idx;
DROP INDEX IF EXISTS activities_deal_idx;
DROP INDEX IF EXISTS activities_lead_idx;
DROP INDEX IF EXISTS activities_org_occurred_idx;

ALTER TABLE activities DROP COLUMN IF EXISTS duration_minutes;
ALTER TABLE activities DROP COLUMN IF EXISTS occurred_at;
ALTER TABLE activities DROP COLUMN IF EXISTS subject;
ALTER TABLE activities DROP COLUMN IF EXISTS created_by;
ALTER TABLE activities DROP COLUMN IF EXISTS invoice_id;
ALTER TABLE activities DROP COLUMN IF EXISTS quote_id;
ALTER TABLE activities DROP COLUMN IF EXISTS account_id;
ALTER TABLE activities DROP COLUMN IF EXISTS lead_id;

ALTER TABLE activities DROP COLUMN IF EXISTS kind;
ALTER TABLE activities ADD COLUMN type TEXT NOT NULL DEFAULT 'note';
