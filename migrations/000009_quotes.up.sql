-- Quotes: the first module with child rows and computed money.

-- Per-org document numbering. A counter column rather than max(number)+1:
-- incrementing it inside the creating transaction takes a row lock on the
-- organization, which serializes numbering per tenant and leaves no window for
-- two quotes to claim the same number.
ALTER TABLE organizations ADD COLUMN quote_seq INTEGER NOT NULL DEFAULT 0;

CREATE TABLE quotes (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    -- Human-facing document number, e.g. "Q-0007".
    number        TEXT NOT NULL,
    title         TEXT,
    status        TEXT NOT NULL DEFAULT 'draft'
                  CHECK (status IN ('draft', 'sent', 'accepted', 'declined', 'expired')),

    -- Who it is for. All SET NULL: deleting a company must never delete the
    -- record of a document that was sent to them.
    account_id    UUID REFERENCES accounts(id) ON DELETE SET NULL,
    contact_id    UUID REFERENCES contacts(id) ON DELETE SET NULL,
    deal_id       UUID REFERENCES deals(id)    ON DELETE SET NULL,
    owner_user_id UUID REFERENCES users(id)    ON DELETE SET NULL,

    -- Snapshot of the workspace currency at creation. A quote is a document that
    -- was issued in a currency; changing the workspace setting later must not
    -- silently reprice history.
    currency      TEXT NOT NULL,

    notes         TEXT,
    valid_until   DATE,

    -- Totals are DERIVED — recomputed from quote_items on every write and never
    -- accepted from a client. Stored rather than computed on read so a list page
    -- doesn't have to aggregate child rows per document.
    subtotal       NUMERIC(14,2) NOT NULL DEFAULT 0,
    discount_total NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_total      NUMERIC(14,2) NOT NULL DEFAULT 0,
    total          NUMERIC(14,2) NOT NULL DEFAULT 0,

    sent_at     TIMESTAMPTZ,
    accepted_at TIMESTAMPTZ,
    declined_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Numbers are unique per tenant, not globally: two workspaces both having a
-- "Q-0001" is correct.
CREATE UNIQUE INDEX quotes_org_number_idx ON quotes (org_id, number);
CREATE INDEX quotes_org_created_idx ON quotes (org_id, created_at DESC, id);
CREATE INDEX quotes_org_status_idx  ON quotes (org_id, status);
CREATE INDEX quotes_org_deal_idx    ON quotes (org_id, deal_id);

CREATE TABLE quote_items (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    quote_id UUID NOT NULL REFERENCES quotes(id) ON DELETE CASCADE,
    -- Denormalized so an item can be scoped without joining its parent. Every
    -- query still reaches items through the quote, but this keeps the tenant
    -- filter available at the row level.
    org_id   UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    position    INTEGER NOT NULL DEFAULT 0,
    description TEXT NOT NULL,
    quantity    NUMERIC(12,3) NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    unit_price  NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    -- Percentages, 0–100. Per line, because a discount usually applies to one
    -- item and tax rates differ by line in most jurisdictions.
    discount_percent NUMERIC(5,2) NOT NULL DEFAULT 0
                     CHECK (discount_percent >= 0 AND discount_percent <= 100),
    tax_percent      NUMERIC(5,2) NOT NULL DEFAULT 0
                     CHECK (tax_percent >= 0 AND tax_percent <= 100),
    -- Net of discount, before tax. Derived like the quote totals.
    line_total  NUMERIC(14,2) NOT NULL DEFAULT 0
);

CREATE INDEX quote_items_quote_idx ON quote_items (quote_id, position, id);
