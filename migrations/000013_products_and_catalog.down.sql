ALTER TABLE invoice_items DROP COLUMN IF EXISTS product_id;
ALTER TABLE quote_items DROP COLUMN IF EXISTS product_id;

DROP INDEX IF EXISTS products_org_active_idx;
DROP INDEX IF EXISTS products_org_category_idx;
DROP INDEX IF EXISTS products_org_sku_idx;
DROP TABLE IF EXISTS products;
