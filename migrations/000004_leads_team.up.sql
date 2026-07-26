-- Teammates + the leads pipeline.
--
-- Two related additions: an organization can now have more than one member
-- (via invitations), and leads can be assigned to one of those members.

-- Display name for a user. Optional: password signup only asks for an email,
-- while an invited teammate supplies a name when accepting.
ALTER TABLE users ADD COLUMN name TEXT;

-- ---------------------------------------------------------------------------
-- Invitations
-- ---------------------------------------------------------------------------

CREATE TABLE invitations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    email       TEXT NOT NULL,
    -- Only the SHA-256 of the invite token is stored: a leaked database dump
    -- must not let anyone join an organization.
    token_hash  TEXT NOT NULL UNIQUE,
    invited_by  UUID REFERENCES users(id) ON DELETE SET NULL,
    expires_at  TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One live invitation per email per org; accepted ones drop out so the same
-- person can be re-invited later if they are removed.
CREATE UNIQUE INDEX invitations_pending_idx
    ON invitations (org_id, lower(email))
    WHERE accepted_at IS NULL;

CREATE INDEX invitations_org_idx ON invitations (org_id, created_at DESC);

-- ---------------------------------------------------------------------------
-- Leads
-- ---------------------------------------------------------------------------

CREATE TABLE leads (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    first_name    TEXT NOT NULL,
    -- Everything below is optional: a lead is by definition incomplete
    -- information that gets filled in as it moves along the pipeline.
    last_name     TEXT,
    email         TEXT,
    phone         TEXT,
    company       TEXT,
    source        TEXT,
    notes         TEXT,
    -- Estimated value of the opportunity, used for the pipeline total.
    value         NUMERIC(14,2),
    stage         TEXT NOT NULL DEFAULT 'new'
                  CHECK (stage IN ('new', 'contacted', 'qualified', 'proposal', 'won', 'lost')),
    -- The org member responsible. SET NULL rather than CASCADE: losing an owner
    -- must never delete the lead itself.
    owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    -- Manual ordering within a kanban column. Rewritten as whole-column
    -- multiples of 1000 on every move (see internal/leads/store.go).
    position      DOUBLE PRECISION NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The board's own query: one org, grouped by column, in manual order.
CREATE INDEX leads_org_stage_position_idx ON leads (org_id, stage, position, id);
CREATE INDEX leads_org_owner_idx          ON leads (org_id, owner_user_id);
