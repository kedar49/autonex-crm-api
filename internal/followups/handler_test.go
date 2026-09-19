package followups

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
