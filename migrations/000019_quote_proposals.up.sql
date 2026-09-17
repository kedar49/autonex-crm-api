BEGIN;

-- Structured proposal content for a quote, for document templates that are more
-- than a price list.
--
-- The VIGIL techno-commercial proposal is fifteen pages of scope, SLA, AMC
-- coverage, prerequisites and acceptance criteria wrapped around the same
-- commercials a plain quote carries. Those sections are narrative and vary by
-- template, so they live here as jsonb rather than as columns: a second template
-- later is a new `template` value, not another twenty ALTERs.
--
-- Deliberately a side table, not columns on `quotes`:
--   * a quote without a proposal costs nothing and reads exactly as before,
--   * the list query never pays for a jsonb document it does not render, and
--   * money stays in `quote_items` / `quote_versions` where the totals, the
--     company financials and the invoice conversion already read it. Nothing in
--     here is ever summed.
CREATE TABLE IF NOT EXISTS quote_proposals (
    quote_id   uuid PRIMARY KEY REFERENCES quotes (id) ON DELETE CASCADE,
    template   text        NOT NULL,
    data       jsonb       NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    -- A known template name, so a typo cannot produce a document the editor has
    -- no renderer for. Extend this list when a template is added.
    CONSTRAINT quote_proposals_template_check
        CHECK (template IN ('vigil_technocommercial'))
);

-- The list badges proposal quotes, so the lookup is by template across all rows.
CREATE INDEX IF NOT EXISTS quote_proposals_template_idx
    ON quote_proposals (template);

COMMIT;
