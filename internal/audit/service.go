package audit

import (
	"context"

	"github.com/go-crm/services/internal/audit/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	store *Store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: NewStore(pool)}
}

type LogInput struct {
	UserID     *uuid.UUID `json:"user_id,omitempty"`
	Action     string     `json:"action"`
	EntityType string     `json:"entity_type"`
	EntityID   uuid.UUID  `json:"entity_id"`
	Changes    []byte     `json:"changes,omitempty"`
	IPAddress  *string    `json:"ip_address,omitempty"`
}

func (s *Service) Log(ctx context.Context, orgID uuid.UUID, input LogInput) (auditdb.AuditLog, error) {
	return s.store.CreateLog(ctx, orgID, input)
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID, entityType *string, entityID *uuid.UUID, limit, offset int32) ([]auditdb.ListAuditLogsRow, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.store.ListLogs(ctx, orgID, entityType, entityID, limit, offset)
}
