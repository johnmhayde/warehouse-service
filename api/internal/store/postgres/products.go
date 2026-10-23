package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
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

// productColumns are the columns scanProduct reads, in order.
const productColumns = `id, sku, name, description, category, total_quantity, reorder_threshold, active, created_at, updated_at`

// scanProduct scans a row of productColumns. A NULL category becomes "".
func scanProduct(row pgx.Row) (inventory.Product, error) {
	var (
		p        inventory.Product
		category *string
	)
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &category, &p.TotalQuantity,
		&p.ReorderThreshold, &p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return inventory.Product{}, err
	}
	if category != nil {
		p.Category = *category
	}
	return p, nil
}

// CreateProduct inserts p. total_quantity, active and the timestamps take
// their column defaults, and an empty category is stored as NULL.
func (s *Products) CreateProduct(ctx context.Context, p inventory.NewProduct) (inventory.Product, error) {
	out, err := scanProduct(s.pool.QueryRow(ctx,
		`INSERT INTO products (sku, name, description, category, reorder_threshold)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING `+productColumns,
		string(p.SKU), p.Name, p.Description, p.Category, p.ReorderThreshold,
	))
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == "products_sku_key" {
		return inventory.Product{}, inventory.ErrDuplicateSKU
	}
	if err != nil {
		return inventory.Product{}, fmt.Errorf("insert product: %w", err)
	}
	return out, nil
}

// GetProduct returns the product with this id, whether or not it is active.
func (s *Products) GetProduct(ctx context.Context, id int64) (inventory.Product, error) {
	p, err := scanProduct(s.pool.QueryRow(ctx,
		`SELECT `+productColumns+` FROM products WHERE id = $1`,
		id,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return inventory.Product{}, inventory.ErrNotFound
	}
	if err != nil {
		return inventory.Product{}, fmt.Errorf("select product: %w", err)
	}
	return p, nil
}

// uniqueViolation is the Postgres SQLSTATE for unique_violation.
const uniqueViolation = "23505"
