-- Two related additions: a workspace currency, and lead → deal conversion.

-- ---------------------------------------------------------------------------
-- Currency
-- ---------------------------------------------------------------------------

-- Currency lives on the organization, not per record: a workspace bills in one
-- currency, and mixing them per row would make every pipeline total meaningless
-- without an FX table. Per-document currency can come with invoicing if needed.
ALTER TABLE organizations
    ADD COLUMN currency TEXT NOT NULL DEFAULT 'USD'
    CHECK (currency ~ '^[A-Z]{3}$');

-- ---------------------------------------------------------------------------
-- Contacts: relax two columns that made conversion impossible
-- ---------------------------------------------------------------------------

-- A lead only requires a first name, so requiring both a surname and an email on
-- contacts meant a perfectly good lead could not become one. Both are also just
-- true of real data: mononyms exist, and a phone-only contact is normal.
ALTER TABLE contacts ALTER COLUMN last_name DROP NOT NULL;
ALTER TABLE contacts ALTER COLUMN email DROP NOT NULL;

-- The per-tenant email uniqueness has to become partial: without the WHERE
-- clause a second email-less contact would collide with the first.
DROP INDEX IF EXISTS contacts_org_email_idx;
CREATE UNIQUE INDEX contacts_org_email_idx
    ON contacts (org_id, lower(email))
    WHERE email IS NOT NULL;

-- ---------------------------------------------------------------------------
-- Lead conversion
-- ---------------------------------------------------------------------------

-- What a lead turned into. SET NULL rather than CASCADE: deleting the deal must
-- not delete the history of the lead that produced it.
ALTER TABLE leads ADD COLUMN converted_at         TIMESTAMPTZ;
ALTER TABLE leads ADD COLUMN converted_deal_id    UUID REFERENCES deals(id) ON DELETE SET NULL;
ALTER TABLE leads ADD COLUMN converted_contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL;

-- converted_at doubles as the idempotency guard: conversion claims the lead with
-- `WHERE converted_at IS NULL`, so two concurrent requests can't both produce a
-- deal. This index keeps that claim and the "already converted?" check cheap.
CREATE INDEX leads_org_converted_idx ON leads (org_id, converted_at);
