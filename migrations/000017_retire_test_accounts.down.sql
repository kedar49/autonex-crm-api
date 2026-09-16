-- Not reversible.
--
-- The up migration deletes six users, two organizations and their sessions, and
-- merges twelve pairs of delivery_tracker rows into one. The deleted rows are
-- gone and the merged pairs cannot be split again from what remains, so there
-- is nothing here to undo them with.
--
-- To roll back, restore from the row-level snapshot taken immediately before
-- the up migration ran:
--
--     backups/pre-testuser-cleanup-2026-09-16.json
--
-- It holds the full pre-change contents of organizations, users, profiles,
-- delivery_tracker, deal_tasks, follow_ups, quotes, invoices and every account,
-- deal, lead and activity that the retired logins owned.

BEGIN;

DO $$
BEGIN
    RAISE EXCEPTION
        'migration 000017 is irreversible; restore backups/pre-testuser-cleanup-2026-09-16.json instead';
END $$;

COMMIT;
