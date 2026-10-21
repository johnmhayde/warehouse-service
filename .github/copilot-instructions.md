# GitHub Copilot Code Review Instructions

## Review Philosophy
- Only comment when you have HIGH CONFIDENCE (>80%) that an issue exists
- Be concise: one sentence per comment when possible
- Focus on actionable feedback, not observations
- When reviewing text, only comment on clarity issues if the text is genuinely confusing or could lead to errors. "Could be clearer" is not the same as "is confusing" - stay silent unless HIGH confidence it will cause problems

## Priority Areas (Review These)

### Security & Safety
- SQL built by string concatenation with untrusted input (use pgx placeholders `$1`; identifiers go through `pgx.Identifier{...}.Sanitize()`)
- Credential exposure or hardcoded secrets (the throwaway passwords in `compose.yml` and CI are intentional)
- Secrets reaching logs: config types holding secrets must keep their `String` and `slog.LogValuer` redaction, and connection strings must not be logged
- Missing input validation on external data (path values, query params, request bodies)
- Error responses that leak internal details (driver errors, SQL, stack traces) to clients
- Weakening database TLS (`DB_SSLMODE` default is `verify-full`; `allow`/`prefer` are rejected on purpose)
- Request bodies decoded without a size limit (`http.MaxBytesReader`)

### Correctness Issues
- Logic errors that could cause panics (nil dereference, nil map write, out-of-range index)
- Ignored errors (`_ =` or unchecked returns) outside the few deliberate cases, such as encoding the response body after headers are written
- Errors wrapped without `%w`, or compared with `==` instead of `errors.Is` / `errors.As`
- Error context that doesn't add useful information (e.g. `fmt.Errorf("failed to get item: %w", err)` when the wrapped error already says so)
- Missing `defer rows.Close()`, unchecked `rows.Err()`, or transactions without a deferred `Rollback`
- `pgx.ErrNoRows` not mapped to the domain's `ErrNotFound`
- Goroutine leaks, unbuffered channels that can block forever, data races on shared state
- Not passing `r.Context()` / `ctx` through to database calls, or using `context.Background()` inside a request
- Changing `products.total_quantity` without inserting a matching `stock_adjustments` row in the same transaction
- Off-by-one errors or boundary conditions (e.g. stock going negative)
- Pointer fields or optional types that don't need to be optional
- Overly defensive code that adds unnecessary checks
- Unnecessary comments that just restate what the code already shows (remove them)

### Architecture & Patterns
Follow `docs/conventions.md`. Flag code that:
- Makes domain packages (`internal/inventory`, `internal/movement`) import `net/http`, pgx or any `store/*` / `httpapi/*` package — dependencies point inward
- Defines repository interfaces in `store/*` instead of in the domain package that consumes them
- Adds JSON tags to domain types or encodes domain types directly; DTOs belong in `httpapi/v1`
- Adds package-level mutable globals, `init()` side effects, or a DI framework instead of explicit constructor wiring in `cmd/api`
- Puts business logic in `cmd/api/main.go`, or reads env vars outside `internal/config`
- Introduces `pkg/`, `utils`, `common`, `models` or `services` packages
- Uses a third-party router instead of `http.ServeMux` method + path patterns
- Makes a breaking change to a `/api/v1` response instead of adding `httpapi/v2`
- Derives request contexts from the server's root context (breaks graceful drain)
- Adds an `http.Server` without read-header, read, write and idle timeouts
- Runs migrations or DDL from the API process — migrations run only via `cmd/migrate`

### Migrations (`api/migrations/*.sql`)
- Missing or non-working `-- +goose Down` section
- New constraints without an explicit `<table>_<column>_<rule>` name, or without a test in `migrations/schema_test.go`
- Postgres enum types where a `CHECK` constraint would do
- `ON DELETE CASCADE` on audit-trail foreign keys (we use `RESTRICT` and deactivate instead)
- Changes that break the currently deployed API version (renames/drops must be split expand/contract)
- `CREATE INDEX CONCURRENTLY` without `-- +goose NO TRANSACTION`

