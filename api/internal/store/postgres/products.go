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

// CreateProduct inserts p. active and the timestamps take their column
// defaults, and an empty category is stored as NULL. When p.TotalQuantity
// is above 0, the same transaction inserts the matching "Initial stock"
// stock_adjustments row by actorID, as the products table requires.
func (s *Products) CreateProduct(ctx context.Context, actorID int64, p inventory.NewProduct) (inventory.Product, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return inventory.Product{}, fmt.Errorf("begin: %w", err)
	}
	// Rollback after a successful Commit is a no-op.
	defer tx.Rollback(ctx)

	var (
		out      inventory.Product
		category *string
	)
	err = tx.QueryRow(ctx,
		`INSERT INTO products (sku, name, description, category, total_quantity)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING id, sku, name, description, category, total_quantity, active, created_at, updated_at`,
		string(p.SKU), p.Name, p.Description, p.Category, p.TotalQuantity,
	).Scan(&out.ID, &out.SKU, &out.Name, &out.Description, &category, &out.TotalQuantity,
		&out.Active, &out.CreatedAt, &out.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "products_sku_key" {
		return inventory.Product{}, inventory.ErrDuplicateSKU
	}
	if err != nil {
		return inventory.Product{}, fmt.Errorf("insert product: %w", err)
	}

	if out.TotalQuantity > 0 {
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id)
			VALUES ($1, $2, $2, $3, $4)`,
			out.ID, out.TotalQuantity, inventory.InitialStockReason, actorID,
		); err != nil {
			return inventory.Product{}, fmt.Errorf("insert initial stock adjustment: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return inventory.Product{}, fmt.Errorf("commit: %w", err)
	}
	if category != nil {
		out.Category = *category
	}
	return out, nil
}

// uniqueViolation is the Postgres SQLSTATE for unique_violation.
const uniqueViolation = "23505"
