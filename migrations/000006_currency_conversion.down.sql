DROP INDEX IF EXISTS leads_org_converted_idx;

ALTER TABLE leads DROP COLUMN IF EXISTS converted_contact_id;
ALTER TABLE leads DROP COLUMN IF EXISTS converted_deal_id;
ALTER TABLE leads DROP COLUMN IF EXISTS converted_at;

-- Restoring NOT NULL would fail on any contact created without them, so fill in
-- placeholders first rather than deleting the rows.
UPDATE contacts SET last_name = '' WHERE last_name IS NULL;
UPDATE contacts SET email = concat('unknown+', id::text, '@example.invalid') WHERE email IS NULL;

DROP INDEX IF EXISTS contacts_org_email_idx;
CREATE UNIQUE INDEX contacts_org_email_idx ON contacts (org_id, lower(email));

ALTER TABLE contacts ALTER COLUMN email SET NOT NULL;
ALTER TABLE contacts ALTER COLUMN last_name SET NOT NULL;

ALTER TABLE organizations DROP COLUMN IF EXISTS currency;
