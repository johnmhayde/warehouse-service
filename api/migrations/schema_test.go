package migrations_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

// SQLSTATE codes for integrity violations.
const (
	notNullViolation    = "23502"
	foreignKeyViolation = "23503"
	uniqueViolation     = "23505"
	checkViolation      = "23514"
)

// fixture is one valid user, product and stock adjustment, inserted at the
// start of every subtest.
type fixture struct {
	userID    int64
	productID int64
}

// withFixture runs fn in a transaction that already contains the fixture
// rows, and rolls it back afterwards so subtests can't affect each other.
func withFixture(t *testing.T, pool *pgxpool.Pool, fn func(ctx context.Context, tx pgx.Tx, f fixture)) {
	t.Helper()
	ctx := context.Background()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback(ctx)

	var f fixture
	if err := tx.QueryRow(ctx,
		`INSERT INTO users (email, display_name) VALUES ('fixture@example.com', 'Fixture User') RETURNING id`,
	).Scan(&f.userID); err != nil {
		t.Fatalf("insert fixture user: %v", err)
	}
	if err := tx.QueryRow(ctx,
		`INSERT INTO products (sku, name, category, total_quantity) VALUES ('FIX-1', 'Fixture pallet jack', 'equipment', 10) RETURNING id`,
	).Scan(&f.productID); err != nil {
		t.Fatalf("insert fixture product: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES ($1, 10, 10, 'initial count', $2)`,
		f.productID, f.userID,
	); err != nil {
		t.Fatalf("insert fixture stock adjustment: %v", err)
	}

	fn(ctx, tx, f)
}

func TestSchemaConstraints(t *testing.T) {
	pool := pgtest.Migrated(t)

	tests := []struct {
		name string
		sql  string
		args func(f fixture) []any

		code       string
		constraint string // for NOT NULL violations, the column instead
	}{
		// users
		{
			name: "user email is unique regardless of case",
			sql:  `INSERT INTO users (email, display_name) VALUES ('FIXTURE@Example.com', 'Someone')`,
			code: uniqueViolation, constraint: "users_email_key",
		},
		{
			name: "user email must look like an email",
			sql:  `INSERT INTO users (email, display_name) VALUES ('not-an-email', 'Someone')`,
			code: checkViolation, constraint: "users_email_format",
		},
		{
			name: "user email is required",
			sql:  `INSERT INTO users (display_name) VALUES ('Someone')`,
			code: notNullViolation, constraint: "email",
		},
		{
			name: "user display name can't be blank",
			sql:  `INSERT INTO users (email, display_name) VALUES ('a@example.com', '   ')`,
			code: checkViolation, constraint: "users_display_name_not_blank",
		},
		{
			name: "user role must be requester or admin",
			sql:  `INSERT INTO users (email, display_name, role) VALUES ('a@example.com', 'Someone', 'superuser')`,
			code: checkViolation, constraint: "users_role_check",
		},
		{
			name: "user external subject is unique once set",
			sql: `INSERT INTO users (email, display_name, external_subject)
			      VALUES ('a@example.com', 'A', 'idp|123'), ('b@example.com', 'B', 'idp|123')`,
			code: uniqueViolation, constraint: "users_external_subject_key",
		},
		{
			name: "user external subject can't be blank",
			sql:  `INSERT INTO users (email, display_name, external_subject) VALUES ('a@example.com', 'Someone', '')`,
			code: checkViolation, constraint: "users_external_subject_not_blank",
		},

		// products
		{
			name: "product SKU is unique",
			sql:  `INSERT INTO products (sku, name) VALUES ('FIX-1', 'Another')`,
			code: uniqueViolation, constraint: "products_sku_key",
		},
		{
			name: "product SKU must match the SKU format",
			sql:  `INSERT INTO products (sku, name) VALUES ('fix 2', 'Another')`,
			code: checkViolation, constraint: "products_sku_format",
		},
		{
			name: "product name can't be blank",
			sql:  `INSERT INTO products (sku, name) VALUES ('FIX-2', '')`,
			code: checkViolation, constraint: "products_name_not_blank",
		},
		{
			name: "product category can't be blank",
			sql:  `INSERT INTO products (sku, name, category) VALUES ('FIX-2', 'Another', ' ')`,
			code: checkViolation, constraint: "products_category_not_blank",
		},
		{
			name: "product total quantity can't go negative",
			sql:  `UPDATE products SET total_quantity = -1 WHERE id = $1`,
			args: func(f fixture) []any { return []any{f.productID} },
			code: checkViolation, constraint: "products_total_quantity_nonnegative",
		},
		{
			name: "product reorder threshold can't be negative",
			sql:  `INSERT INTO products (sku, name, reorder_threshold) VALUES ('FIX-2', 'Another', -5)`,
			code: checkViolation, constraint: "products_reorder_threshold_nonnegative",
		},

		// stock_adjustments
		{
			name: "adjustment delta can't be zero",
			sql:  `INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES ($1, 0, 10, 'noop', $2)`,
			args: func(f fixture) []any { return []any{f.productID, f.userID} },
			code: checkViolation, constraint: "stock_adjustments_delta_nonzero",
		},
		{
			name: "adjustment quantity after can't be negative",
			sql:  `INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES ($1, -11, -1, 'picked', $2)`,
			args: func(f fixture) []any { return []any{f.productID, f.userID} },
			code: checkViolation, constraint: "stock_adjustments_quantity_after_nonnegative",
		},
		{
			name: "adjustment reason can't be blank",
			sql:  `INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES ($1, 1, 11, '', $2)`,
			args: func(f fixture) []any { return []any{f.productID, f.userID} },
			code: checkViolation, constraint: "stock_adjustments_reason_not_blank",
		},
		{
			name: "adjustment needs an existing product",
			sql:  `INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES (-1, 1, 1, 'received', $1)`,
			args: func(f fixture) []any { return []any{f.userID} },
			code: foreignKeyViolation, constraint: "stock_adjustments_product_fk",
		},
		{
			name: "adjustment needs an existing actor",
			sql:  `INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES ($1, 1, 11, 'received', -1)`,
			args: func(f fixture) []any { return []any{f.productID} },
			code: foreignKeyViolation, constraint: "stock_adjustments_actor_fk",
		},
		{
			name: "product with stock history can't be deleted",
			sql:  `DELETE FROM products WHERE id = $1`,
			args: func(f fixture) []any { return []any{f.productID} },
			code: foreignKeyViolation, constraint: "stock_adjustments_product_fk",
		},
		{
			name: "user with stock history can't be deleted",
			sql:  `DELETE FROM users WHERE id = $1`,
			args: func(f fixture) []any { return []any{f.userID} },
			code: foreignKeyViolation, constraint: "stock_adjustments_actor_fk",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withFixture(t, pool, func(ctx context.Context, tx pgx.Tx, f fixture) {
				var args []any
				if tt.args != nil {
					args = tt.args(f)
				}
				_, err := tx.Exec(ctx, tt.sql, args...)

				var pgErr *pgconn.PgError
				if !errors.As(err, &pgErr) {
					t.Fatalf("got error %v, want SQLSTATE %s on %s", err, tt.code, tt.constraint)
				}
				got := pgErr.ConstraintName
				if tt.code == notNullViolation {
					got = pgErr.ColumnName
				}
				if pgErr.Code != tt.code || got != tt.constraint {
					t.Errorf("violated %s (SQLSTATE %s), want %s (SQLSTATE %s): %s",
						got, pgErr.Code, tt.constraint, tt.code, pgErr.Message)
				}
			})
		})
	}
}

