package leads

import "testing"

func ptr[T any](v T) *T { return &v }

func TestStagesMatchTheDatabaseVocabulary(t *testing.T) {
	// Kept in sync with the leads_status_check constraint in the deployed
	// database. These names carry spaces and differ from the original brief;
	// the database is the authority.
	for _, s := range []string{
		"new", "initial count", "deck sent", "call scheduled",
		"call done", "proposal sent", "closed", "not interested",
	} {
		if !ValidStage(s) {
			t.Errorf("ValidStage(%q) = false, want true", s)
		}
	}

	// The brief's underscored names, which no row in this database uses.
	for _, s := range []string{
		"contacted", "replied", "call_booked", "call_done", "converted", "dropped", "",
	} {
		if ValidStage(s) {
			t.Errorf("ValidStage(%q) = true, want false — not a stage in this database", s)
		}
	}

	if len(Stages) != 8 {
		t.Fatalf("len(Stages) = %d, want 8", len(Stages))
	}
}

func TestNextStageWalksTheLifecycle(t *testing.T) {
	steps := map[string]string{
		"new":            "initial count",
		"initial count":  "deck sent",
		"deck sent":      "call scheduled",
		"call scheduled": "call done",
		"call done":      "proposal sent",
		"proposal sent":  "closed",
	}
	for from, want := range steps {
		if got := NextStage(from); got != want {
			t.Errorf("NextStage(%q) = %q, want %q", from, got, want)
		}
	}

	// A finished lead has nowhere to go.
	for _, s := range []string{"closed", "not interested"} {
		if got := NextStage(s); got != "" {
			t.Errorf("NextStage(%q) = %q, want no next step", s, got)
		}
	}
}

func TestTerminalStages(t *testing.T) {
	if !IsTerminal("closed") || !IsTerminal("not interested") {
		t.Error("closed and not interested are terminal")
	}
	for _, s := range []string{"new", "initial count", "deck sent", "call scheduled", "call done", "proposal sent"} {
		if IsTerminal(s) {
			t.Errorf("%q is still in play", s)
		}
	}
}

func TestValidFilterAcceptsUrgencyViews(t *testing.T) {
	// Overdue and due-today are views over the follow-up date, not stages — the
	// list must accept them even though ValidStage rejects them.
	for _, f := range []string{FilterOverdue, FilterDueToday, FilterOpen, "new", "call done"} {
		if !ValidFilter(f) {
			t.Errorf("ValidFilter(%q) = false, want true", f)
		}
	}
	for _, f := range []string{"urgent", "won", ""} {
		if ValidFilter(f) {
			t.Errorf("ValidFilter(%q) = true, want false", f)
		}
	}
}

func TestNormalizeAddsSchemeToLinkedIn(t *testing.T) {
	// Stored as an href; "linkedin.com/in/x" without a scheme resolves as a
	// *relative* path inside the app.
	cases := map[string]string{
		"linkedin.com/in/rohan":         "https://linkedin.com/in/rohan",
		"  www.linkedin.com/in/rohan  ": "https://www.linkedin.com/in/rohan",
		"https://linkedin.com/in/rohan": "https://linkedin.com/in/rohan",
		"http://linkedin.com/in/rohan":  "http://linkedin.com/in/rohan",
	}
	for input, want := range cases {
		got := normalize(Input{FirstName: "Rohan", LinkedIn: ptr(input)})
		if got.LinkedIn == nil || *got.LinkedIn != want {
			t.Errorf("LinkedIn %q → %v, want %q", input, got.LinkedIn, want)
		}
	}

	if in := normalize(Input{FirstName: "Rohan", LinkedIn: ptr("   ")}); in.LinkedIn != nil {
		t.Errorf("blank LinkedIn = %q, want nil", *in.LinkedIn)
	}
}

func TestNormalizeDefaultsStageAndBlanksToNil(t *testing.T) {
	in := normalize(Input{
		FirstName: "  Rohan  ",
		LastName:  ptr("   "),
		Email:     ptr("  Rohan@Example.COM "),
		Title:     ptr(" VP Operations "),
		AccountID: ptr("  "),
		Stage:     "   ",
	})

	if in.FirstName != "Rohan" {
		t.Errorf("FirstName = %q, want Rohan", in.FirstName)
	}
	if in.LastName != nil {
		t.Errorf("blank LastName = %q, want nil", *in.LastName)
	}
	if in.Email == nil || *in.Email != "rohan@example.com" {
		t.Errorf("Email = %v, want rohan@example.com", in.Email)
	}
	if in.Title == nil || *in.Title != "VP Operations" {
		t.Errorf("Title = %v, want trimmed", in.Title)
	}
	// A blank FK must be NULL, not "", which Postgres rejects as a UUID.
	if in.AccountID != nil {
		t.Errorf("blank AccountID = %q, want nil", *in.AccountID)
	}
	if in.Stage != "new" {
		t.Errorf("Stage = %q, want new", in.Stage)
	}
}

func TestValidateAcceptsBareMinimum(t *testing.T) {
	// A lead is incomplete by nature: a name and a stage must be enough.
	if err := validate(normalize(Input{FirstName: "Rohan"})); err != nil {
		t.Fatalf("minimal lead rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]Input{
		"no name":        {Stage: "new"},
		"removed stage":  {FirstName: "Rohan", Stage: "qualified"},
		"unknown stage":  {FirstName: "Rohan", Stage: "archived"},
		"bad email":      {FirstName: "Rohan", Stage: "new", Email: ptr("nope")},
		"negative value": {FirstName: "Rohan", Stage: "new", Value: ptr(-1.0)},
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
