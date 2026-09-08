BEGIN;

DROP TRIGGER IF EXISTS set_delivery_tracker_updated_at ON delivery_tracker;
DROP TABLE IF EXISTS delivery_tracker;

COMMIT;
