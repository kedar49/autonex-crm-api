DROP INDEX IF EXISTS leads_org_contact_idx;
DROP INDEX IF EXISTS leads_org_account_idx;
DROP INDEX IF EXISTS leads_org_followup_idx;
DROP INDEX IF EXISTS leads_org_stage_idx;

ALTER TABLE leads ADD COLUMN IF NOT EXISTS position DOUBLE PRECISION NOT NULL DEFAULT 0;
-- Re-spread the cards so the restored board isn't a single stack at position 0.
WITH ordered AS (
    SELECT id, row_number() OVER (PARTITION BY org_id, stage ORDER BY created_at) AS n
    FROM leads
)
UPDATE leads SET position = ordered.n * 1000 FROM ordered WHERE leads.id = ordered.id;

CREATE INDEX leads_org_stage_position_idx ON leads (org_id, stage, position, id);

UPDATE leads SET stage = CASE stage
    WHEN 'replied'     THEN 'qualified'
    WHEN 'call_booked' THEN 'proposal'
    -- call_done has no pre-redesign equivalent; the nearest is proposal.
    WHEN 'call_done'   THEN 'proposal'
    WHEN 'converted'   THEN 'won'
    WHEN 'dropped'     THEN 'lost'
    ELSE stage
END;

ALTER TABLE leads DROP CONSTRAINT IF EXISTS leads_stage_check;
ALTER TABLE leads ADD CONSTRAINT leads_stage_check
    CHECK (stage IN ('new', 'contacted', 'qualified', 'proposal', 'won', 'lost'));

ALTER TABLE leads DROP COLUMN IF EXISTS follow_up_at;
ALTER TABLE leads DROP COLUMN IF EXISTS account_id;
ALTER TABLE leads DROP COLUMN IF EXISTS contact_id;
ALTER TABLE leads DROP COLUMN IF EXISTS linkedin_url;
ALTER TABLE leads DROP COLUMN IF EXISTS title;
