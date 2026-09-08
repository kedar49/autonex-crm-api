package delivery

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
)

// maxImportRows caps one upload. Well past any real tracker, low enough that a
// pathological sheet cannot be turned into an out-of-memory.
const maxImportRows = 5000

// Import actions, as reported by a preview.
const (
	// ActionCreate: no row for this client yet.
	ActionCreate = "create"
	// ActionUpdate: a row exists and the sheet changes at least one cell.
	ActionUpdate = "update"
	// ActionUnchanged: a row exists and the sheet says exactly the same thing.
	ActionUnchanged = "unchanged"
)

// PreviewRow is one parsed sheet line and what committing it would do.
type PreviewRow struct {
	// SheetRow is the 1-based row number in the uploaded file, so a person can
	// find the line the message is about.
	SheetRow int    `json:"sheetRow"`
	Action   string `json:"action"`
	Values   Input  `json:"values"`
	// ExistingID is set for update/unchanged: the row that would be overwritten.
	ExistingID *string `json:"existingId"`
	// Changes names the fields an update would alter, for the preview's diff.
	Changes []string `json:"changes"`
}

// Preview is the answer to an upload: what was understood, and what was not.
type Preview struct {
	Rows []PreviewRow `json:"rows"`
	// Errors are per-line problems (a missing client, an unreadable number).
	// Reported rather than fatal, so one bad line does not reject the sheet.
	Errors []string `json:"errors"`
	// Columns the file had that the tracker has no home for. Surfaced because a
	// silently dropped column is how a sheet quietly loses data.
	IgnoredColumns []string `json:"ignoredColumns"`
	Created        int      `json:"created"`
	Updated        int      `json:"updated"`
	Unchanged      int      `json:"unchanged"`
}

// CommitResult reports what an applied import actually did.
type CommitResult struct {
	Created int `json:"created"`
	Updated int `json:"updated"`
}

// headerAliases maps the spellings a real sheet uses to tracker fields. Keys are
// normalized (lower-cased, punctuation and spaces stripped) before lookup, so
// "Key Location(s)", "key locations" and "Key_Locations" all land in one place.
var headerAliases = map[string]string{
	"client":             "client",
	"clientname":         "client",
	"customer":           "client",
	"account":            "client",
	"product":            "products",
	"products":           "products",
	"producttype":        "products",
	"keylocation":        "locations",
	"keylocations":       "locations",
	"location":           "locations",
	"locations":          "locations",
	"site":               "locations",
	"sites":              "locations",
	"totalcameras":       "totalCameras",
	"cameras":            "totalCameras",
	"cameracount":        "totalCameras",
	"noofcameras":        "totalCameras",
	"status":             "status",
	"implementationdate": "implementationDate",
	"implementation":     "implementationDate",
	"golivedate":         "implementationDate",
	"installdate":        "implementationDate",
	"date":               "implementationDate",
	"currentstage":       "currentStages",
	"currentstages":      "currentStages",
	"stage":              "currentStages",
	"stages":             "currentStages",
	"keycontact":         "keyContacts",
	"keycontacts":        "keyContacts",
	"contact":            "keyContacts",
	"contacts":           "keyContacts",
	"nextstep":           "nextSteps",
	"nextsteps":          "nextSteps",
	"nextaction":         "nextSteps",
	"note":               "notes",
	"notes":              "notes",
	"remark":             "notes",
	"remarks":            "notes",
}

// dateLayouts are the spellings a date arrives in when the sheet stored it as
// text. Day-first before month-first: this tracker is maintained in India, where
// 03/04 means 3 April.
var dateLayouts = []string{
	"2006-01-02",
	"02/01/2006",
	"02-01-2006",
	"2/1/2006",
	"01/02/2006",
	"02 Jan 2006",
	"2 Jan 2006",
	"Jan 2, 2006",
	"02-Jan-2006",
	"2006/01/02",
}

// ErrNoHeader means the file had no recognizable header row.
var ErrNoHeader = errors.New("no header row found")

