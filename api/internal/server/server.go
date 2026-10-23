// Package server owns the HTTP server: routing, middleware, timeouts and
// the start/shutdown lifecycle.
package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/johnmhayde/warehouse-service/api/internal/config"
	"github.com/johnmhayde/warehouse-service/api/internal/health"
	v1 "github.com/johnmhayde/warehouse-service/api/internal/httpapi/v1"
	"github.com/johnmhayde/warehouse-service/api/internal/identity"
)

// Server is the API's HTTP server.
type Server struct {
	cfg    config.Config
	logger *slog.Logger
	health *health.Checker
	http   *http.Server
}

// New builds a Server with all routes and middleware mounted. It does not
// start listening; call Run for that. users resolves the X-Dev-User header
// on the /api/v1 routes (the POC identity mode; see package identity).
func New(cfg config.Config, logger *slog.Logger, hc *health.Checker, products v1.ProductService, users identity.Users) *Server {
	mux := http.NewServeMux()
	hc.Register(mux)
	v1.Register(mux, products, users)

	return &Server{
		cfg:    cfg,
		logger: logger,
		health: hc,
		http: &http.Server{
			Addr:    cfg.Addr,
			Handler: handler(logger, mux),
			// http.Server has no timeouts by default, so a slow or idle client
			// can hold a connection open forever.
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       120 * time.Second,
			ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
			// BaseContext is left as the default on purpose. If request
			// contexts derived from Run's ctx, the shutdown signal would cancel
			// every in-flight request instead of letting them drain.
		},
	}
}

// handler wraps mux in the middleware chain. Identity is resolved per
// route inside mux (see v1.Register), so logRequests sees the matched
// route even for requests the identity middleware rejects.
func handler(logger *slog.Logger, mux *http.ServeMux) http.Handler {
	return logRequests(logger, mux)
}

// Run serves HTTP until ctx is cancelled, then shuts down gracefully: it
// marks the service not ready, stops accepting new connections, and waits up
// to cfg.ShutdownTimeout for in-flight requests to finish.
func (s *Server) Run(ctx context.Context) error {
	// Listen separately from Serve so a bad address fails before we report
	// ready, and so the log shows the real address (useful with ":0").
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.Addr, err)
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.http.Serve(ln)
	}()

	s.health.SetReady(true)
	s.logger.Info("server started", "addr", ln.Addr().String())

	select {
	case err := <-serveErr:
		// Serve stopped without Shutdown being called, so this is a real error.
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
	}

	s.logger.Info("shutdown started", "timeout", s.cfg.ShutdownTimeout.String())
	s.health.SetReady(false)

	// ctx is already cancelled, so the shutdown deadline needs a fresh context.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
	defer cancel()

	if err := s.http.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-serveErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve: %w", err)
	}

	s.logger.Info("shutdown complete")
	return nil
}
