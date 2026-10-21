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

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

// Prefix is the path prefix every v1 route is served under.
const Prefix = "/api/v1"

// ItemService is what the v1 handlers need from the inventory domain.
type ItemService interface {
	GetItem(ctx context.Context, sku inventory.SKU) (inventory.Item, error)
}

type handler struct {
	items ItemService
}

// Register mounts the v1 routes on mux. Use full method + path patterns
func Register(mux *http.ServeMux, items ItemService) {
	h := &handler{items: items}
	mux.HandleFunc("GET "+Prefix+"/items/{sku}", h.getItem)
}
