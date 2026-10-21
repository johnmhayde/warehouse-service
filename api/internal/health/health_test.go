package health

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func get(t *testing.T, mux *http.ServeMux, path string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

func TestLiveness(t *testing.T) {
	mux := http.NewServeMux()
	New().Register(mux)

	if code := get(t, mux, "/healthz"); code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want %d", code, http.StatusOK)
	}
}

func TestReadinessFollowsState(t *testing.T) {
	mux := http.NewServeMux()
	c := New()
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
