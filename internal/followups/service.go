package followups

import (
	"context"
	"time"

	"github.com/go-crm/services/internal/followups/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	store *Store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: NewStore(pool)}
}

type CreateFollowUpInput struct {
	AssignedTo *uuid.UUID `json:"assigned_to,omitempty"`
	Title      string     `json:"title"`
	DueAt      time.Time  `json:"due_at"`
	LeadID     *uuid.UUID `json:"lead_id,omitempty"`
	DealID     *uuid.UUID `json:"deal_id,omitempty"`
}

func (s *Service) Create(ctx context.Context, orgID uuid.UUID, input CreateFollowUpInput) (followupsdb.FollowUp, error) {
	return s.store.CreateFollowUp(ctx, orgID, input)
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID, assignedTo *uuid.UUID, completed *bool) ([]followupsdb.FollowUp, error) {
	return s.store.ListFollowUps(ctx, orgID, assignedTo, completed)
}

func (s *Service) Complete(ctx context.Context, orgID, id uuid.UUID) (followupsdb.FollowUp, error) {
	return s.store.CompleteFollowUp(ctx, orgID, id)
}
