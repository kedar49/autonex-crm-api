-- Turn the placeholder `deals` table from 000001 into a real pipeline.
--
-- The original shape assumed a deal always belongs to an account, and carried no
-- owner, no close date and no manual ordering — none of which works for a board.

-- An account is optional: there is no accounts UI yet, and in practice a deal is
-- often created straight off a lead before the company record exists.
ALTER TABLE deals ALTER COLUMN account_id DROP NOT NULL;

ALTER TABLE deals ADD COLUMN description   TEXT;
-- SET NULL, not CASCADE: losing an owner must never delete the deal.
ALTER TABLE deals ADD COLUMN owner_user_id UUID REFERENCES users(id) ON DELETE SET NULL;
ALTER TABLE deals ADD COLUMN contact_id    UUID REFERENCES contacts(id) ON DELETE SET NULL;
ALTER TABLE deals ADD COLUMN expected_close_date DATE;
-- Manual ordering within a kanban column, rewritten as whole-column multiples of
-- 1000 on every move (see internal/deals/store.go).
ALTER TABLE deals ADD COLUMN position      DOUBLE PRECISION NOT NULL DEFAULT 0;
ALTER TABLE deals ADD COLUMN updated_at    TIMESTAMPTZ NOT NULL DEFAULT now();

-- Constrain the stage the same way leads are. These five match the deal stages
-- already declared in shared/schemas (dealSchema) — note they are NOT the lead
-- stages: a deal has no "contacted" step.
UPDATE deals SET stage = 'lead' WHERE stage NOT IN ('lead', 'qualified', 'proposal', 'won', 'lost');
ALTER TABLE deals ADD CONSTRAINT deals_stage_check
    CHECK (stage IN ('lead', 'qualified', 'proposal', 'won', 'lost'));

-- The board's own query: one org, grouped by column, in manual order.
CREATE INDEX deals_org_stage_position_idx ON deals (org_id, stage, position, id);
CREATE INDEX deals_org_owner_idx          ON deals (org_id, owner_user_id);
CREATE INDEX deals_org_contact_idx        ON deals (org_id, contact_id);
