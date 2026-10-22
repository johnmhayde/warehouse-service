package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunRefusesProduction(t *testing.T) {
	vars := map[string]string{
		"APP_ENV":     "production",
		"DB_HOST":     "localhost",
		"DB_NAME":     "warehouse",
		"DB_USER":     "warehouse",
		"DB_PASSWORD": "s3cret",
	}

	err := run(context.Background(), func(k string) string { return vars[k] }, io.Discard)

	if err == nil || !strings.Contains(err.Error(), "APP_ENV=production") {
		t.Fatalf("run = %v, want an error refusing APP_ENV=production", err)
	}
}
