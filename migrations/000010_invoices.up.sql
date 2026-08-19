-- Invoices: the document a customer actually pays, plus the payments against it.
--
-- Deliberately its own table rather than a flag on quotes. A quote is an offer
-- and an invoice is a demand: they have different numbering, different
-- lifecycles, and an invoice must stay unchanged after issue even if the quote
-- it came from is later revised.

ALTER TABLE organizations ADD COLUMN invoice_seq INTEGER NOT NULL DEFAULT 0;

CREATE TABLE invoices (
    id       UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id   UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    number   TEXT NOT NULL,
    title    TEXT,
    status   TEXT NOT NULL DEFAULT 'draft'
             CHECK (status IN ('draft', 'sent', 'paid', 'void')),

    -- Where it came from. SET NULL: revising or deleting the quote must never
    -- delete the invoice raised from it.
    quote_id      UUID REFERENCES quotes(id)   ON DELETE SET NULL,
    account_id    UUID REFERENCES accounts(id) ON DELETE SET NULL,
    contact_id    UUID REFERENCES contacts(id) ON DELETE SET NULL,
    deal_id       UUID REFERENCES deals(id)    ON DELETE SET NULL,
    owner_user_id UUID REFERENCES users(id)    ON DELETE SET NULL,

    -- Snapshot, like quotes: an invoice is denominated in the currency it was
    -- issued in, whatever the workspace setting says later.
    currency TEXT NOT NULL,

    notes      TEXT,
    issue_date DATE,
    due_date   DATE,

    -- Derived from invoice_items on every write; never accepted from a client.
    subtotal       NUMERIC(14,2) NOT NULL DEFAULT 0,
    discount_total NUMERIC(14,2) NOT NULL DEFAULT 0,
    tax_total      NUMERIC(14,2) NOT NULL DEFAULT 0,
    total          NUMERIC(14,2) NOT NULL DEFAULT 0,
    -- Derived from payments. Balance is total - amount_paid, computed on read
    -- rather than stored: one less figure that can disagree with its source.
    amount_paid    NUMERIC(14,2) NOT NULL DEFAULT 0,

    sent_at   TIMESTAMPTZ,
    paid_at   TIMESTAMPTZ,
    voided_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX invoices_org_number_idx ON invoices (org_id, number);
CREATE INDEX invoices_org_created_idx ON invoices (org_id, created_at DESC, id);
CREATE INDEX invoices_org_status_idx  ON invoices (org_id, status);
-- Overdue is derived (sent, past due, still owing), so the due date is a filter
-- column rather than a stored flag that could go stale overnight.
CREATE INDEX invoices_org_due_idx     ON invoices (org_id, due_date) WHERE status = 'sent';

-- One live invoice per quote. Partial, so a voided invoice can be replaced —
-- billing the same quote twice by accident is the mistake worth preventing.
CREATE UNIQUE INDEX invoices_quote_idx
    ON invoices (quote_id)
    WHERE quote_id IS NOT NULL AND status <> 'void';

CREATE TABLE invoice_items (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    position    INTEGER NOT NULL DEFAULT 0,
    description TEXT NOT NULL,
    quantity    NUMERIC(12,3) NOT NULL DEFAULT 1 CHECK (quantity >= 0),
    unit_price  NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    discount_percent NUMERIC(5,2) NOT NULL DEFAULT 0
                     CHECK (discount_percent >= 0 AND discount_percent <= 100),
    tax_percent      NUMERIC(5,2) NOT NULL DEFAULT 0
                     CHECK (tax_percent >= 0 AND tax_percent <= 100),
    line_total  NUMERIC(14,2) NOT NULL DEFAULT 0
);

CREATE INDEX invoice_items_invoice_idx ON invoice_items (invoice_id, position, id);

-- Payments are append-only history, not a running figure on the invoice: "how
-- much is owed" and "what was received when" are different questions, and only
-- the second one survives an audit.
CREATE TABLE payments (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    invoice_id UUID NOT NULL REFERENCES invoices(id) ON DELETE CASCADE,
    org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,

    amount    NUMERIC(14,2) NOT NULL CHECK (amount > 0),
    paid_on   DATE NOT NULL DEFAULT CURRENT_DATE,
    method    TEXT,
    reference TEXT,
    note      TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX payments_invoice_idx ON payments (invoice_id, paid_on DESC, id);
CREATE INDEX payments_org_idx     ON payments (org_id, paid_on DESC);
