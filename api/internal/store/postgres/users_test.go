package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

func TestUsersLookupByEmail(t *testing.T) {
	pool := pgtest.Migrated(t)
	ctx := context.Background()

	var adminID, requesterID int64
	for _, u := range []struct {
		email, role string
		active      bool
		id          *int64
	}{
		{"Admin@Example.com", "admin", true, &adminID},
		{"requester@example.com", "requester", true, &requesterID},
		{"gone@example.com", "admin", false, new(int64)},
	} {
		err := pool.QueryRow(ctx,
			"INSERT INTO users (email, display_name, role, active) VALUES ($1, $2, $3, $4) RETURNING id",
			u.email, "Test user", u.role, u.active,
		).Scan(u.id)
		if err != nil {
			t.Fatalf("insert user %s: %v", u.email, err)
		}
	}

	users := NewUsers(pool)
	tests := []struct {
		name    string
		email   string
		want    identity.Identity
		wantErr error
	}{
		{"known admin", "Admin@Example.com", identity.Identity{UserID: adminID, Role: identity.RoleAdmin}, nil},
		{"case-insensitive", "admin@EXAMPLE.COM", identity.Identity{UserID: adminID, Role: identity.RoleAdmin}, nil},
		{"known requester", "requester@example.com", identity.Identity{UserID: requesterID, Role: identity.RoleRequester}, nil},
		{"inactive", "gone@example.com", identity.Identity{}, identity.ErrUnknownUser},
		{"missing", "nobody@example.com", identity.Identity{}, identity.ErrUnknownUser},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := users.LookupByEmail(ctx, tt.email)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("LookupByEmail error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("LookupByEmail = %+v, want %+v", got, tt.want)
			}
		})
	}
}
