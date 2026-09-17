package quotes

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// TemplateVigil is the Autonex VIGIL techno-commercial proposal: the fifteen-page
// document of scope, SLA, AMC coverage, prerequisites and acceptance criteria
// that wraps the same commercials a plain quote carries.
const TemplateVigil = "vigil_technocommercial"

// ErrUnknownTemplate means the client asked for a document layout that has no
// renderer. The database enforces this too; failing here gives a usable message
// instead of a constraint violation.
var ErrUnknownTemplate = errors.New("unknown proposal template")

// templates is the set the editor and the print view can actually render. It
// must stay in step with quote_proposals_template_check.
var templates = map[string]bool{TemplateVigil: true}

// maxProposalBytes bounds one stored document. The VIGIL template runs to a few
// kilobytes of text; a megabyte is a runaway client, not a proposal.
const maxProposalBytes = 1 << 20

// validateTemplate checks the template name and the size of its payload.
//
// The content itself is deliberately not validated field by field: it is a
// document the client renders, its shape belongs to the template, and a server
// that enforced the shape would need redeploying to add a row to a table.
func validateTemplate(name string, data json.RawMessage) error {
	if !templates[name] {
		return ErrUnknownTemplate
	}
	if len(data) > maxProposalBytes {
		return errors.New("proposal content is too large")
	}
	return nil
}

// proposal reads the stored document for a quote, or nil when it has none.
//
// A quote with no proposal is the normal case, so a missing row is not an error.
func (s *store) proposal(ctx context.Context, quoteID string) (json.RawMessage, error) {
	var data []byte
	err := s.pool.QueryRow(ctx,
		`SELECT data FROM quote_proposals WHERE quote_id = $1`, quoteID).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return json.RawMessage(data), nil
}

// saveProposal writes the document, replacing whatever was there.
//
// Called from inside the create/update transaction: a proposal that survived a
// rolled-back quote would be an orphan the editor could never reach.
func saveProposal(ctx context.Context, tx pgx.Tx, quoteID, template string, data json.RawMessage) error {
	if len(data) == 0 {
		data = json.RawMessage("{}")
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO quote_proposals (quote_id, template, data)
		 VALUES ($1, $2, $3::jsonb)
		 ON CONFLICT (quote_id) DO UPDATE
		 SET template   = EXCLUDED.template,
		     data       = EXCLUDED.data,
		     updated_at = now()`,
		quoteID, template, []byte(data))
	return err
}
