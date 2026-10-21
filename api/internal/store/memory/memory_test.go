package memory

import (
	"context"
	"errors"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Compile-time check that Store satisfies the domain's Repository interface.
var _ inventory.Repository = (*Store)(nil)

func TestGetItemFound(t *testing.T) {
	want := inventory.Item{SKU: "SKU-1", Name: "Pallet jack", Quantity: 3}
	s := New(want)

	got, err := s.GetItem(context.Background(), "SKU-1")
	if err != nil {
		t.Fatalf("GetItem: %v", err)
	}
	if got != want {
		t.Errorf("GetItem = %+v, want %+v", got, want)
	}
}

func TestGetItemNotFound(t *testing.T) {
	s := New(inventory.Item{SKU: "SKU-1"})

	_, err := s.GetItem(context.Background(), "SKU-2")
	if !errors.Is(err, inventory.ErrNotFound) {
		t.Errorf("GetItem error = %v, want ErrNotFound", err)
	}
}
