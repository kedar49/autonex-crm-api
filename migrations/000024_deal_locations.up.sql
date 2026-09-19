BEGIN;

-- A deal can be delivered to more than one of a company's sites.
--
-- 000023 gave a deal a single location_id, which was wrong: the tracker has
-- carried multi-site deals from the start (one deal's site already reads
-- "Pune (Plant 1); Nashik") and a company like Thermax runs three plants
-- against one commercial conversation. A single column forced that back into
-- free text, which is the thing the site records exist to stop.
--
-- deals.location stays, and stays authoritative for everything that reads it —
-- the quote builder, the delivery sync, the sheet importer. It now holds the
-- chosen sites' names joined with "; ", the separator the sheet already used.
CREATE TABLE IF NOT EXISTS deal_locations (
    deal_id     UUID NOT NULL REFERENCES deals(id) ON DELETE CASCADE,
    location_id UUID NOT NULL REFERENCES account_locations(id) ON DELETE CASCADE,
    -- The order they were picked in, so the joined text reads the same way
    -- twice running.
    position    INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (deal_id, location_id)
);

CREATE INDEX IF NOT EXISTS deal_locations_location_idx ON deal_locations (location_id);

-- Carry over what 000023 linked.
INSERT INTO deal_locations (deal_id, location_id, position)
SELECT d.id, d.location_id, 0
  FROM deals d
 WHERE d.location_id IS NOT NULL
ON CONFLICT DO NOTHING;

-- One source of truth. The column was added today and every value in it also
-- exists in deals.location as text and in deal_locations as a row, so dropping
-- it loses nothing. Leaving it would be the same two-definitions-of-one-fact
-- problem that made the lead stage and the Converted count disagree.
DROP INDEX IF EXISTS deals_location_idx;
ALTER TABLE deals DROP COLUMN IF EXISTS location_id;

COMMIT;
