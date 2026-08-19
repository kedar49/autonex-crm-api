package bulkimport

import (
	"strings"
	"testing"
)

func TestParseLeadsCSV(t *testing.T) {
	csvData := `first_name,last_name,email,company,source
John,Doe,john@example.com,Acme Corp,Website
Jane,Smith,jane@example.com,Beta Inc,Referral`

	res, err := ParseLeadsCSV(strings.NewReader(csvData))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Successful != 2 {
		t.Errorf("expected 2 successful rows, got %d", res.Successful)
	}
	if len(res.Leads) != 2 {
		t.Errorf("expected 2 leads parsed, got %d", len(res.Leads))
	}
	if res.Leads[0].Company != "Acme Corp" {
		t.Errorf("expected company Acme Corp, got %s", res.Leads[0].Company)
	}
}
