// Package memory is an in-memory inventory store for development and tests.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

type Store struct {
	mu            sync.RWMutex
	items         map[inventory.SKU]inventory.Item
	products      map[inventory.SKU]inventory.Product
	lastProductID int64
}

func New(seed ...inventory.Item) *Store {
	s := &Store{
		items:    make(map[inventory.SKU]inventory.Item),
		products: make(map[inventory.SKU]inventory.Product),
	}
	for _, it := range seed {
		s.items[it.SKU] = it
	}
	return s
}

func (s *Store) GetItem(ctx context.Context, sku inventory.SKU) (inventory.Item, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[sku]
	if !ok {
		return inventory.Item{}, inventory.ErrNotFound
	}
	return item, nil
}

// CreateProduct stores p with the same defaults as the products table: no
// stock, active, and both timestamps set to now.
func (s *Store) CreateProduct(ctx context.Context, p inventory.NewProduct) (inventory.Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.products[p.SKU]; ok {
		return inventory.Product{}, inventory.ErrDuplicateSKU
	}
	s.lastProductID++
	now := time.Now()
	product := inventory.Product{
		ID:          s.lastProductID,
		SKU:         p.SKU,
		Name:        p.Name,
		Description: p.Description,
		Category:    p.Category,
		Active:      true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if p.ReorderThreshold != nil {
		// Copied so the caller can't change the stored product.
		threshold := *p.ReorderThreshold
		product.ReorderThreshold = &threshold
	}
	s.products[p.SKU] = product
	return product, nil
}
