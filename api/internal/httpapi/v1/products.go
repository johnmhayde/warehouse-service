package v1

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

// updateProductRequest is a partial update: a field the body leaves out is
// unchanged.
type updateProductRequest struct {
	Name        optional[string] `json:"name"`
	Description optional[string] `json:"description"`
	// Category may be null, which clears it like "".
	Category optional[string] `json:"category"`
	// TotalQuantity is decoded only to refuse it with a specific message:
	// the owned count changes only through stock adjustments (ADR 0003).
	TotalQuantity optional[json.RawMessage] `json:"total_quantity"`
}

// toUpdate converts r to an inventory.ProductUpdate. A request that sets
// total_quantity, or sets name or description to null, gets a client-safe
// message instead.
func (r updateProductRequest) toUpdate() (inventory.ProductUpdate, string) {
	switch {
	case r.TotalQuantity.Set:
		return inventory.ProductUpdate{}, "total_quantity can't be changed here; use stock adjustments"
	case r.Name.Null:
		return inventory.ProductUpdate{}, "name must not be null"
	case r.Description.Null:
		return inventory.ProductUpdate{}, "description must not be null"
	}
	var u inventory.ProductUpdate
	if r.Name.Set {
		u.Name = &r.Name.Value
	}
	if r.Description.Set {
		u.Description = &r.Description.Value
	}
	if r.Category.Set {
		u.Category = &r.Category.Value
	}
	return u, ""
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

// productListResponse is one page of products. NextCursor is null on the
// last page.
type productListResponse struct {
	Items      []productResponse `json:"items"`
	NextCursor *string           `json:"next_cursor"`
}

// listProducts handles GET /api/v1/products. Like getProduct it needs no
// identity. The query parameters are parsed here (400 if one is
// malformed); the service then checks include_inactive against the role
// (403) before the filter rules (400).
func (h *handler) listProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var actor inventory.Actor
	if id, ok := identity.From(ctx); ok {
		telemetry.Add(ctx, slog.Int64("user.id", id.UserID))
		actor = inventory.Actor{UserID: id.UserID, Admin: id.IsAdmin()}
	}

	filter, err := parseProductFilter(r.URL.RawQuery)
	if err != nil {
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	telemetry.Add(ctx,
		slog.Int("product.limit", filter.Limit),
		slog.Bool("product.include_inactive", filter.IncludeInactive),
		slog.Bool("product.has_cursor", filter.After != nil),
	)
	// The length, not the text: search terms are free text from the client.
	if n := utf8.RuneCountInString(strings.TrimSpace(filter.Query)); n > 0 {
		telemetry.Add(ctx, slog.Int("product.query_length", n))
	}

	page, err := h.products.ListProducts(ctx, actor, filter)
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
	case err != nil:
		addProductOutcome(ctx, "error")
		telemetry.Add(ctx, slog.String("error.message", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	resp := productListResponse{Items: make([]productResponse, 0, len(page.Products))}
	for _, p := range page.Products {
		resp.Items = append(resp.Items, newProductResponse(p))
	}
	if page.Next != nil {
		next := encodeCursor(*page.Next)
		resp.NextCursor = &next
	}
	// The category passed validation, so it is short enough to record.
	if c := strings.TrimSpace(filter.Category); c != "" {
		telemetry.Add(ctx, slog.String("product.category", c))
	}
	telemetry.Add(ctx,
		slog.Int("product.count", len(resp.Items)),
		slog.Bool("product.has_more", page.Next != nil),
	)
	addProductOutcome(ctx, "listed")
	writeJSON(w, http.StatusOK, resp)
}

// parseProductFilter reads the list query parameters from rawQuery into a
// filter. It checks their form only: the query string must parse, limit
// must be an integer (DefaultProductLimit when absent), include_inactive
// "true" or "false", and cursor one this API returned. The service checks
// the values. Unknown parameters are ignored, and only the first value of
// a repeated one is used. The error's message is safe to send to the
// client.
func parseProductFilter(rawQuery string) (inventory.ProductFilter, error) {
	// url.URL.Query drops pairs it can't decode; a filter that silently
	// went missing would list more than the client asked for.
	q, err := url.ParseQuery(rawQuery)
	if err != nil {
		return inventory.ProductFilter{}, errors.New("invalid query string")
	}
	f := inventory.ProductFilter{
		Query:    q.Get("q"),
		Category: q.Get("category"),
		Limit:    inventory.DefaultProductLimit,
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil {
			return inventory.ProductFilter{}, errors.New("invalid limit")
		}
		f.Limit = n
	}
	if q.Has("include_inactive") {
		switch q.Get("include_inactive") {
		case "true":
			f.IncludeInactive = true
		case "false":
		default:
			return inventory.ProductFilter{}, errors.New("invalid include_inactive")
		}
	}
	if q.Has("cursor") {
		c, ok := decodeCursor(q.Get("cursor"))
		if !ok {
			return inventory.ProductFilter{}, errors.New("invalid cursor")
		}
		f.After = &c
	}
	return f, nil
}

// updateProduct handles PATCH /api/v1/admin/products/{id}. The path id and
// the body's shape (valid JSON, known fields, no total_quantity, no null
// name or description) are checked before the service runs; the service
// then checks the admin role before the product rules, so a non-admin gets
// 403 for any well-formed request.
func (h *handler) updateProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id, ok := identity.From(ctx)
	if !ok {
		addProductOutcome(ctx, "unauthenticated")
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	telemetry.Add(ctx, slog.Int64("user.id", id.UserID))

	productID, ok := parseProductID(r.PathValue("id"))
	if !ok {
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, "invalid product id")
		return
	}
	telemetry.Add(ctx, slog.Int64("product.id", productID))

	var req updateProductRequest
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
	update, msg := req.toUpdate()
	if msg != "" {
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, msg)
		return
	}

	product, err := h.products.UpdateProduct(ctx,
		inventory.Actor{UserID: id.UserID, Admin: id.IsAdmin()}, productID, update)
	var invalid *inventory.ValidationError
	switch {
	case errors.As(err, &invalid):
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, invalid.Error())
		return
	case errors.Is(err, inventory.ErrEmptyUpdate):
		addProductOutcome(ctx, "invalid")
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	case errors.Is(err, inventory.ErrForbidden):
		addProductOutcome(ctx, "forbidden")
		writeError(w, http.StatusForbidden, "admin role required")
		return
	case errors.Is(err, inventory.ErrNotFound):
		addProductOutcome(ctx, "not_found")
		writeError(w, http.StatusNotFound, "product not found")
		return
	case err != nil:
		addProductOutcome(ctx, "error")
		telemetry.Add(ctx, slog.String("error.message", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	telemetry.Add(ctx,
		slog.String("product.sku", string(product.SKU)),
		slog.String("product.updated_fields", strings.Join(update.Fields(), ",")),
	)
	addProductOutcome(ctx, "updated")
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
