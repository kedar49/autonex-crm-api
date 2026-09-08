package delivery

import (
	"strings"
	"testing"
	"time"
)

// The real tracker opens with a title and a subtitle above the headings, so the
// header is never line 1. Everything below depends on finding it anyway.
const sheetWithBanner = `Client Delivery Tracker,,,,,,,,,
Master overview - see individual client tabs for full detail,,,,,,,,,
Client,Product(s),Key Location(s),Total Cameras,Status,Implementation Date,Current Stage(s),Key Contacts,Next Steps,Notes
Acme Steel,VIGIL,Pune; Nashik,24,In progress,2026-03-14,Cabling,Ravi,Site visit,Phase 1
,,,,,,,,,
Borax Mills,VIGIL Lite,Surat,8,Planned,02/04/2026,Survey,Meera,Send quote,
`

func parseCSVString(t *testing.T, body string) ([]parsedRow, []string, []string) {
	t.Helper()
	rows, ignored, errs, err := parseCSV(strings.NewReader(body))
	if err != nil {
		t.Fatalf("parseCSV: %v", err)
	}
	return rows, ignored, errs
}

func TestParseSkipsBannerAndBlankLines(t *testing.T) {
	rows, _, errs := parseCSVString(t, sheetWithBanner)

	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if len(rows) != 2 {
		t.Fatalf("parsed %d rows, want 2 (the banner and the spacer are not data)", len(rows))
	}

	first := rows[0]
	// 1-based file line, so a person can find the row the message is about.
	if first.sheetRow != 4 {
		t.Errorf("sheetRow = %d, want 4", first.sheetRow)
	}
	if first.values.Client != "Acme Steel" {
		t.Errorf("client = %q, want Acme Steel", first.values.Client)
	}
	if first.values.TotalCameras == nil || *first.values.TotalCameras != 24 {
		t.Errorf("totalCameras = %v, want 24", first.values.TotalCameras)
	}
	if first.values.ImplementationDate == nil {
		t.Fatal("implementationDate was not parsed")
	}
	if got := first.values.ImplementationDate.Format("2006-01-02"); got != "2026-03-14" {
		t.Errorf("implementationDate = %s, want 2026-03-14", got)
	}

	// An empty trailing cell must store NULL, not "".
	if rows[1].values.Notes != nil {
		t.Errorf("notes = %v, want nil for an empty cell", *rows[1].values.Notes)
	}
}

// The tracker is maintained in India: 02/04/2026 is 2 April, not 4 February.
func TestParseReadsAmbiguousDatesDayFirst(t *testing.T) {
	rows, _, _ := parseCSVString(t, sheetWithBanner)

	got := rows[1].values.ImplementationDate
	if got == nil {
		t.Fatal("implementationDate was not parsed")
	}
	if want := "2026-04-02"; got.Format("2006-01-02") != want {
		t.Errorf("02/04/2026 parsed as %s, want %s", got.Format("2006-01-02"), want)
	}
}

