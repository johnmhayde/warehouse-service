package v1

import (
	"context"
	"encoding/base64"
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

// fakeUsers is an identity.Users serving identities by email.
type fakeUsers map[string]identity.Identity

func (f fakeUsers) LookupByEmail(ctx context.Context, email string) (identity.Identity, error) {
	id, ok := f[email]
	if !ok {
		return identity.Identity{}, fmt.Errorf("lookup %s: %w", email, identity.ErrUnknownUser)
	}
	return id, nil
}

// fakeProducts is a ProductService that returns canned results and records
// what it was called with.
type fakeProducts struct {
	product inventory.Product
	err     error

	// page is what ListProducts returns.
	page inventory.ProductPage

	called     bool
	gotActor   inventory.Actor
	gotProduct inventory.NewProduct
	gotID      int64
	gotFilter  inventory.ProductFilter
}

func (f *fakeProducts) ListProducts(ctx context.Context, actor inventory.Actor, filter inventory.ProductFilter) (inventory.ProductPage, error) {
	f.called = true
	f.gotActor = actor
	f.gotFilter = filter
	return f.page, f.err
}

func (f *fakeProducts) GetProduct(ctx context.Context, actor inventory.Actor, id int64) (inventory.Product, error) {
	f.called = true
	f.gotActor = actor
	f.gotID = id
	return f.product, f.err
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
	Register(mux, products, fakeUsers{})

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
	tests := []struct {
		name        string
		body        string
		product     inventory.Product
		wantRequest inventory.NewProduct
		wantJSON    string
	}{
		{
			name: "all fields",
			body: `{"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling"}`,
			product: inventory.Product{
				ID: 42, SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling",
				Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling"},
			wantJSON: `{"id":42,"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling",` +
				`"total_quantity":0,"active":true,` +
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
				`"total_quantity":0,"active":true,` +
				`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`,
		},
		{
			name: "initial stock",
			body: `{"sku":"123456","name":"Folding chair","total_quantity":120}`,
			product: inventory.Product{
				ID: 8, SKU: "123456", Name: "Folding chair", TotalQuantity: 120, Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "123456", Name: "Folding chair", TotalQuantity: 120},
			wantJSON: `{"id":8,"sku":"123456","name":"Folding chair","description":"","category":null,` +
				`"total_quantity":120,"active":true,` +
				`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`,
		},
		{
			name: "zero initial stock",
			body: `{"sku":"123456","name":"Folding chair","total_quantity":0}`,
			product: inventory.Product{
				ID: 9, SKU: "123456", Name: "Folding chair", Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "123456", Name: "Folding chair"},
			wantJSON: `{"id":9,"sku":"123456","name":"Folding chair","description":"","category":null,` +
				`"total_quantity":0,"active":true,` +
				`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`,
		},
		{
			name: "null initial stock",
			body: `{"sku":"123456","name":"Folding chair","total_quantity":null}`,
			product: inventory.Product{
				ID: 10, SKU: "123456", Name: "Folding chair", Active: true, CreatedAt: created, UpdatedAt: created,
			},
			wantRequest: inventory.NewProduct{SKU: "123456", Name: "Folding chair"},
			wantJSON: `{"id":10,"sku":"123456","name":"Folding chair","description":"","category":null,` +
				`"total_quantity":0,"active":true,` +
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
				"user.id":                slog.Int64Value(1),
				"product.sku":            slog.StringValue(string(tt.product.SKU)),
				"product.total_quantity": slog.IntValue(tt.product.TotalQuantity),
				"product.id":             slog.Int64Value(tt.product.ID),
				"product.outcome":        slog.StringValue("created"),
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
		{"unknown field", &adminID, `{"sku":"123456","name":"Pallet jack","quantity":10}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"total quantity wrong type", &adminID, `{"sku":"123456","name":"Pallet jack","total_quantity":"10"}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"fractional total quantity", &adminID, `{"sku":"123456","name":"Pallet jack","total_quantity":1.5}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"total quantity overflows", &adminID, `{"sku":"123456","name":"Pallet jack","total_quantity":1e30}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"removed field", &adminID, `{"sku":"123456","name":"Pallet jack","reorder_threshold":5}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
		{"wrong type", &adminID, `{"sku":"123456","name":"Pallet jack","category":5}`, nil, http.StatusBadRequest, "invalid request body", "invalid", false},
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
			if _, ok := attrs["product.total_quantity"]; ok {
				t.Error("event has product.total_quantity for a failed request")
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

func (stubRepository) CreateProduct(ctx context.Context, actorID int64, p inventory.NewProduct) (inventory.Product, error) {
	return inventory.Product{ID: 1, SKU: p.SKU, Name: p.Name, TotalQuantity: p.TotalQuantity, Active: true}, nil
}

// GetProduct returns product 1, which is active, and product 2, which is
// inactive.
func (stubRepository) GetProduct(ctx context.Context, id int64) (inventory.Product, error) {
	switch id {
	case 1:
		return inventory.Product{ID: 1, SKU: "123456", Name: "Pallet jack", Active: true}, nil
	case 2:
		return inventory.Product{ID: 2, SKU: "654321", Name: "Old pallet jack", Active: false}, nil
	}
	return inventory.Product{}, fmt.Errorf("select product: %w", inventory.ErrNotFound)
}

// ListProducts lists the products GetProduct serves, in (name, id) order,
// the way the store does.
func (r stubRepository) ListProducts(ctx context.Context, f inventory.ProductFilter) ([]inventory.Product, error) {
	var out []inventory.Product
	for _, id := range []int64{2, 1} { // "Old pallet jack" < "Pallet jack"
		p, _ := r.GetProduct(ctx, id)
		if !p.Active && !f.IncludeInactive {
			continue
		}
		if f.After != nil && (p.Name < f.After.Name || p.Name == f.After.Name && p.ID <= f.After.ID) {
			continue
		}
		out = append(out, p)
	}
	return out[:min(f.Limit, len(out))], nil
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
		{"largest total quantity", adminID, `{"sku":"123456","name":"x","total_quantity":2147483647}`, http.StatusCreated, ""},
		{"negative total quantity", adminID, `{"sku":"123456","name":"x","total_quantity":-1}`, http.StatusBadRequest, "total_quantity must be between 0 and 2147483647"},
		{"total quantity too large", adminID, `{"sku":"123456","name":"x","total_quantity":2147483648}`, http.StatusBadRequest, "total_quantity must be between 0 and 2147483647"},
		{"requester with negative total quantity", requesterID, `{"sku":"123456","name":"x","total_quantity":-1}`, http.StatusForbidden, "admin role required"},
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

// TestRoutesResolveDevUser checks that Register puts identity.DevHeader in
// front of every v1 route.
func TestRoutesResolveDevUser(t *testing.T) {
	users := fakeUsers{"admin@example.com": adminID}
	tests := []struct {
		name, method, path, body string
	}{
		{"get product", http.MethodGet, "/api/v1/products/42", ""},
		{"list products", http.MethodGet, "/api/v1/products", ""},
		{"create product", http.MethodPost, "/api/v1/admin/products", `{"sku":"123456","name":"Pallet jack"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name+" unknown user", func(t *testing.T) {
			mux := http.NewServeMux()
			Register(mux, &fakeProducts{}, users)
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
			req.Header.Set(identity.DevUserHeader, "nobody@example.com")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			if got := errorBody(t, rec); got != "unknown dev user" {
				t.Errorf("error = %q, want %q", got, "unknown dev user")
			}
		})
	}

	t.Run("create product known user", func(t *testing.T) {
		products := &fakeProducts{product: inventory.Product{ID: 1, SKU: "123456"}}
		mux := http.NewServeMux()
		Register(mux, products, users)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", strings.NewReader(`{"sku":"123456","name":"Pallet jack"}`))
		req.Header.Set(identity.DevUserHeader, "admin@example.com")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d, want %d", rec.Code, http.StatusCreated)
		}
		if want := (inventory.Actor{UserID: 1, Admin: true}); products.gotActor != want {
			t.Errorf("service called with actor %+v, want %+v", products.gotActor, want)
		}
	})
}

// sendGet sends GET path through a mux with the v1 routes registered.
// id, if not nil, is put in the request context the way identity.DevHeader
// does. It returns the response and the request's telemetry event.
func sendGet(t *testing.T, products ProductService, id *identity.Identity, path string) (*httptest.ResponseRecorder, *telemetry.Event) {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, products, fakeUsers{})

	req := httptest.NewRequest(http.MethodGet, path, nil)
	ctx, event := telemetry.NewContext(req.Context())
	if id != nil {
		ctx = identity.With(ctx, *id)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req.WithContext(ctx))
	return rec, event
}

func TestGetProductFound(t *testing.T) {
	created := time.Date(2026, 10, 21, 21, 14, 50, 123456000, time.FixedZone("EDT", -4*60*60))
	products := &fakeProducts{product: inventory.Product{
		ID: 42, SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling",
		TotalQuantity: 140, Active: true, CreatedAt: created, UpdatedAt: created,
	}}

	rec, event := sendGet(t, products, &requesterID, "/api/v1/products/42")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	wantJSON := `{"id":42,"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling",` +
		`"total_quantity":140,"active":true,` +
		`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`
	if got := strings.TrimSpace(rec.Body.String()); got != wantJSON {
		t.Errorf("body =\n%s\nwant\n%s", got, wantJSON)
	}
	if products.gotID != 42 {
		t.Errorf("service called with id %d, want 42", products.gotID)
	}
	if want := (inventory.Actor{UserID: 2}); products.gotActor != want {
		t.Errorf("service called with actor %+v, want %+v", products.gotActor, want)
	}

	attrs := eventAttrs(t, event)
	want := map[string]slog.Value{
		"user.id":         slog.Int64Value(2),
		"product.id":      slog.Int64Value(42),
		"product.sku":     slog.StringValue("000123"),
		"product.outcome": slog.StringValue("found"),
	}
	if len(attrs) != len(want) {
		t.Errorf("event attrs = %v, want %v", attrs, want)
	}
	for k, v := range want {
		if !attrs[k].Equal(v) {
			t.Errorf("event %s = %v, want %v", k, attrs[k], v)
		}
	}
}

func TestGetProductErrors(t *testing.T) {
	tests := []struct {
		name        string
		id          *identity.Identity
		path        string
		err         error
		wantStatus  int
		wantError   string
		wantOutcome string
		// wantID is set when the id parsed, so the service was called and
		// product.id is on the event.
		wantID bool
		// wantHidden is set when product.hidden should be on the event.
		wantHidden bool
	}{
		{"not a number", &requesterID, "/api/v1/products/abc", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"SKU instead of id", &requesterID, "/api/v1/products/SKU-00921", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"zero", &requesterID, "/api/v1/products/0", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"negative", &requesterID, "/api/v1/products/-1", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"plus sign", &requesterID, "/api/v1/products/+1", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"fraction", &requesterID, "/api/v1/products/1.5", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"space", &requesterID, "/api/v1/products/%201", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"hex", &requesterID, "/api/v1/products/0x1", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"fullwidth digit", &requesterID, "/api/v1/products/%EF%BC%91", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"overflows bigint", &requesterID, "/api/v1/products/9223372036854775808", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
		{"not found", &requesterID, "/api/v1/products/42", inventory.ErrNotFound, http.StatusNotFound, "product not found", "not_found", true, false},
		{"wrapped not found", &adminID, "/api/v1/products/42", fmt.Errorf("select product: %w", inventory.ErrNotFound), http.StatusNotFound, "product not found", "not_found", true, false},
		{"store failure", &requesterID, "/api/v1/products/42", errors.New("db down: connection refused"), http.StatusInternalServerError, "internal error", "error", true, false},
		// Same response as not found; only the event tells them apart.
		{"hidden", &requesterID, "/api/v1/products/42", inventory.ErrHidden, http.StatusNotFound, "product not found", "not_found", true, true},
		{"wrapped hidden", &requesterID, "/api/v1/products/42", fmt.Errorf("get: %w", inventory.ErrHidden), http.StatusNotFound, "product not found", "not_found", true, true},
		{"anonymous, hidden", nil, "/api/v1/products/42", inventory.ErrHidden, http.StatusNotFound, "product not found", "not_found", true, true},
		{"anonymous, invalid id", nil, "/api/v1/products/abc", nil, http.StatusBadRequest, "invalid product id", "invalid", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := &fakeProducts{err: tt.err}

			rec, event := sendGet(t, products, tt.id, tt.path)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := errorBody(t, rec); got != tt.wantError {
				t.Errorf("error = %q, want %q", got, tt.wantError)
			}
			if products.called != tt.wantID {
				t.Errorf("service called = %v, want %v", products.called, tt.wantID)
			}

			attrs := eventAttrs(t, event)
			if got := attrs["product.outcome"].String(); got != tt.wantOutcome {
				t.Errorf("event product.outcome = %q, want %q", got, tt.wantOutcome)
			}
			if got, ok := attrs["product.id"]; ok != tt.wantID || (ok && got.Int64() != 42) {
				t.Errorf("event product.id = %v (present %v), want 42 present %v", got, ok, tt.wantID)
			}
			if _, ok := attrs["product.sku"]; ok {
				t.Error("event has product.sku for a failed request")
			}
			if got, ok := attrs["product.hidden"]; ok != tt.wantHidden || (ok && !got.Bool()) {
				t.Errorf("event product.hidden = %v (present %v), want present %v and true", got, ok, tt.wantHidden)
			}
			if got, ok := attrs["user.id"]; ok != (tt.id != nil) || (ok && got.Int64() != tt.id.UserID) {
				t.Errorf("event user.id = %v (present %v), want the caller's id", got, ok)
			}
		})
	}
}

// TestGetProductAnonymous checks that the route needs no identity: the
// service is called with the zero Actor and the event has no user.id.
func TestGetProductAnonymous(t *testing.T) {
	products := &fakeProducts{product: inventory.Product{ID: 42, SKU: "000123", Name: "Pallet jack", Active: true}}

	rec, event := sendGet(t, products, nil, "/api/v1/products/42")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body)
	}
	if !products.called || products.gotActor != (inventory.Actor{}) {
		t.Errorf("service called = %v with actor %+v, want the zero Actor", products.called, products.gotActor)
	}
	attrs := eventAttrs(t, event)
	if _, ok := attrs["user.id"]; ok {
		t.Error("event has user.id for an anonymous request")
	}
	if got := attrs["product.outcome"].String(); got != "found" {
		t.Errorf("event product.outcome = %q, want found", got)
	}
}

