package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Products implements inventory.ProductRepository on the products table.
type Products struct {
	pool *pgxpool.Pool
}

// NewProducts returns a Products that queries through pool.
func NewProducts(pool *pgxpool.Pool) *Products {
	return &Products{pool: pool}
}

// CreateProduct inserts p. total_quantity, active and the timestamps take
// their column defaults, and an empty category is stored as NULL.
func (s *Products) CreateProduct(ctx context.Context, p inventory.NewProduct) (inventory.Product, error) {
	var (
		out      inventory.Product
		category *string
	)
	err := s.pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, description, category, reorder_threshold)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING id, sku, name, description, category, total_quantity, reorder_threshold, active, created_at, updated_at`,
		string(p.SKU), p.Name, p.Description, p.Category, p.ReorderThreshold,
	).Scan(&out.ID, &out.SKU, &out.Name, &out.Description, &category, &out.TotalQuantity,
		&out.ReorderThreshold, &out.Active, &out.CreatedAt, &out.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "products_sku_key" {
		return inventory.Product{}, inventory.ErrDuplicateSKU
	}
	if err != nil {
		return inventory.Product{}, fmt.Errorf("insert product: %w", err)
	}
	if category != nil {
		out.Category = *category
	}
	return out, nil
}

// uniqueViolation is the Postgres SQLSTATE for unique_violation.
const uniqueViolation = "23505"
