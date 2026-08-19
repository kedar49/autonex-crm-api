package followups

import (
	"context"
	"time"

	"github.com/go-crm/services/internal/followups/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	q    *followupsdb.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
		q:    followupsdb.New(pool),
	}
}

func toPGUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func (s *Store) CreateFollowUp(ctx context.Context, orgID uuid.UUID, input CreateFollowUpInput) (followupsdb.FollowUp, error) {
	var assigned, lead, deal pgtype.UUID
	if input.AssignedTo != nil {
		assigned = toPGUUID(*input.AssignedTo)
	}
	if input.LeadID != nil {
		lead = toPGUUID(*input.LeadID)
	}
	if input.DealID != nil {
		deal = toPGUUID(*input.DealID)
	}

	return s.q.CreateFollowUp(ctx, followupsdb.CreateFollowUpParams{
		OrgID:      toPGUUID(orgID),
		AssignedTo: assigned,
		Title:      input.Title,
		DueAt:      pgtype.Timestamptz{Time: input.DueAt, Valid: true},
		LeadID:     lead,
		DealID:     deal,
	})
}

func (s *Store) ListFollowUps(ctx context.Context, orgID uuid.UUID, assignedTo *uuid.UUID, completed *bool) ([]followupsdb.FollowUp, error) {
	var assigned pgtype.UUID
	if assignedTo != nil {
		assigned = toPGUUID(*assignedTo)
	}

	return s.q.ListFollowUps(ctx, followupsdb.ListFollowUpsParams{
		OrgID:      toPGUUID(orgID),
		AssignedTo: assigned,
		Completed:  completed,
	})
}

func (s *Store) CompleteFollowUp(ctx context.Context, orgID, id uuid.UUID) (followupsdb.FollowUp, error) {
	return s.q.MarkFollowUpCompleted(ctx, followupsdb.MarkFollowUpCompletedParams{
		OrgID: toPGUUID(orgID),
		ID:    toPGUUID(id),
	})
}

func (s *Store) CreateCalendarEvent(ctx context.Context, orgID uuid.UUID, title string, startAt, endAt time.Time) (followupsdb.CalendarEvent, error) {
	return s.q.CreateCalendarEvent(ctx, followupsdb.CreateCalendarEventParams{
		OrgID:   toPGUUID(orgID),
		Title:   title,
		StartAt: pgtype.Timestamptz{Time: startAt, Valid: true},
		EndAt:   pgtype.Timestamptz{Time: endAt, Valid: true},
	})
}

func (s *Store) ListCalendarEvents(ctx context.Context, orgID uuid.UUID, startAt, endAt time.Time) ([]followupsdb.CalendarEvent, error) {
	return s.q.ListCalendarEvents(ctx, followupsdb.ListCalendarEventsParams{
		OrgID:   toPGUUID(orgID),
		StartAt: pgtype.Timestamptz{Time: startAt, Valid: true},
		EndAt:   pgtype.Timestamptz{Time: endAt, Valid: true},
	})
}
