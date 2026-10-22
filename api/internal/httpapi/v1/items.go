package v1

import (
	"errors"
	"net/http"

	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
)

type itemResponse struct {
	SKU      string `json:"sku"`
	Name     string `json:"name"`
	Quantity int    `json:"quantity"`
}

func (h *handler) getItem(w http.ResponseWriter, r *http.Request) {
	sku, err := inventory.ParseSKU(r.PathValue("sku"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid SKU")
		return
	}

	item, err := h.items.GetItem(r.Context(), sku)
	switch {
	case errors.Is(err, inventory.ErrNotFound):
		writeError(w, http.StatusNotFound, "item not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, itemResponse{
		SKU:      string(item.SKU),
		Name:     item.Name,
		Quantity: item.Quantity,
	})
}
