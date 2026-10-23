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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if c, ok := decodeCursor(tt.cursor); ok {
				t.Errorf("decodeCursor(%q) = %+v, want it rejected", tt.cursor, c)
			}
		})
	}
}
