package v1

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// maxBodyBytes caps the size of a request body.
const maxBodyBytes = 64 << 10

// errTrailingData means the body held more than one JSON value.
var errTrailingData = errors.New("unexpected data after JSON value")

// decodeJSON decodes r's body, a single JSON value, into v. Fields that v
// doesn't have are an error, as is anything after the value. A body over
// maxBodyBytes returns an error wrapping *http.MaxBytesError.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errTrailingData
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
