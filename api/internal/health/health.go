// Package health serves the liveness and readiness endpoints used by
// container runtimes and load balancers.
package health

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// checkTimeout bounds how long /readyz waits on all dependency checks.
const checkTimeout = 2 * time.Second

// Checker tracks whether the service should receive traffic.
//
//   - /healthz (liveness) answers "is the process alive?" It returns 200 as
//     long as the server can respond at all. It never checks dependencies:
//     a database outage should take the instance out of rotation, not get
//     it restarted.
//   - /readyz (readiness) answers "should traffic be sent here?" It returns
//     503 until startup finishes, once shutdown begins, and whenever a
//     dependency check (such as the database ping) fails.
type Checker struct {
	logger *slog.Logger
	ready  atomic.Bool
	checks []check
}

type check struct {
	name string
	fn   func(context.Context) error
}

// New returns a Checker that starts out not ready. Failed dependency
// checks are logged to logger.
func New(logger *slog.Logger) *Checker {
	return &Checker{logger: logger}
}

// SetReady marks the service as ready or not ready for traffic.
func (c *Checker) SetReady(ready bool) {
	c.ready.Store(ready)
}

// AddCheck registers a dependency check that /readyz runs on every request.
// Call it while wiring up, before the server starts; it is not safe to call
// concurrently with requests.
func (c *Checker) AddCheck(name string, fn func(context.Context) error) {
	c.checks = append(c.checks, check{name: name, fn: fn})
}

// Register mounts the health endpoints on mux. They are deliberately
// unversioned: they are for infrastructure, not API clients.
func (c *Checker) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", c.handleLive)
	mux.HandleFunc("GET /readyz", c.handleReady)
}

type response struct {
	Status string `json:"status"`
	// Failed names the dependency check that failed, if any. The error
	// itself is only logged, not returned.
	Failed string `json:"failed,omitempty"`
}

func (c *Checker) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, response{Status: "ok"})
}

func (c *Checker) handleReady(w http.ResponseWriter, r *http.Request) {
	if !c.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, response{Status: "not ready"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
	defer cancel()
	for _, ch := range c.checks {
		if err := ch.fn(ctx); err != nil {
			c.logger.WarnContext(ctx, "readiness check failed", "check", ch.name, "error", err)
			writeJSON(w, http.StatusServiceUnavailable, response{Status: "not ready", Failed: ch.name})
			return
		}
	}
	writeJSON(w, http.StatusOK, response{Status: "ready"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
