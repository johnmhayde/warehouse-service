package identity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/telemetry"
)

// fakeUsers is a Users that serves identities from a map keyed by email and
// records the email it was asked for.
type fakeUsers struct {
	byEmail map[string]Identity
	err     error

	gotEmail string
}

func (f *fakeUsers) LookupByEmail(ctx context.Context, email string) (Identity, error) {
	f.gotEmail = email
	if f.err != nil {
		return Identity{}, f.err
	}
	id, ok := f.byEmail[email]
	if !ok {
		return Identity{}, fmt.Errorf("lookup %s: %w", email, ErrUnknownUser)
	}
	return id, nil
}

func TestDevHeader(t *testing.T) {
	users := map[string]Identity{
		"admin@example.com":     {UserID: 1, Role: RoleAdmin},
		"requester@example.com": {UserID: 2, Role: RoleRequester},
	}
	tests := []struct {
		name       string
		header     string
		err        error
		wantStatus int
		wantError  string
		wantID     Identity
		wantFound  bool
	}{
		{name: "no header", wantStatus: http.StatusOK},
		{name: "known admin", header: "admin@example.com", wantStatus: http.StatusOK, wantID: Identity{UserID: 1, Role: RoleAdmin}, wantFound: true},
		{name: "known requester", header: "requester@example.com", wantStatus: http.StatusOK, wantID: Identity{UserID: 2, Role: RoleRequester}, wantFound: true},
		{name: "unknown user", header: "nobody@example.com", wantStatus: http.StatusUnauthorized, wantError: "unknown dev user"},
		{name: "lookup failure", header: "admin@example.com", err: errors.New("db down: connection refused"), wantStatus: http.StatusInternalServerError, wantError: "internal error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var (
				called   bool
				gotID    Identity
				gotFound bool
			)
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				gotID, gotFound = From(r.Context())
			})

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set(DevUserHeader, tt.header)
			}
			rec := httptest.NewRecorder()
			DevHeader(&fakeUsers{byEmail: users, err: tt.err}, next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if tt.wantError != "" {
				if called {
					t.Error("next handler called after an error response")
				}
				var body struct {
					Error string `json:"error"`
				}
				if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
					t.Fatalf("decode body: %v", err)
				}
				if body.Error != tt.wantError {
					t.Errorf("error = %q, want %q", body.Error, tt.wantError)
				}
				return
			}
			if !called {
				t.Fatal("next handler not called")
			}
			if gotFound != tt.wantFound || gotID != tt.wantID {
				t.Errorf("From = %+v, %v; want %+v, %v", gotID, gotFound, tt.wantID, tt.wantFound)
			}
		})
	}
}

func TestDevHeaderDoesNotLeakInternalErrors(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DevUserHeader, "admin@example.com")
	rec := httptest.NewRecorder()
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	DevHeader(&fakeUsers{err: errors.New("db down: connection refused")}, next).ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), "db down") {
		t.Errorf("response body leaks internal error: %s", rec.Body.String())
	}
}

func TestDevHeaderRecordsLookupError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set(DevUserHeader, "admin@example.com")
	ctx, event := telemetry.NewContext(req.Context())
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	DevHeader(&fakeUsers{err: errors.New("db down: connection refused")}, next).ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))

	attrs := event.Attrs()
	if len(attrs) != 1 || attrs[0].Key != "error.message" || !strings.Contains(attrs[0].Value.String(), "db down") {
		t.Errorf("event attrs = %v, want error.message with the lookup error", attrs)
	}
}

func TestDevHeaderSkipsLookupWithoutHeader(t *testing.T) {
	users := &fakeUsers{}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})

	DevHeader(users, next).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if users.gotEmail != "" {
		t.Errorf("lookup called with %q for a request without %s", users.gotEmail, DevUserHeader)
	}
}

func TestWithFrom(t *testing.T) {
	if _, ok := From(context.Background()); ok {
		t.Error("From found an identity in an empty context")
	}

	want := Identity{UserID: 7, Role: RoleAdmin}
	got, ok := From(With(context.Background(), want))
	if !ok || got != want {
		t.Errorf("From = %+v, %v; want %+v, true", got, ok, want)
	}
}

func TestIsAdmin(t *testing.T) {
	if !(Identity{Role: RoleAdmin}).IsAdmin() {
		t.Error("admin IsAdmin = false")
	}
	if (Identity{Role: RoleRequester}).IsAdmin() {
		t.Error("requester IsAdmin = true")
	}
}