// TestGetProductLeadingZeros pins that ids with leading zeros are accepted
// and mean the same product: /products/007 is product 7.
func TestGetProductLeadingZeros(t *testing.T) {
	products := &fakeProducts{product: inventory.Product{ID: 7, SKU: "123456", Active: true}}

	rec, event := sendGet(t, products, &requesterID, "/api/v1/products/007")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if products.gotID != 7 {
		t.Errorf("service called with id %d, want 7", products.gotID)
	}
	if got := eventAttrs(t, event)["product.id"]; !got.Equal(slog.Int64Value(7)) {
		t.Errorf("event product.id = %v, want 7", got)
	}
}

func TestGetProductLargestID(t *testing.T) {
	products := &fakeProducts{product: inventory.Product{ID: 9223372036854775807, SKU: "123456", Active: true}}

	rec, _ := sendGet(t, products, &requesterID, "/api/v1/products/9223372036854775807")

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if products.gotID != 9223372036854775807 {
		t.Errorf("service called with id %d, want the largest int64", products.gotID)
	}
}

func TestGetProductDoesNotLeakInternalErrors(t *testing.T) {
	rec, event := sendGet(t, &fakeProducts{err: errors.New("db down: connection refused")}, &requesterID, "/api/v1/products/42")

	if strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("response body leaks internal error: %s", rec.Body.String())
	}
	if got := eventAttrs(t, event)["error.message"].String(); !strings.Contains(got, "db down") {
		t.Errorf("event error.message = %q, want the internal error", got)
	}
}

