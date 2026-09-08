BEGIN;

-- Drop redundant account_id columns if they were pointing to the unused accounts table
ALTER TABLE contacts DROP COLUMN IF EXISTS account_id;
ALTER TABLE leads DROP COLUMN IF EXISTS account_id;
ALTER TABLE deals DROP COLUMN IF EXISTS account_id;
ALTER TABLE quotes DROP COLUMN IF EXISTS account_id;
ALTER TABLE invoices DROP COLUMN IF EXISTS account_id;

-- Drop the unused accounts table if it exists
DROP TABLE IF EXISTS accounts CASCADE;

-- Rename companies to accounts if companies table exists
DO $$
BEGIN
  IF EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'companies') THEN
    ALTER TABLE companies RENAME TO accounts;
  END IF;
END $$;

-- Rename company_id columns to account_id if they exist
DO $$
BEGIN
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'contacts' AND column_name = 'company_id') THEN
    ALTER TABLE contacts RENAME COLUMN company_id TO account_id;
  END IF;
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'leads' AND column_name = 'company_id') THEN
    ALTER TABLE leads RENAME COLUMN company_id TO account_id;
  END IF;
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'deals' AND column_name = 'company_id') THEN
    ALTER TABLE deals RENAME COLUMN company_id TO account_id;
  END IF;
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'quotes' AND column_name = 'company_id') THEN
    ALTER TABLE quotes RENAME COLUMN company_id TO account_id;
  END IF;
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'invoices' AND column_name = 'company_id') THEN
    ALTER TABLE invoices RENAME COLUMN company_id TO account_id;
  END IF;
  IF EXISTS (SELECT FROM pg_tables WHERE schemaname = 'public' AND tablename = 'company_profiles') THEN
    ALTER TABLE company_profiles RENAME TO account_profiles;
  END IF;
  IF EXISTS (SELECT FROM information_schema.columns WHERE table_name = 'account_profiles' AND column_name = 'company_id') THEN
    ALTER TABLE account_profiles RENAME COLUMN company_id TO account_id;
  END IF;
END $$;

-- Update polymorphic references in activities if any
UPDATE activities SET entity_type = 'account' WHERE entity_type = 'company';

COMMIT;
