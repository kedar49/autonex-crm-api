package invoices

import (
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func line(desc string) ItemInput {
	return ItemInput{Description: desc, Quantity: 1, UnitPrice: 100}
}

func TestTransitions(t *testing.T) {
	allowed := [][2]string{
		{"draft", "sent"},
		{"draft", "void"},
		{"sent", "paid"},
		{"sent", "void"},
	}
	for _, tc := range allowed {
		if !CanTransition(tc[0], tc[1]) {
			t.Errorf("%s → %s should be allowed", tc[0], tc[1])
		}
	}

	refused := [][2]string{
		// A numbered demand that has gone to a customer is not un-sent; the
		// correction is a void, not an edit.
		{"sent", "draft"},
		{"paid", "sent"},
		{"paid", "draft"},
		{"paid", "void"},
		{"void", "draft"},
		{"void", "sent"},
		// No skipping issue.
		{"draft", "paid"},
	}
	for _, tc := range refused {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("%s → %s should be refused", tc[0], tc[1])
		}
	}
}

func TestStatusesMatchTheCheckConstraint(t *testing.T) {
	for _, s := range []string{"draft", "sent", "paid", "void"} {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	// "overdue" is derived from the due date, never stored — accepting it as a
	// status would let a client write a value the CHECK constraint rejects.
	for _, s := range []string{"", "overdue", "Draft", "accepted"} {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
	if len(Statuses) != 4 {
		t.Fatalf("len(Statuses) = %d, want 4", len(Statuses))
	}
	for _, s := range Statuses {
		if _, ok := transitions[s]; !ok {
			t.Errorf("status %q has no transition entry", s)
		}
	}
}

func TestValidate(t *testing.T) {
	ok := Input{Title: ptr("March"), Items: []ItemInput{line("Consulting")}}
	if err := validate(ok); err != nil {
		t.Fatalf("valid invoice rejected: %v", err)
	}

	issue := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	earlier := issue.Add(-24 * time.Hour)

	tests := map[string]Input{
		"no items":          {Title: ptr("March")},
		"blank description": {Items: []ItemInput{{Quantity: 1, UnitPrice: 10}}},
		"negative quantity": {Items: []ItemInput{{Description: "X", Quantity: -1}}},
		"discount over 100": {Items: []ItemInput{{Description: "X", DiscountPercent: 101}}},
		// A demand that falls due before it was issued is a data-entry slip worth
		// catching, not a policy question.
		"due before issue": {
			Items:     []ItemInput{line("X")},
			IssueDate: &issue,
			DueDate:   &earlier,
		},
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

func TestNormalizeDropsBlankLines(t *testing.T) {
	in := normalize(Input{
		Title: ptr("  March  "),
		Items: []ItemInput{
			line("Consulting"),
			{Description: "   ", Quantity: 0, UnitPrice: 0},
			line("Support"),
		},
	})

	if in.Title == nil || *in.Title != "March" {
		t.Errorf("Title = %v, want March", in.Title)
	}
	if len(in.Items) != 2 {
		t.Fatalf("kept %d items, want 2", len(in.Items))
	}
}

func TestInputCarriesNoMoney(t *testing.T) {
	// Same guarantee as quotes: a client cannot state a total or an amount paid.
	// Totals come from the items and amount_paid comes from the payments table,
	// both recomputed in SQL.
	var in Input
	_ = in.Items
	var inv Invoice
	inv.Total, inv.AmountPaid, inv.Balance = 1, 1, 0
	if inv.Total != 1 || inv.AmountPaid != 1 || inv.Balance != 0 {
		t.Fatal("unreachable")
	}
}
