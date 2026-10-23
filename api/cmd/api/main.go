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
	"github.com/johnmhayde/warehouse-service/api/internal/store/postgres"
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

	// X-Dev-User is the only identity mode until real authentication (EP07),
	// and anyone can send it, so it must never serve production traffic.
	if cfg.Env == config.EnvProduction {
		return fmt.Errorf("POC identity mode (X-Dev-User header) must not run with APP_ENV=%s", config.EnvProduction)
	}

	logger := slog.New(slog.NewJSONHandler(stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	logger.Info("config loaded", "env", cfg.Env, "db", cfg.Database)

	pool, err := postgres.NewPool(ctx, cfg.Database.ConnString(), "warehouse-api")
	if err != nil {
		return err
	}
	// Deferred, so the pool closes only after the server has drained.
	defer pool.Close()

	hc := health.New(logger)
	hc.AddCheck("postgres", pool.Ping)

	products := inventory.NewProductService(postgres.NewProducts(pool))

	srv := server.New(cfg, logger, hc, products, postgres.NewUsers(pool))
	return srv.Run(ctx)
}
