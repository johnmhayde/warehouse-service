// Package inventory defines items, SKUs and the rules for working with them.
package inventory

import (
	"context"
	"errors"
	"regexp"
)

// SKU identifies a stocked item. It is exactly six ASCII digits, e.g.
// "123456". Leading zeros are part of the SKU ("000123"), so it is a
// string, never a number.
type SKU string

// skuPattern must match the products_sku_format constraint in api/migrations.
var skuPattern = regexp.MustCompile(`^[0-9]{6}$`)

// ParseSKU validates s and returns it as a SKU. s must be exactly six ASCII
// digits.
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
