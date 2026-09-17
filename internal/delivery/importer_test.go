package delivery

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-crm/services/pkg/apperr"
	"github.com/jackc/pgx/v5/pgxpool"
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

// An unreadable cell costs that cell, not the row it is in and not the upload.
//
// Borax Mills is a client whether or not anyone typed a number in its camera
// column. Dropping the line used to take the client, the location and the
// contact with it, over one word.
func TestParseReportsBadCellsWithoutLosingTheRow(t *testing.T) {
	rows, _, errs := parseCSVString(t, strings.Join([]string{
		"Client,Total Cameras",
		"Acme Steel,24",
		"Borax Mills,lots",
		"Cinder Co,8",
	}, "\n"))

	if len(rows) != 3 {
		t.Fatalf("parsed %d rows, want all 3", len(rows))
	}
	if rows[1].values.Client != "Borax Mills" {
		t.Errorf("row 2 client = %q, want the row kept", rows[1].values.Client)
	}
	if rows[1].values.TotalCameras != nil {
		t.Errorf("camera count = %v, want it left blank", *rows[1].values.TotalCameras)
	}
	if len(errs) != 1 {
		t.Fatalf("errors = %v, want exactly one", errs)
	}
	if !strings.Contains(errs[0], "row 3") {
		t.Errorf("error %q should name the offending sheet row", errs[0])
	}
}

