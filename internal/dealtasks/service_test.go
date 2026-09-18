package dealtasks

import (
	"context"
	"strings"
	"testing"

	"github.com/Autonex009/autonex-crm-api/pkg/apperr"
)

// These guards all run before the store is touched, so a zero Service is
// enough: reaching the database would be the bug they exist to catch.
func TestPrepareRejectsEmptyText(t *testing.T) {
	svc := &Service{}
	for _, text := range []string{"", "   ", "\t\n"} {
		_, err := svc.prepare(context.Background(), "org-1", Input{Text: text}, false)
		if !apperr.IsValidation(err) {
			t.Errorf("text %q: err = %v, want a validation error", text, err)
		}
	}
}

func TestPrepareRejectsOverlongText(t *testing.T) {
	svc := &Service{}
	_, err := svc.prepare(context.Background(), "org-1",
		Input{Text: strings.Repeat("a", 501)}, false)
	if !apperr.IsValidation(err) {
		t.Fatalf("err = %v, want a validation error", err)
	}
}

func TestPrepareRejectsUnknownPriority(t *testing.T) {
	svc := &Service{}
	for _, p := range []string{"urgent", "HIGH", "low", "1"} {
		_, err := svc.prepare(context.Background(), "org-1", Input{Text: "call", Priority: p}, false)
		if !apperr.IsValidation(err) {
			t.Errorf("priority %q: err = %v, want a validation error", p, err)
		}
	}
}

// An omitted priority is the common case from a quick-add box.
func TestPrepareDefaultsPriorityToNormal(t *testing.T) {
	svc := &Service{}
	in, err := svc.prepare(context.Background(), "org-1", Input{Text: "  call the client  "}, false)
	if err != nil {
		t.Fatalf("prepare returned %v", err)
	}
	if in.Priority != "normal" {
		t.Errorf("priority = %q, want normal", in.Priority)
	}
	if in.Text != "call the client" {
		t.Errorf("text = %q, want it trimmed", in.Text)
	}
}

// A blank assignee from a "— Unassigned —" option must become NULL, not a
// lookup for the empty string.
func TestPrepareBlanksAssigneeToNil(t *testing.T) {
	svc := &Service{}
	blank := "   "
	in, err := svc.prepare(context.Background(), "org-1", Input{Text: "call", AssignedTo: &blank}, false)
	if err != nil {
		t.Fatalf("prepare returned %v", err)
	}
	if in.AssignedTo != nil {
		t.Errorf("assignedTo = %v, want nil", *in.AssignedTo)
	}
}

func TestPrepareRequiresDealOnCreate(t *testing.T) {
	svc := &Service{}
	_, err := svc.prepare(context.Background(), "org-1", Input{Text: "call"}, true)
	if !apperr.IsValidation(err) {
		t.Fatalf("err = %v, want a validation error", err)
	}
}
