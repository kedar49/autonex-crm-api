BEGIN;

-- Give an action the same three-level priority a deal task has.
--
-- The dashboard already sorts by when something is due, which answers "what is
-- next" but not "what matters" — a note-to-self and a contract that decides the
-- quarter can fall on the same afternoon. Deal tasks have carried a priority
-- since they shipped, and the two lists sit side by side in the deal's working
-- view, so the same three levels and the same colours mean one thing to learn
-- rather than two.
--
-- 'normal' for every existing row: it is the default a task gets, and inventing
-- urgency for actions nobody has triaged would be worse than saying nothing.
ALTER TABLE follow_ups
    ADD COLUMN IF NOT EXISTS priority text NOT NULL DEFAULT 'normal';

ALTER TABLE follow_ups
    DROP CONSTRAINT IF EXISTS follow_ups_priority_check;
ALTER TABLE follow_ups
    ADD CONSTRAINT follow_ups_priority_check
    CHECK (priority IN ('high', 'medium', 'normal'));

COMMIT;
