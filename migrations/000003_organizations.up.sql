-- Tenancy. Every user belongs to exactly one organization and every CRM row is
-- scoped to one, so an authenticated request can only ever reach its own data.
--
-- Naming: "organizations" is the TENANT (a customer of go-CRM). The pre-existing
-- "accounts" table is CRM-speak for a company the tenant sells to, and is itself
-- scoped by org_id below.

CREATE TABLE organizations (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Added nullable so existing rows can be backfilled before the NOT NULL below.
ALTER TABLE users ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE RESTRICT;

-- Backfill: give every pre-tenancy user their own personal workspace.
DO $$
DECLARE
    u       RECORD;
    new_org UUID;
BEGIN
    FOR u IN SELECT id, email FROM users WHERE org_id IS NULL LOOP
        INSERT INTO organizations (name)
        VALUES (split_part(u.email, '@', 1) || '''s workspace')
        RETURNING id INTO new_org;

        UPDATE users SET org_id = new_org WHERE id = u.id;
    END LOOP;
END $$;

ALTER TABLE users ALTER COLUMN org_id SET NOT NULL;

-- Scope the CRM tables. ON DELETE CASCADE: removing a tenant removes its data.
ALTER TABLE accounts   ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE contacts   ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE deals      ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;
ALTER TABLE activities ADD COLUMN org_id UUID REFERENCES organizations(id) ON DELETE CASCADE;

-- Any pre-tenancy CRM rows cannot be attributed to a tenant, so they go to the
-- oldest organization. In practice these tables are empty: no code has ever
-- written to them. If a row here surprises you, the SET NOT NULL below is what
-- fails loudly rather than silently mis-attributing data.
UPDATE accounts   SET org_id = (SELECT id FROM organizations ORDER BY created_at, id LIMIT 1) WHERE org_id IS NULL;
UPDATE contacts   SET org_id = (SELECT id FROM organizations ORDER BY created_at, id LIMIT 1) WHERE org_id IS NULL;
UPDATE deals      SET org_id = (SELECT id FROM organizations ORDER BY created_at, id LIMIT 1) WHERE org_id IS NULL;
UPDATE activities SET org_id = (SELECT id FROM organizations ORDER BY created_at, id LIMIT 1) WHERE org_id IS NULL;

ALTER TABLE accounts   ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE contacts   ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE deals      ALTER COLUMN org_id SET NOT NULL;
ALTER TABLE activities ALTER COLUMN org_id SET NOT NULL;

-- Every list query is "newest first, within my org" — index for exactly that.
CREATE INDEX accounts_org_created_idx   ON accounts (org_id, created_at DESC);
CREATE INDEX contacts_org_created_idx   ON contacts (org_id, created_at DESC);
CREATE INDEX deals_org_created_idx      ON deals (org_id, created_at DESC);
CREATE INDEX activities_org_created_idx ON activities (org_id, created_at DESC);

-- Contact email is unique per tenant, not globally: two tenants may each hold a
-- contact for the same person.
CREATE UNIQUE INDEX contacts_org_email_idx ON contacts (org_id, lower(email));
