BEGIN;

-- Safe to reverse: every location also exists as text on the record that used
-- it, and account_profiles.plant_locations was never touched, so dropping these
-- loses the ids and nothing else.
ALTER TABLE deals  DROP COLUMN IF EXISTS location_id;
DROP TABLE IF EXISTS account_locations;

COMMIT;
