# Conventions

How the warehouse-service repo is organized, and the rules the Go API follows.

## Repository layout

```
warehouse-service/
├── api/          # Go inventory API (its own Go module)
├── web/          # TypeScript frontend
├── deploy/       # deployment config beyond local compose
├── dev/          # local-only development assets (seed data); never in images
├── docs/         # project documentation (this file, database.md, adr/)
├── .github/      # CI workflows
└── compose.yml   # local development stack (Postgres, migrate, API, test runner)
```

Architecture decisions are recorded as numbered ADRs in `docs/adr/`.

## Go module

- There is one module, rooted at `api/`: `github.com/johnmhayde/warehouse-service/api`.
- Run Go commands from `api/` (`go test ./...`, `go run ./cmd/migrate status`), or through compose (`docker compose run --rm test`).
- Don't add a `go.work` file unless a second Go module is added.
- The module path never gets a `/vN` suffix. That is for libraries other code imports, and nothing imports this service.

## API package layout

```
api/
├── cmd/
│   ├── api/              # main package: wiring only, no business logic
│   └── migrate/          # applies migrations; its own image, never run by the API
├── migrations/           # goose SQL migrations, embedded into cmd/migrate
├── openapi.yaml          # OpenAPI 3.1 contract for /api/v1, /healthz and /readyz
└── internal/
    ├── config/           # environment variables → Config
    ├── server/           # http.Server, middleware, lifecycle
    ├── health/           # /healthz, /readyz (+ dependency checks)
    ├── identity/         # who the request is from (POC: X-Dev-User header)
    ├── httpapi/v1/       # v1 handlers and request/response DTOs
    ├── inventory/        # domain: products, SKUs, stock levels
    ├── movement/         # (planned) domain: receiving, picking, transfers, adjustments
    ├── store/postgres/   # connection pool; Postgres implementations of domain repositories
    ├── pgtest/           # throwaway databases for integration tests
    └── telemetry/        # request-scoped wide event that handlers enrich
```

Rules:

- **Everything goes under `internal/`** unless there's a concrete reason it must be importable from outside `api/`. There is no `pkg/` directory.
- **Packages are named after what they provide, not their layer.** Write `inventory`, `movement`, `postgres`, never `models`, `services`, `utils` or `common`.
- **Dependencies point inward.** Domain packages (`inventory`, `movement`) define the interfaces they need (for example `inventory.ProductRepository`) and import neither `net/http` nor a database driver. `store/*` implements those interfaces, and `httpapi/*` calls the domain.
- **Wiring is explicit.** Dependencies are built in `cmd/api` (`run`) and passed in through constructors. There are no package-level globals and no DI framework.
- **`main` stays small.** It sets up signal handling and calls `run(ctx, getenv, stdout) error`, and every input to `run` is a parameter so it can be tested.
- **Architecture rules are enforced by go-arch-lint.** The import rules above are encoded in [`api/.go-arch-lint.yml`](../api/.go-arch-lint.yml) and checked in CI. Run it from `api/` with `go run github.com/fe3dback/go-arch-lint@v1.19.0 check`, or without a local Go install with `docker compose run --rm --no-deps test go run github.com/fe3dback/go-arch-lint@v1.19.0 check`. A new package under `api/` must be added as a component (the check fails on unmapped files), and each new domain package gets its own component so domains can't import each other. go-arch-lint doesn't check standard-library imports, so "no `net/http` or `database/sql` in domain packages" is still enforced by review. `_test.go` files are not checked; that is what keeps `pgtest` test-only, since no component may import it.

## HTTP

