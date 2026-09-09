package database

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestPredicatesMatchTheirOwnCode(t *testing.T) {
	cases := []struct {
		name string
		code string
		pred func(error) bool
	}{
		{"invalid text repr", CodeInvalidTextRepresentation, IsInvalidTextRepr},
		{"foreign key", CodeForeignKeyViolation, IsForeignKeyViolation},
		{"unique", CodeUniqueViolation, IsUniqueViolation},
		{"check", CodeCheckViolation, IsCheckViolation},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !tc.pred(&pgconn.PgError{Code: tc.code}) {
				t.Errorf("predicate rejected its own code %s", tc.code)
			}
			// Every other code must not match, or a store would translate a
			// check violation into "not found" and hide a real constraint bug.
			for _, other := range cases {
				if other.code == tc.code {
					continue
				}
				if tc.pred(&pgconn.PgError{Code: other.code}) {
					t.Errorf("predicate for %s also matched %s", tc.code, other.code)
				}
			}
		})
	}
}

func TestPredicatesUnwrap(t *testing.T) {
	// Stores wrap driver errors before the translate step sees them.
	wrapped := fmt.Errorf("update lead: %w", &pgconn.PgError{Code: CodeCheckViolation})
	if !IsCheckViolation(wrapped) {
		t.Error("IsCheckViolation did not unwrap a wrapped PgError")
	}
}

func TestPredicatesIgnoreNonPgErrors(t *testing.T) {
	for _, err := range []error{nil, errors.New("boom")} {
		if IsCode(err, CodeUniqueViolation) {
			t.Errorf("IsCode matched non-Postgres error %v", err)
		}
	}
}

func TestIsUniqueViolationOnRequiresBothCodeAndConstraint(t *testing.T) {
	const idx = "delivery_tracker_client_idx"

	if !IsUniqueViolationOn(
		&pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: idx}, idx) {
		t.Error("did not match the constraint that fired")
	}
	// A different unique index on the same table returns the same code, so
	// matching on code alone would produce the wrong error message.
	if IsUniqueViolationOn(
		&pgconn.PgError{Code: CodeUniqueViolation, ConstraintName: "other_idx"}, idx) {
		t.Error("matched a different constraint")
	}
	if IsUniqueViolationOn(
		&pgconn.PgError{Code: CodeCheckViolation, ConstraintName: idx}, idx) {
		t.Error("matched the right constraint with the wrong code")
	}
}
