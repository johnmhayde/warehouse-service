package v1

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

func TestCursorRoundTrip(t *testing.T) {
	for _, c := range []inventory.ProductCursor{
		{Name: "Pallet jack", ID: 42},
		{Name: "a", ID: 1},
		{Name: "Bolt: M8 x 40", ID: 7},
		{Name: "50% off / é ü 漢字 ✓", ID: 9223372036854775807},
		{Name: " padded ", ID: 3},
		{Name: strings.Repeat("é", inventory.MaxNameLen), ID: 4},
	} {
		s := encodeCursor(c)
		if strings.ContainsAny(s, "+/=") {
			t.Errorf("cursor %q for %+v is not URL-safe", s, c)
		}
		got, ok := decodeCursor(s)
		if !ok || got != c {
			t.Errorf("decodeCursor(encodeCursor(%+v)) = %+v, %v", c, got, ok)
		}
	}
}

func TestDecodeCursorRejects(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	// "1:ab" encodes to "MTphYg", whose last character carries 4 unused
	// bits. Setting one gives a second spelling of the same bytes, which
	// lenient decoding accepts and strict decoding rejects.
	nonCanonical := enc("1:ab")
	nonCanonical = nonCanonical[:len(nonCanonical)-1] + "h"
	if b, err := base64.RawURLEncoding.DecodeString(nonCanonical); err != nil || string(b) != "1:ab" {
		t.Fatalf("lenient decode of %q = %q, %v; want \"1:ab\"", nonCanonical, b, err)
	}
	tests := []struct {
		name, cursor string
	}{
		{"empty", ""},
		{"not base64", "!!!"},
		{"padded base64", base64.URLEncoding.EncodeToString([]byte("1:ab"))},
		{"standard alphabet", base64.RawStdEncoding.EncodeToString([]byte("1:>>>?"))},
		{"no separator", enc("42")},
		{"empty name", enc("42:")},
		{"empty id", enc(":Pallet jack")},
		{"zero id", enc("0:Pallet jack")},
		{"negative id", enc("-1:Pallet jack")},
		{"id with sign", enc("+1:Pallet jack")},
		{"id overflows", enc("9223372036854775808:Pallet jack")},
		{"id not a number", enc("x:Pallet jack")},
		{"NUL in name", enc("1:a\x00b")},
		{"invalid UTF-8 in name", enc("1:\xff")},
		{"name too long", enc("1:" + strings.Repeat("é", inventory.MaxNameLen+1))},
		{"non-zero padding bits", nonCanonical},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c, ok := decodeCursor(tt.cursor); ok {
				t.Errorf("decodeCursor(%q) = %+v, want it rejected", tt.cursor, c)
			}
		})
	}
}
