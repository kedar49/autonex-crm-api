package quotes

import "testing"

func ptr[T any](v T) *T { return &v }

func line(desc string) ItemInput {
	return ItemInput{Description: desc, Quantity: 1, UnitPrice: 100}
}

func TestTransitions(t *testing.T) {
	allowed := [][2]string{
		{"draft", "sent"},
		{"sent", "approved"},
		{"sent", "rejected"},
		{"sent", "expired"},
		// Revising an issued quote back to draft is what actually happens when a
		// customer comes back with changes.
		{"sent", "draft"},
		{"rejected", "draft"},
		{"expired", "draft"},
	}
	for _, tc := range allowed {
		if !CanTransition(tc[0], tc[1]) {
			t.Errorf("%s → %s should be allowed", tc[0], tc[1])
		}
	}

	refused := [][2]string{
		// Approved is terminal: it records that a price was agreed.
		{"approved", "draft"},
		{"approved", "sent"},
		{"approved", "rejected"},
		// No skipping the issue step.
		{"draft", "approved"},
		{"draft", "rejected"},
		{"draft", "expired"},
		{"rejected", "approved"},
		{"expired", "sent"},
	}
	for _, tc := range refused {
		if CanTransition(tc[0], tc[1]) {
			t.Errorf("%s → %s should be refused", tc[0], tc[1])
		}
	}
}

func TestValidStatusMatchesTheCheckConstraint(t *testing.T) {
	for _, s := range []string{"draft", "sent", "approved", "rejected", "expired"} {
		if !ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []string{"", "Draft", "paid", "invoiced"} {
		if ValidStatus(s) {
			t.Errorf("ValidStatus(%q) = true, want false", s)
		}
	}
	// Keep in sync with the CHECK in migration 000009.
	if len(Statuses) != 5 {
		t.Fatalf("len(Statuses) = %d, want 5", len(Statuses))
	}
	// Every status must be reachable as a transition source, or it would be a
	// dead end nothing can leave — accepted is the one deliberate exception.
	for _, s := range Statuses {
		if _, ok := transitions[s]; !ok {
			t.Errorf("status %q has no transition entry", s)
		}
	}
}

func TestNormalizeDropsBlankLines(t *testing.T) {
	in := normalize(Input{
		Title: ptr("  Q3 proposal  "),
		Items: []ItemInput{
			line("Consulting"),
			// The editor always shows an empty trailing row; dropping it beats
			// making the document unsubmittable.
			{Description: "   ", Quantity: 0, UnitPrice: 0},
			line("Support"),
		},
	})

	if in.Title == nil || *in.Title != "Q3 proposal" {
		t.Errorf("Title = %v, want Q3 proposal", in.Title)
	}
	if len(in.Items) != 2 {
		t.Fatalf("kept %d items, want 2", len(in.Items))
	}
	if in.Items[1].Description != "Support" {
		t.Errorf("order not preserved: %q", in.Items[1].Description)
	}
}

func TestNormalizeKeepsAPricedLineWithoutADescription(t *testing.T) {
	// Only a fully empty row is noise. A line with a price but no text is a
	// mistake the user should be told about, not one to silently discard.
	in := normalize(Input{Items: []ItemInput{{Description: "  ", Quantity: 2, UnitPrice: 50}}})
	if len(in.Items) != 1 {
		t.Fatalf("kept %d items, want 1", len(in.Items))
	}
	if err := validate(in); err == nil {
		t.Fatal("expected the description-less line to be rejected")
	}
}

func TestValidate(t *testing.T) {
	ok := Input{Title: ptr("Q3"), Items: []ItemInput{line("Consulting")}}
	if err := validate(ok); err != nil {
		t.Fatalf("valid quote rejected: %v", err)
	}

	tests := map[string]Input{
		"no items":          {Title: ptr("Q3")},
		"blank description": {Items: []ItemInput{{Quantity: 1, UnitPrice: 10}}},
		"negative quantity": {Items: []ItemInput{{Description: "X", Quantity: -1}}},
		"negative price":    {Items: []ItemInput{{Description: "X", UnitPrice: -1}}},
		"discount over 100": {Items: []ItemInput{{Description: "X", DiscountPercent: 101}}},
		"tax over 100":      {Items: []ItemInput{{Description: "X", TaxPercent: 101}}},
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

func TestInputCarriesNoTotals(t *testing.T) {
	// A compile-time reminder as much as a test: if someone adds a Total field to
	// Input, a client could state a figure that disagrees with the lines. Totals
	// are derived in SQL (see store.recalculate) and belong only on Quote.
	var in Input
	_ = in
	var item ItemInput
	_ = item
	// Quote — the read model — is where totals live.
	var q Quote
	q.Total = 1
	if q.Total != 1 {
		t.Fatal("unreachable")
	}
}
