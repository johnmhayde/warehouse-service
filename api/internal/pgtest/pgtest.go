// Package pgtest creates throwaway PostgreSQL databases for integration
// tests. Tests that use it are skipped unless TEST_DATABASE_URL is set.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/johnmhayde/warehouse-service/api/migrations"
)

// EnvURL names the variable holding a postgres:// URL for a role that can
// create databases, e.g.
// postgres://warehouse:warehouse@localhost:5432/warehouse?sslmode=disable.
const EnvURL = "TEST_DATABASE_URL"

// NewDatabase creates an empty database and returns a connection string for
// it. The database is dropped when the test finishes. If TEST_DATABASE_URL
// is not set, the test is skipped.
func NewDatabase(t testing.TB) string {
	t.Helper()
	adminURL := os.Getenv(EnvURL)
	if adminURL == "" {
		t.Skipf("%s not set; skipping integration test", EnvURL)
	}
	ctx := context.Background()

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("%s is not a valid URL: %v", EnvURL, err)
	}

	name := "test_" + randomSuffix(t)
	ident := pgx.Identifier{name}.Sanitize()

	admin, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		t.Fatalf("connect to %s: %v", EnvURL, err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+ident); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}

	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, adminURL)
		if err != nil {
			t.Errorf("connect to drop database %s: %v", name, err)
			return
		}
		defer conn.Close(ctx)
		// FORCE disconnects any pools the test forgot to close.
		if _, err := conn.Exec(ctx, "DROP DATABASE IF EXISTS "+ident+" WITH (FORCE)"); err != nil {
			t.Errorf("drop database %s: %v", name, err)
		}
	})

	u.Path = "/" + name
	return u.String()
}

// Provider returns a goose provider for a new empty database, for tests of
// the migrations themselves.
func Provider(t testing.TB) *goose.Provider {
	t.Helper()
	connString := NewDatabase(t)

	cfg, err := pgx.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	db := stdlib.OpenDB(*cfg)
	t.Cleanup(func() { db.Close() })

	p, err := migrations.NewProvider(db)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	return p
}

// Migrated returns a pool connected to a new database with every migration
// applied.
func Migrated(t testing.TB) *pgxpool.Pool {
	t.Helper()
	connString := NewDatabase(t)
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(connString)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	db := stdlib.OpenDB(*cfg)
	defer db.Close()

	p, err := migrations.NewProvider(db)
	if err != nil {
		t.Fatalf("create migration provider: %v", err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	pool, err := pgxpool.New(ctx, connString)
	if err != nil {
		t.Fatalf("connect to migrated database: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func randomSuffix(t testing.TB) string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	return hex.EncodeToString(b)
}
