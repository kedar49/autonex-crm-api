BEGIN;

-- Link each tracker row to the deal it is delivering.
--
-- ON DELETE SET NULL rather than CASCADE: deleting a deal must not delete the
-- record of an installation that may already be on site.
ALTER TABLE delivery_tracker
    ADD COLUMN IF NOT EXISTS deal_id UUID REFERENCES deals(id) ON DELETE SET NULL;

-- Backfill by client name, matching a tracker row to the most recent deal of
-- the account with that name.
--
-- Two guards, because a bad auto-match silently attaches delivery data to the
-- wrong sale: one deal per tracker row (the newest), and one tracker row per
-- deal (the lowest id, chosen only so the result is deterministic). Anything
-- ambiguous is simply left unlinked for a person to resolve.
--
-- The join cannot be org-scoped: neither accounts nor deals carries org_id in
-- this database. That is the pre-existing tenancy gap, not something this
-- migration introduces.
WITH ranked AS (
    SELECT t.id                                              AS tracker_id,
           d.id                                              AS deal_id,
           row_number() OVER (PARTITION BY t.id
                              ORDER BY d.created_at DESC, d.id) AS deal_rank
      FROM delivery_tracker t
      JOIN accounts a
        ON lower(btrim(a.name)) = lower(btrim(t.client))
       AND a.deleted_at IS NULL
      JOIN deals d
        ON d.account_id = a.id
       AND d.deleted_at IS NULL
     WHERE t.deal_id IS NULL
),
best AS (
    SELECT DISTINCT ON (deal_id) tracker_id, deal_id
      FROM ranked
     WHERE deal_rank = 1
     ORDER BY deal_id, tracker_id
)
UPDATE delivery_tracker t
   SET deal_id = b.deal_id
  FROM best b
 WHERE t.id = b.tracker_id;

-- One tracker row per deal.
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_deal_idx
    ON delivery_tracker (deal_id) WHERE deal_id IS NOT NULL;

-- Client uniqueness now applies only to rows with no deal behind them.
--
-- A client legitimately has several deals — two plants, two rollouts, two
-- tracker rows — so the old blanket rule would block the second one from ever
-- reaching the tracker. Unlinked rows (typed by hand, or imported from a sheet)
-- still collapse to one per client, which is what import upserts against.
DROP INDEX IF EXISTS delivery_tracker_client_idx;
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client))) WHERE deal_id IS NULL;

CREATE INDEX IF NOT EXISTS delivery_tracker_deal_lookup_idx
    ON delivery_tracker (deal_id);

COMMIT;