// TestGetProductWithService runs the handler against the real
// inventory.ProductService, so inactive products are hidden the way clients
// actually see it.
func TestGetProductWithService(t *testing.T) {
	tests := []struct {
		name       string
		id         *identity.Identity
		path       string
		wantStatus int
		wantHidden bool
	}{
		{"requester, active", &requesterID, "/api/v1/products/1", http.StatusOK, false},
		{"admin, active", &adminID, "/api/v1/products/1", http.StatusOK, false},
		{"requester, inactive", &requesterID, "/api/v1/products/2", http.StatusNotFound, true},
		{"admin, inactive", &adminID, "/api/v1/products/2", http.StatusOK, false},
		{"requester, unknown", &requesterID, "/api/v1/products/3", http.StatusNotFound, false},
		{"admin, unknown", &adminID, "/api/v1/products/3", http.StatusNotFound, false},
		{"anonymous, active", nil, "/api/v1/products/1", http.StatusOK, false},
		{"anonymous, inactive", nil, "/api/v1/products/2", http.StatusNotFound, true},
		{"anonymous, unknown", nil, "/api/v1/products/3", http.StatusNotFound, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, event := sendGet(t, inventory.NewProductService(stubRepository{}), tt.id, tt.path)

			if _, ok := eventAttrs(t, event)["product.hidden"]; ok != tt.wantHidden {
				t.Errorf("event has product.hidden = %v, want %v", ok, tt.wantHidden)
			}

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusNotFound {
				if got := errorBody(t, rec); got != "product not found" {
					t.Errorf("error = %q, want %q", got, "product not found")
				}
			}
		})
	}
}

