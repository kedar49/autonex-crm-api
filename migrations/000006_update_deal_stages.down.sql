BEGIN;

ALTER TABLE deals DROP CONSTRAINT IF EXISTS deals_stage_check;

ALTER TABLE deals ALTER COLUMN stage SET DEFAULT 'prospect';

UPDATE deals SET stage = 'prospect' WHERE stage = 'discovery';
UPDATE deals SET stage = 'proposal' WHERE stage = 'quote_sent';
UPDATE deals SET stage = 'prospect' WHERE stage = 'site_assessment';

ALTER TABLE deals ADD CONSTRAINT deals_stage_check CHECK (stage = ANY (ARRAY['prospect', 'proposal', 'negotiation', 'won', 'lost']));

COMMIT;