// ErrNoClientColumn means the sheet has no column naming the client, so nothing
// in it can be filed.
var ErrNoClientColumn = errors.New("no client column found")

// Preview parses an uploaded sheet and reports what committing it would do,
// without writing anything.
//
// Parsing happens here rather than in the browser for the ordinary reason: the
// server has to validate the rows before storing them anyway, so letting the
// client parse would mean writing the same logic twice and trusting the copy we
// do not control.
func (s *Service) Preview(ctx context.Context, orgID, filename string, r io.Reader) (Preview, error) {
	parsed, ignored, errs, err := parseSheet(filename, r)
	if err != nil {
		return Preview{}, err
	}

	existing, err := s.store.list(ctx, orgID, maxRows)
	if err != nil {
		return Preview{}, err
	}
	byClient := make(map[string]Row, len(existing))
	for _, row := range existing {
		byClient[clientKey(row.Client)] = row
	}

	out := Preview{
		Rows:           make([]PreviewRow, 0, len(parsed)),
		Errors:         errs,
		IgnoredColumns: ignored,
	}
	// A sheet listing the same client twice would otherwise preview as two
	// creates and commit as one create plus one update. Fold them: the later
	// line wins, which is how a person reading top-to-bottom would resolve it.
	seen := make(map[string]int, len(parsed))

	for _, p := range parsed {
		key := clientKey(p.values.Client)
		row := PreviewRow{SheetRow: p.sheetRow, Values: p.values, Action: ActionCreate}

		if prior, ok := existing2(byClient, key); ok {
			row.ExistingID = &prior.ID
			row.Changes = diff(prior, p.values)
			if len(row.Changes) == 0 {
				row.Action = ActionUnchanged
			} else {
				row.Action = ActionUpdate
			}
		}

		if at, dup := seen[key]; dup {
			out.Rows[at] = row
			out.Rows[at].SheetRow = p.sheetRow
			continue
		}
		seen[key] = len(out.Rows)
		out.Rows = append(out.Rows, row)
	}

	for _, row := range out.Rows {
		switch row.Action {
		case ActionCreate:
			out.Created++
		case ActionUpdate:
			out.Updated++
		default:
			out.Unchanged++
		}
	}
	return out, nil
}

// Commit applies rows the user has confirmed, upserting on the client name.
//
// It takes the rows rather than the file again: the preview is the contract, so
// what gets written is exactly what was shown, even if someone edits a cell in
// the preview before accepting it.
func (s *Service) Commit(ctx context.Context, orgID, userID string, rows []Input) (CommitResult, error) {
	if len(rows) == 0 {
		return CommitResult{}, invalid("nothing to import")
	}
	if len(rows) > maxImportRows {
		return CommitResult{}, invalid("that import is too large (%d rows, limit %d)", len(rows), maxImportRows)
	}

	clean := make([]Input, 0, len(rows))
	for i, in := range rows {
		v, err := validate(in)
		if err != nil {
			return CommitResult{}, invalid("row %d: %s", i+1, err.Error())
		}
		clean = append(clean, v)
	}
	return s.store.upsertMany(ctx, orgID, userID, clean)
}

// parsedRow is a sheet line plus where it came from.
type parsedRow struct {
	sheetRow int
	values   Input
}

// parseSheet dispatches on the file extension. CSV is accepted alongside xlsx
// because exporting one is the fastest way out of Google Sheets.
func parseSheet(filename string, r io.Reader) ([]parsedRow, []string, []string, error) {
	if strings.EqualFold(strings.TrimPrefix(fileExt(filename), "."), "csv") {
		return parseCSV(r)
	}
	return parseXLSX(r)
}

func fileExt(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i:]
	}
	return ""
}

