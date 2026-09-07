BEGIN;

UPDATE activities SET entity_type = 'company' WHERE entity_type = 'account';

ALTER TABLE account_profiles RENAME COLUMN account_id TO company_id;
ALTER TABLE account_profiles RENAME TO company_profiles;

ALTER TABLE contacts RENAME COLUMN account_id TO company_id;
ALTER TABLE leads RENAME COLUMN account_id TO company_id;
ALTER TABLE deals RENAME COLUMN account_id TO company_id;
ALTER TABLE quotes RENAME COLUMN account_id TO company_id;
ALTER TABLE invoices RENAME COLUMN account_id TO company_id;

ALTER TABLE accounts RENAME TO companies;

CREATE TABLE IF NOT EXISTS accounts (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    website       TEXT,
    industry      TEXT,
    phone         TEXT,
    notes         TEXT,
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE contacts ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE leads ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE deals ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE CASCADE;
ALTER TABLE quotes ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;
ALTER TABLE invoices ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

COMMIT;
