-- Deal tasks: the checklist on a deal card, promoted out of deals.notes into a
-- table of its own so that who created a task and who completed it are real
-- foreign keys rather than text a stray remark edit can destroy.
BEGIN;

CREATE TABLE IF NOT EXISTS deal_tasks (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- No org_id: deals, accounts and leads carry none in this schema, so a task
    -- is scoped by the deal it hangs off rather than claiming a tenancy its own
    -- parent does not have. ON DELETE CASCADE ties its life to that deal.
    deal_id      UUID NOT NULL REFERENCES deals(id) ON DELETE CASCADE,
    text         TEXT NOT NULL,
    priority     TEXT NOT NULL DEFAULT 'normal'
                 CHECK (priority IN ('high', 'medium', 'normal')),
    position     INTEGER NOT NULL DEFAULT 0,

    -- Assignment and audit. All three point at profiles, which is what deals
    -- and follow_ups already use for people; ON DELETE SET NULL keeps a task
    -- readable after someone leaves rather than deleting their work.
    assigned_to  UUID REFERENCES profiles(id) ON DELETE SET NULL,
    created_by   UUID REFERENCES profiles(id) ON DELETE SET NULL,
    completed_by UUID REFERENCES profiles(id) ON DELETE SET NULL,

    done         BOOLEAN NOT NULL DEFAULT false,
    completed_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The card reads a deal's open tasks in priority order; the org scope is what
-- every list query filters on first.
CREATE INDEX IF NOT EXISTS idx_deal_tasks_deal ON deal_tasks(deal_id, done, position);
CREATE INDEX IF NOT EXISTS idx_deal_tasks_assigned ON deal_tasks(assigned_to) WHERE assigned_to IS NOT NULL;

DROP TRIGGER IF EXISTS trg_deal_tasks_updated_at ON deal_tasks;
CREATE TRIGGER trg_deal_tasks_updated_at
    BEFORE UPDATE ON deal_tasks
    FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- Carry across the checklists already living in deals.notes, so nothing a user
-- has typed disappears when the card switches to reading this table. Lines look
-- like "- [ ] !high Call the customer"; anything else in notes is left alone.
INSERT INTO deal_tasks (deal_id, text, priority, position, done, completed_at)
SELECT
    d.id,
    btrim(regexp_replace(line.value, '^\s*[-*]\s*\[[ xX]\]\s*(!(?:high|urgent|med|medium|low|normal)\s*)?', '')),
    CASE
        WHEN line.value ~* '\[\s*[ xX]\s*\]\s*!(high|urgent)' THEN 'high'
        WHEN line.value ~* '\[\s*[ xX]\s*\]\s*!med' THEN 'medium'
        ELSE 'normal'
    END,
    line.ordinality,
    line.value ~ '\[[xX]\]',
    CASE WHEN line.value ~ '\[[xX]\]' THEN now() ELSE NULL END
FROM deals d
CROSS JOIN LATERAL regexp_split_to_table(d.notes, E'\n') WITH ORDINALITY AS line(value, ordinality)
WHERE d.deleted_at IS NULL
  AND d.notes IS NOT NULL
  AND line.value ~ '^\s*[-*]\s*\[[ xX]\]'
  AND btrim(regexp_replace(line.value, '^\s*[-*]\s*\[[ xX]\]\s*(!(?:high|urgent|med|medium|low|normal)\s*)?', '')) <> '';

COMMIT;
