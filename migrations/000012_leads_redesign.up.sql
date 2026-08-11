-- Leads, per the redesign brief §3: a shorter outreach-only lifecycle, a real
-- link to the person and the company, and a follow-up date to sort urgency by.

-- ---------------------------------------------------------------------------
-- Who and where
-- ---------------------------------------------------------------------------

ALTER TABLE leads ADD COLUMN title        TEXT;  -- job title: "VP Operations"
ALTER TABLE leads ADD COLUMN linkedin_url TEXT;

-- The person. SET NULL rather than CASCADE: deleting a contact must not erase
-- the record that the outreach happened.
ALTER TABLE leads ADD COLUMN contact_id UUID REFERENCES contacts(id) ON DELETE SET NULL;
-- The company. This is what makes "Companies with no lead" and the company page's
-- lead list answerable at all.
ALTER TABLE leads ADD COLUMN account_id UUID REFERENCES accounts(id) ON DELETE SET NULL;

-- When the next touch is due. Drives the Overdue / Due today filters and the
-- default sort; NULL means nothing is scheduled.
ALTER TABLE leads ADD COLUMN follow_up_at DATE;

-- ---------------------------------------------------------------------------
-- Backfill: every lead gets a contact, and a company where one is named
-- ---------------------------------------------------------------------------

DO $$
DECLARE
    l   RECORD;
    cid UUID;
    aid UUID;
BEGIN
    FOR l IN SELECT * FROM leads LOOP
        cid := NULL;
        aid := NULL;

        -- Reuse a contact with the same email before creating a duplicate; the
        -- per-tenant unique index on lower(email) would reject one anyway.
        IF l.email IS NOT NULL THEN
            SELECT id INTO cid FROM contacts
            WHERE org_id = l.org_id AND lower(email) = lower(l.email) LIMIT 1;
        END IF;

        IF cid IS NULL THEN
            INSERT INTO contacts (org_id, first_name, last_name, email, phone)
            VALUES (l.org_id, l.first_name, l.last_name, l.email, l.phone)
            RETURNING id INTO cid;
        END IF;

        -- Match the free-text company against a real account, creating one if the
        -- name is new. Without this the company link would start out empty for
        -- every existing lead.
        IF l.company IS NOT NULL AND btrim(l.company) <> '' THEN
            SELECT id INTO aid FROM accounts
            WHERE org_id = l.org_id AND lower(name) = lower(btrim(l.company)) LIMIT 1;

            IF aid IS NULL THEN
                INSERT INTO accounts (org_id, name) VALUES (l.org_id, btrim(l.company))
                RETURNING id INTO aid;
            END IF;
        END IF;

        UPDATE leads SET contact_id = cid, account_id = aid WHERE id = l.id;
    END LOOP;
END $$;

-- `company` stays as a free-text fallback: you often have a company name at
-- capture time, before anyone has created the account record. account_id is the
-- real link; the text is only shown when it is absent.

-- ---------------------------------------------------------------------------
-- Lifecycle: 6 stages → 5 + 2 terminal
-- ---------------------------------------------------------------------------

-- new       → new
-- contacted → contacted
-- qualified → replied          (they answered)
-- proposal  → call_booked      (a call was in the diary)
-- won       → converted        (leads don't close; deals do)
-- lost      → dropped
UPDATE leads SET stage = CASE stage
    WHEN 'qualified' THEN 'replied'
    WHEN 'proposal'  THEN 'call_booked'
    WHEN 'won'       THEN 'converted'
    WHEN 'lost'      THEN 'dropped'
    ELSE stage
END;

ALTER TABLE leads DROP CONSTRAINT IF EXISTS leads_stage_check;
ALTER TABLE leads ADD CONSTRAINT leads_stage_check
    CHECK (stage IN ('new', 'contacted', 'replied', 'call_booked', 'call_done',
                     'converted', 'dropped'));

-- ---------------------------------------------------------------------------
-- Ordering
-- ---------------------------------------------------------------------------

-- The board is replaced by an urgency-sorted list, so manual card ordering has
-- nothing left to order.
DROP INDEX IF EXISTS leads_org_stage_position_idx;
ALTER TABLE leads DROP COLUMN IF EXISTS position;

CREATE INDEX leads_org_stage_idx ON leads (org_id, stage);
-- The default sort and the Overdue/Due-today filters, which only ever consider
-- leads still in play.
CREATE INDEX leads_org_followup_idx ON leads (org_id, follow_up_at)
    WHERE stage NOT IN ('converted', 'dropped');
CREATE INDEX leads_org_account_idx ON leads (org_id, account_id);
CREATE INDEX leads_org_contact_idx ON leads (org_id, contact_id);
