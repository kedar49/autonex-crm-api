BEGIN;

ALTER TABLE leads DROP CONSTRAINT IF EXISTS leads_status_check;

ALTER TABLE leads ADD CONSTRAINT leads_status_check
  CHECK (status = ANY (ARRAY['new', 'initial count', 'deck sent', 'not interested', 'call scheduled', 'call done', 'proposal sent', 'closed']));

COMMIT;
