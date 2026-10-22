package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

// fakeProducts is a ProductService that returns canned results and records
// what it was called with.
type fakeProducts struct {
	product inventory.Product
	err     error

	called     bool
	gotActor   inventory.Actor
	gotProduct inventory.NewProduct
}

func (f *fakeProducts) CreateProduct(ctx context.Context, actor inventory.Actor, p inventory.NewProduct) (inventory.Product, error) {
	f.called = true
	f.gotActor = actor
	f.gotProduct = p
	return f.product, f.err
}

var (
	adminID     = identity.Identity{UserID: 1, Role: identity.RoleAdmin}
	requesterID = identity.Identity{UserID: 2, Role: identity.RoleRequester}
)

// postProduct sends body to POST /api/v1/admin/products through a mux with
// the v1 routes registered. id, if not nil, is put in the request context
// the way identity.DevHeader does. It returns the response and the
// request's telemetry event.
func postProduct(t *testing.T, products ProductService, id *identity.Identity, body string) (*httptest.ResponseRecorder, *telemetry.Event) {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, &fakeItems{}, products)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx, event := telemetry.NewContext(req.Context())
	if id != nil {
		ctx = identity.With(ctx, *id)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec, event
}

// eventAttrs returns the event's attributes by key. It fails the test if a
// key was added more than once.
func eventAttrs(t *testing.T, e *telemetry.Event) map[string]slog.Value {
	t.Helper()
	m := make(map[string]slog.Value)
	for _, a := range e.Attrs() {
		if _, ok := m[a.Key]; ok {
			t.Errorf("event attribute %s added more than once", a.Key)
		}
		m[a.Key] = a.Value
	}
	return m
}

func errorBody(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body.Error
}