func parseXLSX(r io.Reader) ([]parsedRow, []string, []string, error) {
	f, err := excelize.OpenReader(r)
	if err != nil {
		return nil, nil, nil, invalid("that file could not be read as a spreadsheet")
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, nil, nil, invalid("that workbook has no sheets")
	}
	// The first sheet only: the tracker's master tab is the first one, and
	// silently merging per-client tabs behind it would be a guess.
	grid, err := f.GetRows(sheets[0])
	if err != nil {
		return nil, nil, nil, invalid("sheet %q could not be read", sheets[0])
	}
	return rowsFromGrid(grid)
}

func parseCSV(r io.Reader) ([]parsedRow, []string, []string, error) {
	reader := csv.NewReader(r)
	// Exported sheets have ragged trailing columns; let the mapper decide what is
	// missing rather than failing the whole file on a short line.
	reader.FieldsPerRecord = -1

	grid, err := reader.ReadAll()
	if err != nil {
		return nil, nil, nil, invalid("that file could not be read as CSV")
	}
	return rowsFromGrid(grid)
}

// rowsFromGrid turns a raw cell grid into tracker rows.
//
// The header is searched for rather than assumed to be line 1: the real sheet
// opens with a title ("Client Delivery Tracker") and a subtitle above the
// headings, and asking people to delete those before uploading is how an import
// feature goes unused.
func rowsFromGrid(grid [][]string) ([]parsedRow, []string, []string, error) {
	headerAt, mapping, ignored := findHeader(grid)
	if headerAt < 0 {
		return nil, nil, nil, ErrNoHeader
	}
	if !hasField(mapping, "client") {
		return nil, nil, nil, ErrNoClientColumn
	}

	var (
		rows []parsedRow
		errs []string
	)
	for i := headerAt + 1; i < len(grid); i++ {
		if len(rows) >= maxImportRows {
			errs = append(errs, "stopped after "+strconv.Itoa(maxImportRows)+" rows; the rest of the sheet was not read")
			break
		}
		cells := grid[i]
		if allBlank(cells) {
			continue // spacer line
		}

		in, err := rowFromCells(cells, mapping)
		if err != nil {
			errs = append(errs, "row "+strconv.Itoa(i+1)+": "+err.Error())
			continue
		}
		rows = append(rows, parsedRow{sheetRow: i + 1, values: in})
	}
	return rows, ignored, errs, nil
}

// findHeader returns the index of the header line, the column→field mapping, and
// the headings that matched nothing. The header is the first line that names at
// least two known fields — one match is more likely a stray label than a table
// heading.
func findHeader(grid [][]string) (int, map[int]string, []string) {
	limit := min(len(grid), 25) // the heading is near the top or it is not a table

	for i := range limit {
		mapping := make(map[int]string)
		var ignored []string
		for col, cell := range grid[i] {
			label := strings.TrimSpace(cell)
			if label == "" {
				continue
			}
			if field, ok := headerAliases[normalizeHeader(label)]; ok {
				// First column wins, so a duplicate heading cannot silently
				// shadow the one people filled in.
				if _, taken := fieldColumn(mapping, field); !taken {
					mapping[col] = field
				}
				continue
			}
			ignored = append(ignored, label)
		}
		if len(mapping) >= 2 {
			return i, mapping, ignored
		}
	}
	return -1, nil, nil
}

