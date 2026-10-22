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
	// Env is the deployment environment (APP_ENV): development, test or
	// production.
	Env string
	// Addr is the TCP address the HTTP server listens on (API_ADDR).
	Addr string
	// LogLevel is the minimum slog level that is emitted (API_LOG_LEVEL).
	LogLevel slog.Level
	// ShutdownTimeout bounds how long in-flight requests get to finish
	// after a shutdown signal (API_SHUTDOWN_TIMEOUT).
	ShutdownTimeout time.Duration
	// Database is the PostgreSQL connection (DB_*).
	Database Database
}

// Values of Config.Env.
const (
	EnvDevelopment = "development"
	EnvTest        = "test"
	EnvProduction  = "production"
)

var envs = map[string]bool{
	EnvDevelopment: true,
	EnvTest:        true,
	EnvProduction:  true,
}

// Load reads the configuration using getenv, applying defaults for unset
// variables. It returns an error if any value is present but invalid, or if
// a required value is missing.
func Load(getenv func(string) string) (Config, error) {
	cfg := Config{
		Env:             EnvDevelopment,
		Addr:            ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 15 * time.Second,
	}

	if v := getenv("APP_ENV"); v != "" {
		if !envs[v] {
			return Config{}, fmt.Errorf("APP_ENV: must be one of development, test, production, got %q", v)
		}
		cfg.Env = v
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

	db, err := LoadDatabase(getenv)
	if err != nil {
		return Config{}, err
	}
	cfg.Database = db

	return cfg, nil
}
