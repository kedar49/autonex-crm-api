package followups

import (
	"testing"
	"time"

	"github.com/Autonex009/autonex-crm-api/pkg/apperr"
)

func TestValidate(t *testing.T) {
	// validate runs after normalize, which is where the priority default is
	// filled in — so it asserts the invariant rather than re-establishing it.
	if err := validate(Input{Title: "Call Acme", DueAt: time.Now(), Status: "open",
		Priority: "normal"}, true); err != nil {
		t.Fatalf("minimal action rejected: %v", err)
	}
	// Create never requires a status.
	if err := validate(Input{Title: "Call Acme", DueAt: time.Now(), Priority: "high"}, false); err != nil {
		t.Fatalf("create without status rejected: %v", err)
	}
}

// A caller that has never heard of priority still gets a valid action: the
// default is applied on the way in, not demanded of the client.
func TestNormalizeDefaultsPriority(t *testing.T) {
	in := normalize(Input{Title: "Call Acme", DueAt: time.Now(), Status: "open"})
	if in.Priority != "normal" {
		t.Fatalf("priority = %q, want \"normal\"", in.Priority)
	}
	if err := validate(in, true); err != nil {
		t.Fatalf("normalized action rejected: %v", err)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := map[string]struct {
		in            Input
		requireStatus bool
	}{
		"empty title":              {in: Input{DueAt: time.Now(), Status: "open", Priority: "normal"}, requireStatus: true},
		"zero due date":            {in: Input{Title: "X", Status: "open", Priority: "normal"}, requireStatus: true},
		"unknown status":           {in: Input{Title: "X", DueAt: time.Now(), Status: "pending", Priority: "normal"}, requireStatus: true},
		"missing status on update": {in: Input{Title: "X", DueAt: time.Now(), Priority: "normal"}, requireStatus: true},
		// The DB has a check constraint on this column, so an unvetted value
		// would turn a 400 into a 500.
		"unknown priority": {in: Input{Title: "X", DueAt: time.Now(), Status: "open", Priority: "urgent"},
			requireStatus: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := validate(tt.in, tt.requireStatus)
			if err == nil {
				t.Fatal("expected a validation error")
			}
			if !apperr.IsValidation(err) {
				t.Errorf("error = %v, want an apperr.Validation", err)
			}
		})
	}
}

func TestValidPriority(t *testing.T) {
	for _, p := range Priorities {
		if !validPriority(p) {
			t.Errorf("validPriority(%q) = false, want true", p)
		}
	}
	for _, p := range []string{"urgent", "low", "", "High"} {
		if validPriority(p) {
			t.Errorf("validPriority(%q) = true, want false", p)
		}
	}
}

func TestValidStatus(t *testing.T) {
	for _, s := range Statuses {
		if !validStatus(s) {
			t.Errorf("validStatus(%q) = false, want true", s)
		}
	}
	// The status vocabulary this table used before the Actions migration —
	// accepting it would silently let a stale client write a status the new
	// check constraint rejects at the DB, turning a 400 into a 500.
	for _, s := range []string{"pending", "sent", "cancelled", "", "Open"} {
		if validStatus(s) {
			t.Errorf("validStatus(%q) = true, want false", s)
		}
	}
}

func TestNormalizeBlanksToNil(t *testing.T) {
	in := normalize(Input{
		Title:      "  Call Acme  ",
		AssignedTo: ptr("   "),
		AccountID:  ptr("  "),
		Status:     " open ",
	})
	if in.Title != "Call Acme" {
		t.Errorf("Title = %q, want trimmed", in.Title)
	}
	if in.AssignedTo != nil {
		t.Errorf("AssignedTo = %q, want nil", *in.AssignedTo)
	}
	if in.AccountID != nil {
		t.Errorf("AccountID = %q, want nil", *in.AccountID)
	}
	if in.Status != "open" {
		t.Errorf("Status = %q, want trimmed", in.Status)
	}
}

func ptr[T any](v T) *T { return &v }