// normalizeHeader reduces a heading to letters and digits, so punctuation,
// case and spacing stop mattering: "Total Cameras", "total_cameras" and
// "Total Cameras:" all normalize alike.
func normalizeHeader(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func rowFromCells(cells []string, mapping map[int]string) (Input, error) {
	var in Input
	for col, field := range mapping {
		if col >= len(cells) {
			continue
		}
		value := strings.TrimSpace(cells[col])
		if value == "" {
			continue
		}

		switch field {
		case "client":
			in.Client = value
		case "products":
			in.Products = &value
		case "locations":
			in.Locations = &value
		case "status":
			in.Status = &value
		case "currentStages":
			in.CurrentStages = &value
		case "keyContacts":
			in.KeyContacts = &value
		case "nextSteps":
			in.NextSteps = &value
		case "notes":
			in.Notes = &value
		case "totalCameras":
			n, err := parseCameraCount(value)
			if err != nil {
				return Input{}, err
			}
			in.TotalCameras = &n
		case "implementationDate":
			d, err := parseSheetDate(value)
			if err != nil {
				return Input{}, err
			}
			parsed := NewDate(d)
			in.ImplementationDate = &parsed
		}
	}
	if strings.TrimSpace(in.Client) == "" {
		return Input{}, errors.New("no client name")
	}
	return in, nil
}

// parseCameraCount reads a count that may have arrived as "12", "12.0" (Excel
// stores integers as floats) or "12 cameras".
func parseCameraCount(v string) (int, error) {
	cleaned := strings.Map(func(r rune) rune {
		if (r >= '0' && r <= '9') || r == '.' || r == '-' {
			return r
		}
		return -1
	}, strings.ReplaceAll(v, ",", ""))

	if cleaned == "" {
		return 0, errors.New("total cameras is not a number: " + v)
	}
	f, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, errors.New("total cameras is not a number: " + v)
	}
	if f < 0 {
		return 0, errors.New("total cameras cannot be negative: " + v)
	}
	return int(f), nil
}

// parseSheetDate handles both spellings a date arrives in: text in one of the
// common layouts, or the serial number Excel stores when the cell is formatted
// as a date.
func parseSheetDate(v string) (time.Time, error) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t, nil
		}
	}
	// An Excel serial: days since 1899-12-30. Bounded to a plausible range so a
	// stray "12" in a date column reads as an error, not as January 1900.
	if serial, err := strconv.ParseFloat(v, 64); err == nil && serial > 20000 && serial < 80000 {
		if t, err := excelize.ExcelDateToTime(serial, false); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("could not read the date: " + v)
}

// diff names the fields an import would change on an existing row, so the
// preview can show what is actually at stake instead of "1 row will change".
//
// A blank cell in the sheet is "no opinion", not "clear this": a tracker is
// filled in column by column, and a partial upload must not wipe the columns it
// does not carry. Clearing a cell stays a deliberate edit in the table.
func diff(existing Row, in Input) []string {
	var changes []string
	add := func(field string, cond bool) {
		if cond {
			changes = append(changes, field)
		}
	}

	add("client", strings.TrimSpace(existing.Client) != strings.TrimSpace(in.Client))
	add("products", changedText(existing.Products, in.Products))
	add("locations", changedText(existing.Locations, in.Locations))
	add("status", changedText(existing.Status, in.Status))
	add("currentStages", changedText(existing.CurrentStages, in.CurrentStages))
	add("keyContacts", changedText(existing.KeyContacts, in.KeyContacts))
	add("nextSteps", changedText(existing.NextSteps, in.NextSteps))
	add("notes", changedText(existing.Notes, in.Notes))
	add("totalCameras", in.TotalCameras != nil &&
		(existing.TotalCameras == nil || *existing.TotalCameras != *in.TotalCameras))
	add("implementationDate", in.ImplementationDate != nil &&
		!sameDate(existing.ImplementationDate, in.ImplementationDate))

	return changes
}

func changedText(existing, incoming *string) bool {
	if incoming == nil {
		return false // blank cell: no opinion
	}
	if existing == nil {
		return true
	}
	return strings.TrimSpace(*existing) != strings.TrimSpace(*incoming)
}

// clientKey is the identity an import upserts on, matching the unique index:
// case- and whitespace-insensitive.
func clientKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func existing2(m map[string]Row, key string) (Row, bool) {
	r, ok := m[key]
	return r, ok
}

func hasField(mapping map[int]string, field string) bool {
	_, ok := fieldColumn(mapping, field)
	return ok
}

func fieldColumn(mapping map[int]string, field string) (int, bool) {
	for col, f := range mapping {
		if f == field {
			return col, true
		}
	}
	return 0, false
}

func allBlank(cells []string) bool {
	for _, c := range cells {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}
