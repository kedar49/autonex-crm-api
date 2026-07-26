DROP TABLE IF EXISTS leads;

DROP INDEX IF EXISTS invitations_org_idx;
DROP INDEX IF EXISTS invitations_pending_idx;
DROP TABLE IF EXISTS invitations;

ALTER TABLE users DROP COLUMN IF EXISTS name;
