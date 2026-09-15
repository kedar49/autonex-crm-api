BEGIN;

DROP TRIGGER IF EXISTS trg_deal_tasks_updated_at ON deal_tasks;
DROP TABLE IF EXISTS deal_tasks;

COMMIT;
