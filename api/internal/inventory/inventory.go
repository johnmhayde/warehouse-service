// Package inventory defines items, SKUs and the rules for working with them.
package inventory

import (
	"context"
	"errors"
	"regexp"
)

// SKU identifies a stocked item, e.g. "SKU-00921".
type SKU string

var skuPattern = regexp.MustCompile(`^[A-Z0-9-]{1,64}$`)

// ParseSKU validates s and returns it as a SKU.
func ParseSKU(s string) (SKU, error) {
	if !skuPattern.MatchString(s) {
		return "", ErrInvalidSKU
	}
	return SKU(s), nil
}

// Item is a stocked item. It has no JSON tags: how it's shown over HTTP is
// the API layer's concern.
type Item struct {
	SKU      SKU
	Name     string
	Quantity int
}

var (
	ErrNotFound   = errors.New("item not found")
	ErrInvalidSKU = errors.New("invalid SKU")
)

// Repository is what the inventory domain needs from storage. The domain
// defines it; store packages implement it
type Repository interface {
	GetItem(ctx context.Context, sku SKU) (Item, error)
}

// Service holds the inventory business logic.
type Service struct {
	repo Repository
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

func (s *Service) GetItem(ctx context.Context, sku SKU) (Item, error) {
	return s.repo.GetItem(ctx, sku)
}
