package server

import (
	"context"
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
	"github.com/johnmhayde/warehouse-service/api/internal/store/memory"
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
		inventory.NewService(memory.New()),
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
