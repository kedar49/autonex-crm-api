package products

import (
	"context"

	"github.com/go-crm/services/internal/products/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Service struct {
	store *Store
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{store: NewStore(pool)}
}

type CreateProductInput struct {
	SKU         string  `json:"sku"`
	Name        string  `json:"name"`
	Description *string `json:"description,omitempty"`
	UnitPrice   float64 `json:"unit_price"`
	TaxRate     float64 `json:"tax_rate"`
	Category    *string `json:"category,omitempty"`
	IsActive    bool    `json:"is_active"`
}

func (s *Service) Create(ctx context.Context, orgID uuid.UUID, input CreateProductInput) (productsdb.Product, error) {
	return s.store.CreateProduct(ctx, orgID, input)
}

func (s *Service) Get(ctx context.Context, orgID, id uuid.UUID) (productsdb.Product, error) {
	return s.store.GetProductByID(ctx, orgID, id)
}

func (s *Service) List(ctx context.Context, orgID uuid.UUID, category *string, isActive *bool) ([]productsdb.Product, error) {
	return s.store.ListProducts(ctx, orgID, category, isActive)
}

func (s *Service) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	return s.store.DeleteProduct(ctx, orgID, id)
}
