package contacts

import "testing"

func TestNormalizeTrimsAndLowercases(t *testing.T) {
	phone := "  555-0100  "
	blank := "   "
	in := normalize(Input{
		FirstName: "  Ada ",
		LastName:  " Lovelace  ",
		Email:     "  Ada@Example.COM ",
		Phone:     &phone,
		AccountID: &blank,
	})

	if in.FirstName != "Ada" || in.LastName != "Lovelace" {
		t.Fatalf("names not trimmed: %q %q", in.FirstName, in.LastName)
	}
	if in.Email != "ada@example.com" {
		t.Fatalf("email = %q, want ada@example.com", in.Email)
	}
	if in.Phone == nil || *in.Phone != "555-0100" {
		t.Fatalf("phone not trimmed: %v", in.Phone)
	}
	// A blank optional field must become NULL, not an empty-string FK.
	if in.AccountID != nil {
		t.Fatalf("blank accountId = %q, want nil", *in.AccountID)
	}
}

func TestValidate(t *testing.T) {
	ok := Input{FirstName: "Ada", LastName: "Lovelace", Email: "ada@example.com"}
	if err := validate(ok); err != nil {
		t.Fatalf("valid input rejected: %v", err)
	}

	tests := map[string]Input{
		"missing first name": {LastName: "L", Email: "a@b.com"},
		"missing last name":  {FirstName: "A", Email: "a@b.com"},
		"missing email":      {FirstName: "A", LastName: "L"},
		"email without @":    {FirstName: "A", LastName: "L", Email: "not-an-email"},
		"email with space":   {FirstName: "A", LastName: "L", Email: "a b@c.com"},
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
