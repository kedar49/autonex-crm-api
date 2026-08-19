package products

import (
	"context"
	"fmt"

	"github.com/go-crm/services/internal/products/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
	q    *productsdb.Queries
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{
		pool: pool,
		q:    productsdb.New(pool),
	}
}

func toPGUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}

func toNumeric(val float64) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(fmt.Sprintf("%.4f", val))
	return n
}

func (s *Store) CreateProduct(ctx context.Context, orgID uuid.UUID, input CreateProductInput) (productsdb.Product, error) {
	return s.q.CreateProduct(ctx, productsdb.CreateProductParams{
		OrgID:       toPGUUID(orgID),
		Sku:         input.SKU,
		Name:        input.Name,
		Description: input.Description,
		UnitPrice:   toNumeric(input.UnitPrice),
		TaxRate:     toNumeric(input.TaxRate),
		Category:    input.Category,
		IsActive:    input.IsActive,
	})
}

func (s *Store) GetProductByID(ctx context.Context, orgID, id uuid.UUID) (productsdb.Product, error) {
	return s.q.GetProductByID(ctx, productsdb.GetProductByIDParams{
		OrgID: toPGUUID(orgID),
		ID:    toPGUUID(id),
	})
}

func (s *Store) ListProducts(ctx context.Context, orgID uuid.UUID, category *string, isActive *bool) ([]productsdb.Product, error) {
	return s.q.ListProducts(ctx, productsdb.ListProductsParams{
		OrgID:    toPGUUID(orgID),
		Category: category,
		IsActive: isActive,
	})
}

func (s *Store) DeleteProduct(ctx context.Context, orgID, id uuid.UUID) error {
	return s.q.DeleteProduct(ctx, productsdb.DeleteProductParams{
		OrgID: toPGUUID(orgID),
		ID:    toPGUUID(id),
	})
}
