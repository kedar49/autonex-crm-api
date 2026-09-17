BEGIN;

-- Allow multiple unlinked tracker rows for a client if they are for different plants/locations.
--
-- In manufacturing and industrial delivery, a client often has multiple plants (e.g. Raigarh, Raipur).
-- Unlinked rows are unique on (org_id, client, location) so multiple sites can be imported
-- without conflicting, while still preventing true duplicate entries.
DROP INDEX IF EXISTS delivery_tracker_client_idx;
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client)), COALESCE(lower(btrim(locations)), ''))
    WHERE deal_id IS NULL;

COMMIT;
