// Package postgres connects to PostgreSQL. Repository implementations for
// the domain packages live here too, as they are added.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool returns a connection pool for connString. appName is reported to
// Postgres as application_name, so the API and the migrate command can be
// told apart in pg_stat_activity.
//
// The pool connects lazily: NewPool doesn't fail when the database is
// unreachable. Callers find out through Ping, which the API wires into
// /readyz.
func NewPool(ctx context.Context, connString, appName string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("parse postgres config: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["application_name"] = appName

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create postgres pool: %w", err)
	}
	return pool, nil
}
