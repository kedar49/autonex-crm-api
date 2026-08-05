package contacts

import "testing"

func ptr[T any](v T) *T { return &v }

func TestNormalizeTrimsAndLowercases(t *testing.T) {
	in := normalize(Input{
		FirstName: "  Ada ",
		LastName:  ptr(" Lovelace  "),
		Email:     ptr("  Ada@Example.COM "),
		Phone:     ptr("  555-0100  "),
		AccountID: ptr("   "),
	})

	if in.FirstName != "Ada" {
		t.Errorf("FirstName = %q, want Ada", in.FirstName)
	}
	if in.LastName == nil || *in.LastName != "Lovelace" {
		t.Errorf("LastName = %v, want Lovelace", in.LastName)
	}
	if in.Email == nil || *in.Email != "ada@example.com" {
		t.Errorf("Email = %v, want ada@example.com", in.Email)
	}
	if in.Phone == nil || *in.Phone != "555-0100" {
		t.Errorf("phone not trimmed: %v", in.Phone)
	}
	// A blank optional field must become NULL, not an empty-string FK.
	if in.AccountID != nil {
		t.Errorf("blank accountId = %q, want nil", *in.AccountID)
	}
}

func TestNormalizeBlankOptionalNamesAndEmailsBecomeNil(t *testing.T) {
	in := normalize(Input{FirstName: "Ada", LastName: ptr("  "), Email: ptr("   ")})

	if in.LastName != nil {
		t.Errorf("blank LastName = %q, want nil", *in.LastName)
	}
	// Must be NULL rather than "": the partial unique index on lower(email)
	// would otherwise collide across every email-less contact.
	if in.Email != nil {
		t.Errorf("blank Email = %q, want nil", *in.Email)
	}
}

func TestValidateRequiresOnlyAFirstName(t *testing.T) {
	// Relaxed in migration 000006 so a lead — which only requires a first name —
	// can be converted into a contact. Mononyms and phone-only contacts are also
	// simply real.
	cases := map[string]Input{
		"name only":      {FirstName: "Ada"},
		"name + surname": {FirstName: "Ada", LastName: ptr("Lovelace")},
		"name + email":   {FirstName: "Ada", Email: ptr("ada@example.com")},
		"name + phone":   {FirstName: "Ada", Phone: ptr("555-0100")},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if err := validate(in); err != nil {
				t.Fatalf("valid input rejected: %v", err)
			}
		})
	}
}

func TestValidateRejects(t *testing.T) {
	long := make([]byte, 101)
	for i := range long {
		long[i] = 'a'
	}

	tests := map[string]Input{
		"missing first name": {LastName: ptr("L")},
		"blank first name":   {FirstName: ""},
		// Optional, but must look like an address when supplied.
		"email without @":  {FirstName: "A", Email: ptr("not-an-email")},
		"email with space": {FirstName: "A", Email: ptr("a b@c.com")},
		"over-long name":   {FirstName: string(long)},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			err := validate(in)
			if err == nil {
				t.Fatal("expected rejection")
			}
			// The handler turns only ValidationError into a 400; anything else
			// would surface as a 500.
			if !IsValidation(err) {
				t.Fatalf("err = %v, want a ValidationError", err)
			}
		})
	}
}

func TestClampPage(t *testing.T) {
	tests := []struct {
		limit, offset      int
		wantLimit, wantOff int
	}{
		{0, 0, defaultLimit, 0},   // unset → default
		{-5, -5, defaultLimit, 0}, // negatives are nonsense
		{10, 30, 10, 30},          // honoured as-is
		{1000, 0, maxLimit, 0},    // capped, so a client can't ask for everything
	}
	for _, tt := range tests {
		limit, offset := clampPage(tt.limit, tt.offset)
		if limit != tt.wantLimit || offset != tt.wantOff {
			t.Errorf("clampPage(%d, %d) = (%d, %d), want (%d, %d)",
				tt.limit, tt.offset, limit, offset, tt.wantLimit, tt.wantOff)
		}
	}
}
