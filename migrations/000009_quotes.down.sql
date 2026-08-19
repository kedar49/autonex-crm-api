DROP INDEX IF EXISTS quote_items_quote_idx;
DROP TABLE IF EXISTS quote_items;

DROP INDEX IF EXISTS quotes_org_deal_idx;
DROP INDEX IF EXISTS quotes_org_status_idx;
DROP INDEX IF EXISTS quotes_org_created_idx;
DROP INDEX IF EXISTS quotes_org_number_idx;
DROP TABLE IF EXISTS quotes;

ALTER TABLE organizations DROP COLUMN IF EXISTS quote_seq;
