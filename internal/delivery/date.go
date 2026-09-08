package delivery

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// wireDateLayout is the only format a date crosses the wire in.
const wireDateLayout = "2006-01-02"

// Date is a calendar date: no time, no zone.
//
// implementation_date is a DATE column and the UI is an <input type="date">, and
// both of those speak "2026-03-14". Modelling it as a time.Time forced the wire
// format to be a full RFC 3339 timestamp, which meant inventing a time and a
// zone for a value that has neither — and any zone west of UTC turns midnight
// into the previous day by the time Postgres casts it back to a DATE. That is an
// off-by-one that only appears for some deployments, in some months, which is
// the worst kind.
//
// The zero value is "no date"; callers hold a *Date and use nil for that.
type Date struct{ time.Time }

// NewDate wraps a parsed time as a calendar date, discarding its clock and zone.
func NewDate(t time.Time) Date {
	y, m, d := t.Date()
	return Date{time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
}

// MarshalJSON writes "2026-03-14".
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Format(wireDateLayout))
}

// UnmarshalJSON reads "2026-03-14".
//
// A full RFC 3339 timestamp is also accepted and truncated: an older client, or
// a value pasted out of an export, should not be rejected over a time nobody
// asked it to carry.
func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("invalid date: use YYYY-MM-DD")
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil // an empty string clears the cell, same as null
	}

	if t, err := time.Parse(wireDateLayout, raw); err == nil {
		*d = NewDate(t)
		return nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		*d = NewDate(t)
		return nil
	}
	return fmt.Errorf("invalid date %q: use YYYY-MM-DD", raw)
}

// timePtr converts to the *time.Time the pgx driver binds as a DATE. Midnight
// UTC, so the cast cannot land on a neighbouring day.
func (d *Date) timePtr() *time.Time {
	if d == nil {
		return nil
	}
	t := d.Time
	return &t
}

// dateFromPtr wraps a scanned DATE, preserving NULL as nil.
func dateFromPtr(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	d := NewDate(*t)
	return &d
}

// sameDate reports whether two optional dates name the same day. Nil is only
// equal to nil.
func sameDate(a, b *Date) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}