### Telemetry & Logging
- Logging with `log`/`fmt` instead of `log/slog`
- Wide-event field names that aren't lowercase, dot-namespaced snake_case (`http.status`, `warehouse.id`, `duration_ms`), or that omit units (`_ms`, `_bytes`)
- Using the raw path for `http.route` instead of the matched pattern (`r.Pattern`)
- Extra per-step log lines where the data should be added to the request's single event

## Project-Specific Context

- Go service in `api/`, a single module: `github.com/johnmhayde/warehouse-service/api` (Go 1.26). No `go.work`, no `/vN` suffix
- Standard library first: `net/http` (`ServeMux`), `log/slog`, `encoding/json`, `testing`
- PostgreSQL 17 via `pgx/v5` (`pgxpool`); schema changes via goose v3 SQL migrations embedded in `cmd/migrate`. Note: "goose" here is the migration tool
- Config comes only from environment variables; secrets (e.g. `DB_PASSWORD`) are injected from Kubernetes Secrets
- Health: `/healthz` must never check dependencies; `/readyz` checks Postgres
- The API is a testbed for wide-event telemetry: one rich structured event per request
- A TypeScript frontend (`web/`) is planned but not yet started
- Docs live in `docs/` (`conventions.md`, `database.md`, ADRs in `docs/adr/`); a code change that alters documented behavior should update the relevant doc in the same PR

## Testing Expectations
- Tests sit next to the code (`foo_test.go`); prefer table-driven tests and `net/http/httptest` for handlers
- Integration tests use `internal/pgtest` (`NewDatabase`, `Migrated`, `Provider`) and skip when `TEST_DATABASE_URL` is unset — don't flag the skip as a bug
- Flag new domain logic, handlers or repository methods that ship with no tests, and new schema constraints with no constraint test
- Tests should use `t.Helper()` in helpers and `t.Cleanup` for teardown, not shared state between tests

## CI Pipeline Context

**Important**: You review PRs immediately, before CI completes. Do not flag issues that CI will catch.

### What Our CI Checks (`.github/workflows/ci.yml`)

**API job** (runs in `api/`, against a Postgres 17 service container):
- `gofmt -l .` - Formatting
- `go vet ./...` - Static analysis
- `go test -count=1 ./...` - Unit and integration tests
- Migrations apply to an empty DB, roll down and back up, and the latest applies on top of the previous version
- `go run ./cmd/migrate up` / `status` against a fresh database

**Other jobs:**
- **Merged migrations are unchanged** - fails the PR if an existing `api/migrations/*.sql` file is modified, renamed or deleted
- **Docker images build** - `docker build --target api` and `--target migrate`

**Local workflow**: everything runs through `docker compose` (`docker compose up --build`, `docker compose run --rm test`). There is no Makefile and Go isn't required locally — don't suggest adding either.

## Skip These (Low Value)

Do not comment on:
- **Style/formatting** - CI handles this (gofmt)
- **`go vet` findings** - CI handles this
- **Test failures** - CI handles this (full test suite)
- **Edits to merged migrations** - CI handles this
- **Missing/unused imports or dependencies** - the build fails
- **Minor naming suggestions** - unless truly confusing
- **Suggestions to add comments** - for self-documenting code
- **Refactoring suggestions** - unless there's a clear bug or maintainability issue
- **Multiple issues in one comment** - choose the single most critical issue
- **Logging suggestions** - unless for errors or security events (the request's wide event should carry the data, not extra log lines)
- **Suggestions to add third-party libraries** (routers, ORMs, logging, assertion libraries) where the standard library suffices
- **Pedantic accuracy in text** - unless it would cause actual confusion or errors. No one likes a reply guy

## Response Format

When you identify an issue:
1. **State the problem** (1 sentence)
2. **Why it matters** (1 sentence, only if not obvious)
3. **Suggested fix** (code snippet or specific action)

Example:
```
`rows.Err()` is never checked, so an error mid-iteration returns a partial list as success. Check it after the loop and return it.
```

## When to Stay Silent

If you're uncertain whether something is an issue, don't comment. False positives create noise and reduce trust in the review process.
