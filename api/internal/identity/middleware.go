package identity

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

// DevUserHeader carries the email of the user a request is made as, in the
// POC identity mode.
const DevUserHeader = "X-Dev-User"

// DevHeader resolves the DevUserHeader email against users and stores the
// identity in the request context. A request without the header (or with an
// empty one) passes through with no identity; handlers that need one decide
// how to respond. An email that matches no active user gets a 401.
func DevHeader(users Users, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email := r.Header.Get(DevUserHeader)
		if email == "" {
			next.ServeHTTP(w, r)
			return
		}

		id, err := users.LookupByEmail(r.Context(), email)
		switch {
		case errors.Is(err, ErrUnknownUser):
			writeError(w, http.StatusUnauthorized, "unknown dev user")
			return
		case err != nil:
			// The client gets a generic message; the cause goes on the
			// request's event.
			telemetry.Add(r.Context(), slog.String("error.message", err.Error()))
			writeError(w, http.StatusInternalServerError, "internal error")
			return
		}

		next.ServeHTTP(w, r.WithContext(With(r.Context(), id)))
	})
}

// writeError writes the API's {"error": "..."} body. httpapi depends on
// identity, so identity can't share httpapi's helper.
func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