func TestSchemaAcceptsValidRows(t *testing.T) {
	pool := pgtest.Migrated(t)

	withFixture(t, pool, func(ctx context.Context, tx pgx.Tx, f fixture) {
		valid := []struct {
			name string
			sql  string
		}{
			{"several users without an external subject",
				`INSERT INTO users (email, display_name) VALUES ('a@example.com', 'A'), ('b@example.com', 'B')`},
			{"an admin user",
				`INSERT INTO users (email, display_name, role) VALUES ('admin@example.com', 'Admin', 'admin')`},
			{"an uncategorized product with no threshold",
				`INSERT INTO products (sku, name) VALUES ('SKU-00921', 'Hand truck')`},
			{"a product at zero stock",
				`INSERT INTO products (sku, name, total_quantity, reorder_threshold) VALUES ('SKU-00922', 'Dolly', 0, 0)`},
		}
		for _, v := range valid {
			if _, err := tx.Exec(ctx, v.sql); err != nil {
				t.Errorf("%s: %v", v.name, err)
			}
		}

		var defaults struct {
			role   string
			active bool
		}
		if err := tx.QueryRow(ctx, `SELECT role, active FROM users WHERE id = $1`, f.userID).
			Scan(&defaults.role, &defaults.active); err != nil {
			t.Fatalf("read fixture user: %v", err)
		}
		if defaults.role != "requester" || !defaults.active {
			t.Errorf("new user defaults: role=%q active=%v, want requester/true", defaults.role, defaults.active)
		}
	})
}

