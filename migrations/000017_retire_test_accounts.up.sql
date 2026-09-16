-- Retire the six test logins, keeping every real record they were holding.
--
-- These accounts were created for RBAC testing but were then used as working
-- logins: between them they owned 100% of the quotes and invoices, 53% of the
-- deals and 41% of the delivery tracker — Hindalco, L&T, Mahindra, Bharat
-- Forge, Thermax, Schneider and the rest. So this is a reassignment, not a
-- delete: ownership moves to the real owner, the records stay.
--
-- Retired logins:
--   test+69875781@example.com              (owner, test+69875781's workspace)
--   test@autonex.com                       (owner, test's workspace)
--   test+sales@autonex.com                 Sales (test), test's workspace
--   test+ops@autonex.com                   Ops (test),   test's workspace
--   karan.paigude+sales@autonexai360.com   Sales (test), Autonex
--   karan.paigude+ops@autonexai360.com     Ops (test),   Autonex
--
-- dev@autonexai360.com and dev's workspace are deliberately left alone: they
-- were not on the list.
--
-- Row-level backup of everything touched: backups/pre-testuser-cleanup-2026-09-16.json

BEGIN;

CREATE TEMP TABLE _retire (id uuid PRIMARY KEY) ON COMMIT DROP;
INSERT INTO _retire (id) VALUES
    ('ccdd13b5-56ed-4ab6-9498-15cea382536f'),  -- test+69875781@example.com
    ('5eda1431-92b6-4448-89e9-5c9bd13bf13e'),  -- test@autonex.com
    ('1d1257d6-702a-453c-8ee3-53edbb9e7712'),  -- karan.paigude+sales@
    ('beeab427-9d50-4349-93d5-060a2d5499d6'),  -- karan.paigude+ops@
    ('37a52670-a19a-41c8-a45a-cdea0d74b4f8'),  -- test+sales@autonex.com
    ('c5988d58-6077-47ef-b8ba-a76af01cd2dc');  -- test+ops@autonex.com

-- Guard: the inheritor and the surviving org must exist, or the whole thing is
-- a no-op that silently orphans data.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM users WHERE id = '6f9af07f-085f-4300-82d7-006c287853a5') THEN
        RAISE EXCEPTION 'inheriting user karan.paigude@autonexai360.com is missing';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM organizations WHERE id = 'acfb1796-b957-41dd-b96c-a1db37cfd688') THEN
        RAISE EXCEPTION 'surviving org Autonex is missing';
    END IF;
END $$;

-- ---------------------------------------------------------------------------
-- 1. Provably-fake rows, deleted rather than inherited.
-- ---------------------------------------------------------------------------

-- A stray tracker row typed into the test workspace: client "da", no deal, and
-- the only field filled in is next_steps = 'bfhwrvgfwhefvbhwrgvfuiwrgvwriugv'.
-- Matched by id, with the client name as a second condition, so this cannot hit
-- anything else if it is re-run.
DELETE FROM delivery_tracker
 WHERE id = 'a26f27fb-f01a-4ea5-9e33-7fa33e60f60f'
   AND lower(btrim(client)) = 'da'
   AND deal_id IS NULL;

-- ---------------------------------------------------------------------------
-- 2. Merge the duplicated delivery_tracker rows.
--
-- The same twelve clients exist twice: once under test's workspace and once
-- under Autonex. The two halves are complementary, not redundant — the
-- test-workspace row is the live one (linked to a deal, still being edited this
-- week) but carries no deployment detail, while the Autonex row is the frozen
-- 2026-09-09 import that holds total_cameras / locations / products.
--
-- So the deal-linked row survives and absorbs the other's fields. COALESCE
-- only, so the survivor never loses a value it already had; where both sides
-- hold a *different* non-empty value the loser's is appended to notes rather
-- than dropped, because picking a winner between "Barbil, Odissa" and
-- "Barbil, Odisha (mining)" is a judgement call for a person, not a migration.
-- ---------------------------------------------------------------------------