func TestCreateProductCreated(t *testing.T) {
	created := time.Date(2026, 10, 21, 21, 14, 50, 123456000, time.FixedZone("EDT", -4*60*60))
	threshold := 5
	tests := []struct {
		name        string
		body        string
		product     inventory.Product
		wantRequest inventory.NewProduct
		wantJSON    string
	}{
		{
			name: "all fields",
			body: `{"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling","reorder_threshold":5}`,
			product: inventory.Product{
				ID: 42, SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling",
				ReorderThreshold: &threshold, Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling", ReorderThreshold: &threshold},
			wantJSON: `{"id":42,"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling",` +
				`"total_quantity":0,"reorder_threshold":5,"active":true,` +
				`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`,
		},
		{
			name: "required fields only",
			body: `{"sku":"123456","name":"Shrink wrap"}`,
			product: inventory.Product{
				ID: 7, SKU: "123456", Name: "Shrink wrap", Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "123456", Name: "Shrink wrap"},
			wantJSON: `{"id":7,"sku":"123456","name":"Shrink wrap","description":"","category":null,` +
				`"total_quantity":0,"reorder_threshold":null,"active":true,` +
				`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := &fakeProducts{product: tt.product}

			rec, event := postProduct(t, products, &adminID, tt.body)

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantJSON {
				t.Errorf("body =\n%s\nwant\n%s", got, tt.wantJSON)
			}
			if want := (inventory.Actor{UserID: 1, Admin: true}); products.gotActor != want {
				t.Errorf("service called with actor %+v, want %+v", products.gotActor, want)
			}
			if !reflect.DeepEqual(products.gotProduct, tt.wantRequest) {
				t.Errorf("service called with %+v, want %+v", products.gotProduct, tt.wantRequest)
			}

			attrs := eventAttrs(t, event)
			want := map[string]slog.Value{
				"user.id":         slog.Int64Value(1),
				"product.sku":     slog.StringValue(string(tt.product.SKU)),
				"product.id":      slog.Int64Value(tt.product.ID),
				"product.outcome": slog.StringValue("created"),
			}
			if len(attrs) != len(want) {
				t.Errorf("event attrs = %v, want %v", attrs, want)
			}
			for k, v := range want {
				if !attrs[k].Equal(v) {
					t.Errorf("event %s = %v, want %v", k, attrs[k], v)
				}
			}
		})
	}
}

func TestCreateProductErrors(t *testing.T) {
	valid := `{"sku":"123456","name":"Pallet jack"}`
	tests := []struct {
		name        string
		id          *identity.Identity
		body        string
		err         error
		wantStatus  int
		wantError   string
		wantOutcome string
		// wantSKU is set when the SKU parsed, so product.sku is on the event.
		wantSKU bool
	}{
		{"no identity", nil, valid, nil, http.StatusUnauthorized, "authentication required", "unauthenticated", false},
		{"empty body", &adminID, ``, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"malformed JSON", &adminID, `{"sku":"123456",`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"not an object", &adminID, `["123456"]`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"unknown field", &adminID, `{"sku":"123456","name":"Pallet jack","total_quantity":10}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"wrong type", &adminID, `{"sku":"123456","name":"Pallet jack","reorder_threshold":"5"}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"fractional threshold", &adminID, `{"sku":"123456","name":"Pallet jack","reorder_threshold":1.5}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"second object", &adminID, valid + `{}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"trailing garbage", &adminID, valid + ` x`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"too large", &adminID, `{"sku":"123456","name":"` + strings.Repeat("a", maxBodyBytes) + `"}`, nil, http.StatusRequestEntityTooLarge, "request body too large", "invalid", false},
		{"missing SKU", &adminID, `{"name":"Pallet jack"}`, nil, http.StatusBadRequest, "invalid SKU", "invalid", false},
		{"invalid SKU", &adminID, `{"sku":"SKU-00921","name":"Pallet jack"}`, nil, http.StatusBadRequest, "invalid SKU", "invalid", false},
		{"numeric SKU", &adminID, `{"sku":123456,"name":"Pallet jack"}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"validation error", &adminID, valid, &inventory.ValidationError{Field: "name", Reason: "is required"}, http.StatusBadRequest, "name is required", "invalid", true},
		{"forbidden", &requesterID, valid, inventory.ErrForbidden, http.StatusForbidden, "admin role required", "forbidden", true},
		{"duplicate SKU", &adminID, valid, fmt.Errorf("insert: %w", inventory.ErrDuplicateSKU), http.StatusConflict, "product with this SKU already exists", "duplicate_sku", true},
		{"store failure", &adminID, valid, errors.New("db down: connection refused"), http.StatusInternalServerError, "internal error", "error", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := &fakeProducts{err: tt.err}

			rec, event := postProduct(t, products, tt.id, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := errorBody(t, rec); got != tt.wantError {
				t.Errorf("error = %q, want %q", got, tt.wantError)
			}
			if products.called != tt.wantSKU {
				t.Errorf("service called = %v, want %v", products.called, tt.wantSKU)
			}

			attrs := eventAttrs(t, event)
			if got := attrs["product.outcome"].String(); got != tt.wantOutcome {
				t.Errorf("event product.outcome = %q, want %q", got, tt.wantOutcome)
			}
			if _, ok := attrs["product.id"]; ok {
				t.Error("event has product.id for a failed request")
			}
			if _, ok := attrs["product.sku"]; ok != tt.wantSKU {
				t.Errorf("event has product.sku = %v, want %v", ok, tt.wantSKU)
			}
			if got, ok := attrs["user.id"]; ok != (tt.id != nil) || (ok && got.Int64() != tt.id.UserID) {
				t.Errorf("event user.id = %v (present %v), want the caller's id", got, ok)
			}
		})
	}
}

func TestCreateProductDoesNotLeakInternalErrors(t *testing.T) {
	rec, event := postProduct(t, &fakeProducts{err: errors.New("db down: connection refused")}, &adminID, `{"sku":"123456","name":"Pallet jack"}`)

	if strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("response body leaks internal error: %s", rec.Body.String())
	}
	if got := eventAttrs(t, event)["error.message"].String(); !strings.Contains(got, "db down") {
		t.Errorf("event error.message = %q, want the internal error", got)
	}
}

// stubRepository is an inventory.ProductRepository that accepts every
// product.
type stubRepository struct{}

func (stubRepository) CreateProduct(ctx context.Context, p inventory.NewProduct) (inventory.Product, error) {
	return inventory.Product{ID: 1, SKU: p.SKU, Name: p.Name, Active: true}, nil
}

// TestCreateProductWithService runs the handler against the real
// inventory.ProductService, so the order of the checks and the validation
// messages are those clients actually see.
func TestCreateProductWithService(t *testing.T) {
	tests := []struct {
		name       string
		id         identity.Identity
		body       string
		wantStatus int
		wantError  string
	}{
		{"admin", adminID, `{"sku":"123456","name":"Pallet jack"}`, http.StatusCreated, ""},
		{"requester", requesterID, `{"sku":"123456","name":"Pallet jack"}`, http.StatusForbidden, "admin role required"},
		// The role is checked before the product rules...
		{"requester with blank name", requesterID, `{"sku":"123456","name":" "}`, http.StatusForbidden, "admin role required"},
		// ...but after the body is decoded and the SKU parsed.
		{"requester with malformed body", requesterID, `{`, http.StatusBadRequest, "invalid request body"},
		{"requester with invalid SKU", requesterID, `{"sku":"12","name":"Pallet jack"}`, http.StatusBadRequest, "invalid SKU"},
		{"missing name", adminID, `{"sku":"123456"}`, http.StatusBadRequest, "name is required"},
		{"blank name", adminID, `{"sku":"123456","name":"  "}`, http.StatusBadRequest, "name is required"},
		{"long name", adminID, `{"sku":"123456","name":"` + strings.Repeat("a", 201) + `"}`, http.StatusBadRequest, "name must be at most 200 characters"},
		{"long description", adminID, `{"sku":"123456","name":"x","description":"` + strings.Repeat("a", 2001) + `"}`, http.StatusBadRequest, "description must be at most 2000 characters"},
		{"long category", adminID, `{"sku":"123456","name":"x","category":"` + strings.Repeat("a", 101) + `"}`, http.StatusBadRequest, "category must be at most 100 characters"},
		{"NUL in name", adminID, `{"sku":"123456","name":"a\u0000b"}`, http.StatusBadRequest, "name must not contain NUL characters"},
		{"negative threshold", adminID, `{"sku":"123456","name":"x","reorder_threshold":-1}`, http.StatusBadRequest, "reorder_threshold must be between 0 and 2147483647"},
		{"threshold too large", adminID, `{"sku":"123456","name":"x","reorder_threshold":2147483648}`, http.StatusBadRequest, "reorder_threshold must be between 0 and 2147483647"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, _ := postProduct(t, inventory.NewProductService(stubRepository{}), &tt.id, tt.body)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantError == "" {
				return
			}
			if got := errorBody(t, rec); got != tt.wantError {
				t.Errorf("error = %q, want %q", got, tt.wantError)
			}
		})
	}
}
