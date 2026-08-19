ALTER TABLE deals DROP COLUMN IF EXISTS site_assessment;
ALTER TABLE deals DROP COLUMN IF EXISTS lost_reason;
ALTER TABLE deals DROP COLUMN IF EXISTS expected_revenue;
ALTER TABLE deals DROP COLUMN IF EXISTS probability;

ALTER TABLE invoices DROP COLUMN IF EXISTS is_interstate;
ALTER TABLE invoices DROP COLUMN IF EXISTS igst_amount;
ALTER TABLE invoices DROP COLUMN IF EXISTS sgst_amount;
ALTER TABLE invoices DROP COLUMN IF EXISTS cgst_amount;

DROP TABLE IF EXISTS slack_channels;
DROP TABLE IF EXISTS integration_connections;
