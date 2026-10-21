# Conventions

How the warehouse-service repo is organized, and the rules the Go API follows.

## Repository layout

```
warehouse-service/
├── api/          # Go inventory API (its own Go module)
├── web/          # TypeScript frontend
├── deploy/       # deployment config beyond local compose
├── docs/         # project documentation (this file)
└── compose.yml   # local development stack
```

## Go module

- There is one module, rooted at `api/`: `github.com/johnmhayde/warehouse-service/api`.
- Run Go commands from `api/` (`go run ./cmd/api`, `go test ./...`).
- Don't add a `go.work` file unless a second Go module is added.
- The module path never gets a `/vN` suffix. That is for libraries other code imports, and nothing imports this service.

## API package layout

```
api/
├── cmd/api/              # main package: wiring only, no business logic
└── internal/
    ├── config/           # environment variables → Config
    ├── server/           # http.Server, middleware, lifecycle
    ├── health/           # /healthz, /readyz
    ├── httpapi/v1/       # v1 handlers and request/response DTOs
    ├── inventory/        # (planned) domain: items, SKUs, stock levels
    ├── movement/         # (planned) domain: receiving, picking, transfers, adjustments
    ├── store/postgres/   # (planned) implementations of domain repositories
    └── telemetry/        # (planned) wide-event builder and middleware
```

Rules:

- **Everything goes under `internal/`** unless there's a concrete reason it must be importable from outside `api/`. There is no `pkg/` directory.
- **Packages are named after what they provide, not their layer.** Write `inventory`, `movement`, `postgres`, never `models`, `services`, `utils` or `common`.
- **Dependencies point inward.** Domain packages (`inventory`, `movement`) define the interfaces they need (for example `inventory.Repository`) and import neither `net/http` nor a database driver. `store/*` implements those interfaces, and `httpapi/*` calls the domain.
- **Wiring is explicit.** Dependencies are built in `cmd/api` (`run`) and passed in through constructors. There are no package-level globals and no DI framework.
- **`main` stays small.** It sets up signal handling and calls `run(ctx, getenv, stdout) error`, and every input to `run` is a parameter so it can be tested.

## HTTP

- **Versioning:** public endpoints live under `/api/v1`. A breaking change gets a new `httpapi/v2` package mounted at `/api/v2`, next to v1 and reusing the same domain services. Additive changes (new fields, new endpoints) stay in the current version.
- **Infrastructure endpoints aren't versioned:** `/healthz` (liveness) and `/readyz` (readiness).
- **Routing:** use the standard library `http.ServeMux` with method + path patterns (`"GET /api/v1/items/{sku}"`). Read path values with `r.PathValue("sku")`.
- **DTOs:** request and response structs are defined in the version package. Domain types carry no JSON tags and are never encoded directly.
- **Timeouts:** the server always sets read-header, read, write and idle timeouts.

## Configuration

Configuration comes only from environment variables, which are read once at startup in `internal/config`. A variable that is set but invalid makes startup fail.

| Variable               | Default | Meaning                                         |
| ---------------------- | ------- | ----------------------------------------------- |
| `API_ADDR`             | `:8080` | Listen address                                  |
| `API_LOG_LEVEL`        | `info`  | `debug`, `info`, `warn` or `error`              |
| `API_SHUTDOWN_TIMEOUT` | `15s`   | Maximum time in-flight requests get to drain    |

## Lifecycle

1. `signal.NotifyContext` cancels the root context on the first SIGINT or SIGTERM. A second signal kills the process immediately.
2. On cancellation, the server marks itself not ready (`/readyz` returns 503), stops accepting connections, and waits up to `API_SHUTDOWN_TIMEOUT` for in-flight requests.
3. Request contexts are **not** derived from the root context, so a shutdown signal doesn't cancel requests that are already running.
4. In compose, `stop_grace_period` must be longer than `API_SHUTDOWN_TIMEOUT`.

## Logging and telemetry

- Logs are written with `log/slog` and its JSON handler to stdout.
- Request middleware emits **one event per request**. This will grow into the wide event described in the README.
- Field names are lowercase, dot-namespaced and snake_case within a segment: `http.method`, `http.route`, `http.status`, `duration_ms`, `warehouse.id`, `stock.below_reorder`. The frontend uses the same names.
- `http.route` is the matched pattern (`GET /api/v1/items/{sku}`), not the raw path, so it can be grouped on. The raw path goes in `http.path`.
- Units go in the field name (`_ms`, `_bytes`).

## Testing

- Tests sit next to the code they test (`foo.go` / `foo_test.go`).
- Use `net/http/httptest` for handlers, and table-driven tests where cases vary only by input.
- `go test ./...` from `api/` must pass before merging.
