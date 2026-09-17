package quotes

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestValidateTemplateAcceptsKnownTemplate(t *testing.T) {
	if err := validateTemplate(TemplateVigil, json.RawMessage(`{"customerName":"Acme"}`)); err != nil {
		t.Fatalf("known template rejected: %v", err)
	}
}

func TestValidateTemplateRejectsUnknownName(t *testing.T) {
	err := validateTemplate("not_a_template", json.RawMessage(`{}`))
	if !errors.Is(err, ErrUnknownTemplate) {
		t.Fatalf("want ErrUnknownTemplate, got %v", err)
	}
}

// The template list and the database constraint are two copies of the same
// decision, so a template added to one and not the other must be caught here
// rather than as a constraint violation in production.
func TestTemplatesMatchTheMigrationConstraint(t *testing.T) {
	const inMigration = "vigil_technocommercial"

	if len(templates) != 1 {
		t.Fatalf("templates has %d entries; update this test and "+
			"quote_proposals_template_check together", len(templates))
	}
	if !templates[inMigration] {
		t.Fatalf("%q is in the migration's CHECK but not in templates", inMigration)
	}
}

func TestValidateTemplateRejectsOversizedDocument(t *testing.T) {
	huge := json.RawMessage(`{"notes":"` + strings.Repeat("x", maxProposalBytes) + `"}`)

	err := validateTemplate(TemplateVigil, huge)
	if err == nil {
		t.Fatal("an oversized proposal was accepted")
	}
	if errors.Is(err, ErrUnknownTemplate) {
		t.Fatalf("size failure reported as an unknown template: %v", err)
	}
}

// An empty payload is the state a freshly created proposal is in before the
// user has typed anything, so it must not be an error.
func TestValidateTemplateAcceptsEmptyDocument(t *testing.T) {
	if err := validateTemplate(TemplateVigil, nil); err != nil {
		t.Fatalf("empty proposal rejected: %v", err)
	}
}
