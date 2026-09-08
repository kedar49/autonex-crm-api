BEGIN;

DROP INDEX IF EXISTS delivery_tracker_deal_lookup_idx;
DROP INDEX IF EXISTS delivery_tracker_deal_idx;
DROP INDEX IF EXISTS delivery_tracker_client_idx;

-- Restoring the unconditional client index can fail if linked rows have left
-- duplicate client names behind. Deduplicate before rolling back: keep the
-- oldest row per client, since that is the one an import would have been
-- updating all along.
DELETE FROM delivery_tracker t
 WHERE EXISTS (
   SELECT 1 FROM delivery_tracker o
    WHERE o.org_id = t.org_id
      AND lower(btrim(o.client)) = lower(btrim(t.client))
      AND (o.created_at, o.id) < (t.created_at, t.id)
 );

CREATE UNIQUE INDEX delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client)));

ALTER TABLE delivery_tracker DROP COLUMN IF EXISTS deal_id;

COMMIT;
