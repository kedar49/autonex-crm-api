package activities

import "testing"

func ptr[T any](v T) *T { return &v }

// attached returns a valid input, so each test can vary one thing.
func attached() Input {
	return Input{Kind: "call", Body: ptr("Spoke to Rohan"), DealID: ptr("d-1")}
}

func TestValidKindExcludesSystem(t *testing.T) {
	for _, k := range []string{"note", "call", "email", "meeting", "site_visit"} {
		if !ValidKind(k) {
			t.Errorf("ValidKind(%q) = false, want true", k)
		}
	}
	// Only the app writes system events, through Log. A client must not be able
	// to forge one — a fake "Deal moved to Won" would be indistinguishable from
	// the real thing on the timeline.
	if ValidKind("system") {
		t.Error(`"system" must not be writable by a client`)
	}
	if len(Kinds) != 5 {
		t.Fatalf("len(Kinds) = %d, want 5 — keep in sync with the CHECK constraint", len(Kinds))
	}
}

func TestNormalizeDefaultsKind(t *testing.T) {
	in := normalize(Input{Body: ptr("  something happened  "), DealID: ptr("  d-1  ")})
	if in.Kind != "note" {
		t.Errorf("Kind = %q, want note", in.Kind)
	}
	if in.Body == nil || *in.Body != "something happened" {
		t.Errorf("Body = %v, want trimmed", in.Body)
	}
	// A blank id must be NULL, not "", which Postgres would reject as a UUID.
	in = normalize(Input{Body: ptr("x"), LeadID: ptr("   ")})
	if in.LeadID != nil {
		t.Errorf("blank LeadID = %q, want nil", *in.LeadID)
	}
}

func TestValidateRequiresAnAttachment(t *testing.T) {
	// An activity attached to nothing appears on no timeline — it would be
	// silently lost rather than rejected.
	in := Input{Kind: "note", Body: ptr("floating")}
	err := validate(normalize(in))
	if err == nil {
		t.Fatal("expected an unattached activity to be rejected")
	}
	if !IsValidation(err) {
		t.Fatalf("err = %v, want a ValidationError", err)
	}

	for name, id := range map[string]*string{
		"lead":    ptr("l-1"),
		"deal":    ptr("d-1"),
		"account": ptr("a-1"),
		"contact": ptr("c-1"),
		"quote":   ptr("q-1"),
		"invoice": ptr("i-1"),
	} {
		t.Run(name, func(t *testing.T) {
			attached := Input{Kind: "note", Body: ptr("x")}
			switch name {
			case "lead":
				attached.LeadID = id
			case "deal":
				attached.DealID = id
			case "account":
				attached.AccountID = id
			case "contact":
				attached.ContactID = id
			case "quote":
				attached.QuoteID = id
			case "invoice":
				attached.InvoiceID = id
			}
			if err := validate(normalize(attached)); err != nil {
				t.Fatalf("attaching to a %s was rejected: %v", name, err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	long := make([]byte, 5001)
	for i := range long {
		long[i] = 'a'
	}

	cases := map[string]func(Input) Input{
		"unknown kind":     func(in Input) Input { in.Kind = "telepathy"; return in },
		"system kind":      func(in Input) Input { in.Kind = "system"; return in },
		"no subject/body":  func(in Input) Input { in.Body = nil; in.Subject = nil; return in },
		"over-long body":   func(in Input) Input { in.Body = ptr(string(long)); return in },
		"negative minutes": func(in Input) Input { in.DurationMinutes = ptr(-5); return in },
		"absurd minutes":   func(in Input) Input { in.DurationMinutes = ptr(5000); return in },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			err := validate(normalize(mutate(attached())))
			if err == nil {
				t.Fatal("expected rejection")
			}
			if !IsValidation(err) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
		})
	}
}

func TestEditDoesNotRequireAnAttachment(t *testing.T) {
	// An edit can't change what an activity hangs off — the UPDATE doesn't touch
	// those columns — so a PUT body has no reason to repeat the ids. Applying the
	// create-time rule here rejected every edit with "not attached to anything",
	// which also masked the 409 for system events and the 404 for another org's.
	edit := Input{Kind: "call", Subject: ptr("Demo call (updated)"), DurationMinutes: ptr(50)}

	if err := validateEditable(normalize(edit)); err != nil {
		t.Fatalf("edit rejected: %v", err)
	}
	if err := validate(normalize(edit)); err == nil {
		t.Fatal("create-time validation should still demand an attachment")
	}
}
