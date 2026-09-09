// Package paging holds the offset-pagination rules the list endpoints share.
//
// Reading the query string and bounding the result belong together but happen
// at different layers: a handler parses what the client asked for, and the
// service that owns the table decides what it is willing to serve. Both halves
// live here so the two cannot drift — a handler that stopped clamping, or a
// service whose cap no longer matched the documented one, would only show up as
// a slow query in production.
package paging

import (
	"net/http"
	"strconv"
)

// Params reads `limit` and `offset` from the query string.
//
// Both fall back to 0 on absent or unparseable input rather than erroring,
// because 0 is meaningful on the way through: Clamp reads it as "no opinion"
// and substitutes the owning service's default. That makes `?limit=` and a
// missing limit behave identically, which is what a client building URLs from
// optional filters expects.
func Params(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	offset, _ = strconv.Atoi(q.Get("offset"))
	return limit, offset
}

// Clamp bounds a requested page against the caller's own defaults.
//
// def and max are parameters rather than package constants because the ceiling
// is a per-table decision: an activity feed serves wider pages than a list of
// companies. Passing them in keeps that choice next to the table it describes.
//
// A limit at or below zero becomes def, anything above max becomes max, and a
// negative offset becomes 0. The cap is the load-bearing part — without it
// `?limit=1000000` is an unbounded query wearing a query parameter.
func Clamp(limit, offset, def, max int) (int, int) {
	if limit <= 0 {
		limit = def
	}
	if limit > max {
		limit = max
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
