BEGIN;

-- Restores the single-location column, keeping the first site of each deal.
-- A deal with several sites loses the rest here, which is exactly why the
-- column went: deals.location keeps all of their names as text either way.
ALTER TABLE deals
    ADD COLUMN IF NOT EXISTS location_id UUID REFERENCES account_locations(id) ON DELETE SET NULL;

UPDATE deals d
   SET location_id = (
         SELECT dl.location_id FROM deal_locations dl
          WHERE dl.deal_id = d.id
          ORDER BY dl.position, dl.created_at
          LIMIT 1);

CREATE INDEX IF NOT EXISTS deals_location_idx ON deals (location_id);
DROP TABLE IF EXISTS deal_locations;

COMMIT;
