package leads

import "testing"

func ptr[T any](v T) *T { return &v }

func TestNormalizeDefaultsStageAndBlanksToNil(t *testing.T) {
	in := normalize(Input{
		FirstName: "  Ada  ",
		LastName:  ptr("   "),
		Email:     ptr("  Ada@Example.COM "),
		Company:   ptr(" Acme "),
		Stage:     "   ",
	})

	if in.FirstName != "Ada" {
		t.Errorf("FirstName = %q, want Ada", in.FirstName)
	}
	// A blank optional field must be NULL, not an empty string.
	if in.LastName != nil {
		t.Errorf("LastName = %q, want nil", *in.LastName)
	}
	if in.Email == nil || *in.Email != "ada@example.com" {
		t.Errorf("Email = %v, want ada@example.com", in.Email)
	}
	if in.Company == nil || *in.Company != "Acme" {
		t.Errorf("Company = %v, want Acme", in.Company)
	}
	// An omitted stage starts the lead at the top of the pipeline.
	if in.Stage != "new" {
		t.Errorf("Stage = %q, want new", in.Stage)
	}
}

func TestValidateAcceptsBareMinimum(t *testing.T) {
	// A lead is incomplete by nature: a name and a stage must be enough.
	if err := validate(Input{FirstName: "Ada", Stage: "new"}); err != nil {
		t.Fatalf("minimal lead rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]Input{
		"no name":        {Stage: "new"},
		"unknown stage":  {FirstName: "Ada", Stage: "archived"},
		"empty stage":    {FirstName: "Ada", Stage: ""},
		"bad email":      {FirstName: "Ada", Stage: "new", Email: ptr("nope")},
		"negative value": {FirstName: "Ada", Stage: "new", Value: ptr(-1.0)},
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

func TestValidStageCoversTheLifecycle(t *testing.T) {
	for _, s := range []string{"new", "contacted", "qualified", "proposal", "won", "lost"} {
		if !ValidStage(s) {
			t.Errorf("ValidStage(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "New", "archived", "deleted"} {
		if ValidStage(s) {
			t.Errorf("ValidStage(%q) = true, want false", s)
		}
	}
	// The DB CHECK constraint in migration 000004 lists exactly these; if this
	// count changes without the migration changing, inserts will start failing.
	if len(Stages) != 6 {
		t.Fatalf("len(Stages) = %d, want 6 — keep in sync with the CHECK constraint", len(Stages))
	}
}
