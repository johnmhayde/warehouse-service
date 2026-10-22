package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/johnmhayde/warehouse-service/api/internal/identity"
)

// Users implements identity.Users on the users table.
type Users struct {
	pool *pgxpool.Pool
}

// NewUsers returns a Users that queries through pool.
func NewUsers(pool *pgxpool.Pool) *Users {
	return &Users{pool: pool}
}

// LookupByEmail returns the active user whose email matches, ignoring case
// (the same comparison as the users_email_key index). Inactive users are
// treated as unknown.
func (u *Users) LookupByEmail(ctx context.Context, email string) (identity.Identity, error) {
	var (
		id   identity.Identity
		role string
	)
	err := u.pool.QueryRow(ctx,
		"SELECT id, role FROM users WHERE lower(email) = lower($1) AND active",
		email,
	).Scan(&id.UserID, &role)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Identity{}, identity.ErrUnknownUser
	}
	if err != nil {
		return identity.Identity{}, fmt.Errorf("look up user by email: %w", err)
	}
	id.Role = identity.Role(role)
	return id, nil
}
