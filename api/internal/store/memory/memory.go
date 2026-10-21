// Package memory is an in-memory inventory store for development and tests.
package memory

import (
	"context"
	"sync"
	
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

type Store struct {
	mu    sync.RWMutex
	items map[inventory.SKU]inventory.Item
}

func New(seed ...inventory.Item) *Store {
	s := &Store{items: make(map[inventory.SKU]inventory.Item)}
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