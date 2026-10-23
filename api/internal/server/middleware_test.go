package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/config"
	"github.com/johnmhayde/warehouse-service/api/internal/health"
	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

// fakeUsers knows a single admin, admin@example.com.
type fakeUsers struct{}

func (fakeUsers) LookupByEmail(ctx context.Context, email string) (identity.Identity, error) {
	if email == "admin@example.com" {
		return identity.Identity{UserID: 1, Role: identity.RoleAdmin}, nil
	}
	return identity.Identity{}, fmt.Errorf("lookup %s: %w", email, identity.ErrUnknownUser)
}

// serveLogged sends req through the middleware chain around a mux with one
// route, GET /things/{id}, that resolves identity the way the v1 routes do
// and enriches the event. It returns the response and every log line
// written, decoded.
func serveLogged(t *testing.T, req *http.Request) (*httptest.ResponseRecorder, []map[string]any) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))

	mux := http.NewServeMux()
	mux.Handle("GET /things/{id}", identity.DevHeader(fakeUsers{}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telemetry.Add(r.Context(), slog.String("thing.id", r.PathValue("id")))
		if id, ok := identity.From(r.Context()); ok {
			telemetry.Add(r.Context(), slog.Int64("user.id", id.UserID))
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("ok"))
	})))

	rec := httptest.NewRecorder()
	handler(logger, mux).ServeHTTP(rec, req)

	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		lines = append(lines, m)
	}
	return rec, lines
}

func TestLogRequests(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		devUser    string
		wantStatus int
		// want lists fields the log line must have; absent lists fields it
		// must not.
		want   map[string]any
		absent []string
	}{
		{
			name:       "handler enriches event",
			path:       "/things/42",
			wantStatus: http.StatusAccepted,
			want: map[string]any{
				"msg":                 "http request",
				"http.method":         "GET",
				"http.route":          "GET /things/{id}",
				"http.path":           "/things/42",
				"http.status":         float64(http.StatusAccepted),
				"http.response_bytes": float64(2),
				"thing.id":            "42",
			},
			absent: []string{"user.id"},
		},
		{
			// identity.DevHeader hands the route a copy of the request, so
			// this checks the identity still reaches the event.
			name:       "with identity",
			path:       "/things/42",
			devUser:    "admin@example.com",
			wantStatus: http.StatusAccepted,
			want: map[string]any{
				"http.route": "GET /things/{id}",
				"thing.id":   "42",
				"user.id":    float64(1),
			},
		},
		{
			name:       "unknown dev user is still logged",
			path:       "/things/42",
			devUser:    "nobody@example.com",
			wantStatus: http.StatusUnauthorized,
			// The mux matches before identity runs, so the route is known.
			want: map[string]any{
				"http.route":  "GET /things/{id}",
				"http.status": float64(http.StatusUnauthorized),
			},
			absent: []string{"thing.id"},
		},
		{
			name:       "no matching route",
			path:       "/nope",
			wantStatus: http.StatusNotFound,
			want: map[string]any{
				"http.route":  "",
				"http.path":   "/nope",
				"http.status": float64(http.StatusNotFound),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if tt.devUser != "" {
				req.Header.Set(identity.DevUserHeader, tt.devUser)
			}

			rec, lines := serveLogged(t, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if len(lines) != 1 {
				t.Fatalf("got %d log lines, want exactly 1: %v", len(lines), lines)
			}
			line := lines[0]
			for k, want := range tt.want {
				if got, ok := line[k]; !ok || got != want {
					t.Errorf("%s = %#v, want %#v", k, got, want)
				}
			}
			for _, k := range tt.absent {
				if got, ok := line[k]; ok {
					t.Errorf("%s = %#v, want absent", k, got)
				}
			}
			if _, ok := line["duration_ms"].(float64); !ok {
				t.Errorf("duration_ms = %#v, want a number", line["duration_ms"])
			}
		})
	}
}

// failingUsers is an identity.Users whose lookups always fail, and which
// counts them.
type failingUsers struct {
	calls int
}

func (f *failingUsers) LookupByEmail(ctx context.Context, email string) (identity.Identity, error) {
	f.calls++
	return identity.Identity{}, errors.New("db down: connection refused")
}

func TestHealthEndpointsSkipIdentity(t *testing.T) {
	for _, path := range []string{"/healthz", "/readyz"} {
		t.Run(path, func(t *testing.T) {
			logger := slog.New(slog.DiscardHandler)
			hc := health.New(logger)
			hc.SetReady(true)
			users := &failingUsers{}
			srv := New(config.Config{}, logger, hc, nil, users)

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set(identity.DevUserHeader, "admin@example.com")
			rec := httptest.NewRecorder()
			srv.http.Handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if users.calls != 0 {
				t.Errorf("user lookup called %d times, want 0", users.calls)
			}
		})
	}
}
