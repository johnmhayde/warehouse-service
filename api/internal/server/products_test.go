package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/config"
	"github.com/johnmhayde/warehouse-service/api/internal/health"
	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
	"github.com/johnmhayde/warehouse-service/api/internal/store/postgres"
)

// TestCreateProductEndToEnd sends requests through the server's full
// handler, wired the way cmd/api wires it, against a migrated database.
func TestCreateProductEndToEnd(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	for _, u := range []struct{ email, role string }{
		{"admin@example.com", "admin"},
		{"requester@example.com", "requester"},
	} {
		if _, err := pool.Exec(ctx,
			"INSERT INTO users (email, display_name, role) VALUES ($1, $2, $3)",
			u.email, "Test user", u.role,
		); err != nil {
			t.Fatalf("insert user %s: %v", u.email, err)
		}
	}

	logger := slog.New(slog.DiscardHandler)
	srv := New(config.Config{}, logger, health.New(logger),
		inventory.NewProductService(postgres.NewProducts(pool)),
		postgres.NewUsers(pool),
	)

	// The steps share the database, so they run in order.
	steps := []struct {
		name       string
		devUser    string
		body       string
		wantStatus int
	}{
		{"admin creates", "admin@example.com", `{"sku":"123456","name":"Pallet jack","category":"Handling"}`, http.StatusCreated},
		{"duplicate SKU", "admin@example.com", `{"sku":"123456","name":"Another"}`, http.StatusConflict},
		{"requester", "requester@example.com", `{"sku":"654321","name":"Shrink wrap"}`, http.StatusForbidden},
		{"no identity", "", `{"sku":"654321","name":"Shrink wrap"}`, http.StatusUnauthorized},
	}
	for _, st := range steps {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/products", strings.NewReader(st.body))
		if st.devUser != "" {
			req.Header.Set(identity.DevUserHeader, st.devUser)
		}
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)

		if rec.Code != st.wantStatus {
			t.Errorf("%s: status = %d, want %d; body %s", st.name, rec.Code, st.wantStatus, rec.Body)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&count); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if count != 1 {
		t.Errorf("products = %d, want 1", count)
	}
}

// TestGetProductEndToEnd sends requests through the server's full handler,
// wired the way cmd/api wires it, against a migrated database.
func TestGetProductEndToEnd(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()
	for _, u := range []struct{ email, role string }{
		{"admin@example.com", "admin"},
		{"requester@example.com", "requester"},
	} {
		if _, err := pool.Exec(ctx,
			"INSERT INTO users (email, display_name, role) VALUES ($1, $2, $3)",
			u.email, "Test user", u.role,
		); err != nil {
			t.Fatalf("insert user %s: %v", u.email, err)
		}
	}
	var activeID, inactiveID int64
	if err := pool.QueryRow(ctx,
		"INSERT INTO products (sku, name, category) VALUES ('000123', 'Pallet jack', 'Handling') RETURNING id",
	).Scan(&activeID); err != nil {
		t.Fatalf("insert product: %v", err)
	}
	if err := pool.QueryRow(ctx,
		"INSERT INTO products (sku, name, active) VALUES ('654321', 'Old pallet jack', false) RETURNING id",
	).Scan(&inactiveID); err != nil {
		t.Fatalf("insert product: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	srv := New(config.Config{}, logger, health.New(logger),
		inventory.NewProductService(postgres.NewProducts(pool)),
		postgres.NewUsers(pool),
	)

	tests := []struct {
		name       string
		devUser    string
		id         int64
		wantStatus int
		wantBody   string
	}{
		{"requester, active", "requester@example.com", activeID, http.StatusOK, `"sku":"000123","name":"Pallet jack"`},
		{"requester, inactive", "requester@example.com", inactiveID, http.StatusNotFound, `{"error":"product not found"}`},
		{"admin, inactive", "admin@example.com", inactiveID, http.StatusOK, `"active":false`},
		{"unknown id", "admin@example.com", inactiveID + 1000, http.StatusNotFound, `{"error":"product not found"}`},
		{"no identity", "", activeID, http.StatusUnauthorized, `{"error":"authentication required"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/products/%d", tt.id), nil)
			if tt.devUser != "" {
				req.Header.Set(identity.DevUserHeader, tt.devUser)
			}
			rec := httptest.NewRecorder()
			srv.http.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tt.wantStatus, rec.Body)
			}
			if !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("body = %s, want it to contain %s", rec.Body, tt.wantBody)
			}
		})
	}
}
