CREATE TABLE quote_versions (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id   UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    version    INTEGER NOT NULL,
    snapshot   JSONB NOT NULL,
    created_by UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX quote_versions_num_idx ON quote_versions (quote_id, version);
CREATE INDEX quote_versions_org_idx ON quote_versions (org_id, created_at DESC);

CREATE TABLE quote_approvals (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id     UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    approver_id  UUID REFERENCES users(id) ON DELETE SET NULL,
    status       TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    discount_pct NUMERIC(5,2) NOT NULL DEFAULT 0,
    notes        TEXT,
    actioned_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX quote_approvals_org_status_idx ON quote_approvals (org_id, status);
CREATE INDEX quote_approvals_quote_idx ON quote_approvals (quote_id);
