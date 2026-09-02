package deals

import (
	"testing"

	"github.com/go-crm/services/internal/leads"
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
	if in.Stage != "prospect" {
		t.Errorf("Stage = %q, want prospect", in.Stage)
	}
}

func TestValidate(t *testing.T) {
	if err := validate(Input{Title: "Acme renewal", Stage: "prospect"}); err != nil {
		t.Fatalf("minimal deal rejected: %v", err)
	}

	tests := map[string]Input{
		"no title":        {Stage: "prospect"},
		"unknown stage":   {Title: "X", Stage: "deck sent"}, // a lead stage, not a deal stage
		"empty stage":     {Title: "X", Stage: ""},
		"negative amount": {Title: "X", Stage: "prospect", Amount: -1},
		"absurd amount":   {Title: "X", Stage: "prospect", Amount: 1e13},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			err := validate(in)
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !IsValidation(err) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
		})
	}
}

func TestStagesAreDistinctFromLeadStages(t *testing.T) {
	// The two pipelines are intentionally different shapes. If they ever get
	// unified, that should be a deliberate change with a migration behind it —
	// not something that drifts in silently.
	if len(Stages) != 5 {
		t.Fatalf("len(Stages) = %d, want 5 — keep in sync with the CHECK constraint", len(Stages))
	}
	if ValidStage("deck sent") {
		t.Error(`"deck sent" is a lead stage and must not be valid for a deal`)
	}
	if leads.ValidStage("prospect") {
		t.Error(`"prospect" is a deal stage and must not be valid for a lead`)
	}
}
