-- Consolidated Initial Schema Migration for go_CRM

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- ===========================================================================
-- SECTION 1: Gateway Auth & Multi-Tenancy
-- ===========================================================================

CREATE TABLE IF NOT EXISTS organizations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name        TEXT NOT NULL,
    currency    TEXT NOT NULL DEFAULT 'USD' CHECK (currency ~ '^[A-Z]{3}$'),
    quote_seq   INTEGER NOT NULL DEFAULT 0,
    invoice_seq INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE RESTRICT,
    email            TEXT NOT NULL UNIQUE,
    password_hash    TEXT,
    auth_provider    TEXT NOT NULL DEFAULT 'email',
    provider_user_id TEXT,
    name             TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS profiles (
    id         UUID PRIMARY KEY,
    full_name  TEXT NOT NULL,
    role       TEXT NOT NULL DEFAULT 'sales' CHECK (role IN ('owner', 'admin', 'sales', 'account_manager', 'client')),
    avatar_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ,
    replaced_by UUID REFERENCES refresh_tokens(id)
);

CREATE TABLE IF NOT EXISTS invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    token_hash  TEXT NOT NULL UNIQUE,
    invited_by  UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- SECTION 2: Core CRM Entities
-- ===========================================================================

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

CREATE TABLE IF NOT EXISTS companies (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    domain      TEXT,
    industry    TEXT,
    city        TEXT,
    website     TEXT,
    source      TEXT,
    tags        TEXT[],
    logo_path   TEXT,
    owner_id    UUID REFERENCES profiles(id),
    deleted_at  TIMESTAMPTZ,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS contacts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    company_id  UUID REFERENCES companies(id) ON DELETE CASCADE,
    account_id  UUID REFERENCES accounts(id) ON DELETE SET NULL,
    first_name  TEXT NOT NULL,
    last_name   TEXT,
    email       TEXT,
    phone       TEXT,
    title       TEXT,
    deleted_at  TIMESTAMPTZ,
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS leads (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id               UUID REFERENCES organizations(id) ON DELETE CASCADE,
    account_id           UUID REFERENCES accounts(id) ON DELETE SET NULL,
    company_id           UUID REFERENCES companies(id),
    contact_id           UUID REFERENCES contacts(id),
    title                TEXT,
    first_name           TEXT,
    last_name            TEXT,
    company              TEXT,
    contact_name         TEXT,
    job_title            TEXT,
    email                TEXT,
    phone                TEXT,
    linkedin_url         TEXT,
    industry             TEXT,
    location             TEXT,
    product_interest     TEXT,
    source               TEXT,
    status               TEXT NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'contacted', 'replied', 'call_booked', 'call_done', 'converted', 'dropped')),
    stage                TEXT NOT NULL DEFAULT 'new',
    assigned_to          UUID REFERENCES profiles(id),
    owner_user_id        UUID REFERENCES users(id),
    value_estimate       NUMERIC(15,2),
    value                NUMERIC(15,2),
    next_follow_up_date  DATE,
    follow_up_at         TIMESTAMPTZ,
    notes                TEXT,
    converted_at         TIMESTAMPTZ,
    converted_deal_id    UUID,
    converted_contact_id UUID,
    deleted_at           TIMESTAMPTZ,
    archived_at          TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS deals (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                   UUID REFERENCES organizations(id) ON DELETE CASCADE,
    title                    TEXT NOT NULL DEFAULT 'New Deal',
    job_title                TEXT,
    company_id               UUID REFERENCES companies(id),
    account_id               UUID REFERENCES accounts(id) ON DELETE CASCADE,
    contact_id               UUID REFERENCES contacts(id),
    primary_contact_id       UUID REFERENCES contacts(id),
    lead_id                  UUID REFERENCES leads(id) ON DELETE SET NULL,
    stage                    TEXT NOT NULL DEFAULT 'discovery' CHECK (stage IN ('discovery', 'site_assessment', 'quote_sent', 'negotiation', 'won', 'lost')),
    position                 INTEGER NOT NULL DEFAULT 0,
    amount                   NUMERIC(15,2) NOT NULL DEFAULT 0,
    description              TEXT,
    product_use_case         TEXT,
    probability              INTEGER CHECK (probability IS NULL OR (probability >= 0 AND probability <= 100)),
    next_action              TEXT,
    site_assessment_date     DATE,
    site_assessment_location TEXT,
    site_assessment_notes    TEXT,
    lost_reason              TEXT,
    notes                    TEXT,
    owner_id                 UUID REFERENCES profiles(id),
    owner_user_id            UUID REFERENCES users(id) ON DELETE SET NULL,
    expected_close_date      DATE,
    deleted_at               TIMESTAMPTZ,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- SECTION 3: Quotes, Invoices & Sales Entities
-- ===========================================================================

CREATE TABLE IF NOT EXISTS quotes (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID REFERENCES organizations(id) ON DELETE CASCADE,
    number          TEXT,
    deal_id         UUID REFERENCES deals(id),
    company_id      UUID REFERENCES companies(id),
    account_id      UUID REFERENCES accounts(id) ON DELETE SET NULL,
    contact_id      UUID REFERENCES contacts(id) ON DELETE SET NULL,
    owner_user_id   UUID REFERENCES users(id) ON DELETE SET NULL,
    status          TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'approved', 'rejected', 'expired')),
    current_version INTEGER NOT NULL DEFAULT 1,
    created_by      UUID REFERENCES profiles(id),
    valid_until     DATE,
    currency        VARCHAR(3) NOT NULL DEFAULT 'USD',
    subtotal        NUMERIC(15,2) NOT NULL DEFAULT 0,
    discount_total  NUMERIC(15,2) NOT NULL DEFAULT 0,
    tax_total       NUMERIC(15,2) NOT NULL DEFAULT 0,
    total           NUMERIC(15,2) NOT NULL DEFAULT 0,
    notes           TEXT,
    sent_at         TIMESTAMPTZ,
    accepted_at     TIMESTAMPTZ,
    declined_at     TIMESTAMPTZ,
    deleted_at      TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quote_versions (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id       UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL,
    line_items     JSONB NOT NULL DEFAULT '[]'::jsonb,
    subtotal       NUMERIC(15,2) NOT NULL DEFAULT 0,
    tax            NUMERIC(15,2) NOT NULL DEFAULT 0,
    total          NUMERIC(15,2) NOT NULL DEFAULT 0,
    currency       VARCHAR(3) NOT NULL DEFAULT 'USD',
    pdf_path       TEXT,
    is_current     BOOLEAN NOT NULL DEFAULT false,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quote_approvals (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_version_id UUID NOT NULL REFERENCES quote_versions(id),
    magic_link_token TEXT UNIQUE NOT NULL,
    approved_by_name TEXT,
    approved_by_email TEXT,
    signature_data   TEXT,
    approved_at      TIMESTAMPTZ,
    ip_address       TEXT,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS quote_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id         UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    position         INTEGER NOT NULL DEFAULT 0,
    description      TEXT NOT NULL,
    quantity         NUMERIC(12,3) NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    unit_price       NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    discount_percent NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (discount_percent >= 0 AND discount_percent <= 100),
    tax_percent      NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (tax_percent >= 0 AND tax_percent <= 100),
    line_total       NUMERIC(14,2) NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS invoices (
    id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id             UUID REFERENCES organizations(id) ON DELETE CASCADE,
    quote_id           UUID REFERENCES quotes(id),
    company_id         UUID REFERENCES companies(id),
    account_id         UUID REFERENCES accounts(id) ON DELETE SET NULL,
    contact_id         UUID REFERENCES contacts(id) ON DELETE SET NULL,
    deal_id            UUID REFERENCES deals(id) ON DELETE SET NULL,
    owner_user_id      UUID REFERENCES users(id) ON DELETE SET NULL,
    number             TEXT,
    invoice_number     TEXT UNIQUE,
    title              TEXT,
    status             TEXT NOT NULL DEFAULT 'draft' CHECK (status IN ('draft', 'sent', 'paid', 'overdue', 'void')),
    amount_due         NUMERIC(15,2) NOT NULL DEFAULT 0,
    currency           VARCHAR(3) NOT NULL DEFAULT 'USD',
    issue_date         DATE,
    due_date           DATE,
    subtotal           NUMERIC(15,2) NOT NULL DEFAULT 0,
    discount_total     NUMERIC(15,2) NOT NULL DEFAULT 0,
    tax_total          NUMERIC(15,2) NOT NULL DEFAULT 0,
    total              NUMERIC(15,2) NOT NULL DEFAULT 0,
    amount_paid        NUMERIC(15,2) NOT NULL DEFAULT 0,
    stripe_invoice_id  TEXT,
    payment_link       TEXT,
    source_quote_id    TEXT,
    account_manager_id UUID REFERENCES profiles(id),
    sent_at            TIMESTAMPTZ,
    paid_at            TIMESTAMPTZ,
    voided_at          TIMESTAMPTZ,
    deleted_at         TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS invoice_items (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id       UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    position         INTEGER NOT NULL DEFAULT 0,
    description      TEXT NOT NULL,
    quantity         NUMERIC(12,3) NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    unit_price       NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    discount_percent NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (discount_percent >= 0 AND discount_percent <= 100),
    tax_percent      NUMERIC(5,2) NOT NULL DEFAULT 0 CHECK (tax_percent >= 0 AND tax_percent <= 100),
    line_total       NUMERIC(14,2) NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS payments (
    id                       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id                   UUID REFERENCES organizations(id) ON DELETE CASCADE,
    invoice_id               UUID NOT NULL REFERENCES invoices(id),
    amount                   NUMERIC(15,2) NOT NULL,
    currency                 VARCHAR(3) NOT NULL DEFAULT 'USD',
    stripe_payment_intent_id TEXT,
    status                   TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'succeeded', 'failed', 'refunded')),
    paid_on                  DATE DEFAULT CURRENT_DATE,
    paid_at                  TIMESTAMPTZ,
    method                   TEXT,
    reference                TEXT,
    note                     TEXT,
    created_at               TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS follow_ups (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         UUID REFERENCES organizations(id) ON DELETE CASCADE,
    invoice_id     UUID REFERENCES invoices(id),
    lead_id        UUID REFERENCES leads(id),
    deal_id        UUID REFERENCES deals(id),
    assigned_to    UUID REFERENCES profiles(id),
    title          TEXT NOT NULL DEFAULT '',
    scheduled_for  TIMESTAMPTZ,
    due_at         TIMESTAMPTZ,
    channel        TEXT CHECK (channel IN ('email', 'slack')),
    status         TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'cancelled')),
    attempt_number INTEGER NOT NULL DEFAULT 0,
    completed_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    sku         TEXT NOT NULL DEFAULT '',
    category    TEXT,
    description TEXT,
    unit_price  NUMERIC(15,4) NOT NULL DEFAULT 0,
    currency    VARCHAR(3) NOT NULL DEFAULT 'USD',
    tax_rate    NUMERIC(5,2) NOT NULL DEFAULT 0,
    active      BOOLEAN NOT NULL DEFAULT true,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- SECTION 4: Activity, Auditing & Integrations
-- ===========================================================================

CREATE TABLE IF NOT EXISTS activities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL CHECK (entity_type IN ('company', 'contact', 'lead', 'deal', 'quote', 'invoice')),
    entity_id   UUID NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('note', 'call', 'email', 'meeting', 'system')),
    author_id   UUID REFERENCES profiles(id),
    contact_id  UUID REFERENCES contacts(id) ON DELETE CASCADE,
    deal_id     UUID REFERENCES deals(id) ON DELETE CASCADE,
    body        TEXT NOT NULL DEFAULT '',
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     UUID REFERENCES users(id),
    actor_id    UUID REFERENCES profiles(id),
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    ip_address  TEXT,
    before      JSONB,
    after       JSONB,
    changes     JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS calendar_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID REFERENCES organizations(id) ON DELETE CASCADE,
    deal_id         UUID REFERENCES deals(id),
    lead_id         UUID REFERENCES leads(id) ON DELETE SET NULL,
    google_event_id TEXT UNIQUE NOT NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    start_at        TIMESTAMPTZ NOT NULL,
    end_at          TIMESTAMPTZ NOT NULL,
    meet_link       TEXT,
    created_by      UUID REFERENCES profiles(id),
    synced_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS integration_connections (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID REFERENCES organizations(id) ON DELETE CASCADE,
    user_id             UUID REFERENCES profiles(id) ON DELETE CASCADE,
    provider            TEXT NOT NULL CHECK (provider IN ('google', 'slack')),
    access_token        TEXT NOT NULL DEFAULT '',
    refresh_token       TEXT,
    expires_at          TIMESTAMPTZ,
    scope               TEXT,
    provider_account_id TEXT,
    encrypted_tokens    TEXT,
    metadata            JSONB,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(org_id, provider)
);

CREATE TABLE IF NOT EXISTS slack_channels (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID REFERENCES organizations(id) ON DELETE CASCADE,
    deal_id          UUID REFERENCES deals(id),
    entity_type      TEXT,
    entity_id        UUID,
    channel_id       TEXT,
    slack_channel_id TEXT UNIQUE,
    channel_name     TEXT NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- SECTION 5: Indexes & Performance Optimization
-- ===========================================================================

CREATE INDEX IF NOT EXISTS idx_users_org_id ON users(org_id);
CREATE INDEX IF NOT EXISTS idx_companies_org_id ON companies(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_contacts_org_id ON contacts(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_leads_org_id ON leads(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_deals_org_id ON deals(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_quotes_org_id ON quotes(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_invoices_org_id ON invoices(org_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_activities_org_id ON activities(org_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_org_id ON audit_logs(org_id);

-- ===========================================================================
-- SECTION 6: Stored Procedures & RLS Helper Functions
-- ===========================================================================

CREATE OR REPLACE FUNCTION public.set_updated_at() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.handle_new_user() RETURNS trigger
    LANGUAGE plpgsql SECURITY DEFINER
    AS $$
BEGIN
  INSERT INTO public.profiles (id, full_name, role)
  VALUES (
    NEW.id,
    COALESCE(NEW.raw_user_meta_data->>'full_name', NEW.email),
    COALESCE(NEW.raw_user_meta_data->>'role', 'sales')
  )
  ON CONFLICT (id) DO UPDATE SET
    full_name = EXCLUDED.full_name,
    role = EXCLUDED.role;
  RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION public.current_user_role() RETURNS text
    LANGUAGE sql STABLE SECURITY DEFINER
    AS $$
  SELECT role FROM public.profiles WHERE id = auth.uid();
$$;

CREATE OR REPLACE FUNCTION public.current_user_is_privileged() RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    AS $$ SELECT public.current_user_role() IN ('admin', 'owner'); $$;

CREATE OR REPLACE FUNCTION public.current_user_is_internal() RETURNS boolean
    LANGUAGE sql STABLE SECURITY DEFINER
    AS $$ SELECT public.current_user_role() IN ('owner', 'admin', 'sales', 'account_manager'); $$;
