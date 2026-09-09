package deals

import (
	"testing"

	"github.com/go-crm/services/internal/leads"
	"github.com/go-crm/services/pkg/apperr"
)

func ptr[T any](v T) *T { return &v }

func TestNormalizeDefaultsStageAndBlanksToNil(t *testing.T) {
	in := normalize(Input{
		Title:       "  Acme renewal  ",
		Description: ptr("   "),
		OwnerUserID: ptr("  "),
		Stage:       "  ",
	})

	if in.Title != "Acme renewal" {
		t.Errorf("Title = %q, want Acme renewal", in.Title)
	}
	if in.Description != nil {
		t.Errorf("Description = %q, want nil", *in.Description)
	}
	// A blank optional FK must be NULL, not an empty string Postgres would
	// reject as an invalid UUID.
	if in.OwnerUserID != nil {
		t.Errorf("OwnerUserID = %q, want nil", *in.OwnerUserID)
	}
	if in.Stage != "discovery" {
		t.Errorf("Stage = %q, want discovery", in.Stage)
	}
}

func TestValidate(t *testing.T) {
	if err := validate(Input{Title: "Acme renewal", Stage: "discovery"}); err != nil {
		t.Fatalf("minimal deal rejected: %v", err)
	}

	tests := map[string]Input{
		"no title":        {Stage: "discovery"},
		"unknown stage":   {Title: "X", Stage: "deck sent"}, // a lead stage, not a deal stage
		"empty stage":     {Title: "X", Stage: ""},
		"negative amount": {Title: "X", Stage: "discovery", Amount: -1},
		"absurd amount":   {Title: "X", Stage: "discovery", Amount: 1e13},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			err := validate(in)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !apperr.IsValidation(err) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
		})
	}
}

func TestStagesAreDistinctFromLeadStages(t *testing.T) {
	// The two pipelines are intentionally different shapes. If they ever get
	// unified, that should be a deliberate change with a migration behind it —
	// not something that drifts in silently.
	if len(Stages) != 7 {
		t.Fatalf("len(Stages) = %d, want 7 — keep in sync with the CHECK constraint", len(Stages))
	}
	if ValidStage("deck sent") {
		t.Error(`"deck sent" is a lead stage and must not be valid for a deal`)
	}
	if leads.ValidStage("discovery") {
		t.Error(`"discovery" is a deal stage and must not be valid for a lead`)
	}
}

func TestNormalizeStage(t *testing.T) {
	cases := map[string]string{
		"discovery":       "discovery",
		"Discovery":       "discovery",
		"  discovery  ":   "discovery",
		"site_assessment": "site_assessment",
		"Site assessment": "site_assessment",
		"Site Assessment": "site_assessment",
		"site-assessment": "site_assessment",
		"quote_sent":      "quote_sent",
		"Quote sent":      "quote_sent",
		"Quote Sent":      "quote_sent",
		"negotiation":     "negotiation",
		"Negotiation":     "negotiation",
		"won":             "won",
		"Won":             "won",
		"lost":            "lost",
		"Lost":            "lost",
		"prospect":        "discovery",
		"lead":            "discovery",
		"proposal":        "quote_sent",
		"qualified":       "site_assessment",
	}

	for input, want := range cases {
		if got := NormalizeStage(input); got != want {
			t.Errorf("NormalizeStage(%q) = %q, want %q", input, got, want)
		}
		if !ValidStage(input) {
			t.Errorf("ValidStage(%q) = false, want true", input)
		}
	}
}
