package accounts

import "testing"

func ptr[T any](v T) *T { return &v }

func TestNormalizeAddsASchemeToBareDomains(t *testing.T) {
	// Stored values are rendered as hrefs, and "acme.com" without a scheme is
	// resolved by the browser as a *relative* path.
	cases := map[string]string{
		"acme.com":          "https://acme.com",
		"  acme.com  ":      "https://acme.com",
		"http://acme.com":   "http://acme.com",
		"https://acme.com":  "https://acme.com",
		"www.acme.com/team": "https://www.acme.com/team",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got := normalize(Input{Name: "Acme", Website: ptr(input)})
			if got.Website == nil || *got.Website != want {
				t.Errorf("website = %v, want %q", got.Website, want)
			}
		})
	}
}

func TestNormalizeBlanksBecomeNil(t *testing.T) {
	in := normalize(Input{
		Name:        "  Acme Ltd  ",
		Website:     ptr("   "),
		Industry:    ptr(""),
		Phone:       ptr(" \t "),
		OwnerUserID: ptr("  "),
	})

	if in.Name != "Acme Ltd" {
		t.Errorf("Name = %q, want Acme Ltd", in.Name)
	}
	for label, got := range map[string]*string{
		"Website":     in.Website,
		"Industry":    in.Industry,
		"Phone":       in.Phone,
		"OwnerUserID": in.OwnerUserID,
	} {
		if got != nil {
			// An empty-string FK in particular would reach Postgres as an invalid
			// UUID rather than NULL.
			t.Errorf("%s = %q, want nil", label, *got)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := validate(Input{Name: "Acme"}); err != nil {
		t.Fatalf("minimal account rejected: %v", err)
	}
	if err := validate(normalize(Input{Name: "Acme", Website: ptr("acme.com")})); err != nil {
		t.Fatalf("normalized website rejected: %v", err)
	}

	long := make([]byte, 161)
	for i := range long {
		long[i] = 'a'
	}

	tests := map[string]Input{
		"no name":         {},
		"blank name":      {Name: "   "},
		"over-long name":  {Name: string(long)},
		"spacey website":  {Name: "Acme", Website: ptr("https://acme .com")},
		"scheme-only URL": {Name: "Acme", Website: ptr("https://")},
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			// normalize first: that is the real call order, and it is what turns
			// "   " into the empty name the validator must reject.
			err := validate(normalize(in))
			if err == nil {
				t.Fatal("expected rejection")
			}
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
		{0, 0, defaultLimit, 0},
		{-5, -5, defaultLimit, 0},
		{10, 30, 10, 30},
		{1000, 0, maxLimit, 0},
	}
	for _, tt := range tests {
		limit, offset := clampPage(tt.limit, tt.offset)
		if limit != tt.wantLimit || offset != tt.wantOff {
			t.Errorf("clampPage(%d, %d) = (%d, %d), want (%d, %d)",
				tt.limit, tt.offset, limit, offset, tt.wantLimit, tt.wantOff)
		}
	}
}