// The date spellings a hand-maintained operations sheet actually contains.
// Every one of these used to lose its entire row.
func TestParseSheetDateTolerance(t *testing.T) {
	want := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	for _, v := range []string{
		"2026-09-17", "2026/09/17", "17/09/2026", "17-09-2026", "17/9/2026",
		"17 Sep 2026", "17 Sept 2026", "17 September 2026", "Sept 17, 2026",
		"17-Sep-26", "17-Sep-2026", "17.09.2026", "17/09/26", "17/9/26",
		"17th September 2026", "Wed 17 Sep 2026", "17 sep 2026",
		"2026-09-17T00:00:00Z", "09/17/2026",
	} {
		got, err := parseSheetDate(v)
		if err != nil {
			t.Errorf("parseSheetDate(%q) failed: %v", v, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseSheetDate(%q) = %s, want %s", v, got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
	}
}

// A month with no day is a real commitment to that month, so it lands on the
// first of it rather than being thrown away.
func TestParseSheetDateMonthOnly(t *testing.T) {
	want := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, v := range []string{"Sep 2026", "Sept 2026", "September 2026", "Sep-26", "2026-09"} {
		got, err := parseSheetDate(v)
		if err != nil {
			t.Errorf("parseSheetDate(%q) failed: %v", v, err)
			continue
		}
		if !got.Equal(want) {
			t.Errorf("parseSheetDate(%q) = %s, want %s", v, got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
	}
}

// Still an error: a date column is not a notes column, and guessing at these
// would put a fabricated date in front of someone planning an installation.
func TestParseSheetDateRejectsNonDates(t *testing.T) {
	for _, v := range []string{"TBD", "next quarter", "asap", "12", "", "-"} {
		if got, err := parseSheetDate(v); err == nil {
			t.Errorf("parseSheetDate(%q) = %s, want an error", v, got)
		}
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

// Dates are compared by calendar day. A sheet value parsed in one zone and a
// stored value read back in another are the same date, and reporting that as a
// change would make every re-import look like it rewrites every row.
func TestDiffComparesDatesByCalendarDay(t *testing.T) {
	stored := NewDate(time.Date(2026, 3, 14, 0, 0, 0, 0, time.UTC))
	sameDayElsewhere := NewDate(
		time.Date(2026, 3, 14, 22, 15, 0, 0, time.FixedZone("IST", 5*3600+1800)))

	existing := Row{Client: "Acme Steel", ImplementationDate: &stored}
	in := Input{Client: "Acme Steel", ImplementationDate: &sameDayElsewhere}

	if changes := diff(existing, in); len(changes) != 0 {
		t.Errorf("changes = %v, want none — it is the same calendar day", changes)
	}

	moved := NewDate(time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC))
	changes := diff(existing, Input{Client: "Acme Steel", ImplementationDate: &moved})
	if len(changes) != 1 || changes[0] != "implementationDate" {
		t.Errorf("changes = %v, want [implementationDate]", changes)
	}
}

func TestValidateRequiresClient(t *testing.T) {
	if _, err := validate(Input{Client: "   "}); err == nil || !apperr.IsValidation(err) {
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

// The regression this type exists for: the browser's date input sends
// "2026-09-08", and a *time.Time field rejected it with "expected an RFC 3339
// timestamp" — so the date column could not be edited at all.
func TestInputAcceptsBareDateFromDateInput(t *testing.T) {
	var in Input
	body := `{"client":"Acme Steel","products":null,"locations":null,"totalCameras":null,` +
		`"status":null,"implementationDate":"2026-09-08","currentStages":null,` +
		`"keyContacts":null,"nextSteps":null,"notes":null}`

	if err := json.Unmarshal([]byte(body), &in); err != nil {
		t.Fatalf("a bare YYYY-MM-DD must decode: %v", err)
	}
	if in.ImplementationDate == nil {
		t.Fatal("implementationDate was dropped")
	}
	if got := in.ImplementationDate.Format("2006-01-02"); got != "2026-09-08" {
		t.Errorf("date = %s, want 2026-09-08", got)
	}
}

func TestDateJSONRoundTrip(t *testing.T) {
	tests := map[string]string{
		"bare date":      `"2026-09-08"`,
		"rfc3339":        `"2026-09-08T00:00:00Z"`,
		"rfc3339 offset": `"2026-09-08T09:30:00+05:30"`,
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			var d Date
			if err := json.Unmarshal([]byte(raw), &d); err != nil {
				t.Fatalf("unmarshal %s: %v", raw, err)
			}
			// Whatever came in, a date goes out — no time, no zone.
			out, err := json.Marshal(d)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(out) != `"2026-09-08"` {
				t.Errorf("marshalled %s, want \"2026-09-08\"", out)
			}
		})
	}
}

// An empty string is how a cleared date input reports itself; it must mean NULL,
// not a parse error the user cannot act on.
func TestDateRejectsGarbageAndAcceptsEmpty(t *testing.T) {
	var empty Input
	if err := json.Unmarshal([]byte(`{"client":"x","implementationDate":""}`), &empty); err != nil {
		t.Fatalf("an empty date must clear the cell, not fail: %v", err)
	}

	var bad Date
	if err := json.Unmarshal([]byte(`"not-a-date"`), &bad); err == nil {
		t.Error("garbage should be rejected with a message naming the format")
	}
}

// Midnight UTC, so Postgres casting the bound value to DATE cannot land on the
// day before in a westward session zone.
func TestDateNormalizesToMidnightUTC(t *testing.T) {
	d := NewDate(time.Date(2026, 9, 8, 23, 45, 0, 0, time.FixedZone("IST", 5*3600+1800)))

	if h, m, s := d.Clock(); h != 0 || m != 0 || s != 0 {
		t.Errorf("clock = %02d:%02d:%02d, want midnight", h, m, s)
	}
	if d.Location() != time.UTC {
		t.Errorf("location = %v, want UTC", d.Location())
	}
	if got := d.Format("2006-01-02"); got != "2026-09-08" {
		t.Errorf("date = %s, want 2026-09-08 (the calendar day, not a shifted one)", got)
	}
}

// Once rows link to deals, a client can have several — so which one an import
// writes to has to be decided, and decided the same way in the preview and the
// commit. Unlinked wins; otherwise the oldest.
func TestPreferRowPicksUnlinkedThenOldest(t *testing.T) {
	old := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	dealID := "d1"

	unlinkedRecent := Row{CreatedAt: recent}
	linkedOld := Row{CreatedAt: old, DealID: &dealID}

	if !preferRow(unlinkedRecent, linkedOld) {
		t.Error("an unlinked row should win even when a linked row is older")
	}
	if preferRow(linkedOld, unlinkedRecent) {
		t.Error("a linked row should not displace an unlinked one")
	}

	linkedRecent := Row{CreatedAt: recent, DealID: &dealID}
	if !preferRow(linkedOld, linkedRecent) {
		t.Error("between two linked rows the oldest should win")
	}

	if preferRow(Row{CreatedAt: recent}, Row{CreatedAt: old}) {
		t.Error("between two unlinked rows the oldest should win")
	}
}

func TestParseActualAutonexWorkbook(t *testing.T) {
	filePath := "../../../Copy of Autonex Client Tracker .xlsx"
	f, err := os.Open(filePath)
	if err != nil {
		t.Skipf("skipping test; file not found: %v", err)
	}
	defer f.Close()

	rows, _, errs, err := parseSheet("Copy of Autonex Client Tracker .xlsx", f)
	if err != nil {
		t.Fatalf("parseSheet failed: %v", err)
	}
	if len(errs) != 0 {
		t.Errorf("expected 0 errors, got %d: %v", len(errs), errs)
	}

	byClient := make(map[string][]Input)
	for _, r := range rows {
		k := clientKey(r.values.Client)
		byClient[k] = append(byClient[k], r.values)
	}

	expectedClients := []string{
		"mahindra",
		"hikal",
		"hindalco",
		"hindalco downstream",
		"hindalco upstream",
		"thermax jhagadia pp",
		"schneider electric",
		"thermax jhagadia",
		"thermax savli",
		"thermax shirwal",
		"b&b packaging",
		"winpack",
		"birla opus",
		"align",
		"jsw steel mining",
		"spml",
		"miracle group",
		"ultratech cement",
		"jindal steel",
		"lnt",
		"jubilant foodworks",
		"godrej & boyce",
		"bharat forge",
		"aditya birla renewables",
	}

	for _, c := range expectedClients {
		list, found := byClient[c]
		if !found || len(list) == 0 {
			t.Errorf("missing expected client: %s", c)
			continue
		}
		for _, in := range list {
			if in.CurrentStages == nil || *in.CurrentStages == "" {
				t.Errorf("client %s has empty CurrentStages", c)
			}
		}
	}

	if len(byClient["jindal steel"]) != 2 {
		t.Fatalf("expected 2 rows for 'jindal steel', got %d", len(byClient["jindal steel"]))
	}

	// Verify specific values match Excel exactly
	if list, ok := byClient["hindalco"]; ok && len(list) > 0 {
		in := list[0]
		if in.Client != "Hindalco" {
			t.Errorf("hindalco client name = %q, want 'Hindalco'", in.Client)
		}
		if in.TotalCameras == nil || *in.TotalCameras != 15 {
			t.Errorf("hindalco totalCameras = %v, want 15", in.TotalCameras)
		}
		if in.Locations == nil || *in.Locations != "Bhiwandi Warehouse" {
			t.Errorf("hindalco locations = %v, want Bhiwandi Warehouse", in.Locations)
		}
		if in.CurrentStages == nil || *in.CurrentStages != "Quotation Sent" {
			t.Errorf("hindalco stage = %v, want Quotation Sent", in.CurrentStages)
		}
	}

	if list, ok := byClient["schneider electric"]; ok && len(list) > 0 {
		in := list[0]
		if in.TotalCameras == nil || *in.TotalCameras != 4 {
			t.Errorf("schneider electric totalCameras = %v, want 4", in.TotalCameras)
		}
	}

	if list, ok := byClient["thermax savli"]; ok && len(list) > 0 {
		in := list[0]
		if in.CurrentStages == nil || *in.CurrentStages != "Quotation Sent" {
			t.Errorf("thermax savli stage = %v, want Quotation Sent", in.CurrentStages)
		}
	}

	// Jindal Steel has two separate plant rows: Raigarh (100 cams, Quotation Sent) and Raipur (nil cams, Use Case Discussion)
	jindalRows := byClient["jindal steel"]
	var raigarh, raipur *Input
	for i := range jindalRows {
		if jindalRows[i].Locations != nil && *jindalRows[i].Locations == "Raigarh" {
			raigarh = &jindalRows[i]
		}
		if jindalRows[i].Locations != nil && *jindalRows[i].Locations == "Raipur" {
			raipur = &jindalRows[i]
		}
	}

	if raigarh == nil {
		t.Fatal("missing Jindal Steel Raigarh row")
	} else {
		if raigarh.Client != "Jindal Steel" {
			t.Errorf("raigarh client name = %q, want 'Jindal Steel'", raigarh.Client)
		}
		if raigarh.TotalCameras == nil || *raigarh.TotalCameras != 100 {
			t.Errorf("raigarh totalCameras = %v, want 100", raigarh.TotalCameras)
		}
		if raigarh.CurrentStages == nil || *raigarh.CurrentStages != "Quotation Sent" {
			t.Errorf("raigarh stage = %v, want Quotation Sent", raigarh.CurrentStages)
		}
	}

	if raipur == nil {
		t.Fatal("missing Jindal Steel Raipur row")
	} else {
		if raipur.Client != "Jindal Steel" {
			t.Errorf("raipur client name = %q, want 'Jindal Steel'", raipur.Client)
		}
		if raipur.TotalCameras != nil {
			t.Errorf("raipur totalCameras = %v, want nil", raipur.TotalCameras)
		}
		if raipur.CurrentStages == nil || *raipur.CurrentStages != "Use Case Discussion" {
			t.Errorf("raipur stage = %v, want Use Case Discussion", raipur.CurrentStages)
		}
	}
}

func TestCommitActualAutonexWorkbookToDB(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		if envBytes, err := os.ReadFile("../../../.env"); err == nil {
			for _, line := range strings.Split(string(envBytes), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "DATABASE_URL=") {
					dbURL = strings.TrimPrefix(line, "DATABASE_URL=")
					break
				}
			}
		}
	}
	if dbURL == "" {
		t.Skip("DATABASE_URL not set, skipping DB commit test")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("pgxpool.New failed: %v", err)
	}
	defer pool.Close()

	svc := newService(pool)
	filePath := "../../../Copy of Autonex Client Tracker .xlsx"
	f, err := os.Open(filePath)
	if err != nil {
		t.Skipf("skipping test; file not found: %v", err)
	}
	defer f.Close()

	orgID := "acfb1796-b957-41dd-b96c-a1db37cfd688"
	userID := "6f9af07f-085f-4300-82d7-006c287853a5"

	// Ensure migration 000018 is applied
	upSQL, _ := os.ReadFile("../../migrations/000018_tracker_location_unique.up.sql")
	if len(upSQL) > 0 {
		_, _ = pool.Exec(ctx, string(upSQL))
	}
	// Remove any old rows with modified names from previous tests
	_, _ = pool.Exec(ctx, `DELETE FROM delivery_tracker WHERE org_id = $1 AND client LIKE '%(%)%'`, orgID)

	preview, err := svc.Preview(ctx, orgID, "Copy of Autonex Client Tracker .xlsx", f)
	if err != nil {
		t.Fatalf("svc.Preview failed: %v", err)
	}
	if len(preview.Errors) != 0 {
		t.Errorf("preview had errors: %v", preview.Errors)
	}
	t.Logf("Preview result: %d rows (created=%d, updated=%d, unchanged=%d)",
		len(preview.Rows), preview.Created, preview.Updated, preview.Unchanged)

	if len(preview.Rows) != 25 {
		t.Errorf("expected 25 preview rows, got %d", len(preview.Rows))
	}

	rowsToCommit := make([]Input, 0, len(preview.Rows))
	for _, r := range preview.Rows {
		rowsToCommit = append(rowsToCommit, r.Values)
	}

	res, err := svc.Commit(ctx, orgID, userID, rowsToCommit)
	if err != nil {
		t.Fatalf("svc.Commit failed: %v", err)
	}
	t.Logf("Commit result: created=%d, updated=%d", res.Created, res.Updated)

	list, err := svc.List(ctx, orgID)
	if err != nil {
		t.Fatalf("svc.List failed: %v", err)
	}

	byKey := make(map[string]Row)
	for _, item := range list.Items {
		byKey[clientLocationKey(item.Client, item.Locations)] = item
	}

	if h, ok := byKey["hindalco::bhiwandi warehouse"]; !ok {
		t.Error("hindalco missing from delivery_tracker")
	} else {
		if h.Client != "Hindalco" {
			t.Errorf("hindalco client name = %q, want 'Hindalco'", h.Client)
		}
		if h.Locations == nil || *h.Locations != "Bhiwandi Warehouse" {
			t.Errorf("hindalco location = %v, want 'Bhiwandi Warehouse'", h.Locations)
		}
		if h.TotalCameras == nil || *h.TotalCameras != 15 {
			t.Errorf("hindalco total_cameras = %v, want 15", h.TotalCameras)
		}
		if h.CurrentStages == nil || *h.CurrentStages != "Quotation Sent" {
			t.Errorf("hindalco current_stages = %v, want 'Quotation Sent'", h.CurrentStages)
		}
	}

	if se, ok := byKey["schneider electric::bengaluru"]; !ok {
		t.Error("schneider electric missing from delivery_tracker")
	} else {
		if se.Client != "Schneider Electric" {
			t.Errorf("schneider electric client name = %q, want 'Schneider Electric'", se.Client)
		}
		if se.TotalCameras == nil || *se.TotalCameras != 4 {
			t.Errorf("schneider electric total_cameras = %v, want 4", se.TotalCameras)
		}
	}

	if ts, ok := byKey["thermax savli::savli"]; !ok {
		t.Error("thermax savli missing from delivery_tracker")
	} else {
		if ts.CurrentStages == nil || *ts.CurrentStages != "Quotation Sent" {
			t.Errorf("thermax savli current_stages = %v, want 'Quotation Sent'", ts.CurrentStages)
		}
	}

	if bo, ok := byKey["birla opus::mahad plant"]; !ok {
		t.Error("birla opus missing from delivery_tracker")
	} else {
		if bo.Client != "Birla Opus" {
			t.Errorf("birla opus client = %q, want 'Birla Opus'", bo.Client)
		}
		if bo.Locations == nil || *bo.Locations != "Mahad Plant" {
			t.Errorf("birla opus location = %v, want 'Mahad Plant'", bo.Locations)
		}
		if bo.CurrentStages == nil || *bo.CurrentStages != "PoC" {
			t.Errorf("birla opus current_stages = %v, want 'PoC'", bo.CurrentStages)
		}
	}

	if jsR, ok := byKey["jindal steel::raigarh"]; !ok {
		t.Error("jindal steel::raigarh missing from delivery_tracker")
	} else {
		if jsR.Client != "Jindal Steel" {
			t.Errorf("jindal steel (raigarh) client = %q, want 'Jindal Steel'", jsR.Client)
		}
		if jsR.Locations == nil || *jsR.Locations != "Raigarh" {
			t.Errorf("jindal steel (raigarh) location = %v, want 'Raigarh'", jsR.Locations)
		}
		if jsR.TotalCameras == nil || *jsR.TotalCameras != 100 {
			t.Errorf("jindal steel (raigarh) total_cameras = %v, want 100", jsR.TotalCameras)
		}
		if jsR.CurrentStages == nil || *jsR.CurrentStages != "Quotation Sent" {
			t.Errorf("jindal steel (raigarh) current_stages = %v, want 'Quotation Sent'", jsR.CurrentStages)
		}
	}

	if jsP, ok := byKey["jindal steel::raipur"]; !ok {
		t.Error("jindal steel::raipur missing from delivery_tracker")
	} else {
		if jsP.Client != "Jindal Steel" {
			t.Errorf("jindal steel (raipur) client = %q, want 'Jindal Steel'", jsP.Client)
		}
		if jsP.Locations == nil || *jsP.Locations != "Raipur" {
			t.Errorf("jindal steel (raipur) location = %v, want 'Raipur'", jsP.Locations)
		}
		if jsP.CurrentStages == nil || *jsP.CurrentStages != "Use Case Discussion" {
			t.Errorf("jindal steel (raipur) current_stages = %v, want 'Use Case Discussion'", jsP.CurrentStages)
		}
	}

	tabKeys := []string{"jubilant foodworks", "godrej & boyce::khalapur plant (forklift manufacturing)", "bharat forge::pune (heavy press forging - crankshaft/camshaft)", "aditya birla renewables"}
	for _, tk := range tabKeys {
		if _, ok := byKey[tk]; !ok {
			t.Errorf("tab key %s missing from delivery_tracker", tk)
		}
	}
}

func TestParseSalesPersonDeliveryTrackerColumns(t *testing.T) {
	// The exact columns provided by the sales person:
	// #	Client	Deal	Product(s)	Key Location(s)	Total Cameras	Status	Implementation Date	Current Stage(s)	Key Contacts	Next Steps	Notes	Delete row
	sheetContent := strings.Join([]string{
		"#\tClient\tDeal\tProduct(s)\tKey Location(s)\tTotal Cameras\tStatus\tImplementation Date\tCurrent Stage(s)\tKey Contacts\tNext Steps\tNotes\tDelete row",
		"1\tTata Steel\tTata Steel Kalinganagar\tVIGIL AI\tKalinganagar Plant\t50\tActive\t2026-05-15\tDeployment\tRajesh Sharma\tFinal acceptance\tGate 1 cameras\t",
	}, "\n")

	rows, ignored, errs, err := parseSheet("tracker.tsv", strings.NewReader(sheetContent))
	if err != nil {
		t.Fatalf("parseSheet failed: %v", err)
	}
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}

	r := rows[0].values
	if r.Client != "Tata Steel" {
		t.Errorf("Client = %q, want 'Tata Steel'", r.Client)
	}
	if r.DealTitle == nil || *r.DealTitle != "Tata Steel Kalinganagar" {
		t.Errorf("DealTitle = %v, want 'Tata Steel Kalinganagar'", r.DealTitle)
	}
	if r.Products == nil || *r.Products != "VIGIL AI" {
		t.Errorf("Products = %v, want 'VIGIL AI'", r.Products)
	}
	if r.Locations == nil || *r.Locations != "Kalinganagar Plant" {
		t.Errorf("Locations = %v, want 'Kalinganagar Plant'", r.Locations)
	}
	if r.TotalCameras == nil || *r.TotalCameras != 50 {
		t.Errorf("TotalCameras = %v, want 50", r.TotalCameras)
	}
	if r.Status == nil || *r.Status != "Active" {
		t.Errorf("Status = %v, want 'Active'", r.Status)
	}
	if r.ImplementationDate == nil || r.ImplementationDate.Format("2006-01-02") != "2026-05-15" {
		t.Errorf("ImplementationDate = %v, want 2026-05-15", r.ImplementationDate)
	}
	if r.CurrentStages == nil || *r.CurrentStages != "Deployment" {
		t.Errorf("CurrentStages = %v, want 'Deployment'", r.CurrentStages)
	}
	if r.KeyContacts == nil || *r.KeyContacts != "Rajesh Sharma" {
		t.Errorf("KeyContacts = %v, want 'Rajesh Sharma'", r.KeyContacts)
	}
	if r.NextSteps == nil || *r.NextSteps != "Final acceptance" {
		t.Errorf("NextSteps = %v, want 'Final acceptance'", r.NextSteps)
	}
	if r.Notes == nil || *r.Notes != "Gate 1 cameras" {
		t.Errorf("Notes = %v, want 'Gate 1 cameras'", r.Notes)
	}

	// Also verify that "#" and "Delete row" were cleanly treated as ignored columns
	hasHash := false
	hasDelete := false
	for _, col := range ignored {
		if col == "#" {
			hasHash = true
		}
		if strings.EqualFold(col, "Delete row") {
			hasDelete = true
		}
	}
	if !hasHash {
		t.Errorf("expected '#' in ignored columns, got %v", ignored)
	}
	if !hasDelete {
		t.Errorf("expected 'Delete row' in ignored columns, got %v", ignored)
	}
}
