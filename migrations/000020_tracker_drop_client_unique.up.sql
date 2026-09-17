BEGIN;

-- Let any tracker cell be edited to any value.
--
-- Unlinked rows were unique on (org_id, client, location), which meant a plain
-- cell edit could be refused: correcting a mistyped plant name to one another
-- row already used came back as a 409 and the typing was thrown away. The sheet
-- this table replaced had no such rule, and operations relies on being able to
-- fix a name the moment they notice it — a duplicate row is a tidying job, not
-- something worth blocking a correction over.
--
-- delivery_tracker_deal_idx stays: one tracker row per deal is a different
-- rule, and nothing about editing a cell runs into it.
DROP INDEX IF EXISTS delivery_tracker_client_idx;

COMMIT;
