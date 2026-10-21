package v1

import (
	"encoding/json"
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
