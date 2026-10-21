// Package health serves the liveness and readiness endpoints used by
// container runtimes and load balancers.
package health

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Checker tracks whether the service should receive traffic.
//
//   - /healthz (liveness) answers "is the process alive?" It returns 200 as
//     long as the server can respond at all.
//   - /readyz (readiness) answers "should traffic be sent here?" It returns
//     503 until startup finishes and again once shutdown begins, so load
//     balancers stop routing to an instance that is draining.
//
// Later, readiness will also check dependencies such as the database.
type Checker struct {
	ready atomic.Bool
}

// New returns a Checker that starts out not ready.
func New() *Checker {
	return &Checker{}
}

// SetReady marks the service as ready or not ready for traffic.
func (c *Checker) SetReady(ready bool) {
	c.ready.Store(ready)
}

// Register mounts the health endpoints on mux. They are deliberately
// unversioned: they are for infrastructure, not API clients.
func (c *Checker) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", c.handleLive)
	mux.HandleFunc("GET /readyz", c.handleReady)
}

type response struct {
	Status string `json:"status"`
}

func (c *Checker) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func (c *Checker) handleReady(w http.ResponseWriter, r *http.Request) {
	if !c.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, response{Status: "ready"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