func TestUpdatedAtTriggers(t *testing.T) {
	pool := pgtest.Migrated(t)
	old := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, table := range []string{"users", "products"} {
		t.Run(table, func(t *testing.T) {
			withFixture(t, pool, func(ctx context.Context, tx pgx.Tx, f fixture) {
				id := f.userID
				if table == "products" {
					id = f.productID
				}
				// Backdate the row (this UPDATE also fires the trigger, so
				// disable it for the statement), then touch it normally.
				if _, err := tx.Exec(ctx, `ALTER TABLE `+table+` DISABLE TRIGGER `+table+`_set_updated_at`); err != nil {
					t.Fatalf("disable trigger: %v", err)
				}
				if _, err := tx.Exec(ctx, `UPDATE `+table+` SET updated_at = $1 WHERE id = $2`, old, id); err != nil {
					t.Fatalf("backdate: %v", err)
				}
				if _, err := tx.Exec(ctx, `ALTER TABLE `+table+` ENABLE TRIGGER `+table+`_set_updated_at`); err != nil {
					t.Fatalf("enable trigger: %v", err)
				}
				if _, err := tx.Exec(ctx, `UPDATE `+table+` SET active = false WHERE id = $1`, id); err != nil {
					t.Fatalf("update: %v", err)
				}

				var updatedAt time.Time
				if err := tx.QueryRow(ctx, `SELECT updated_at FROM `+table+` WHERE id = $1`, id).Scan(&updatedAt); err != nil {
					t.Fatalf("read updated_at: %v", err)
				}
				if !updatedAt.After(old) {
					t.Errorf("updated_at = %v after an update, want it refreshed", updatedAt)
				}
			})
		})
	}
}

func TestProductIndexes(t *testing.T) {
	pool := pgtest.Migrated(t)

	t.Run("indexes exist", func(t *testing.T) {
		ctx := context.Background()
		rows, err := pool.Query(ctx, `SELECT indexname FROM pg_indexes WHERE tablename = 'products'`)
		if err != nil {
			t.Fatalf("list indexes: %v", err)
		}
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatalf("read indexes: %v", err)
		}
		have := strings.Join(names, ",")
		for _, want := range []string{"products_sku_key", "products_category_idx", "products_active_idx", "products_name_trgm_idx"} {
			if !strings.Contains(have, want) {
				t.Errorf("missing index %s (have %s)", want, have)
			}
		}
	})

	t.Run("name ILIKE search can use the trigram index", func(t *testing.T) {
		withFixture(t, pool, func(ctx context.Context, tx pgx.Tx, f fixture) {
			// With a near-empty table the planner would always pick a
			// sequential scan, so rule that out to see whether the index can
			// serve the query at all.
			if _, err := tx.Exec(ctx, `SET LOCAL enable_seqscan = off`); err != nil {
				t.Fatalf("disable seqscan: %v", err)
			}
			rows, err := tx.Query(ctx, `EXPLAIN SELECT id FROM products WHERE name ILIKE '%pallet%'`)
			if err != nil {
				t.Fatalf("explain: %v", err)
			}
			lines, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatalf("read plan: %v", err)
			}
			plan := strings.Join(lines, "\n")
			if !strings.Contains(plan, "products_name_trgm_idx") {
				t.Errorf("plan does not use products_name_trgm_idx:\n%s", plan)
			}
		})
	})
}
