package followups

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsUUID(t *testing.T) {
	valid := []string{
		"3f2504e0-4f89-11d3-9a0c-0305e82c3301",
		"3F2504E0-4F89-11D3-9A0C-0305E82C3301",
	}
	for _, v := range valid {
		if !isUUID(v) {
			t.Errorf("isUUID(%q) = false, want true", v)
		}
	}

	invalid := []string{
		"", "abc", "not-a-uuid-at-all-really-nope-nope",
		"3f2504e0-4f89-11d3-9a0c-0305e82c330",   // too short
		"3f2504e0-4f89-11d3-9a0c-0305e82c33011", // too long
		"3f2504e04f8911d39a0c0305e82c3301",      // no dashes
		"3f2504e0-4f89-11d3-9a0c-0305e82c330g",  // non-hex
		"3f2504e0_4f89-11d3-9a0c-0305e82c3301",  // wrong separator
	}
	for _, v := range invalid {
		if isUUID(v) {
			t.Errorf("isUUID(%q) = true, want false", v)
		}
	}
}

// parseFilter is the only thing standing between a malformed query string and a
// ::uuid cast error surfacing as a 500.
func TestParseFilterRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"bad account":  "?accountId=abc",
		"bad lead":     "?leadId=abc",
		"bad assignee": "?assignedTo=abc",
		"bad status":   "?status=archived",
		"bad due":      "?dueBefore=tomorrow",
		"bad exclude":  "?excludeDone=yes",
	}
	for name, qs := range cases {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			if _, ok := parseFilter(rec, httptest.NewRequest(http.MethodGet, "/actions"+qs, nil)); ok {
				t.Fatal("parseFilter accepted the request, want rejected")
			}
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
		})
	}
}

func TestParseFilterAcceptsValidInput(t *testing.T) {
	const id = "3f2504e0-4f89-11d3-9a0c-0305e82c3301"
	req := httptest.NewRequest(http.MethodGet,
		"/actions?accountId="+id+"&assignedTo="+id+"&status=open&excludeDone=true&dueBefore=2026-01-01T00:00:00Z", nil)

	f, ok := parseFilter(httptest.NewRecorder(), req)
	if !ok {
		t.Fatal("parseFilter rejected a valid request")
	}
	if f.AccountID != id || f.AssignedTo != id || f.Status != "open" || !f.ExcludeDone {
		t.Fatalf("filter = %+v, want the query's values", f)
	}
	if f.DueBefore == nil {
		t.Fatal("DueBefore = nil, want the parsed timestamp")
	}
}

// An empty query string is the dashboard's default view: no filters at all.
func TestParseFilterEmptyIsPermissive(t *testing.T) {
	f, ok := parseFilter(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/actions", nil))
	if !ok {
		t.Fatal("parseFilter rejected an unfiltered request")
	}
	if (f != Filter{}) {
		t.Fatalf("filter = %+v, want zero value", f)
	}
}