CREATE TEMP TABLE _merge ON COMMIT DROP AS
SELECT s.id           AS keep_id,
       l.id           AS drop_id,
       to_jsonb(s)    AS surv,
       to_jsonb(l)    AS loser
  FROM delivery_tracker s
  JOIN delivery_tracker l
    ON lower(btrim(l.client)) = lower(btrim(s.client))
   AND l.org_id  = 'acfb1796-b957-41dd-b96c-a1db37cfd688'
   AND l.deal_id IS NULL
 WHERE s.org_id  = '30ca4c37-8213-46e2-b6d1-bc7b679c8ea6'
   AND s.deal_id IS NOT NULL;

-- One loser per survivor and vice versa, or the UPDATE below is non-deterministic.
DO $$
DECLARE n_dup int;
BEGIN
    SELECT count(*) INTO n_dup FROM (
        SELECT keep_id FROM _merge GROUP BY 1 HAVING count(*) > 1
        UNION ALL
        SELECT drop_id FROM _merge GROUP BY 1 HAVING count(*) > 1
    ) x;
    IF n_dup > 0 THEN
        RAISE EXCEPTION 'delivery_tracker merge is ambiguous for % row(s)', n_dup;
    END IF;
END $$;

CREATE TEMP TABLE _conflict ON COMMIT DROP AS
SELECT m.keep_id,
       m.drop_id,
       (SELECT string_agg(k || ': ' || (m.loser ->> k), E'\n' ORDER BY k)
          FROM unnest(ARRAY['total_cameras','locations','products','status',
                            'current_stages','key_contacts','next_steps',
                            'notes','implementation_date']) AS k
         WHERE coalesce(m.loser ->> k, '') <> ''
           AND coalesce(m.surv  ->> k, '') <> ''
           AND (m.loser ->> k) IS DISTINCT FROM (m.surv ->> k)) AS conflict_txt
  FROM _merge m;

UPDATE delivery_tracker s
   SET total_cameras       = coalesce(s.total_cameras, (m.loser ->> 'total_cameras')::int),
       locations           = coalesce(nullif(s.locations, ''),      m.loser ->> 'locations'),
       products            = coalesce(nullif(s.products, ''),       m.loser ->> 'products'),
       status              = coalesce(nullif(s.status, ''),         m.loser ->> 'status'),
       current_stages      = coalesce(nullif(s.current_stages, ''), m.loser ->> 'current_stages'),
       key_contacts        = coalesce(nullif(s.key_contacts, ''),   m.loser ->> 'key_contacts'),
       next_steps          = coalesce(nullif(s.next_steps, ''),     m.loser ->> 'next_steps'),
       implementation_date = coalesce(s.implementation_date, (m.loser ->> 'implementation_date')::date),
       notes               = nullif(btrim(
                                 coalesce(nullif(s.notes, ''), '') ||
                                 CASE WHEN c.conflict_txt IS NULL THEN ''
                                      ELSE E'\n\n[merged from duplicate tracker row ' || m.drop_id ||
                                           E' on 2026-09-16]\n' || c.conflict_txt
                                 END), '')
  FROM _merge m
  JOIN _conflict c ON c.keep_id = m.keep_id
 WHERE s.id = m.keep_id;

DELETE FROM delivery_tracker WHERE id IN (SELECT drop_id FROM _merge);

-- ---------------------------------------------------------------------------
-- 3. Move what is left of the retired workspaces into Autonex.
--    After step 2 the client-name collisions are gone, so the partial unique
--    index on (org_id, lower(btrim(client))) WHERE deal_id IS NULL holds.
-- ---------------------------------------------------------------------------

UPDATE delivery_tracker
   SET org_id = 'acfb1796-b957-41dd-b96c-a1db37cfd688'
 WHERE org_id IN ('30ca4c37-8213-46e2-b6d1-bc7b679c8ea6',
                  '3b66e2cd-c565-435b-bf1c-7100c5d3bc3b');

UPDATE follow_ups
   SET org_id = 'acfb1796-b957-41dd-b96c-a1db37cfd688'
 WHERE org_id IN ('30ca4c37-8213-46e2-b6d1-bc7b679c8ea6',
                  '3b66e2cd-c565-435b-bf1c-7100c5d3bc3b');

