package config

import (
	"log/slog"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// withDB returns m plus the required DB_* variables, so tests of the API
// settings don't fail on a missing database config.
func withDB(m map[string]string) map[string]string {
	out := map[string]string{
		"DB_HOST":     "localhost",
		"DB_NAME":     "warehouse",
		"DB_USER":     "warehouse",
		"DB_PASSWORD": "s3cret",
	}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(withDB(nil)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("Env = %q, want %q", cfg.Env, "development")
	}
	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want %v", cfg.LogLevel, slog.LevelInfo)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v, want %v", cfg.ShutdownTimeout, 15*time.Second)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(withDB(map[string]string{
		"APP_ENV":              "production",
		"API_ADDR":             "127.0.0.1:9000",
		"API_LOG_LEVEL":        "debug",
		"API_SHUTDOWN_TIMEOUT": "3s",
	})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Env != "production" {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.Addr != "127.0.0.1:9000" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := map[string]map[string]string{
		"unknown env":           {"APP_ENV": "staging"},
		"env wrong case":        {"APP_ENV": "Production"},
		"bad log level":         {"API_LOG_LEVEL": "loud"},
		"bad duration":          {"API_SHUTDOWN_TIMEOUT": "soon"},
		"non-positive duration": {"API_SHUTDOWN_TIMEOUT": "0s"},
	}
	for name, vars := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(env(withDB(vars))); err == nil {
				t.Fatal("Load succeeded, want error")
			}
		})
	}
}

func TestLoadEnvValues(t *testing.T) {
	for _, v := range []string{"development", "test", "production"} {
		t.Run(v, func(t *testing.T) {
			cfg, err := Load(env(withDB(map[string]string{"APP_ENV": v})))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Env != v {
				t.Errorf("Env = %q, want %q", cfg.Env, v)
			}
		})
	}
}

func TestLoadRequiresDatabase(t *testing.T) {
	if _, err := Load(env(nil)); err == nil {
		t.Fatal("Load succeeded without DB_* variables, want error")
	}
}
