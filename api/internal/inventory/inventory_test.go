package inventory

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSKU(t *testing.T) {
	valid := []string{
		"SKU-00921",
		"000123",
		"A",
		strings.Repeat("A", 64),
	}
	for _, s := range valid {
		t.Run("valid "+s, func(t *testing.T) {
			sku, err := ParseSKU(s)
			if err != nil {
				t.Fatalf("ParseSKU(%q) error: %v", s, err)
			}
			if sku != SKU(s) {
				t.Errorf("ParseSKU(%q) = %q", s, sku)
			}
		})
	}

	invalid := map[string]string{
		"empty":     "",
		"lowercase": "sku-00921",
		"space":     "SKU 00921",
		"symbol":    "SKU_00921",
		"too long":  strings.Repeat("A", 65),
		"newline":   "SKU-1\n",
	}
	for name, s := range invalid {
		t.Run("invalid "+name, func(t *testing.T) {
			if _, err := ParseSKU(s); !errors.Is(err, ErrInvalidSKU) {
				t.Errorf("ParseSKU(%q) error = %v, want ErrInvalidSKU", s, err)
			}
		})
	}
}
