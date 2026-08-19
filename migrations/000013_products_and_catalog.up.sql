CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    sku         TEXT NOT NULL,
    name        TEXT NOT NULL,
    description TEXT,
    unit_price  NUMERIC(14,2) NOT NULL DEFAULT 0 CHECK (unit_price >= 0),
    tax_rate    NUMERIC(5,2)  NOT NULL DEFAULT 0 CHECK (tax_rate >= 0),
    category    TEXT,
    is_active   BOOLEAN NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX products_org_sku_idx ON products (org_id, lower(sku));
CREATE INDEX products_org_category_idx ON products (org_id, category);
CREATE INDEX products_org_active_idx ON products (org_id, is_active);

ALTER TABLE quote_items ADD COLUMN product_id UUID REFERENCES products(id) ON DELETE SET NULL;
ALTER TABLE invoice_items ADD COLUMN product_id UUID REFERENCES products(id) ON DELETE SET NULL;
