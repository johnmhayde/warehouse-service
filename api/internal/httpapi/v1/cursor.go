package v1

import (
	"encoding/base64"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Page cursors are opaque to clients: base64url (unpadded) of
// "<id>:<name>", the last product of the previous page. Clients only pass
// back the next_cursor they were given, so the format can change.

// encodeCursor returns the next_cursor for c.
func encodeCursor(c inventory.ProductCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(c.ID, 10) + ":" + c.Name))
}

// decodeCursor parses a cursor made by encodeCursor. It rejects anything
// encodeCursor can't have produced from a stored product: a bad encoding,
// an id that isn't a positive integer, or a name that is empty, isn't
// UTF-8 or contains NUL (which Postgres text can't hold).
func decodeCursor(s string) (inventory.ProductCursor, bool) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return inventory.ProductCursor{}, false
	}
	idPart, name, ok := strings.Cut(string(b), ":")
	if !ok {
		return inventory.ProductCursor{}, false
	}
	id, ok := parseProductID(idPart)
	if !ok || name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) {
		return inventory.ProductCursor{}, false
	}
	return inventory.ProductCursor{Name: name, ID: id}, true
}
