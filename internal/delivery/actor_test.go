package delivery

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeRow returns a canned Scan result.
type fakeRow struct {
	id  string
	err error
}

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	if p, ok := dest[0].(*string); ok {
		*p = r.id
	}
	return nil
}

// fakeQuerier answers the one lookup knownActor makes.
type fakeQuerier struct {
	row     fakeRow
	queried bool
}

func (q *fakeQuerier) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}

func (q *fakeQuerier) QueryRow(context.Context, string, ...any) pgx.Row {
	q.queried = true
	return q.row
}

// An import runs in one transaction, so a foreign key violation on created_by
// used to take the whole sheet down with an opaque 500. An unknown actor has to
// degrade to "no recorded author" instead — the column is nullable and its FK is
// ON DELETE SET NULL, so that state already exists in this table.
func TestKnownActorReturnsNullForAUserWithNoRow(t *testing.T) {
	q := &fakeQuerier{row: fakeRow{err: pgx.ErrNoRows}}

	actor, err := knownActor(context.Background(), q, "35e5d4b1-d4c1-425b-964e-3599bb093612")
	if err != nil {
		t.Fatalf("knownActor returned an error for an absent user: %v", err)
	}
	if actor != nil {
		t.Errorf("actor = %v, want nil so the insert records no author", *actor)
	}
}

func TestKnownActorPassesThroughARealUser(t *testing.T) {
	const id = "6f9af07f-085f-4300-82d7-006c287853a5"
	q := &fakeQuerier{row: fakeRow{id: id}}

	actor, err := knownActor(context.Background(), q, id)
	if err != nil {
		t.Fatalf("knownActor: %v", err)
	}
	if actor == nil || *actor != id {
		t.Errorf("actor = %v, want %q preserved", actor, id)
	}
}

// No id means no lookup: an unauthenticated background caller should not cost a
// query to learn what it already said.
func TestKnownActorSkipsTheLookupForAnEmptyID(t *testing.T) {
	q := &fakeQuerier{}

	actor, err := knownActor(context.Background(), q, "")
	if err != nil || actor != nil {
		t.Errorf("got (%v, %v), want (nil, nil)", actor, err)
	}
	if q.queried {
		t.Error("an empty actor id still hit the database")
	}
}

// A real failure must still surface — silently dropping it would hide an outage
// behind a missing author name.
func TestKnownActorPropagatesRealErrors(t *testing.T) {
	boom := errors.New("connection reset")
	q := &fakeQuerier{row: fakeRow{err: boom}}

	if _, err := knownActor(context.Background(), q, "some-id"); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the underlying failure", err)
	}
}
