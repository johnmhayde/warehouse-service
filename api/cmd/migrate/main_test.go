package main

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/johnmhayde/warehouse-service/api/internal/pgtest"
)

func TestParseArgs(t *testing.T) {
	valid := []struct {
		args    []string
		command string
		version int64
	}{
		{[]string{"up"}, "up", 0},
		{[]string{"down"}, "down", 0},
		{[]string{"status"}, "status", 0},
		{[]string{"version"}, "version", 0},
		{[]string{"up-to", "3"}, "up-to", 3},
		{[]string{"down-to", "0"}, "down-to", 0},
	}
	for _, tt := range valid {
		command, version, err := parseArgs(tt.args)
		if err != nil || command != tt.command || version != tt.version {
			t.Errorf("parseArgs(%q) = %q, %d, %v; want %q, %d, nil", tt.args, command, version, err, tt.command, tt.version)
		}
	}

	invalid := [][]string{
		nil,
		{"sideways"},
		{"up", "3"},
		{"up-to"},
		{"up-to", "three"},
		{"down-to", "-1"},
	}
	for _, args := range invalid {
		if _, _, err := parseArgs(args); !errors.Is(err, errUsage) {
			t.Errorf("parseArgs(%q) error = %v, want errUsage", args, err)
		}
	}
}

// TestRunAgainstDatabase drives the real command end to end: up on an empty
// database, then status and version.
func TestRunAgainstDatabase(t *testing.T) {
	getenv := dbEnv(t, pgtest.NewDatabase(t))
	ctx := context.Background()

	for _, args := range [][]string{{"up"}, {"status"}, {"version"}, {"up"}} {
		var out bytes.Buffer
		if err := run(ctx, args, getenv, &out); err != nil {
			t.Fatalf("migrate %s: %v\n%s", strings.Join(args, " "), err, out.String())
		}
	}
}

// dbEnv turns a test connection string into a DB_* getenv function, the
// way the command is configured in compose and Kubernetes.
func dbEnv(t *testing.T, connString string) func(string) string {
	t.Helper()
	u, err := url.Parse(connString)
	if err != nil {
		t.Fatalf("parse connection string: %v", err)
	}
	password, _ := u.User.Password()
	vars := map[string]string{
		"DB_HOST":     u.Hostname(),
		"DB_PORT":     u.Port(),
		"DB_NAME":     strings.TrimPrefix(u.Path, "/"),
		"DB_USER":     u.User.Username(),
		"DB_PASSWORD": password,
		"DB_SSLMODE":  u.Query().Get("sslmode"),
	}
	return func(k string) string { return vars[k] }
}
