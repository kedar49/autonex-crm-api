-- Flesh out the accounts (company) table from 000001, which only had a name.
--
-- Both contacts.account_id and deals.account_id have existed and been nullable
-- since 000005, with no way to set them. This is the module that fills them in.

ALTER TABLE accounts ADD COLUMN website       TEXT;
ALTER TABLE accounts ADD COLUMN industry      TEXT;
ALTER TABLE accounts ADD COLUMN phone         TEXT;
ALTER TABLE accounts ADD COLUMN notes         TEXT;
-- SET NULL, not CASCADE: losing an owner must never delete the company.
ALTER TABLE accounts ADD COLUMN owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE accounts ADD COLUMN updated_at    TIMESTAMPTZ NOT NULL DEFAULT now();

-- The list query: one org, newest first.
CREATE INDEX accounts_org_created_name_idx ON accounts (org_id, created_at DESC, id);
CREATE INDEX accounts_org_owner_idx        ON accounts (org_id, owner_user_id);

-- Name is deliberately NOT unique per org. Two legitimately distinct entities can
-- share a name ("Acme Ltd" in two regions), and a hard constraint would block a
-- real record for the sake of tidiness. An index for name search instead.
CREATE INDEX accounts_org_name_idx ON accounts (org_id, lower(name));
