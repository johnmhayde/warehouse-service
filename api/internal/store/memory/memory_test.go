package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Compile-time checks that Store satisfies the domain's repository
// interfaces.
var (
	_ inventory.Repository        = (*Store)(nil)
	_ inventory.ProductRepository = (*Store)(nil)
)

func TestGetItemFound(t *testing.T) {
	want := inventory.Item{SKU: "123456", Name: "Pallet jack", Quantity: 3}
	s := New(want)

	got, err := s.GetItem(context.Background(), "123456")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got != want {
		t.Errorf("GetItem = %+v, want %+v", got, want)
	}
}

func TestGetItemNotFound(t *testing.T) {
	s := New(inventory.Item{SKU: "123456"})

	_, err := s.GetItem(context.Background(), "654321")
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("GetItem error = %v, want ErrNotFound", err)
	}
}

func TestCreateProduct(t *testing.T) {
	s := New()
	ctx := context.Background()
	threshold := 5

	first, err := s.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Pallet jack", Category: "Handling", ReorderThreshold: &threshold})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	threshold = 9
	second, err := s.CreateProduct(ctx, inventory.NewProduct{SKU: "654321", Name: "Shrink wrap"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}

	if first.ID != 1 || second.ID != 2 {
		t.Errorf("IDs = %d, %d; want 1, 2", first.ID, second.ID)
	}
	if first.SKU != "123456" || first.Name != "Pallet jack" || first.Category != "Handling" {
		t.Errorf("first = %+v, want the fields it was created with", first)
	}
	if first.ReorderThreshold == nil || *first.ReorderThreshold != 5 {
		t.Errorf("ReorderThreshold = %v, want 5 (unaffected by the caller's later change)", first.ReorderThreshold)
	}
	if second.ReorderThreshold != nil {
		t.Errorf("ReorderThreshold = %d, want nil", *second.ReorderThreshold)
	}
	if first.TotalQuantity != 0 || !first.Active {
		t.Errorf("TotalQuantity, Active = %d, %v; want 0, true", first.TotalQuantity, first.Active)
	}
	if first.CreatedAt.IsZero() || !first.UpdatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt, UpdatedAt = %v, %v; want set and equal", first.CreatedAt, first.UpdatedAt)
	}
}

func TestCreateProductDuplicateSKU(t *testing.T) {
	s := New()
	ctx := context.Background()

	if _, err := s.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Pallet jack"}); err != nil {
		t.Fatalf("first CreateProduct: %v", err)
	}
	_, err := s.CreateProduct(ctx, inventory.NewProduct{SKU: "123456", Name: "Another"})
	if !errors.Is(err, inventory.ErrDuplicateSKU) {
		t.Errorf("CreateProduct error = %v, want ErrDuplicateSKU", err)
	}
}
