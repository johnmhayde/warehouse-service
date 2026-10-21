package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strconv"
)

// Database holds the PostgreSQL connection settings. The API and the
// migrate command both load it, from the same variables but (in deployed
// environments) with different credentials.
//
// Password is a secret. It is injected from a Kubernetes Secret (or a local
// .env / compose file) and is never included in String or LogValue output.
type Database struct {
	Host     string // DB_HOST (required)
	Port     string // DB_PORT (default 5432)
	Name     string // DB_NAME (required)
	User     string // DB_USER (required)
	Password string // DB_PASSWORD (required, secret)

	// SSLMode is the libpq sslmode (DB_SSLMODE). It defaults to
	// verify-full so a deployment that forgets to set it fails closed;
	// local development sets it to disable explicitly.
	SSLMode string
	// SSLRootCert is a path to the CA certificate used to verify the server
	// for verify-ca and verify-full (DB_SSLROOTCERT, optional). When unset,
	// the system trust store is used.
	SSLRootCert string
}

// sslModes are the sslmode values we accept. libpq's "allow" and "prefer"
// are left out: they silently fall back to plaintext.
var sslModes = map[string]bool{
	"disable":     true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

// LoadDatabase reads the DB_* variables using getenv. It reports every
// missing or invalid variable at once rather than stopping at the first.
func LoadDatabase(getenv func(string) string) (Database, error) {
	db := Database{
		Host:        getenv("DB_HOST"),
		Port:        getenv("DB_PORT"),
		Name:        getenv("DB_NAME"),
		User:        getenv("DB_USER"),
		Password:    getenv("DB_PASSWORD"),
		SSLMode:     getenv("DB_SSLMODE"),
		SSLRootCert: getenv("DB_SSLROOTCERT"),
	}
	if db.Port == "" {
		db.Port = "5432"
	}
	if db.SSLMode == "" {
		db.SSLMode = "verify-full"
	}

	var errs []error
	for _, req := range []struct{ key, val string }{
		{"DB_HOST", db.Host},
		{"DB_NAME", db.Name},
		{"DB_USER", db.User},
		{"DB_PASSWORD", db.Password},
	} {
		if req.val == "" {
			errs = append(errs, fmt.Errorf("%s: required", req.key))
		}
	}
	if p, err := strconv.Atoi(db.Port); err != nil || p < 1 || p > 65535 {
		errs = append(errs, fmt.Errorf("DB_PORT: must be a port number, got %q", db.Port))
	}
	if !sslModes[db.SSLMode] {
		errs = append(errs, fmt.Errorf("DB_SSLMODE: must be one of disable, require, verify-ca, verify-full, got %q", db.SSLMode))
	}

	if err := errors.Join(errs...); err != nil {
		return Database{}, err
	}
	return db, nil
}

// ConnString returns a postgres:// URL for pgx, including the password.
// Never log it; log the Database value itself, which is redacted.
func (d Database) ConnString() string {
	q := url.Values{}
	q.Set("sslmode", d.SSLMode)
	if d.SSLRootCert != "" {
		q.Set("sslrootcert", d.SSLRootCert)
	}
	q.Set("connect_timeout", "5")

	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(d.User, d.Password),
		Host:     net.JoinHostPort(d.Host, d.Port),
		Path:     "/" + d.Name,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// String describes the connection without the password, so Database (and
// any Config that contains it) is safe to print with fmt.
func (d Database) String() string {
	return fmt.Sprintf("postgres://%s@%s/%s?sslmode=%s",
		d.User, net.JoinHostPort(d.Host, d.Port), d.Name, d.SSLMode)
}

// LogValue implements slog.LogValuer so logging a Database never emits the
// password.
func (d Database) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("host", d.Host),
		slog.String("port", d.Port),
		slog.String("name", d.Name),
		slog.String("user", d.User),
		slog.String("sslmode", d.SSLMode),
	)
}
