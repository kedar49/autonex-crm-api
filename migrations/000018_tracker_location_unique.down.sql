BEGIN;

DROP INDEX IF EXISTS delivery_tracker_client_idx;
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client)))
    WHERE deal_id IS NULL;

COMMIT;
