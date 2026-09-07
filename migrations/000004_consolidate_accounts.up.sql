BEGIN;

-- Drop redundant account_id columns that were pointing to the unused accounts table
ALTER TABLE contacts DROP COLUMN account_id;
ALTER TABLE leads DROP COLUMN account_id;
ALTER TABLE deals DROP COLUMN account_id;
ALTER TABLE quotes DROP COLUMN account_id;
ALTER TABLE invoices DROP COLUMN account_id;

-- Drop the unused accounts table
DROP TABLE accounts CASCADE;

-- Rename companies to accounts
ALTER TABLE companies RENAME TO accounts;

-- Rename company_id columns to account_id
ALTER TABLE contacts RENAME COLUMN company_id TO account_id;
ALTER TABLE leads RENAME COLUMN company_id TO account_id;
ALTER TABLE deals RENAME COLUMN company_id TO account_id;
ALTER TABLE quotes RENAME COLUMN company_id TO account_id;
ALTER TABLE invoices RENAME COLUMN company_id TO account_id;

-- Handle company_profiles
ALTER TABLE company_profiles RENAME TO account_profiles;
ALTER TABLE account_profiles RENAME COLUMN company_id TO account_id;

-- Update polymorphic references in activities if any
UPDATE activities SET entity_type = 'account' WHERE entity_type = 'company';

COMMIT;
