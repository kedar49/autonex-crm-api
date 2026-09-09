package database

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Postgres SQLSTATE codes the CRM stores actually branch on.
//
// These were previously re-declared in nine separate store packages, each with a
// slightly different subset and its own copy of the lookup helper. Keeping them
// here means a store translating a driver error asks a named question
// ("is this a check violation?") instead of matching a bare string.
//
// Codes are from the Postgres appendix of error codes; the names follow it.
const (
	// CodeInvalidTextRepresentation (22P02) — a value could not be parsed into
	// its column type, e.g. "abc" passed where a UUID is expected. Because every
	// id in this API arrives as a string from a URL path, this is how a
	// malformed id surfaces, and it means "not found", not "server broken".
	CodeInvalidTextRepresentation = "22P02"

	// CodeForeignKeyViolation (23503) — a referenced row does not exist. In an
	// org-scoped schema this usually means the caller referenced a record
	// belonging to someone else, so it maps to a 400, never a 500.
	CodeForeignKeyViolation = "23503"

	// CodeUniqueViolation (23505) — a unique index rejected the write.
	CodeUniqueViolation = "23505"

	// CodeCheckViolation (23514) — a CHECK constraint rejected the value, e.g. a
	// stage or status outside the allowed set.
	CodeCheckViolation = "23514"
)

// IsCode reports whether err is a Postgres error carrying the given SQLSTATE.
//
// Prefer the named predicates below at call sites; this is the escape hatch for
// a code that only one store cares about.
func IsCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}

// IsInvalidTextRepr reports whether err is a 22P02 — typically a malformed id.
func IsInvalidTextRepr(err error) bool {
	return IsCode(err, CodeInvalidTextRepresentation)
}

// IsForeignKeyViolation reports whether err is a 23503.
func IsForeignKeyViolation(err error) bool {
	return IsCode(err, CodeForeignKeyViolation)
}

// IsUniqueViolation reports whether err is a 23505, from any unique index.
func IsUniqueViolation(err error) bool {
	return IsCode(err, CodeUniqueViolation)
}

// IsCheckViolation reports whether err is a 23514.
func IsCheckViolation(err error) bool {
	return IsCode(err, CodeCheckViolation)
}

// IsUniqueViolationOn reports whether err is a 23505 raised by one *named*
// index or constraint.
//
// A table with two unique indexes returns the same code for both, so a store
// that wants to say "another row already tracks that client" — rather than a
// generic conflict — has to check which constraint fired. Matching on
// ConstraintName is the only way the driver exposes that.
func IsUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) &&
		pgErr.Code == CodeUniqueViolation &&
		pgErr.ConstraintName == constraint
}
