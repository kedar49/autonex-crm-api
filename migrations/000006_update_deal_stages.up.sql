BEGIN;

ALTER TABLE deals DROP CONSTRAINT IF EXISTS deals_stage_check;

ALTER TABLE deals ALTER COLUMN stage SET DEFAULT 'discovery';

UPDATE deals SET stage = 'discovery' WHERE stage = 'prospect';
UPDATE deals SET stage = 'quote_sent' WHERE stage = 'proposal';

ALTER TABLE deals ADD CONSTRAINT deals_stage_check CHECK (stage = ANY (ARRAY['discovery', 'site_assessment', 'quote_sent', 'negotiation', 'won', 'lost']));

COMMIT;
