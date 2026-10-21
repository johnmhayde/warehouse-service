// Package config loads the API's configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"time"
)

// Config holds everything the API needs to start. It is loaded once at
// startup and passed explicitly to the packages that need it.
type Config struct {
	// Addr is the TCP address the HTTP server listens on (API_ADDR).
	Addr string
	// LogLevel is the minimum slog level that is emitted (API_LOG_LEVEL).
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish
	// after a shutdown signal (API_SHUTDOWN_TIMEOUT).
	ShutdownTimeout time.Duration
}

// Load reads the configuration using getenv, applying defaults for unset
// variables. It returns an error if any value is present but invalid.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 15 * time.Second,
	}

	if v := getenv("API_ADDR"); v != "" {
		cfg.Addr = v
	}

	if v := getenv("API_LOG_LEVEL"); v != "" {
		if err := cfg.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return Config{}, fmt.Errorf("API_LOG_LEVEL: %w", err)
		}
	}

	if v := getenv("API_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return Config{}, fmt.Errorf("API_SHUTDOWN_TIMEOUT: %w", err)
		}
		if d <= 0 {
			return Config{}, fmt.Errorf("API_SHUTDOWN_TIMEOUT: must be positive, got %s", d)
		}
		cfg.ShutdownTimeout = d
	}

	return cfg, nil
}
