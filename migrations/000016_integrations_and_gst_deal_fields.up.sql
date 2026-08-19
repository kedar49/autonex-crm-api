CREATE TABLE integration_connections (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    provider         TEXT NOT NULL,
    encrypted_tokens BYTEA NOT NULL,
    metadata         JSONB,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX integrations_org_provider_idx ON integration_connections (org_id, provider);

CREATE TABLE slack_channels (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    entity_type TEXT NOT NULL,
    entity_id   UUID NOT NULL,
    channel_id  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX slack_channels_org_entity_idx ON slack_channels (org_id, entity_type, entity_id);

-- Indian Dual GST Tax Polish
ALTER TABLE invoices ADD COLUMN cgst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE invoices ADD COLUMN sgst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE invoices ADD COLUMN igst_amount NUMERIC(14,2) NOT NULL DEFAULT 0;
ALTER TABLE invoices ADD COLUMN is_interstate BOOLEAN NOT NULL DEFAULT false;

-- Advanced Deal Fields
ALTER TABLE deals ADD COLUMN probability INTEGER DEFAULT 50 CHECK (probability >= 0 AND probability <= 100);
ALTER TABLE deals ADD COLUMN expected_revenue NUMERIC(14,2) GENERATED ALWAYS AS (amount * probability / 100.0) STORED;
ALTER TABLE deals ADD COLUMN lost_reason TEXT;
ALTER TABLE deals ADD COLUMN site_assessment JSONB;
