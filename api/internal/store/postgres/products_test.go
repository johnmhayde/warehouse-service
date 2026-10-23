package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

// Compile-time check that Products satisfies the domain's interface.
var _ inventory.ProductRepository = (*Products)(nil)

func TestProductsCreateProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)
	threshold := 5

	tests := []struct {
		name string
		in   inventory.NewProduct
	}{
		{"defaults", inventory.NewProduct{SKU: "000123", Name: "Pallet jack"}},
		{"all fields", inventory.NewProduct{SKU: "123456", Name: "Shrink wrap", Description: "500 mm roll", Category: "Packaging", ReorderThreshold: &threshold}},
		{"zero threshold", inventory.NewProduct{SKU: "654321", Name: "Tape", ReorderThreshold: new(int)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := time.Now()

			got, err := products.CreateProduct(ctx, tt.in)
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
			switch {
			case tt.in.ReorderThreshold == nil && got.ReorderThreshold != nil:
				t.Errorf("ReorderThreshold = %d, want nil", *got.ReorderThreshold)
			case tt.in.ReorderThreshold != nil && (got.ReorderThreshold == nil || *got.ReorderThreshold != *tt.in.ReorderThreshold):
				t.Errorf("ReorderThreshold = %v, want %d", got.ReorderThreshold, *tt.in.ReorderThreshold)
			}
			if got.CreatedAt.Before(before.Add(-time.Minute)) || !got.UpdatedAt.Equal(got.CreatedAt) {
				t.Errorf("CreatedAt, UpdatedAt = %v, %v; want now, equal", got.CreatedAt, got.UpdatedAt)
			}
		})
	}
}

func TestProductsCreateProductStoresUncategorizedAsNull(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	p, err := NewProducts(pool).CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"})
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

	if _, err := products.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"}); err != nil {
		t.Fatalf("first CreateProduct: %v", err)
	}
	_, err := products.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Another"})
	if !errors.Is(err, inventory.ErrDuplicateSKU) {
		t.Errorf("CreateProduct error = %v, want ErrDuplicateSKU", err)
	}
}

func TestProductsGetProduct(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	products := NewProducts(pool)
	threshold := 5

	created, err := products.CreateProduct(ctx, inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual", Category: "Handling", ReorderThreshold: &threshold})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	inactive, err := products.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Shrink wrap"})
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
