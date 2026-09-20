package deals

import (
	"testing"

	"github.com/Autonex009/autonex-crm-api/pkg/apperr"
)

func TestNormalizeLocationIDs(t *testing.T) {
	const (
		a = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
		b = "6ba7b810-9dad-11d1-80b4-00c04fd430c8"
	)

	tests := []struct {
		name    string
		in      []string
		want    []string
		wantErr bool
	}{
		{"empty stays empty", nil, []string{}, false},
		{"passes valid ids through", []string{a, b}, []string{a, b}, false},
		{
			// Position is derived from the slice index, so the surviving copy has
			// to be the first one or the sites reorder themselves on save.
			name: "collapses duplicates keeping first-seen order",
			in:   []string{b, a, b},
			want: []string{b, a},
		},
		{"rejects a non-uuid", []string{a, "not-a-uuid"}, nil, true},
		{"rejects an empty string", []string{""}, nil, true},
		{
			// The failure this guards against: Postgres answers a bad ::uuid cast
			// with an error, which reaches the client as a 500 instead of a 400.
			name:    "rejects a uuid with a bad separator",
			in:      []string{"3f2504e0x4f89-11d3-9a0c-0305e82c3301"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeLocationIDs(tc.in)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("normalizeLocationIDs(%v) = %v, want an error", tc.in, got)
				}
				if !apperr.IsValidation(err) {
					t.Errorf("error is not a validation error, so it would surface as 500: %v", err)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// A duplicate must not be reported as an error. The insert counts rows to detect
// ids that belong to another company, and an uncollapsed duplicate would be
// swallowed by ON CONFLICT, leaving the tally short and failing a valid save.
func TestNormalizeLocationIDsDuplicateIsNotAnError(t *testing.T) {
	const id = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"

	got, err := normalizeLocationIDs([]string{id, id, id})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d ids, want 1 — the tally would not match the insert", len(got))
	}
}
