package audit

import (
	"context"

	"github.com/go-crm/services/internal/audit/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	q    *auditdb.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
		q:    auditdb.New(pool),
	}
}

func toPGUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func (s *Store) CreateLog(ctx context.Context, orgID uuid.UUID, input LogInput) (auditdb.AuditLog, error) {
	var uid pgtype.UUID
	if input.UserID != nil {
		uid = toPGUUID(*input.UserID)
	}

	return s.q.CreateAuditLog(ctx, auditdb.CreateAuditLogParams{
		OrgID:      toPGUUID(orgID),
		UserID:     uid,
		Action:     input.Action,
		EntityType: input.EntityType,
		EntityID:   toPGUUID(input.EntityID),
		Changes:    input.Changes,
		IpAddress:  input.IPAddress,
	})
}

func (s *Store) ListLogs(ctx context.Context, orgID uuid.UUID, entityType *string, entityID *uuid.UUID, limit, offset int32) ([]auditdb.ListAuditLogsRow, error) {
	var eid pgtype.UUID
	if entityID != nil {
		eid = toPGUUID(*entityID)
	}

	return s.q.ListAuditLogs(ctx, auditdb.ListAuditLogsParams{
		OrgID:      toPGUUID(orgID),
		EntityType: entityType,
		EntityID:   eid,
		LimitVal:   limit,
		OffsetVal:  offset,
	})
}
