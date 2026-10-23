// Package v1 holds the handlers and request/response types for version 1
// of the public HTTP API, served under /api/v1.
//
// Handlers here translate HTTP into calls on domain services (inventory,
// movement, ...) and back. Domain packages never import this package, so a
// future v2 can sit alongside it and reuse the same services.
package v1

import (
	"context"
	"net/http"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Prefix is the path prefix every v1 route is served under.
const Prefix = "/api/v1"

// ProductService is what the v1 handlers need from the product catalog.
type ProductService interface {
	CreateProduct(ctx context.Context, actor inventory.Actor, p inventory.NewProduct) (inventory.Product, error)
	GetProduct(ctx context.Context, actor inventory.Actor, id int64) (inventory.Product, error)
	ListProducts(ctx context.Context, actor inventory.Actor, f inventory.ProductFilter) (inventory.ProductPage, error)
	UpdateProduct(ctx context.Context, actor inventory.Actor, id int64, u inventory.ProductUpdate) (inventory.Product, error)
}

type handler struct {
	products ProductService
}

// Register mounts the v1 routes on mux. Use full method + path patterns.
// Routes that only admins may call go under Prefix+"/admin"; the service
// they call still makes the authorization decision.
//
// Every v1 route resolves the caller with identity.DevHeader, using users.
// It wraps each route rather than the whole mux so that /healthz and
// /readyz never look anyone up.
func Register(mux *http.ServeMux, products ProductService, users identity.Users) {
	h := &handler{products: products}
	handle := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, identity.DevHeader(users, fn))
	}
	handle("GET "+Prefix+"/products", h.listProducts)
	handle("GET "+Prefix+"/products/{id}", h.getProduct)
	handle("POST "+Prefix+"/admin/products", h.createProduct)
	handle("PATCH "+Prefix+"/admin/products/{id}", h.updateProduct)
}
