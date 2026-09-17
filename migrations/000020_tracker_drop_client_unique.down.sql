BEGIN;

-- Restoring the guard can fail if duplicates have since been typed in. Clear
-- them first, or this migration will refuse to run — which is the honest
-- outcome: the index cannot come back while the data contradicts it.
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client)), COALESCE(lower(btrim(locations)), ''))
    WHERE deal_id IS NULL;

COMMIT;
