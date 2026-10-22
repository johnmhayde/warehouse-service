package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// fakeUsers is an identity.Users serving identities by email.
type fakeUsers map[string]identity.Identity

func (f fakeUsers) LookupByEmail(ctx context.Context, email string) (identity.Identity, error) {
	id, ok := f[email]
	if !ok {
		return identity.Identity{}, fmt.Errorf("lookup %s: %w", email, identity.ErrUnknownUser)
	}
	return id, nil
}

// fakeItems is an ItemService that returns canned results and records the
// SKU it was asked for.
type fakeItems struct {
	item inventory.Item
	err  error

	gotSKU inventory.SKU
}

func (f *fakeItems) GetItem(ctx context.Context, sku inventory.SKU) (inventory.Item, error) {
	f.gotSKU = sku
	return f.item, f.err
}

// serve sends a GET for path through a mux with the v1 routes registered,
// so routing and r.PathValue are exercised too.
func serve(t *testing.T, items ItemService, path string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, items, &fakeProducts{}, fakeUsers{})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestGetItemFound(t *testing.T) {
	items := &fakeItems{item: inventory.Item{SKU: "123456", Name: "Pallet jack", Quantity: 3}}

	rec := serve(t, items, "/api/v1/items/123456")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if items.gotSKU != "123456" {
		t.Errorf("service called with SKU %q, want %q", items.gotSKU, "123456")
	}

	var body itemResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	want := itemResponse{SKU: "123456", Name: "Pallet jack", Quantity: 3}
	if body != want {
		t.Errorf("body = %+v, want %+v", body, want)
	}
}

func TestGetItemErrors(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		err        error
		wantStatus int
		wantError  string
	}{
		{"invalid sku", "/api/v1/items/bad%20sku", nil, http.StatusBadRequest, "invalid SKU"},
		{"old-format sku", "/api/v1/items/SKU-00921", nil, http.StatusBadRequest, "invalid SKU"},
		{"five-digit sku", "/api/v1/items/12345", nil, http.StatusBadRequest, "invalid SKU"},
		{"seven-digit sku", "/api/v1/items/1234567", nil, http.StatusBadRequest, "invalid SKU"},
		{"not found", "/api/v1/items/123456", inventory.ErrNotFound, http.StatusNotFound, "item not found"},
		{"wrapped not found", "/api/v1/items/123456", wrap(inventory.ErrNotFound), http.StatusNotFound, "item not found"},
		{"store failure", "/api/v1/items/123456", errors.New("db down: connection refused"), http.StatusInternalServerError, "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := serve(t, &fakeItems{err: tt.err}, tt.path)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}

			var body struct {
				Error string `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error != tt.wantError {
				t.Errorf("error = %q, want %q", body.Error, tt.wantError)
			}
		})
	}
}

func TestGetItemDoesNotLeakInternalErrors(t *testing.T) {
	rec := serve(t, &fakeItems{err: errors.New("db down: connection refused")}, "/api/v1/items/123456")

	if strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("response body leaks internal error: %s", rec.Body.String())
	}
}

func TestGetItemInvalidSKUSkipsService(t *testing.T) {
	items := &fakeItems{}

	serve(t, items, "/api/v1/items/bad%20sku")

	if items.gotSKU != "" {
		t.Errorf("service called with %q for an invalid SKU", items.gotSKU)
	}
}

func wrap(err error) error {
	return errors.Join(errors.New("postgres: get item"), err)
}
