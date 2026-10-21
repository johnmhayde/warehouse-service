# warehouse-service

A warehouse inventory service with a dedicated frontend (TypeScript) and backend (Go) that uses a wide-event observability model.

## Project goals

1. **Build a working warehouse inventory app.** It should have a full-stack frontend and backend for tracking stock across one or more warehouses.
2. **Try out wide-event telemetry.** The service is also a testbed for an observability model built around one rich, structured event per unit of work, rather than scattered logs, metrics and traces.

## What it will do

- **Inventory management:** create, view, update and retire SKUs/items, with quantities tracked per warehouse and per location (aisle/bin).
- **Stock movements:** record receiving, picking, transfers between locations or warehouses, and manual adjustments, keeping an auditable history.
- **Search and filtering:** look up items by SKU, name, location or stock level.
- **Low-stock awareness:** show items that fall below a reorder threshold.
- **Frontend dashboard:** a TypeScript web UI for browsing inventory, making stock movements and checking current levels.
- **Backend API:** a Go service that exposes the inventory operations over an HTTP API and owns persistence and business rules.

## Architecture (planned)

```
┌──────────────────┐   HTTP/JSON   ┌──────────────────┐      ┌────────────┐
│ Frontend (TS)    │ ────────────▶ │ Backend (Go)     │ ───▶ │ Datastore  │
│ inventory UI     │               │ inventory API    │      └────────────┘
└────────┬─────────┘               └────────┬─────────┘
         │ wide events                      │ wide events
         └──────────────┬───────────────────┘
                        ▼
              ┌────────────────────┐
              │ Telemetry backend  │
              │ (query / explore)  │
              └────────────────────┘
```

## Wide-event telemetry

A core aim of this project is to test **wide events** as the main observability signal.

### What is a wide event?

A wide event is a single structured record emitted once per unit of work (for example, one per HTTP request or one per background job). Context is added to it throughout the request's lifecycle, and it carries many fields (tens to hundreds of them). For example:

```json
{
  "timestamp": "2026-10-20T14:03:11.482Z",
  "service": "warehouse-backend",
  "trace_id": "4bf92f3577b34da6",
  "http.method": "POST",
  "http.route": "/api/v1/movements",
  "http.status": 201,
  "duration_ms": 38.4,
  "user.id": "u_1842",
  "warehouse.id": "wh_east",
  "movement.type": "transfer",
  "sku": "SKU-00921",
  "quantity": 12,
  "location.from": "A-04-02",
  "location.to": "C-11-01",
  "db.queries": 3,
  "db.duration_ms": 21.7,
  "stock.after": 140,
  "stock.below_reorder": false,
  "build.version": "0.1.0"
}
```

### Why test it?

- **Debugging from a single event:** you can answer "what happened on this request?" without stitching together logs, metrics and traces.
- **High cardinality and dimensionality:** you can slice by any field (SKU, warehouse, user, build version) to find outliers and patterns you didn't plan for.
- **Metrics are derived, not predefined:** you compute aggregates (p99 latency per route, error rate per warehouse) from events at query time.

### What we want to evaluate

- How to build and enrich a wide event across middleware, handlers and the data layer in **Go**.
- How to emit matching wide events from the **TypeScript frontend** (page loads, user actions, API calls) and connect them to backend events through a shared trace/request ID.
- A consistent **field naming convention** across frontend and backend.
- How much the instrumentation costs in performance and code complexity.
- How useful the events are for real debugging and analysis compared with traditional logs and metrics.

## Repository layout (planned)

```
warehouse-service/
├── frontend/   # TypeScript web UI
├── api/        # Go inventory API
└── README.md
```

## Running locally

You need Docker. Go is optional.

```sh
docker compose up --build          # Postgres, then migrations, then the API on :8080
curl localhost:8080/readyz
docker compose run --rm seed       # optional: local sample users and products
docker compose run --rm test       # unit + integration tests
```

See [docs/database.md](docs/database.md) for migrations and database configuration, and [docs/conventions.md](docs/conventions.md) for how the code is organized.

## Status

Early stage: the project is being scaffolded. The API and its database foundation are in place; the frontend hasn't been started.
