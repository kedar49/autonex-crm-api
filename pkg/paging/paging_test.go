package paging

import (
	"net/http/httptest"
	"testing"
)

func TestParams(t *testing.T) {
	cases := []struct {
		name             string
		query            string
		wantLim, wantOff int
	}{
		{"both given", "?limit=10&offset=20", 10, 20},
		{"absent", "", 0, 0},
		// Present-but-empty has to behave like absent, or a client assembling a
		// URL from optional filters gets a different page than one that omits
		// the parameter entirely.
		{"empty values", "?limit=&offset=", 0, 0},
		{"unparseable", "?limit=abc&offset=xyz", 0, 0},
		{"negative offset passes through to Clamp", "?limit=5&offset=-3", 5, -3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/things"+tc.query, nil)
			gotLim, gotOff := Params(r)
			if gotLim != tc.wantLim || gotOff != tc.wantOff {
				t.Errorf("Params() = (%d, %d), want (%d, %d)",
					gotLim, gotOff, tc.wantLim, tc.wantOff)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	const def, max = 25, 100

	cases := []struct {
		name             string
		limit, offset    int
		wantLim, wantOff int
	}{
		{"in range", 50, 10, 50, 10},
		{"zero limit takes the default", 0, 0, def, 0},
		{"negative limit takes the default", -5, 0, def, 0},
		{"over the cap is capped", 1_000_000, 0, max, 0},
		{"exactly the cap is kept", max, 0, max, 0},
		{"negative offset floors at zero", 10, -3, 10, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotLim, gotOff := Clamp(tc.limit, tc.offset, def, max)
			if gotLim != tc.wantLim || gotOff != tc.wantOff {
				t.Errorf("Clamp(%d, %d, %d, %d) = (%d, %d), want (%d, %d)",
					tc.limit, tc.offset, def, max, gotLim, gotOff, tc.wantLim, tc.wantOff)
			}
		})
	}
}

// The activity feed serves wider pages than the record lists, so the bounds
// must come from the caller and not from a package-level constant.
func TestClampHonoursPerCallerBounds(t *testing.T) {
	if got, _ := Clamp(0, 0, 50, 200); got != 50 {
		t.Errorf("default = %d, want 50", got)
	}
	if got, _ := Clamp(500, 0, 50, 200); got != 200 {
		t.Errorf("cap = %d, want 200", got)
	}
}
