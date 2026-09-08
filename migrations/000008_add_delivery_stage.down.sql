BEGIN;

-- Deals mid-delivery have no pre-delivery equivalent; negotiation is the stage
-- they came from and the only one that does not claim the deal is closed.
UPDATE deals SET stage = 'negotiation' WHERE stage = 'delivery';

ALTER TABLE deals DROP CONSTRAINT IF EXISTS deals_stage_check;

ALTER TABLE deals ADD CONSTRAINT deals_stage_check
  CHECK (stage = ANY (ARRAY['discovery', 'site_assessment', 'quote_sent', 'negotiation', 'won', 'lost']));

COMMIT;
