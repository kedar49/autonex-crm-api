DROP INDEX IF EXISTS payments_org_idx;
DROP INDEX IF EXISTS payments_invoice_idx;
DROP TABLE IF EXISTS payments;

DROP INDEX IF EXISTS invoice_items_invoice_idx;
DROP TABLE IF EXISTS invoice_items;

DROP INDEX IF EXISTS invoices_quote_idx;
DROP INDEX IF EXISTS invoices_org_due_idx;
DROP INDEX IF EXISTS invoices_org_status_idx;
DROP INDEX IF EXISTS invoices_org_created_idx;
DROP INDEX IF EXISTS invoices_org_number_idx;
DROP TABLE IF EXISTS invoices;

ALTER TABLE organizations DROP COLUMN IF EXISTS invoice_seq;
