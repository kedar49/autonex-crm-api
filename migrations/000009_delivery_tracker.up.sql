BEGIN;

-- The client delivery tracker: the spreadsheet operations already keep by hand,
-- moved into the database so edits are shared and auditable.
--
-- Deliberately standalone rather than hung off `deals`. A tracker row is about
-- an installation at a client, which is not one-to-one with a deal: a client can
-- have several deals feeding one rollout, and rows are entered here long before
-- (or after) anyone opens the pipeline. Linking the two would force a deal to
-- exist before a row could be typed.
--
-- Every column except the client name is nullable and free text: rows arrive by
-- pasting or importing a sheet, and rejecting a half-filled row at the door is
-- worse than storing it and letting someone finish it.
CREATE TABLE IF NOT EXISTS delivery_tracker (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id              UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    client              TEXT NOT NULL,
    products            TEXT,
    locations           TEXT,
    total_cameras       INTEGER CHECK (total_cameras IS NULL OR total_cameras >= 0),
    status              TEXT,
    implementation_date DATE,
    current_stages      TEXT,
    key_contacts        TEXT,
    next_steps          TEXT,
    notes               TEXT,
    -- Row order as the user arranged it. Sparse (steps of 1000) so a drag or an
    -- insert between two rows does not renumber the table.
    position            INTEGER NOT NULL DEFAULT 0,
    created_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    updated_by          UUID REFERENCES users(id) ON DELETE SET NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Import upserts on the client name, so it has to identify a row. Case- and
-- whitespace-insensitive, because "Acme " and "acme" off a spreadsheet are the
-- same client to everyone except the database.
CREATE UNIQUE INDEX IF NOT EXISTS delivery_tracker_client_idx
    ON delivery_tracker (org_id, lower(btrim(client)));

CREATE INDEX IF NOT EXISTS delivery_tracker_org_position_idx
    ON delivery_tracker (org_id, position, created_at);

DROP TRIGGER IF EXISTS set_delivery_tracker_updated_at ON delivery_tracker;
CREATE TRIGGER set_delivery_tracker_updated_at
BEFORE UPDATE ON delivery_tracker
FOR EACH ROW EXECUTE FUNCTION public.set_updated_at();

COMMIT;
