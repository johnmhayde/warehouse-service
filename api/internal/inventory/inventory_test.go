package inventory

import (
	"errors"
	"testing"
)

func TestParseSKU(t *testing.T) {
	valid := []string{
		"123456",
		"000123",
		"000000",
		"999999",
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
		"empty":                 "",
		"five digits":           "12345",
		"seven digits":          "1234567",
		"letter":                "12345a",
		"uppercase letters":     "ABCDEF",
		"old format":            "SKU-00921",
		"sign":                  "-12345",
		"leading space":         " 123456",
		"trailing space":        "123456 ",
		"inner space":           "123 456",
		"trailing newline":      "123456\n",
		"leading newline":       "\n123456",
		"two SKUs on two lines": "123456\n654321",
		"arabic-indic digits":   "١٢٣٤٥٦",
		"fullwidth digits":      "１２３４５６",
		"one fullwidth digit":   "12345６",
	}
	for name, s := range invalid {
		t.Run("invalid "+name, func(t *testing.T) {
			if _, err := ParseSKU(s); !errors.Is(err, ErrInvalidSKU) {
				t.Errorf("ParseSKU(%q) error = %v, want ErrInvalidSKU", s, err)
			}
		})
	}
}
