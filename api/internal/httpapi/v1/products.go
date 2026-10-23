package v1

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

type createProductRequest struct {
	SKU           string `json:"sku"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Category      string `json:"category"`
	TotalQuantity int    `json:"total_quantity"`
}

type productResponse struct {
	ID          int64   `json:"id"`
	SKU         string  `json:"sku"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Category    *string `json:"category"`
	// TotalQuantity is the number of units the warehouse owns.
	TotalQuantity int       `json:"total_quantity"`
	Active        bool      `json:"active"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

func newProductResponse(p inventory.Product) productResponse {
	resp := productResponse{
		ID:            p.ID,
		SKU:           string(p.SKU),
		Name:          p.Name,
		Description:   p.Description,
		TotalQuantity: p.TotalQuantity,
		Active:        p.Active,
		CreatedAt:     p.CreatedAt.UTC(),
		UpdatedAt:     p.UpdatedAt.UTC(),
	}
	if p.Category != "" {
		resp.Category = &p.Category
	}
	return resp
}

// createProduct handles POST /api/v1/admin/products. The body is checked
// for shape (valid JSON, known fields, a well-formed SKU) before the
// service runs; the service then checks the admin role before the product
// rules, so a non-admin gets 403 for any well-formed request.
func (h *handler) createProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := identity.From(ctx)
	if !ok {
		addProductOutcome(ctx, "unauthenticated")
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	telemetry.Add(ctx, slog.Int64("user.id", id.UserID))

	var req createProductRequest
	if err := decodeJSON(w, r, &req); err != nil {
		addProductOutcome(ctx, "invalid")
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	sku, err := inventory.ParseSKU(req.SKU)
	if err != nil {
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, "invalid SKU")
		return
	}
	telemetry.Add(ctx, slog.String("product.sku", string(sku)))

	product, err := h.products.CreateProduct(ctx,
		inventory.Actor{UserID: id.UserID, Admin: id.IsAdmin()},
		inventory.NewProduct{
			SKU:           sku,
			Name:          req.Name,
			Description:   req.Description,
			Category:      req.Category,
			TotalQuantity: req.TotalQuantity,
		},
	)
	var invalid *inventory.ValidationError
	switch {
	case errors.As(err, &invalid):
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, invalid.Error())
		return
	case errors.Is(err, inventory.ErrForbidden):
		addProductOutcome(ctx, "forbidden")
		writeError(w, http.StatusForbidden, "admin role required")
		return
	case errors.Is(err, inventory.ErrDuplicateSKU):
		addProductOutcome(ctx, "duplicate_sku")
		writeError(w, http.StatusConflict, "product with this SKU already exists")
		return
	case err != nil:
		addProductOutcome(ctx, "error")
		// The client gets a generic message; the cause goes on the
		// request's event.
		telemetry.Add(ctx, slog.String("error.message", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	telemetry.Add(ctx,
		slog.Int64("product.id", product.ID),
		slog.Int("product.total_quantity", product.TotalQuantity),
	)
	addProductOutcome(ctx, "created")
	writeJSON(w, http.StatusCreated, newProductResponse(product))
}

// getProduct handles GET /api/v1/products/{id}. It needs no identity: a
// caller without one is the zero (anonymous, non-admin) Actor. The service
// hides inactive products from non-admins.
func (h *handler) getProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var actor inventory.Actor
	if id, ok := identity.From(ctx); ok {
		telemetry.Add(ctx, slog.Int64("user.id", id.UserID))
		actor = inventory.Actor{UserID: id.UserID, Admin: id.IsAdmin()}
	}

	productID, ok := parseProductID(r.PathValue("id"))
	if !ok {
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	telemetry.Add(ctx, slog.Int64("product.id", productID))

	product, err := h.products.GetProduct(ctx, actor, productID)
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		addProductOutcome(ctx, "not_found")
		// The client can't tell a hidden product from a missing one; the
		// event can.
		if errors.Is(err, inventory.ErrHidden) {
			telemetry.Add(ctx, slog.Bool("product.hidden", true))
		}
		writeError(w, http.StatusNotFound, "product not found")
		return
	case err != nil:
		addProductOutcome(ctx, "error")
		telemetry.Add(ctx, slog.String("error.message", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	telemetry.Add(ctx, slog.String("product.sku", string(product.SKU)))
	addProductOutcome(ctx, "found")
	writeJSON(w, http.StatusOK, newProductResponse(product))
}

// parseProductID parses a product id from a path: a positive decimal
// integer that fits in an int64 (products.id is a bigint). Only ASCII
// digits are accepted, so signs, spaces and hex are rejected.
func parseProductID(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// addProductOutcome records how a product request ended. Each request gets
// exactly one product.outcome.
func addProductOutcome(ctx context.Context, outcome string) {
	telemetry.Add(ctx, slog.String("product.outcome", outcome))
}
