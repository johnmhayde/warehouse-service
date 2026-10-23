package postgres

import (
	"context"
	"errors"
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
