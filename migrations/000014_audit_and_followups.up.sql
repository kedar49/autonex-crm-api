CREATE TABLE audit_logs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    user_id     UUID REFERENCES users(id) ON DELETE SET NULL,
    action      TEXT NOT NULL,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    changes     JSONB,
    ip_address  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX audit_logs_org_entity_idx ON audit_logs (org_id, entity_type, entity_id);
CREATE INDEX audit_logs_org_created_idx ON audit_logs (org_id, created_at DESC);

CREATE TABLE follow_ups (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id       UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    assigned_to  UUID REFERENCES users(id) ON DELETE SET NULL,
    title        TEXT NOT NULL,
    due_at       TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ,
    lead_id      UUID REFERENCES leads(id) ON DELETE CASCADE,
    deal_id      UUID REFERENCES deals(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX follow_ups_org_due_idx ON follow_ups (org_id, due_at) WHERE completed_at IS NULL;
CREATE INDEX follow_ups_lead_idx ON follow_ups (lead_id) WHERE lead_id IS NOT NULL;
CREATE INDEX follow_ups_deal_idx ON follow_ups (deal_id) WHERE deal_id IS NOT NULL;

CREATE TABLE calendar_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id          UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    google_event_id TEXT,
    title           TEXT NOT NULL,
    description     TEXT,
    start_at        TIMESTAMPTZ NOT NULL,
    end_at          TIMESTAMPTZ NOT NULL,
    meet_link       TEXT,
    lead_id         UUID REFERENCES leads(id) ON DELETE CASCADE,
    deal_id         UUID REFERENCES deals(id) ON DELETE CASCADE,
    created_by      UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX calendar_events_org_time_idx ON calendar_events (org_id, start_at, end_at);
CREATE INDEX calendar_events_lead_idx ON calendar_events (lead_id) WHERE lead_id IS NOT NULL;
CREATE INDEX calendar_events_deal_idx ON calendar_events (deal_id) WHERE deal_id IS NOT NULL;