- **Versioning:** public endpoints live under `/api/v1`. A breaking change gets a new `httpapi/v2` package mounted at `/api/v2`, next to v1 and reusing the same domain services. Additive changes (new fields, new endpoints) stay in the current version.
- **Infrastructure endpoints aren't versioned:** `/healthz` (liveness) and `/readyz` (readiness).
- **Routing:** use the standard library `http.ServeMux` with method + path patterns (`"GET /api/v1/products/{id}"`). Read path values with `r.PathValue("id")`.
- **Contract:** [`api/openapi.yaml`](../api/openapi.yaml) (OpenAPI 3.1) describes every endpoint the code serves: status codes, JSON field names and error bodies. The handlers are the source of truth; a change that adds an endpoint or changes a status code, field or error body updates the spec in the same PR. Planned endpoints don't go in it until they exist. Lint it without a local install with `docker run --rm -v "$PWD/api:/spec" redocly/cli lint /spec/openapi.yaml`.
- **Errors:** error responses are JSON `{"error": "<message>"}` with a short, client-safe message; internal details are only logged.
- **DTOs:** request and response structs are defined in the version package. Domain types carry no JSON tags and are never encoded directly.
- **Request bodies:** decode with `decodeJSON` in `httpapi/v1`: one JSON object of at most 64 KiB, no unknown fields, nothing after it. A body that breaks this gets 400 `{"error": "invalid request body"}`, or 413 `{"error": "request body too large"}` when it's over the limit. `Content-Type` isn't checked.
- **Admin routes** go under `/api/v1/admin/...`. The prefix only groups them: the domain service still makes the authorization decision (it takes an `inventory.Actor` built from the request's identity and returns `ErrForbidden`, mapped to 403 `{"error": "admin role required"}`). The service checks the role before its own validation, so a non-admin gets 403 for any request whose body decodes and has a valid SKU.
- **Handler order:** identity (401 when the handler needs one and there is none), then the body and path values (400/413), then the service call, whose errors map to statuses (403, 400 for `*inventory.ValidationError` with its message, 404/409, else 500 `internal error`).
- **Timeouts:** the server always sets read-header, read, write and idle timeouts.
- **Identity (POC):** callers name themselves with an `X-Dev-User: <email>` header. `v1.Register` wraps each `/api/v1` route in `identity.DevHeader`, which looks the email up (case-insensitively) among active users and puts the `identity.Identity` in the request context; `/healthz` and `/readyz` ignore the header, so they never look anyone up. On a `/api/v1` route an unknown email gets 401 `{"error": "unknown dev user"}` and a failed lookup gets 500 `{"error": "internal error"}` (the cause goes in the event's `error.message`), and a request without the header carries no identity (handlers that need one return 401). Anyone can send the header, so the API refuses to start with `APP_ENV=production`. Real authentication replaces this in EP07.

## Configuration

Configuration comes only from environment variables, which are read once at startup in `internal/config`. A variable that is set but invalid makes startup fail.

| Variable               | Default       | Meaning                                         |
| ---------------------- | ------------- | ----------------------------------------------- |
| `APP_ENV`              | `development` | `development`, `test` or `production`; the API refuses to start with `production` while the POC identity mode is the only one |
| `API_ADDR`             | `:8080`       | Listen address                                  |
| `API_LOG_LEVEL`        | `info`        | `debug`, `info`, `warn` or `error`              |
| `API_SHUTDOWN_TIMEOUT` | `15s`         | Maximum time in-flight requests get to drain    |
| `DB_*`                 |               | PostgreSQL connection; see [database.md](database.md#configuration) |

Secrets (currently just `DB_PASSWORD`) are injected from Kubernetes Secrets as environment variables. Config types that hold secrets implement `String` and `slog.LogValuer` so the secret can't end up in logs.

## Lifecycle

1. `signal.NotifyContext` cancels the root context on the first SIGINT or SIGTERM. A second signal kills the process immediately.
2. On cancellation, the server marks itself not ready (`/readyz` returns 503), stops accepting connections, and waits up to `API_SHUTDOWN_TIMEOUT` for in-flight requests.
3. Request contexts are **not** derived from the root context, so a shutdown signal doesn't cancel requests that are already running.
4. In compose, `stop_grace_period` must be longer than `API_SHUTDOWN_TIMEOUT`.

## Logging and telemetry

- Logs are written with `log/slog` and its JSON handler to stdout.
- Request middleware emits **one event per request**: the wide event described in the README. Handlers enrich the request's event with `telemetry.Add(ctx, attrs...)` instead of logging their own lines, and the event's attributes are emitted on the single per-request `http request` log line. OpenTelemetry spans wait for WHS-201.
- Field names are lowercase, dot-namespaced and snake_case within a segment: `http.method`, `http.route`, `http.status`, `duration_ms`, `warehouse.id`, `stock.below_reorder`. The frontend uses the same names.
- `http.route` is the matched pattern (`GET /api/v1/products/{id}`), not the raw path, so it can be grouped on. The raw path goes in `http.path`.
- Units go in the field name (`_ms`, `_bytes`).
- A handler records how its request ended in one `<entity>.outcome` field (e.g. `product.outcome`: `created`, `found`, `unauthenticated`, `forbidden`, `invalid`, `not_found`, `duplicate_sku`, `error`), along with the ids it knows (`user.id`, `product.sku`, `product.id`). `product.hidden` is `true` when a product exists but the caller may not see it (an inactive product requested by a non-admin), so it can be told apart from an unknown id although both get the same 404. On a 500 the internal error goes in `error.message`, never in the response.

## Testing

- Tests sit next to the code they test (`foo.go` / `foo_test.go`).
- Use `net/http/httptest` for handlers, and table-driven tests where cases vary only by input.
- Integration tests that need Postgres use `internal/pgtest`. They skip unless `TEST_DATABASE_URL` is set, so `go test ./...` works without a database.
- `docker compose run --rm test` runs unit and integration tests against the compose Postgres, without a local Go install.
- CI (`.github/workflows/ci.yml`) runs gofmt, vet, and the full suite against Postgres on every PR. It must pass before merging.

## Database

See [database.md](database.md) for configuration, connection mode, secrets and how to write migrations. Migrations run as a dedicated step, never at API startup ([ADR 0001](adr/0001-database-migrations.md)).
