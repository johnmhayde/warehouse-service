package health

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newChecker() *Checker {
	return New(slog.New(slog.DiscardHandler))
}

func get(t *testing.T, mux *http.ServeMux, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestLiveness(t *testing.T) {
	mux := http.NewServeMux()
	newChecker().Register(mux)

	if code := get(t, mux, "/healthz"); code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", code, http.StatusOK)
	}
}

func TestReadinessFollowsState(t *testing.T) {
	mux := http.NewServeMux()
	c := newChecker()
	c.Register(mux)

	steps := []struct {
		ready bool
		want  int
	}{
		{false, http.StatusServiceUnavailable}, // starting up
		{true, http.StatusOK},                  // serving
		{false, http.StatusServiceUnavailable}, // draining
	}
	for _, s := range steps {
		c.SetReady(s.ready)
		if code := get(t, mux, "/readyz"); code != s.want {
			t.Errorf("ready=%v: GET /readyz = %d, want %d", s.ready, code, s.want)
		}
	}
}

func TestReadinessRunsChecks(t *testing.T) {
	mux := http.NewServeMux()
	c := newChecker()
	var dbErr error
	c.AddCheck("postgres", func(context.Context) error { return dbErr })
	c.Register(mux)
	c.SetReady(true)

	if code := get(t, mux, "/readyz"); code != http.StatusOK {
		t.Errorf("healthy dependency: GET /readyz = %d, want %d", code, http.StatusOK)
	}

	dbErr = errors.New("connection refused")
	if code := get(t, mux, "/readyz"); code != http.StatusServiceUnavailable {
		t.Errorf("failing dependency: GET /readyz = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if code := get(t, mux, "/healthz"); code != http.StatusOK {
		t.Errorf("failing dependency: GET /healthz = %d, want %d (liveness ignores dependencies)", code, http.StatusOK)
	}
}
