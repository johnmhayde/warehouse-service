package migrations_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
	"github.com/johnmhayde/warehouse-service/api/migrations"
)

// These tests cover the two upgrade paths a database can take, and need
// TEST_DATABASE_URL (see internal/pgtest).

// TestUpFromEmpty applies every migration to an empty database, the path a
// brand-new environment takes.
func TestUpFromEmpty(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	assertAllApplied(t, p)
}

// TestLatestOnPrevious brings a database to the version before the newest
// migration, then applies the newest one: the path every existing
// environment takes when a PR with a new migration is deployed.
func TestLatestOnPrevious(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	sources := p.ListSources()
	latest := sources[len(sources)-1].Version
	if len(sources) > 1 {
		previous := sources[len(sources)-2].Version
		if _, err := p.UpTo(ctx, previous); err != nil {
			t.Fatalf("UpTo(%d): %v", previous, err)
		}
	}

	results, err := p.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(results) != 1 || results[0].Source.Version != latest {
		t.Fatalf("Up applied %d migrations, want only the latest (%d)", len(results), latest)
	}

	assertAllApplied(t, p)
}

// TestDownAndUpAgain rolls every migration back and re-applies them, which
// catches Down sections that are broken or leave objects behind.
func TestDownAndUpAgain(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != 0 {
		t.Fatalf("after DownTo(0): version = %d, err = %v; want 0", v, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up after DownTo(0): %v", err)
	}

	assertAllApplied(t, p)
}

// TestSixDigitSKUMigration applies 00006 to a database holding products with
// old-format SKUs, some with stock history, and checks that only those rows
// and their history are removed. It then rolls 00006 back and checks the old
// format is accepted again.
func TestSixDigitSKUMigration(t *testing.T) {
	const before, version = 5, 6
	connString := pgtest.NewDatabase(t)
	cfg, err := pgx.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })
	p, err := migrations.NewProvider(db)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	ctx := context.Background()

	if _, err := p.UpTo(ctx, before); err != nil {
		t.Fatalf("UpTo(%d): %v", before, err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, email, display_name) OVERRIDING SYSTEM VALUE
		VALUES (1, 'a@example.com', 'A');
		INSERT INTO products (id, sku, name, total_quantity) OVERRIDING SYSTEM VALUE VALUES
			(1, '123456', 'Keeps its history', 5),
			(2, '000001', 'Leading zeros', 0),
			(3, 'SKU-00921', 'Old format with history', 7),
			(4, 'ABC', 'Old format, no history', 0),
			(5, '12345', 'Five digits', 0);
		INSERT INTO stock_adjustments (product_id, delta, quantity_after, reason, actor_id) VALUES
			(1, 5, 5, 'initial count', 1),
			(3, 7, 7, 'initial count', 1),
			(3, -1, 6, 'picked', 1),
			(3, 1, 7, 'returned', 1);
	`); err != nil {
		t.Fatalf("insert rows at version %d: %v", before, err)
	}

	if _, err := p.UpTo(ctx, version); err != nil {
		t.Fatalf("UpTo(%d): %v", version, err)
	}

	var skus string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(sku, ',' ORDER BY id) FROM products`).Scan(&skus); err != nil {
		t.Fatalf("read products: %v", err)
	}
	if want := "123456,000001"; skus != want {
		t.Errorf("products after migration = %s, want %s", skus, want)
	}
	var adjustments string
	if err := db.QueryRowContext(ctx, `SELECT string_agg(product_id::text, ',' ORDER BY id) FROM stock_adjustments`).Scan(&adjustments); err != nil {
		t.Fatalf("read stock adjustments: %v", err)
	}
	if want := "1"; adjustments != want {
		t.Errorf("stock adjustments left for products %s, want %s", adjustments, want)
	}
	var users int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if users != 1 {
		t.Errorf("users after migration = %d, want 1 (untouched)", users)
	}

	if _, err := p.DownTo(ctx, before); err != nil {
		t.Fatalf("DownTo(%d): %v", before, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO products (id, sku, name) OVERRIDING SYSTEM VALUE VALUES (100, 'SKU-00921', 'Old format again')`); err != nil {
		t.Errorf("old-format SKU rejected after rolling back to %d: %v", before, err)
	}
}

func assertAllApplied(t *testing.T, p *goose.Provider) {
	t.Helper()
	statuses, err := p.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range statuses {
		if s.State != goose.StateApplied {
			t.Errorf("migration %s is %s, want applied", s.Source.Path, s.State)
		}
	}
}
