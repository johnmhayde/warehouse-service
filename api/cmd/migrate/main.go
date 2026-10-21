// Command migrate applies the API's database migrations. It runs as its own
// step: a one-shot compose service locally, a Kubernetes Job in deployed
// environments. The API never migrates on startup.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"strconv"
	"syscall"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/johnmhayde/warehouse-service/api/internal/config"
	"github.com/johnmhayde/warehouse-service/api/migrations"
)

const usage = `usage: migrate <command> [version]

commands:
  up           apply all pending migrations
  up-to V      apply pending migrations up to and including version V
  down         roll back the most recently applied migration
  down-to V    roll back migrations until the database is at version V
  status       list every migration and whether it is applied
  version      print the database's current migration version

The database is configured with the DB_* environment variables
(see docs/database.md).
`

var errUsage = errors.New("invalid arguments")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Getenv, os.Stdout)
	stop()
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		if errors.Is(err, errUsage) {
			fmt.Fprint(os.Stderr, "\n"+usage)
		}
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, getenv func(string) string, stdout io.Writer) error {
	command, version, err := parseArgs(args)
	if err != nil {
		return err
	}

	dbCfg, err := config.LoadDatabase(getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	logger := slog.New(slog.NewJSONHandler(stdout, nil))
	logger.Info("migrate starting", "command", command, "db", dbCfg)

	connCfg, err := pgx.ParseConfig(dbCfg.ConnString())
	if err != nil {
		return fmt.Errorf("parse postgres config: %w", err)
	}
	connCfg.RuntimeParams["application_name"] = "warehouse-migrate"
	db := stdlib.OpenDB(*connCfg)
	defer db.Close()

	// A Postgres advisory lock makes concurrent runs (two Job pods, or a
	// developer and CI) take turns instead of racing.
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration lock: %w", err)
	}
	p, err := migrations.NewProvider(db, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}

	switch command {
	case "up":
		results, err := p.Up(ctx)
		logResults(logger, results)
		return err
	case "up-to":
		results, err := p.UpTo(ctx, version)
		logResults(logger, results)
		return err
	case "down":
		result, err := p.Down(ctx)
		if result != nil {
			logResults(logger, []*goose.MigrationResult{result})
		}
		return err
	case "down-to":
		results, err := p.DownTo(ctx, version)
		logResults(logger, results)
		return err
	case "status":
		statuses, err := p.Status(ctx)
		if err != nil {
			return err
		}
		for _, s := range statuses {
			attrs := []any{
				"version", s.Source.Version,
				"file", path.Base(s.Source.Path),
				"state", string(s.State),
			}
			if s.State == goose.StateApplied {
				attrs = append(attrs, "applied_at", s.AppliedAt)
			}
			logger.Info("migration", attrs...)
		}
		return nil
	case "version":
		v, err := p.GetDBVersion(ctx)
		if err != nil {
			return err
		}
		logger.Info("database version", "version", v)
		return nil
	}
	panic("unreachable: parseArgs accepted unknown command " + command)
}

// parseArgs validates the command line and returns the command and, for
// up-to and down-to, the target version.
func parseArgs(args []string) (command string, version int64, err error) {
	if len(args) == 0 {
		return "", 0, errUsage
	}
	command = args[0]
	switch command {
	case "up", "down", "status", "version":
		if len(args) != 1 {
			return "", 0, fmt.Errorf("%s takes no arguments: %w", command, errUsage)
		}
		return command, 0, nil
	case "up-to", "down-to":
		if len(args) != 2 {
			return "", 0, fmt.Errorf("%s needs a version: %w", command, errUsage)
		}
		version, err = strconv.ParseInt(args[1], 10, 64)
		if err != nil || version < 0 {
			return "", 0, fmt.Errorf("version must be a non-negative integer, got %q: %w", args[1], errUsage)
		}
		return command, version, nil
	default:
		return "", 0, fmt.Errorf("unknown command %q: %w", command, errUsage)
	}
}

// logResults logs one line per migration that ran. goose returns the
// results that completed even when a later migration fails, so this is
// called before the error is returned.
func logResults(logger *slog.Logger, results []*goose.MigrationResult) {
	if len(results) == 0 {
		logger.Info("no migrations to run")
		return
	}
	for _, r := range results {
		logger.Info("migration applied",
			"version", r.Source.Version,
			"file", path.Base(r.Source.Path),
			"direction", r.Direction,
			"duration_ms", float64(r.Duration.Microseconds())/1000,
		)
	}
}
