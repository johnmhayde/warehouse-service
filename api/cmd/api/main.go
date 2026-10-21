// Command api runs the warehouse inventory HTTP API.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnmhayde/warehouse-service/api/internal/config"
	"github.com/johnmhayde/warehouse-service/api/internal/health"
	"github.com/johnmhayde/warehouse-service/api/internal/inventory"
	"github.com/johnmhayde/warehouse-service/api/internal/server"
	"github.com/johnmhayde/warehouse-service/api/internal/store/memory"
)

func main() {
	// ctx is cancelled on the first SIGINT (Ctrl-C) or SIGTERM (docker stop).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// After the first signal, restore default signal handling so a second
	// Ctrl-C kills the process immediately instead of waiting for the drain.
	go func() {
		<-ctx.Done()
		stop()
	}()

	err := run(ctx, os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "api: %v\n", err)
		os.Exit(1)
	}
}

// run wires up dependencies and blocks until the server has shut down.
// Its inputs are passed in rather than read from globals so it can be
// driven from tests.
func run(ctx context.Context, getenv func(string) string, stdout io.Writer) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	store := memory.New(
		inventory.Item{SKU: "000123", Name: "Pallet jack", Quantity: 140},
	)
	items := inventory.NewService(store)

	srv := server.New(cfg, logger, health.New(), items)
	return srv.Run(ctx)
}
