package postgres

import (
	"context"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

func TestNewPoolConnects(t *testing.T) {
	connString := pgtest.NewDatabase(t)
	ctx := context.Background()

	pool, err := NewPool(ctx, connString, "warehouse-test")
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	var appName string
	if err := pool.QueryRow(ctx, "SELECT current_setting('application_name')").Scan(&appName); err != nil {
		t.Fatalf("query application_name: %v", err)
	}
	if appName != "warehouse-test" {
		t.Errorf("application_name = %q, want %q", appName, "warehouse-test")
	}
}

func TestNewPoolIsLazy(t *testing.T) {
	// Nothing listens on port 1, but creating the pool must still succeed:
	// the API starts and reports not-ready rather than crashing.
	pool, err := NewPool(context.Background(), "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=1", "warehouse-test")
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(context.Background()); err == nil {
		t.Fatal("Ping succeeded against a closed port, want error")
	}
}