UPDATE audit_logs
   SET org_id = 'acfb1796-b957-41dd-b96c-a1db37cfd688'
 WHERE org_id IN ('30ca4c37-8213-46e2-b6d1-bc7b679c8ea6',
                  '3b66e2cd-c565-435b-bf1c-7100c5d3bc3b');

DELETE FROM invitations
 WHERE org_id IN ('30ca4c37-8213-46e2-b6d1-bc7b679c8ea6',
                  '3b66e2cd-c565-435b-bf1c-7100c5d3bc3b');

-- ---------------------------------------------------------------------------
-- 4. Hand every remaining record to the real owner.
-- ---------------------------------------------------------------------------

UPDATE accounts    SET owner_id           = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE owner_id           IN (SELECT id FROM _retire);
UPDATE deals       SET owner_id           = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE owner_id           IN (SELECT id FROM _retire);
UPDATE leads       SET assigned_to        = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE assigned_to        IN (SELECT id FROM _retire);
UPDATE activities  SET author_id          = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE author_id          IN (SELECT id FROM _retire);
UPDATE quotes      SET created_by         = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE created_by         IN (SELECT id FROM _retire);
UPDATE invoices    SET account_manager_id = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE account_manager_id IN (SELECT id FROM _retire);
UPDATE follow_ups  SET assigned_to        = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE assigned_to        IN (SELECT id FROM _retire);

UPDATE deal_tasks  SET assigned_to        = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE assigned_to        IN (SELECT id FROM _retire);
UPDATE deal_tasks  SET created_by         = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE created_by         IN (SELECT id FROM _retire);
UPDATE deal_tasks  SET completed_by       = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE completed_by       IN (SELECT id FROM _retire);

UPDATE delivery_tracker SET created_by    = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE created_by        IN (SELECT id FROM _retire);
UPDATE delivery_tracker SET updated_by    = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE updated_by        IN (SELECT id FROM _retire);

UPDATE audit_logs  SET user_id            = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE user_id            IN (SELECT id FROM _retire);
UPDATE audit_log   SET actor_id           = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE actor_id           IN (SELECT id FROM _retire);
UPDATE invitations SET invited_by         = '6f9af07f-085f-4300-82d7-006c287853a5' WHERE invited_by         IN (SELECT id FROM _retire);

-- ---------------------------------------------------------------------------
-- 5. Retire the logins themselves.
-- ---------------------------------------------------------------------------

DELETE FROM refresh_tokens          WHERE user_id IN (SELECT id FROM _retire);
DELETE FROM notifications           WHERE user_id IN (SELECT id FROM _retire);
DELETE FROM push_subscriptions      WHERE user_id IN (SELECT id FROM _retire);
DELETE FROM integration_connections WHERE user_id IN (SELECT id FROM _retire);
DELETE FROM profiles                WHERE id      IN (SELECT id FROM _retire);
DELETE FROM users                   WHERE id      IN (SELECT id FROM _retire);

DELETE FROM organizations
 WHERE id IN ('30ca4c37-8213-46e2-b6d1-bc7b679c8ea6',   -- test's workspace
              '3b66e2cd-c565-435b-bf1c-7100c5d3bc3b');  -- test+69875781's workspace

-- ---------------------------------------------------------------------------
-- 6. Nothing may be left pointing at a retired login.
-- ---------------------------------------------------------------------------

DO $$
DECLARE n_left int;
BEGIN
    SELECT (SELECT count(*) FROM accounts         WHERE owner_id           IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM deals            WHERE owner_id           IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM leads            WHERE assigned_to        IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM activities       WHERE author_id          IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM quotes           WHERE created_by         IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM invoices         WHERE account_manager_id IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM follow_ups       WHERE assigned_to        IN (SELECT id FROM _retire))
         + (SELECT count(*) FROM delivery_tracker WHERE created_by         IN (SELECT id FROM _retire)
                                                     OR updated_by         IN (SELECT id FROM _retire))
      INTO n_left;
    IF n_left > 0 THEN
        RAISE EXCEPTION 'aborting: % row(s) still reference a retired login', n_left;
    END IF;
END $$;

COMMIT;
