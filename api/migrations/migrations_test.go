package migrations_test

import (
	"context"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

// These tests cover the two upgrade paths a database can take, and need
// TEST_DATABASE_URL (see internal/pgtest).

// TestUpFromEmpty applies every migration to an empty database, the path a
// brand-new environment takes.
func TestUpFromEmpty(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}

	assertAllApplied(t, p)
}

// TestLatestOnPrevious brings a database to the version before the newest
// migration, then applies the newest one: the path every existing
// environment takes when a PR with a new migration is deployed.
func TestLatestOnPrevious(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	sources := p.ListSources()
	latest := sources[len(sources)-1].Version
	if len(sources) > 1 {
		previous := sources[len(sources)-2].Version
		if _, err := p.UpTo(ctx, previous); err != nil {
			t.Fatalf("UpTo(%d): %v", previous, err)
		}
	}

	results, err := p.Up(ctx)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	if len(results) != 1 || results[0].Source.Version != latest {
		t.Fatalf("Up applied %d migrations, want only the latest (%d)", len(results), latest)
	}

	assertAllApplied(t, p)
}

// TestDownAndUpAgain rolls every migration back and re-applies them, which
// catches Down sections that are broken or leave objects behind.
func TestDownAndUpAgain(t *testing.T) {
	p := pgtest.Provider(t)
	ctx := context.Background()

	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("DownTo(0): %v", err)
	}
	if v, err := p.GetDBVersion(ctx); err != nil || v != 0 {
		t.Fatalf("after DownTo(0): version = %d, err = %v; want 0", v, err)
	}
	if _, err := p.Up(ctx); err != nil {
		t.Fatalf("Up after DownTo(0): %v", err)
	}

	assertAllApplied(t, p)
}

func assertAllApplied(t *testing.T, p *goose.Provider) {
	t.Helper()
	statuses, err := p.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, s := range statuses {
		if s.State != goose.StateApplied {
			t.Errorf("migration %s is %s, want applied", s.Source.Path, s.State)
		}
	}
}
