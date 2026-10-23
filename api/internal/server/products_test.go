package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
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
		// wantInBody, if set, must appear in the response body.
		wantInBody string
	}{
		{"admin creates", "admin@example.com", `{"sku":"123456","name":"Pallet jack","category":"Handling"}`, http.StatusCreated, `"total_quantity":0`},
		{"admin creates with stock", "admin@example.com", `{"sku":"222222","name":"Folding chair","total_quantity":12}`, http.StatusCreated, `"total_quantity":12`},
		{"duplicate SKU", "admin@example.com", `{"sku":"123456","name":"Another"}`, http.StatusConflict, ""},
		{"requester", "requester@example.com", `{"sku":"654321","name":"Shrink wrap"}`, http.StatusForbidden, ""},
		{"no identity", "", `{"sku":"654321","name":"Shrink wrap"}`, http.StatusUnauthorized, ""},
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
		if st.wantInBody != "" && !strings.Contains(rec.Body.String(), st.wantInBody) {
			t.Errorf("%s: body %s, want it to contain %s", st.name, rec.Body, st.wantInBody)
		}
	}

	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM products").Scan(&count); err != nil {
		t.Fatalf("count products: %v", err)
	}
	if count != 2 {
		t.Errorf("products = %d, want 2", count)
	}

	// Only the product created with stock has an adjustment, by the admin.
	var (
		sku, reason, actor          string
		total, delta, quantityAfter int
	)
	if err := pool.QueryRow(ctx,
		`SELECT count(*) OVER (), p.sku, a.delta, a.quantity_after, a.reason, u.email
		FROM stock_adjustments a JOIN products p ON p.id = a.product_id JOIN users u ON u.id = a.actor_id`,
	).Scan(&total, &sku, &delta, &quantityAfter, &reason, &actor); err != nil {
		t.Fatalf("query stock adjustments: %v", err)
	}
	if total != 1 {
		t.Errorf("stock adjustments = %d, want 1", total)
	}
	if sku != "222222" || delta != 12 || quantityAfter != 12 || reason != "Initial stock" || actor != "admin@example.com" {
		t.Errorf("stock adjustment = %s %d %d %q %s; want 222222 12 12 \"Initial stock\" admin@example.com", sku, delta, quantityAfter, reason, actor)
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
		{"no identity, active", "", activeID, http.StatusOK, `"sku":"000123","name":"Pallet jack"`},
		{"no identity, inactive", "", inactiveID, http.StatusNotFound, `{"error":"product not found"}`},
		{"unknown dev user", "nobody@example.com", activeID, http.StatusUnauthorized, `{"error":"unknown dev user"}`},
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

// TestListProductsEndToEnd sends requests through the server's full
// handler, wired the way cmd/api wires it, against a migrated database. It
// follows next_cursor from page to page the way a client would.
func TestListProductsEndToEnd(t *testing.T) {
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
	if _, err := pool.Exec(ctx,
		`INSERT INTO products (sku, name, category, active) VALUES
			('000001', 'Bolt', 'Fixings', true),
			('000002', 'Anchor', 'Fixings', true),
			('000003', 'Bolt', NULL, true),
			('000004', 'Clamp', 'Fixings', false),
			('000005', 'Washer', 'Fixings', true)`,
	); err != nil {
		t.Fatalf("insert products: %v", err)
	}

	logger := slog.New(slog.DiscardHandler)
	srv := New(config.Config{}, logger, health.New(logger),
		inventory.NewProductService(postgres.NewProducts(pool)),
		postgres.NewUsers(pool),
	)
	type page struct {
		Items []struct {
			SKU string `json:"sku"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	// list sends GET /api/v1/products?query and returns the status and,
	// on a 200, the page's SKUs and next cursor.
	list := func(t *testing.T, devUser, query string) (int, []string, *string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/products?"+query, nil)
		if devUser != "" {
			req.Header.Set(identity.DevUserHeader, devUser)
		}
		rec := httptest.NewRecorder()
		srv.http.Handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			return rec.Code, nil, nil
		}
		var p page
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("decode page: %v", err)
		}
		skus := []string{}
		for _, it := range p.Items {
			skus = append(skus, it.SKU)
		}
		return rec.Code, skus, p.NextCursor
	}

	tests := []struct {
		name       string
		devUser    string
		query      string
		wantStatus int
		// wantPages are the SKUs of each page, following next_cursor.
		wantPages [][]string
	}{
		{"anonymous", "", "", http.StatusOK, [][]string{{"000002", "000001", "000003", "000005"}}},
		{"requester, pages of 2", "requester@example.com", "limit=2", http.StatusOK, [][]string{{"000002", "000001"}, {"000003", "000005"}}},
		{"admin, pages of 3 with inactive", "admin@example.com", "limit=3&include_inactive=true", http.StatusOK,
			[][]string{{"000002", "000001", "000003"}, {"000004", "000005"}}},
		// "o" is in Anchor and both Bolts, but the second Bolt has no
		// category.
		{"filtered pages", "", "q=O&category=Fixings&limit=1", http.StatusOK, [][]string{{"000002"}, {"000001"}}},
		{"no matches", "", "q=forklift", http.StatusOK, [][]string{{}}},
		{"requester, with inactive", "requester@example.com", "include_inactive=true", http.StatusForbidden, nil},
		{"anonymous, with inactive", "", "include_inactive=true", http.StatusForbidden, nil},
		{"bad cursor", "", "cursor=nope", http.StatusBadRequest, nil},
		{"unknown dev user", "nobody@example.com", "", http.StatusUnauthorized, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			query := tt.query
			for i, want := range tt.wantPages {
				status, skus, next := list(t, tt.devUser, query)
				if status != http.StatusOK {
					t.Fatalf("page %d: status = %d, want 200", i, status)
				}
				if !reflect.DeepEqual(skus, want) {
					t.Errorf("page %d SKUs = %v, want %v", i, skus, want)
				}
				last := i == len(tt.wantPages)-1
				if (next == nil) != last {
					t.Fatalf("page %d next_cursor = %v, want one only before the last page", i, next)
				}
				if next != nil {
					query = tt.query + "&cursor=" + url.QueryEscape(*next)
				}
			}
			if tt.wantPages == nil {
				if status, _, _ := list(t, tt.devUser, query); status != tt.wantStatus {
					t.Errorf("status = %d, want %d", status, tt.wantStatus)
				}
			}
		})
	}
}
