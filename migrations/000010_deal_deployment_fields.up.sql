BEGIN;

-- What is actually being deployed, on the deal itself.
--
-- These live here rather than on the account because they describe one sale:
-- a client can buy 24 cameras for one plant this year and 40 for another next
-- year, and rolling them into an account-level total would lose which deal
-- committed to what. The company profile aggregates them back up for display.
--
-- Free text for products and location, deliberately: the catalogue is not
-- modelled (the products table is empty and unreachable), and a site list is
-- "Pune (Plant 1); Nashik" more often than it is a single tidy value.
ALTER TABLE deals ADD COLUMN IF NOT EXISTS total_cameras INTEGER
    CHECK (total_cameras IS NULL OR total_cameras >= 0);
ALTER TABLE deals ADD COLUMN IF NOT EXISTS location TEXT;
ALTER TABLE deals ADD COLUMN IF NOT EXISTS products TEXT;

COMMIT;
