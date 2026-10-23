// Package inventory defines products, SKUs and the rules for working with
// them.
package inventory

import (
	"errors"
	"regexp"
)

// SKU identifies a product. It is exactly six ASCII digits, e.g. "123456".
// Leading zeros are part of the SKU ("000123"), so it is a string, never a
// number.
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

var ErrInvalidSKU = errors.New("invalid SKU")
