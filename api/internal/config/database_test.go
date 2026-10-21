package config

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

func TestLoadDatabaseDefaults(t *testing.T) {
	db, err := LoadDatabase(env(withDB(nil)))
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}
	if db.Port != "5432" {
		t.Errorf("Port = %q, want 5432", db.Port)
	}
	if db.SSLMode != "verify-full" {
		t.Errorf("SSLMode = %q, want verify-full", db.SSLMode)
	}
}

func TestLoadDatabaseReportsEveryProblem(t *testing.T) {
	_, err := LoadDatabase(env(map[string]string{
		"DB_PORT":    "not-a-port",
		"DB_SSLMODE": "prefer",
	}))
	if err == nil {
		t.Fatal("LoadDatabase succeeded, want error")
	}
	for _, want := range []string{"DB_HOST", "DB_NAME", "DB_USER", "DB_PASSWORD", "DB_PORT", "DB_SSLMODE"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s:\n%v", want, err)
		}
	}
}

func TestConnString(t *testing.T) {
	db, err := LoadDatabase(env(withDB(map[string]string{
		"DB_PASSWORD":    "p@ss/word",
		"DB_SSLMODE":     "verify-full",
		"DB_SSLROOTCERT": "/etc/db-ca/ca.crt",
	})))
	if err != nil {
		t.Fatalf("LoadDatabase: %v", err)
	}

	got := db.ConnString()
	want := "postgres://warehouse:p%40ss%2Fword@localhost:5432/warehouse?connect_timeout=5&sslmode=verify-full&sslrootcert=%2Fetc%2Fdb-ca%2Fca.crt"
	if got != want {
		t.Errorf("ConnString =\n  %s\nwant\n  %s", got, want)
	}
}

func TestDatabaseNeverPrintsPassword(t *testing.T) {
	const secret = "hunter2-very-secret"
	cfg, err := Load(env(withDB(map[string]string{"DB_PASSWORD": secret})))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	var logs bytes.Buffer
	slog.New(slog.NewJSONHandler(&logs, nil)).Info("config", "db", cfg.Database)

	outputs := map[string]string{
		"fmt %v of Database": fmt.Sprintf("%v", cfg.Database),
		"fmt %+v of Config":  fmt.Sprintf("%+v", cfg),
		"slog JSON":          logs.String(),
	}
	for name, out := range outputs {
		if strings.Contains(out, secret) {
			t.Errorf("%s leaks the password: %s", name, out)
		}
	}
}