func TestParseHeaderVariantsAndIgnoredColumns(t *testing.T) {
	rows, ignored, errs := parseCSVString(t, strings.Join([]string{
		"CUSTOMER,No. of Cameras,Current Stage,Owner,Remarks",
		"Acme Steel,12,Cabling,Someone,All good",
	}, "\n"))

	if len(errs) != 0 {
		t.Fatalf("errors = %v, want none", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("parsed %d rows, want 1", len(rows))
	}
	if rows[0].values.TotalCameras == nil || *rows[0].values.TotalCameras != 12 {
		t.Errorf("totalCameras = %v, want 12", rows[0].values.TotalCameras)
	}
	if rows[0].values.Notes == nil || *rows[0].values.Notes != "All good" {
		t.Errorf("notes = %v, want All good (Remarks is an alias)", rows[0].values.Notes)
	}
	// A dropped column has to be visible, or a sheet quietly loses data.
	if len(ignored) != 1 || ignored[0] != "Owner" {
		t.Errorf("ignoredColumns = %v, want [Owner]", ignored)
	}
}

func TestParseRejectsSheetWithoutClientColumn(t *testing.T) {
	_, _, _, err := parseCSV(strings.NewReader("Status,Notes\nIn progress,x\n"))
	if err != ErrNoClientColumn {
		t.Fatalf("err = %v, want ErrNoClientColumn", err)
	}
}

func TestParseRejectsSheetWithoutHeader(t *testing.T) {
	_, _, _, err := parseCSV(strings.NewReader("just,some,values\n1,2,3\n"))
	if err != ErrNoHeader {
		t.Fatalf("err = %v, want ErrNoHeader", err)
	}
}

// One unreadable cell must cost one row, not the whole upload.
func TestParseReportsBadRowsWithoutFailingTheSheet(t *testing.T) {
	rows, _, errs := parseCSVString(t, strings.Join([]string{
		"Client,Total Cameras",
		"Acme Steel,24",
		"Borax Mills,lots",
		"Cinder Co,8",
	}, "\n"))

	if len(rows) != 2 {
		t.Fatalf("parsed %d rows, want 2 good ones", len(rows))
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	if !strings.Contains(errs[0], "row 3") {
		t.Errorf("error %q should name the offending sheet row", errs[0])
	}
}

func TestParseCameraCountTolerance(t *testing.T) {
	tests := map[string]int{
		"12":        12,
		"12.0":      12, // Excel stores whole numbers as floats
		"1,200":     1200,
		"8 cameras": 8,
		" 40 ":      40,
	}
	for in, want := range tests {
		t.Run(in, func(t *testing.T) {
			got, err := parseCameraCount(in)
			if err != nil {
				t.Fatalf("parseCameraCount(%q): %v", in, err)
			}
			if got != want {
				t.Errorf("parseCameraCount(%q) = %d, want %d", in, got, want)
			}
		})
	}

	if _, err := parseCameraCount("lots"); err == nil {
		t.Error("parseCameraCount(\"lots\") should fail rather than store 0")
	}
}

// An Excel date cell can arrive as a serial number. A bare small integer must
// not be read as one, or "12" in a date column silently becomes January 1900.
func TestParseSheetDateSerial(t *testing.T) {
	got, err := parseSheetDate("46095") // days since 1899-12-30 → 2026-03-14
	if err != nil {
		t.Fatalf("parseSheetDate serial: %v", err)
	}
	if want := "2026-03-14"; got.Format("2006-01-02") != want {
		t.Errorf("serial parsed as %s, want %s", got.Format("2006-01-02"), want)
	}

	if _, err := parseSheetDate("12"); err == nil {
		t.Error("a bare \"12\" should not be read as an Excel serial")
	}
}

func ptr[T any](v T) *T { return &v }

// A blank cell means "no opinion", so an import must never report it as a
// change — otherwise a partial sheet looks like it would wipe every column it
// does not carry.
func TestDiffIgnoresBlankIncomingCells(t *testing.T) {
	existing := Row{
		Client:       "Acme Steel",
		Products:     ptr("VIGIL"),
		Notes:        ptr("Phase 1"),
		TotalCameras: ptr(24),
	}

	if changes := diff(existing, Input{Client: "Acme Steel"}); len(changes) != 0 {
		t.Errorf("changes = %v, want none for a sheet carrying only the client", changes)
	}

	changes := diff(existing, Input{Client: "Acme Steel", TotalCameras: ptr(30)})
	if len(changes) != 1 || changes[0] != "totalCameras" {
		t.Errorf("changes = %v, want [totalCameras]", changes)
	}
}

// The DATE column comes back at midnight UTC and a parsed sheet date is midnight
// local; comparing instants would report every unchanged date as a change.
func TestDiffComparesDatesByCalendarDay(t *testing.T) {
	stored := time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC)
	sameDayElsewhere := time.Date(2026, 3, 14, 0, 0, 0, 0, time.FixedZone("IST", 5*3600+1800))

	existing := Row{Client: "Acme Steel", ImplementationDate: &stored}
	in := Input{Client: "Acme Steel", ImplementationDate: &sameDayElsewhere}

	if changes := diff(existing, in); len(changes) != 0 {
		t.Errorf("changes = %v, want none — it is the same calendar day", changes)
	}
}

func TestValidateRequiresClient(t *testing.T) {
	if _, err := validate(Input{Client: "   "}); err == nil || !IsValidation(err) {
		t.Fatalf("err = %v, want a validation error", err)
	}

	got, err := validate(Input{Client: "  Acme Steel  ", Notes: ptr("   ")})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got.Client != "Acme Steel" {
		t.Errorf("client = %q, want it trimmed", got.Client)
	}
	// A cleared cell stores NULL, not "".
	if got.Notes != nil {
		t.Errorf("notes = %q, want nil for a whitespace-only cell", *got.Notes)
	}
}
