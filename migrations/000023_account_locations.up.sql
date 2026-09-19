BEGIN;

-- Promote a company's sites from a JSONB blob to rows that can be pointed at.
--
-- They already existed as account_profiles.plant_locations, edited by hand in
-- the Company Profile. A JSONB array cannot be referenced, so a deal recorded
-- its site as free text and renaming "Taloja Plant" silently orphaned every
-- deal whose text said that. These rows have an id, so the name becomes a
-- label rather than the link.
--
-- Nothing is removed here. plant_locations stays exactly as it is, as does
-- deals.location and delivery_tracker.locations, so the quote builder, the
-- tracker sync and the sheet importer all keep reading what they read today.
-- Dropping either is a separate decision for a later migration.
--
-- Only deals gets a link. The quotes table in this schema is a bare header
-- (id, deal_id, account_id, status, version…) with no location column at all —
-- a quote names its site in the version notes, built from the deal by
-- quote-utils.ts. Giving the deal a real location is therefore what gives the
-- quote one, and the delivery sheet keeps its own free-text column by
-- request.
CREATE TABLE IF NOT EXISTS account_locations (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  UUID NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    city        TEXT,
    address     TEXT,
    spoc_name   TEXT,
    spoc_phone  TEXT,
    position    INTEGER NOT NULL DEFAULT 0,
    -- Archived, never deleted: a site named by a won deal is history, and the
    -- deal still has to be able to say where it was delivered.
    archived_at TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS account_locations_account_idx
    ON account_locations (account_id, position);

-- One site per name per company. Case- and space-insensitive, because the
-- backfill below draws the same plant from three places that spell it
-- differently.
CREATE UNIQUE INDEX IF NOT EXISTS account_locations_name_idx
    ON account_locations (account_id, lower(btrim(name)));

DROP TRIGGER IF EXISTS set_account_locations_updated_at ON account_locations;
CREATE TRIGGER set_account_locations_updated_at
BEFORE UPDATE ON account_locations
FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

-- 1. The sites somebody entered in the Company Profile. These carry the most
--    detail — city, address, SPOC — so they go in first and win the unique
--    index against the bare names backfilled afterwards.
INSERT INTO account_locations (account_id, name, city, address, spoc_name, spoc_phone, position)
SELECT p.account_id,
       btrim(s.value ->> 'name'),
       nullif(btrim(coalesce(s.value ->> 'city', '')), ''),
       nullif(btrim(coalesce(s.value ->> 'address', '')), ''),
       nullif(btrim(coalesce(s.value ->> 'spocName', '')), ''),
       nullif(btrim(coalesce(s.value ->> 'spocPhone', '')), ''),
       s.ord::int
  FROM account_profiles p
  CROSS JOIN LATERAL jsonb_array_elements(p.plant_locations) WITH ORDINALITY AS s(value, ord)
 WHERE btrim(coalesce(s.value ->> 'name', '')) <> ''
ON CONFLICT DO NOTHING;

-- 2. The sites that only ever existed as text on a deal. Without this the
--    dropdown would open empty for 31 deals that already name a site, and the
--    feature would look like it had lost them.
INSERT INTO account_locations (account_id, name, position)
SELECT DISTINCT d.account_id, btrim(d.location), 100
  FROM deals d
 WHERE d.account_id IS NOT NULL
   AND d.deleted_at IS NULL
   AND btrim(coalesce(d.location, '')) <> ''
ON CONFLICT DO NOTHING;

-- The structured link, beside the text rather than instead of it.
ALTER TABLE deals
    ADD COLUMN IF NOT EXISTS location_id UUID REFERENCES account_locations(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS deals_location_idx ON deals (location_id);

-- Match the existing text back to the rows it just produced, so today's records
-- arrive already linked instead of reading as "no location chosen".
UPDATE deals d
   SET location_id = l.id
  FROM account_locations l
 WHERE d.location_id IS NULL
   AND d.account_id = l.account_id
   AND lower(btrim(d.location)) = lower(btrim(l.name));

COMMIT;
