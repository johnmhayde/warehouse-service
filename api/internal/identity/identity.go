// Package identity says who is making a request.
//
// This is the POC identity mode (WHS-109, to be fleshed out later): the
// caller names themselves with the X-Dev-User header, and the API trusts it
// as long as it matches an active user. Anyone can send that header, so this
// mode is only acceptable outside production; cmd/api refuses to start with
// APP_ENV=production. Real authentication arrives with EP07.
package identity

import (
	"context"
	"errors"
)

// Role is a user's role. The values match the users_role_check constraint
// in api/migrations.
type Role string

const (
	RoleRequester Role = "requester"
	RoleAdmin     Role = "admin"
)

// Identity is the user a request is made on behalf of.
type Identity struct {
	UserID int64
	Role   Role
}

// IsAdmin reports whether the user has the admin role.
func (i Identity) IsAdmin() bool {
	return i.Role == RoleAdmin
}

// ErrUnknownUser means no active user matches.
var ErrUnknownUser = errors.New("unknown user")

// Users looks up identities. It is implemented in store/postgres.
type Users interface {
	// LookupByEmail returns the active user with email, compared
	// case-insensitively, or an error wrapping ErrUnknownUser if there is
	// none.
	LookupByEmail(ctx context.Context, email string) (Identity, error)
}

type contextKey struct{}

// With returns a copy of ctx carrying id.
func With(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// From returns the identity stored in ctx by With, and whether there was one.
func From(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(contextKey{}).(Identity)
	return id, ok
}
