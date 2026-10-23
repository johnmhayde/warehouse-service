package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

// Compile-time check that Products satisfies the domain's interface.
var _ inventory.ProductRepository = (*Products)(nil)

func TestProductsCreateProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	tests := []struct {
		name string
		in   inventory.NewProduct
	}{
		{"defaults", inventory.NewProduct{SKU: "000123", Name: "Pallet jack"}},
		{"all fields", inventory.NewProduct{SKU: "123456", Name: "Shrink wrap", Description: "500 mm roll", Category: "Packaging"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()

			got, err := products.CreateProduct(ctx, 1, tt.in)
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			if got.ID <= 0 {
				t.Errorf("ID = %d, want a positive id", got.ID)
			}
			if got.SKU != tt.in.SKU || got.Name != tt.in.Name || got.Description != tt.in.Description || got.Category != tt.in.Category {
				t.Errorf("CreateProduct = %+v, want the fields of %+v", got, tt.in)
			}
			if got.TotalQuantity != 0 || !got.Active {
				t.Errorf("TotalQuantity, Active = %d, %v; want 0, true", got.TotalQuantity, got.Active)
			}
			if got.CreatedAt.Before(before.Add(-time.Minute)) || !got.UpdatedAt.Equal(got.CreatedAt) {
				t.Errorf("CreatedAt, UpdatedAt = %v, %v; want now, equal", got.CreatedAt, got.UpdatedAt)
			}
		})
	}
}

// stockAdjustment is a stock_adjustments row, without its id and
// created_at.
type stockAdjustment struct {
	delta, quantityAfter int
	reason               string
	actorID              int64
}

// adjustmentsOf returns productID's stock adjustments, oldest first.
func adjustmentsOf(t *testing.T, pool *pgxpool.Pool, productID int64) []stockAdjustment {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		"SELECT delta, quantity_after, reason, actor_id FROM stock_adjustments WHERE product_id = $1 ORDER BY id", productID)
	if err != nil {
		t.Fatalf("query stock_adjustments: %v", err)
	}
	defer rows.Close()
	var out []stockAdjustment
	for rows.Next() {
		var a stockAdjustment
		if err := rows.Scan(&a.delta, &a.quantityAfter, &a.reason, &a.actorID); err != nil {
			t.Fatalf("scan stock_adjustments: %v", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read stock_adjustments: %v", err)
	}
	return out
}

func TestProductsCreateProductInitialStock(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	var adminID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO users (email, display_name, role) VALUES ('admin@example.com', 'Admin', 'admin') RETURNING id",
	).Scan(&adminID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	tests := []struct {
		name            string
		sku             inventory.SKU
		totalQuantity   int
		wantAdjustments []stockAdjustment
	}{
		{"no stock", "123456", 0, nil},
		{"initial stock", "654321", 12, []stockAdjustment{{delta: 12, quantityAfter: 12, reason: "Initial stock", actorID: adminID}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := products.CreateProduct(ctx, adminID, inventory.NewProduct{SKU: tt.sku, Name: "Pallet jack", TotalQuantity: tt.totalQuantity})
			if err != nil {
				t.Fatalf("CreateProduct: %v", err)
			}

			if got.TotalQuantity != tt.totalQuantity {
				t.Errorf("TotalQuantity = %d, want %d", got.TotalQuantity, tt.totalQuantity)
			}
			var stored int
			if err := pool.QueryRow(ctx, "SELECT total_quantity FROM products WHERE id = $1", got.ID).Scan(&stored); err != nil {
				t.Fatalf("query total_quantity: %v", err)
			}
			if stored != tt.totalQuantity {
				t.Errorf("stored total_quantity = %d, want %d", stored, tt.totalQuantity)
			}
			if adj := adjustmentsOf(t, pool, got.ID); !reflect.DeepEqual(adj, tt.wantAdjustments) {
				t.Errorf("stock adjustments = %+v, want %+v", adj, tt.wantAdjustments)
			}
		})
	}
}

// TestProductsCreateProductInitialStockIsAtomic checks that a product isn't
// created when its initial stock adjustment can't be written.
func TestProductsCreateProductInitialStockIsAtomic(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	// No user has this id, so the adjustment breaks stock_adjustments_actor_fk.
	_, err := NewProducts(pool).CreateProduct(ctx, 999, inventory.NewProduct{SKU: "123456", Name: "Pallet jack", TotalQuantity: 5})
	if err == nil {
		t.Fatal("CreateProduct succeeded with an unknown actor, want an error")
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&count); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if count != 0 {
		t.Errorf("products = %d, want 0 (the insert rolled back)", count)
	}
}

func TestProductsCreateProductStoresUncategorizedAsNull(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	p, err := NewProducts(pool).CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	var isNull bool
	if err := pool.QueryRow(ctx, "SELECT category IS NULL FROM products WHERE id = $1", p.ID).Scan(&isNull); err != nil {
		t.Fatalf("query category: %v", err)
	}
	if !isNull {
		t.Error("category stored as a non-NULL value, want NULL")
	}
}

func TestProductsCreateProductDuplicateSKU(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	if _, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"}); err != nil {
		t.Fatalf("first CreateProduct: %v", err)
	}
	_, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Another"})
	if !errors.Is(err, inventory.ErrDuplicateSKU) {
		t.Errorf("CreateProduct error = %v, want ErrDuplicateSKU", err)
	}
}

func TestProductsGetProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)

	created, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual", Category: "Handling"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	inactive, err := products.CreateProduct(ctx, 1, inventory.NewProduct{SKU: "123456", Name: "Shrink wrap"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE products SET active = false WHERE id = $1", inactive.ID); err != nil {
		t.Fatalf("deactivate product: %v", err)
	}
	inactive.Active = false

	for _, want := range []inventory.Product{created, inactive} {
		t.Run(string(want.SKU), func(t *testing.T) {
			got, err := products.GetProduct(ctx, want.ID)
			if err != nil {
				t.Fatalf("GetProduct: %v", err)
			}
			// The deactivation moves updated_at, so the timestamps are
			// compared on their own.
			if !got.CreatedAt.Equal(want.CreatedAt) || got.UpdatedAt.Before(want.UpdatedAt) {
				t.Errorf("CreatedAt, UpdatedAt = %v, %v; want %v and not before %v", got.CreatedAt, got.UpdatedAt, want.CreatedAt, want.UpdatedAt)
			}
			got.CreatedAt, got.UpdatedAt = time.Time{}, time.Time{}
			want.CreatedAt, want.UpdatedAt = time.Time{}, time.Time{}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("GetProduct = %+v, want %+v", got, want)
			}
		})
	}
}

func TestProductsGetProductNotFound(t *testing.T) {
	pool := pgtest.Migrated(t)

	_, err := NewProducts(pool).GetProduct(context.Background(), 999)
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("GetProduct error = %v, want ErrNotFound", err)
	}
}
