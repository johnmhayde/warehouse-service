package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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
const productColumns = `id, sku, name, description, category, total_quantity, active, created_at, updated_at`

// scanProduct scans a row of productColumns. A NULL category becomes "".
func scanProduct(row pgx.Row) (inventory.Product, error) {
	var (
		p        inventory.Product
		category *string
	)
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &category, &p.TotalQuantity,
		&p.Active, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return inventory.Product{}, err
	}
	if category != nil {
		p.Category = *category
	}
	return p, nil
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

	out, err := scanProduct(tx.QueryRow(ctx,
		`INSERT INTO products (sku, name, description, category, total_quantity)
		VALUES ($1, $2, $3, NULLIF($4, ''), $5)
		RETURNING `+productColumns,
		string(p.SKU), p.Name, p.Description, p.Category, p.TotalQuantity,
	))
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

// ListProducts returns up to f.Limit products matching f in (name, id)
// order, starting after f.After. It pages by keyset, comparing (name, id)
// with the cursor's, so products_name_id_idx can serve a page however deep
// it is; a name search can use products_name_trgm_idx instead. Only
// placeholders carry f's values into the SQL.
func (s *Products) ListProducts(ctx context.Context, f inventory.ProductFilter) ([]inventory.Product, error) {
	var (
		where []string
		args  []any
	)
	// arg adds v to args and returns its placeholder.
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	if !f.IncludeInactive {
		where = append(where, "active")
	}
	if f.Query != "" {
		where = append(where, "name ILIKE "+arg("%"+escapeLike(f.Query)+"%"))
	}
	if f.Category != "" {
		where = append(where, "category = "+arg(f.Category))
	}
	if f.After != nil {
		where = append(where, "(name, id) > ("+arg(f.After.Name)+", "+arg(f.After.ID)+")")
	}

	sql := `SELECT ` + productColumns + ` FROM products`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	sql += ` ORDER BY name, id LIMIT ` + arg(f.Limit)

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("select products: %w", err)
	}
	products, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (inventory.Product, error) {
		return scanProduct(row)
	})
	if err != nil {
		return nil, fmt.Errorf("select products: %w", err)
	}
	return products, nil
}

// escapeLike makes s match itself literally in a LIKE or ILIKE pattern by
// escaping the metacharacters with backslash, LIKE's default escape
// character.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// uniqueViolation is the Postgres SQLSTATE for unique_violation.
const uniqueViolation = "23505"