func TestListProductsListed(t *testing.T) {
	created := time.Date(2026, 10, 21, 21, 14, 50, 123456000, time.FixedZone("EDT", -4*60*60))
	jack := inventory.Product{
		ID: 42, SKU: "000123", Name: "Pallet jack", Description: "Manual, 2500 kg", Category: "Handling",
		TotalQuantity: 140, Active: true, CreatedAt: created, UpdatedAt: created,
	}
	wrap := inventory.Product{ID: 7, SKU: "123456", Name: "Shrink wrap", Active: true, CreatedAt: created, UpdatedAt: created}
	jackJSON := `{"id":42,"sku":"000123","name":"Pallet jack","description":"Manual, 2500 kg","category":"Handling",` +
		`"total_quantity":140,"active":true,` +
		`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`
	wrapJSON := `{"id":7,"sku":"123456","name":"Shrink wrap","description":"","category":null,` +
		`"total_quantity":0,"active":true,` +
		`"created_at":"2026-10-22T01:14:50.123456Z","updated_at":"2026-10-22T01:14:50.123456Z"}`
	afterJack := inventory.ProductCursor{Name: "Pallet jack", ID: 42}

	tests := []struct {
		name       string
		id         *identity.Identity
		query      string
		page       inventory.ProductPage
		wantActor  inventory.Actor
		wantFilter inventory.ProductFilter
		wantJSON   string
		// wantAttrs are the event attributes besides user.id and
		// product.outcome.
		wantAttrs map[string]slog.Value
	}{
		{
			name:       "no parameters, anonymous",
			page:       inventory.ProductPage{Products: []inventory.Product{jack, wrap}},
			wantFilter: inventory.ProductFilter{Limit: 50},
			wantJSON:   `{"items":[` + jackJSON + `,` + wrapJSON + `],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(50), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.count": slog.IntValue(2), "product.has_more": slog.BoolValue(false),
			},
		},
		{
			name:       "empty page",
			id:         &requesterID,
			wantActor:  inventory.Actor{UserID: 2},
			wantFilter: inventory.ProductFilter{Limit: 50},
			wantJSON:   `{"items":[],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(50), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.count": slog.IntValue(0), "product.has_more": slog.BoolValue(false),
			},
		},
		{
			name:       "next page",
			id:         &requesterID,
			query:      "limit=1",
			page:       inventory.ProductPage{Products: []inventory.Product{jack}, Next: &afterJack},
			wantActor:  inventory.Actor{UserID: 2},
			wantFilter: inventory.ProductFilter{Limit: 1},
			wantJSON:   `{"items":[` + jackJSON + `],"next_cursor":"` + encodeCursor(afterJack) + `"}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(1), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.count": slog.IntValue(1), "product.has_more": slog.BoolValue(true),
			},
		},
		{
			name:       "every parameter",
			id:         &adminID,
			query:      "q=+Jack+&category=%20Handling&limit=100&include_inactive=true&cursor=" + encodeCursor(afterJack),
			page:       inventory.ProductPage{Products: []inventory.Product{wrap}},
			wantActor:  inventory.Actor{UserID: 1, Admin: true},
			wantFilter: inventory.ProductFilter{Query: " Jack ", Category: " Handling", IncludeInactive: true, Limit: 100, After: &afterJack},
			wantJSON:   `{"items":[` + wrapJSON + `],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(100), "product.include_inactive": slog.BoolValue(true),
				"product.has_cursor": slog.BoolValue(true), "product.query_length": slog.IntValue(4),
				"product.category": slog.StringValue("Handling"),
				"product.count":    slog.IntValue(1), "product.has_more": slog.BoolValue(false),
			},
		},
		{
			name:       "include_inactive=false, blank q and category",
			id:         &requesterID,
			query:      "include_inactive=false&q=%20&category=",
			wantActor:  inventory.Actor{UserID: 2},
			wantFilter: inventory.ProductFilter{Query: " ", Limit: 50},
			wantJSON:   `{"items":[],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(50), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.count": slog.IntValue(0), "product.has_more": slog.BoolValue(false),
			},
		},
		{
			name:       "unknown parameters are ignored",
			query:      "sort=sku&page=2",
			wantFilter: inventory.ProductFilter{Limit: 50},
			wantJSON:   `{"items":[],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(50), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.count": slog.IntValue(0), "product.has_more": slog.BoolValue(false),
			},
		},
		{
			name:       "query length counts characters",
			query:      "q=%C3%A9t%C3%A9",
			wantFilter: inventory.ProductFilter{Query: "été", Limit: 50},
			wantJSON:   `{"items":[],"next_cursor":null}`,
			wantAttrs: map[string]slog.Value{
				"product.limit": slog.IntValue(50), "product.include_inactive": slog.BoolValue(false),
				"product.has_cursor": slog.BoolValue(false), "product.query_length": slog.IntValue(3),
				"product.count": slog.IntValue(0), "product.has_more": slog.BoolValue(false),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := &fakeProducts{page: tt.page}

			rec, event := sendGet(t, products, tt.id, "/api/v1/products?"+tt.query)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tt.wantJSON {
				t.Errorf("body =\n%s\nwant\n%s", got, tt.wantJSON)
			}
			if products.gotActor != tt.wantActor {
				t.Errorf("service called with actor %+v, want %+v", products.gotActor, tt.wantActor)
			}
			if !reflect.DeepEqual(products.gotFilter, tt.wantFilter) {
				t.Errorf("service called with %+v, want %+v", products.gotFilter, tt.wantFilter)
			}

			attrs := eventAttrs(t, event)
			want := map[string]slog.Value{"product.outcome": slog.StringValue("listed")}
			if tt.id != nil {
				want["user.id"] = slog.Int64Value(tt.id.UserID)
			}
			for k, v := range tt.wantAttrs {
				want[k] = v
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

func TestListProductsErrors(t *testing.T) {
	tests := []struct {
		name        string
		id          *identity.Identity
		query       string
		err         error
		wantStatus  int
		wantError   string
		wantOutcome string
		// wantCalled is set when the parameters parsed, so the service was
		// called.
		wantCalled bool
	}{
		{"limit not a number", &requesterID, "limit=ten", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"limit empty", &requesterID, "limit=", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"limit fraction", &requesterID, "limit=1.5", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"limit overflows", &requesterID, "limit=99999999999999999999", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"limit with space", &requesterID, "limit=%2010", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"include_inactive yes", &adminID, "include_inactive=yes", nil, http.StatusBadRequest, "invalid include_inactive", "invalid", false},
		{"include_inactive 1", &adminID, "include_inactive=1", nil, http.StatusBadRequest, "invalid include_inactive", "invalid", false},
		{"include_inactive TRUE", &adminID, "include_inactive=TRUE", nil, http.StatusBadRequest, "invalid include_inactive", "invalid", false},
		{"include_inactive empty", &adminID, "include_inactive", nil, http.StatusBadRequest, "invalid include_inactive", "invalid", false},
		{"cursor empty", &requesterID, "cursor=", nil, http.StatusBadRequest, "invalid cursor", "invalid", false},
		{"cursor not base64", &requesterID, "cursor=!!!", nil, http.StatusBadRequest, "invalid cursor", "invalid", false},
		{"cursor malformed", &requesterID, "cursor=" + base64.RawURLEncoding.EncodeToString([]byte("Pallet jack")), nil, http.StatusBadRequest, "invalid cursor", "invalid", false},
		{"bad escape", &requesterID, "q=%zz", nil, http.StatusBadRequest, "invalid query string", "invalid", false},
		{"semicolon", &requesterID, "q=a;b", nil, http.StatusBadRequest, "invalid query string", "invalid", false},
		{"anonymous, bad limit", nil, "limit=x", nil, http.StatusBadRequest, "invalid limit", "invalid", false},
		{"validation error", &requesterID, "limit=0", &inventory.ValidationError{Field: "limit", Reason: "must be between 1 and 100"}, http.StatusBadRequest, "limit must be between 1 and 100", "invalid", true},
		{"forbidden", &requesterID, "include_inactive=true", inventory.ErrForbidden, http.StatusForbidden, "admin role required", "forbidden", true},
		{"anonymous, forbidden", nil, "include_inactive=true", inventory.ErrForbidden, http.StatusForbidden, "admin role required", "forbidden", true},
		{"store failure", &requesterID, "", errors.New("db down: connection refused"), http.StatusInternalServerError, "internal error", "error", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			products := &fakeProducts{err: tt.err}

			rec, event := sendGet(t, products, tt.id, "/api/v1/products?"+tt.query)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := errorBody(t, rec); got != tt.wantError {
				t.Errorf("error = %q, want %q", got, tt.wantError)
			}
			if products.called != tt.wantCalled {
				t.Errorf("service called = %v, want %v", products.called, tt.wantCalled)
			}

			attrs := eventAttrs(t, event)
			if got := attrs["product.outcome"].String(); got != tt.wantOutcome {
				t.Errorf("event product.outcome = %q, want %q", got, tt.wantOutcome)
			}
			for _, k := range []string{"product.count", "product.has_more", "product.category"} {
				if _, ok := attrs[k]; ok {
					t.Errorf("event has %s for a failed request", k)
				}
			}
			if _, ok := attrs["product.limit"]; ok != tt.wantCalled {
				t.Errorf("event has product.limit = %v, want %v", ok, tt.wantCalled)
			}
			if _, ok := attrs["error.message"]; ok != (tt.wantStatus == http.StatusInternalServerError) {
				t.Errorf("event has error.message = %v, want it only on a 500", ok)
			}
			if got, ok := attrs["user.id"]; ok != (tt.id != nil) || (ok && got.Int64() != tt.id.UserID) {
				t.Errorf("event user.id = %v (present %v), want the caller's id", got, ok)
			}
		})
	}
}

func TestListProductsDoesNotLeakInternalErrors(t *testing.T) {
	rec, event := sendGet(t, &fakeProducts{err: errors.New("db down: connection refused")}, &requesterID, "/api/v1/products")

	if strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("response body leaks internal error: %s", rec.Body.String())
	}
	if got := eventAttrs(t, event)["error.message"].String(); !strings.Contains(got, "db down") {
		t.Errorf("event error.message = %q, want the internal error", got)
	}
}

// TestListProductsWithService runs the handler against the real
// inventory.ProductService, so the role check, the filter rules and the
// paging are those clients actually see.
func TestListProductsWithService(t *testing.T) {
	tests := []struct {
		name       string
		id         *identity.Identity
		query      string
		wantStatus int
		// wantIDs are the ids listed on a 200; wantError the error otherwise.
		wantIDs   []int64
		wantError string
		wantNext  bool
	}{
		{"anonymous", nil, "", http.StatusOK, []int64{1}, "", false},
		{"requester", &requesterID, "", http.StatusOK, []int64{1}, "", false},
		{"admin, active only", &adminID, "", http.StatusOK, []int64{1}, "", false},
		{"admin, with inactive", &adminID, "include_inactive=true", http.StatusOK, []int64{2, 1}, "", false},
		{"admin, first page", &adminID, "include_inactive=true&limit=1", http.StatusOK, []int64{2}, "", true},
		{"admin, second page", &adminID, "include_inactive=true&limit=1&cursor=" + encodeCursor(inventory.ProductCursor{Name: "Old pallet jack", ID: 2}), http.StatusOK, []int64{1}, "", false},
		{"requester, with inactive", &requesterID, "include_inactive=true", http.StatusForbidden, nil, "admin role required", false},
		{"anonymous, with inactive", nil, "include_inactive=true", http.StatusForbidden, nil, "admin role required", false},
		// The role is checked before the filter rules...
		{"requester, with inactive and bad limit", &requesterID, "include_inactive=true&limit=0", http.StatusForbidden, nil, "admin role required", false},
		// ...but after the parameters are parsed.
		{"requester, with inactive and malformed limit", &requesterID, "include_inactive=true&limit=x", http.StatusBadRequest, nil, "invalid limit", false},
		{"limit 0", &requesterID, "limit=0", http.StatusBadRequest, nil, "limit must be between 1 and 100", false},
		{"negative limit", &requesterID, "limit=-1", http.StatusBadRequest, nil, "limit must be between 1 and 100", false},
		{"limit 101", &requesterID, "limit=101", http.StatusBadRequest, nil, "limit must be between 1 and 100", false},
		{"long query", &requesterID, "q=" + strings.Repeat("a", 201), http.StatusBadRequest, nil, "q must be at most 200 characters", false},
		{"NUL in query", &requesterID, "q=a%00b", http.StatusBadRequest, nil, "q must not contain NUL characters", false},
		{"long category", &requesterID, "category=" + strings.Repeat("a", 101), http.StatusBadRequest, nil, "category must be at most 100 characters", false},
		{"NUL in category", nil, "category=%00", http.StatusBadRequest, nil, "category must not contain NUL characters", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec, _ := sendGet(t, inventory.NewProductService(stubRepository{}), tt.id, "/api/v1/products?"+tt.query)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus != http.StatusOK {
				if got := errorBody(t, rec); got != tt.wantError {
					t.Errorf("error = %q, want %q", got, tt.wantError)
				}
				return
			}
			var body struct {
				Items []struct {
					ID int64 `json:"id"`
				} `json:"items"`
				NextCursor *string `json:"next_cursor"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			var ids []int64
			for _, it := range body.Items {
				ids = append(ids, it.ID)
			}
			if !reflect.DeepEqual(ids, tt.wantIDs) {
				t.Errorf("listed ids = %v, want %v", ids, tt.wantIDs)
			}
			if (body.NextCursor != nil) != tt.wantNext {
				t.Errorf("next_cursor = %v, want present %v", body.NextCursor, tt.wantNext)
			}
		})
	}
}
