package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"testing"
)

func TestAddRecordsAttrs(t *testing.T) {
	ctx, e := NewContext(context.Background())

	Add(ctx, slog.String("user.role", "admin"))
	Add(ctx, slog.Int64("user.id", 7), slog.Bool("stock.below_reorder", true))

	got := e.Attrs()
	want := []slog.Attr{
		slog.String("user.role", "admin"),
		slog.Int64("user.id", 7),
		slog.Bool("stock.below_reorder", true),
	}
	if len(got) != len(want) {
		t.Fatalf("Attrs = %v, want %v", got, want)
	}
	for i := range want {
		if !got[i].Equal(want[i]) {
			t.Errorf("Attrs[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

func TestAddWithoutEventIsNoop(t *testing.T) {
	// Must not panic.
	Add(context.Background(), slog.String("user.role", "admin"))
}

func TestAttrsReturnsCopy(t *testing.T) {
	ctx, e := NewContext(context.Background())
	Add(ctx, slog.String("user.role", "admin"))

	got := e.Attrs()
	got[0] = slog.String("user.role", "changed")

	if v := e.Attrs()[0].Value.String(); v != "admin" {
		t.Errorf("modifying the returned slice changed the event: user.role = %q", v)
	}
}

func TestAddConcurrent(t *testing.T) {
	ctx, e := NewContext(context.Background())

	const n = 50
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			Add(ctx, slog.Int("n", 1))
		})
	}
	wg.Wait()

	if got := len(e.Attrs()); got != n {
		t.Errorf("len(Attrs) = %d, want %d", got, n)
	}
}
