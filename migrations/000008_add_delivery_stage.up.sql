BEGIN;

-- Delivery sits between negotiation and the terminal stages: the deal is agreed
-- but the install is not finished, which is the state the client tracker calls
-- "in progress". Won/lost still close the deal.
ALTER TABLE deals DROP CONSTRAINT IF EXISTS deals_stage_check;

ALTER TABLE deals ADD CONSTRAINT deals_stage_check
  CHECK (stage = ANY (ARRAY['discovery', 'site_assessment', 'quote_sent', 'negotiation', 'delivery', 'won', 'lost']));

COMMIT;
