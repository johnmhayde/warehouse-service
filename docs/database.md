# Database

The API stores its data in **PostgreSQL 17**:

- **Connection:** the API connects through a [pgx](https://github.com/jackc/pgx) connection pool.
- **Schema:** changes are versioned [goose](https://github.com/pressly/goose) migrations, applied by a separate `migrate` step ([ADR 0001](adr/0001-database-migrations.md)).

## Configuration

The API and `migrate` read the same variables. In deployed environments they get **different credentials** (see [Secrets and roles](#secrets-and-roles)). Startup fails with a list of every missing or invalid variable.

| Variable         | Required | Default       | Meaning                                                          |
| ---------------- | -------- | ------------- | ---------------------------------------------------------------- |
| `DB_HOST`        | yes      |               | Hostname of the Postgres server                                  |
| `DB_PORT`        |          | `5432`        | Port                                                             |
| `DB_NAME`        | yes      |               | Database name                                                    |
| `DB_USER`        | yes      |               | Role to connect as                                               |
| `DB_PASSWORD`    | yes      |               | Password (**secret**)                                            |
| `DB_SSLMODE`     |          | `verify-full` | `disable`, `require`, `verify-ca` or `verify-full`               |
| `DB_SSLROOTCERT` |          |               | Path to the CA certificate for `verify-ca` / `verify-full`       |

`DB_PASSWORD` is never logged:

- Logging the config with slog prints host, port, name, user and sslmode only.
- `fmt` printing is redacted the same way.
- The full connection string, which does contain the password, is built only when the pool opens.

## Connection mode

- **TLS:** the default is `verify-full`, which encrypts the connection and checks the server's certificate and hostname. A deployment that forgets to configure TLS therefore fails to connect, instead of quietly sending traffic in plaintext.
  - Local compose and CI set `DB_SSLMODE=disable`, because their Postgres has no certificate.
  - `allow` and `prefer` are rejected: they fall back to plaintext without telling you.
  - With a managed database (RDS, Cloud SQL, etc.), mount the provider's CA bundle and point `DB_SSLROOTCERT` at it.
- **Pooling:** one `pgxpool.Pool` per process, shared by all requests and closed only after the HTTP server has drained.
  - The pool size uses pgx's default, the larger of 4 and the number of CPUs. Tune it with `pool_max_conns` once there's load data.
  - Each process connects directly to Postgres, with no PgBouncer in between. If a transaction-mode pooler is added later, pgx's statement caching needs to be changed to match.
- **Lazy connect:** the API starts even if Postgres is unreachable.
  - `/readyz` pings the database and returns `503 {"failed":"postgres"}` until it can connect, so the instance gets no traffic in the meantime.
  - `/healthz` never checks the database, so a database outage doesn't make Kubernetes restart healthy API pods.
- **Timeouts:** `connect_timeout=5` seconds per connection attempt, and readiness checks are capped at 2 seconds.
- **Identification:** connections set `application_name` to `warehouse-api` or `warehouse-migrate`, so you can tell them apart in `pg_stat_activity`.

## Secrets and roles

Sensitive values come from **Kubernetes Secrets, injected as environment variables**. Values that aren't secret come from a ConfigMap. Nothing secret is baked into images or committed, apart from the throwaway local passwords in `compose.yml`.

Each environment uses two database roles:

| Role                 | Used by       | Privileges                                              |
| -------------------- | ------------- | ------------------------------------------------------- |
| `warehouse_migrator` | `migrate` Job | Owns the schema; can run DDL                            |
| `warehouse_app`      | API pods      | `SELECT`/`INSERT`/`UPDATE`/`DELETE` on tables, `USAGE` on sequences; no DDL |

Create the roles once per environment, as part of provisioning the database rather than in a migration, because names and passwords differ between environments:

```sql
CREATE ROLE warehouse_migrator LOGIN PASSWORD '...';
CREATE ROLE warehouse_app      LOGIN PASSWORD '...';
GRANT CONNECT ON DATABASE warehouse TO warehouse_app;
GRANT USAGE, CREATE ON SCHEMA public TO warehouse_migrator;
GRANT USAGE ON SCHEMA public TO warehouse_app;

-- Tables the migrator creates later are automatically usable by the app.
ALTER DEFAULT PRIVILEGES FOR ROLE warehouse_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO warehouse_app;
ALTER DEFAULT PRIVILEGES FOR ROLE warehouse_migrator IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO warehouse_app;
```

Example manifests (abridged):

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: warehouse-db
data:
  DB_HOST: warehouse-db.internal
  DB_NAME: warehouse
  DB_SSLMODE: verify-full
  DB_SSLROOTCERT: /etc/db-ca/ca.crt
---
# Created by your secret tooling (External Secrets, Sealed Secrets, etc.),
# never committed with real values.
apiVersion: v1
kind: Secret
metadata:
  name: warehouse-db-app
type: Opaque
stringData:
  DB_USER: warehouse_app
  DB_PASSWORD: <from secret store>
---
apiVersion: batch/v1
kind: Job
metadata:
  name: warehouse-migrate
  annotations:
    helm.sh/hook: pre-install,pre-upgrade   # or argocd.argoproj.io/hook: PreSync
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: ghcr.io/johnmhayde/warehouse-migrate:<tag>
          args: ["up"]
          envFrom:
            - configMapRef: { name: warehouse-db }
            - secretRef: { name: warehouse-db-migrator }   # DB_USER / DB_PASSWORD for warehouse_migrator
---
# In the API Deployment's pod spec:
containers:
  - name: api
    image: ghcr.io/johnmhayde/warehouse-api:<tag>
    envFrom:
      - configMapRef: { name: warehouse-db }
      - secretRef: { name: warehouse-db-app }
    readinessProbe: { httpGet: { path: /readyz, port: 8080 } }
    livenessProbe:  { httpGet: { path: /healthz, port: 8080 } }
```

Passwords are read only from environment variables for now. If secrets ever need to be mounted as files instead (for rotation without restarts, for example), add `DB_PASSWORD_FILE` support to `internal/config`.

## Local development

Everything runs through `docker compose`. You don't need Go installed.

| Task | Command |
| ---- | ------- |
| Start Postgres, run migrations, start the API | `docker compose up --build` |
| Start only Postgres | `docker compose up -d --wait postgres` |
| Apply pending migrations | `docker compose run --rm --build migrate up` |
| Roll back the latest migration | `docker compose run --rm --build migrate down` |
| Show migration status | `docker compose run --rm --build migrate status` |
| Load local sample data | `docker compose run --rm --build seed` |
| Run unit and integration tests | `docker compose run --rm test` |
| Run one package's tests | `docker compose run --rm test go test ./migrations` |
| Open psql | `docker compose exec postgres psql -U warehouse` |
| Wipe the local database | `docker compose down -v` |

`migrate` also accepts `up-to V`, `down-to V` and `version`. Run it with no arguments to see the usage.

### Seed data

`docker compose run --rm --build seed` fills the local database with sample data. Keep `--build`: the seed runs migrations first, and without it a stale `migrate` image could seed an old schema. It's **local only**:

- It's not a migration. The SQL lives in `dev/seed/seed.sql`, outside `api/`, so it's never embedded in the `migrate` binary or copied into any image, and CI never runs it.
- The `seed` service runs that file with the stock `postgres:17-alpine` image's `psql`, using the same `DB_*` credentials as the rest of the compose stack. It runs with `ON_ERROR_STOP=1` in a single transaction, so it applies completely or not at all.
- `docker compose up` doesn't run it (it's behind the `seed` profile), and the API doesn't depend on it, so the stack starts with an empty database unless you ask for data. `run --rm --build seed` starts Postgres and applies migrations first.
- The API doesn't serve this data yet. `api/cmd/api/main.go` still wires the in-memory store (`memory.New(...)`), so until the API switches to the Postgres store the seeded rows are only visible through psql (`docker compose exec postgres psql -U warehouse`).

It inserts:

- 3 users with `@example.com` addresses: `admin@example.com` (admin), `robin@example.com` and `casey@example.com` (requesters).
- 20 products with 6-digit SKUs (`100101`–`700701`) across a few categories (one uncategorized), some below their reorder threshold and two out of stock.
- One `stock_adjustments` row per product that starts with stock ("Opening stock (local seed data)", by the admin), so each product's `total_quantity` equals the sum of its adjustments, as the schema requires.

It's safe to re-run. Every insert uses `ON CONFLICT DO NOTHING`, and opening-stock adjustments are only written for products that the same run actually inserted. A re-run, or a seed SKU you've already created yourself, adds nothing and doesn't change existing stock. psql's `INSERT 0 n` output shows how many rows were added: users first, then stock adjustments.
To start over, drop and recreate the database, then seed it again (which also re-applies migrations):

```sh
docker compose exec postgres dropdb -U warehouse --force warehouse
docker compose exec postgres createdb -U warehouse warehouse
docker compose run --rm --build seed
```

`--force` disconnects the running API, if any; restart it afterwards. Avoid `docker compose down -v` for this: besides the database volume (`pgdata`), it deletes the `gomodcache` and `gobuildcache` volumes, so the next test run downloads and rebuilds everything.

## Schema

The migrations in `api/migrations/` are the source of truth. This section is a map of them.

```
users ──< stock_adjustments >── products
 (actor_id)                    (product_id)
```

**`users`**: people who request or manage stock.

| Column | Type | Notes |
| ------ | ---- | ----- |
| `id` | `bigint` identity | Primary key |
| `external_subject` | `text`, nullable | Identity-provider subject. NULL until auth (EP07); unique and non-blank once set |
| `email` | `text` | Unique **case-insensitively** (`users_email_key` on `lower(email)`); must contain one `@` |
| `display_name` | `text` | Non-blank |
| `role` | `text` | `requester` (default) or `admin` |
| `active` | `boolean` | Default `true`. Deactivate users rather than deleting them |
| `created_at`, `updated_at` | `timestamptz` | A trigger keeps `updated_at` current |

**`products`**: the catalog, and current stock on hand. Under [ADR 0003](adr/0003-rental-model-and-reservation-visibility.md), `total_quantity` is the number of units owned, and rented-out stock will be derived from reservations (WHS-401), not stored here.

| Column | Type | Notes |
| ------ | ---- | ----- |
| `id` | `bigint` identity | Primary key |
| `sku` | `text` | Unique; exactly six ASCII digits (`^[0-9]{6}$`, leading zeros allowed), the same rule as `inventory.ParseSKU` ([ADR 0002](adr/0002-six-digit-skus.md)) |
| `name` | `text` | Non-blank; trigram-indexed for `ILIKE '%term%'` search |
| `description` | `text` | Default `''` |
| `category` | `text`, nullable | NULL means uncategorized; indexed |
| `total_quantity` | `integer` | `>= 0`, default 0. Change it only together with a `stock_adjustments` row, in the same transaction |
| `reorder_threshold` | `integer`, nullable | `>= 0`. Stock below it counts as low |
| `active` | `boolean` | Default `true`; indexed |
| `created_at`, `updated_at` | `timestamptz` | A trigger keeps `updated_at` current |

**`stock_adjustments`**: the audit history of stock changes. Rows are only ever inserted. Exception: migration 00006 deleted the history of products with non-conforming SKUs, together with those products ([ADR 0002](adr/0002-six-digit-skus.md)).

| Column | Type | Notes |
| ------ | ---- | ----- |
| `id` | `bigint` identity | Primary key |
| `product_id` | `bigint` | → `products.id`, `ON DELETE RESTRICT` |
| `delta` | `integer` | Signed change; never 0 |
| `quantity_after` | `integer` | `products.total_quantity` after the change; `>= 0` |
| `reason` | `text` | Non-blank |
| `actor_id` | `bigint` | → `users.id`, `ON DELETE RESTRICT` |
| `created_at` | `timestamptz` | |

Indexes: `(product_id, created_at DESC)` for a product's history, and `(actor_id)` for a user's changes.

Conventions used throughout:

- **Named constraints.** Every constraint has an explicit name (`<table>_<column>_<rule>`). The API can then map a violation to a specific error, and tests can check which rule fired.
- **Check constraints instead of enum types** for small value sets like `role`, because a check constraint is easier to change later.
- **`RESTRICT` on foreign keys.** Products and users with history are deactivated, never deleted, so the audit trail stays intact. Exception: migration 00006 deleted products with non-conforming SKUs and their history ([ADR 0002](adr/0002-six-digit-skus.md)).
- **Constraint tests.** Each constraint has an integration test in `api/migrations/schema_test.go`.

## Writing a migration

1. Add the next numbered file to `api/migrations/`, e.g. `00007_add_product_unit.sql`:
   ```sql
   -- +goose Up
   ALTER TABLE products ADD COLUMN unit text NOT NULL DEFAULT 'each';

   -- +goose Down
   ALTER TABLE products DROP COLUMN unit;
   ```
2. Each migration runs in a transaction. Statements that can't run in one, such as `CREATE INDEX CONCURRENTLY`, need `-- +goose NO TRANSACTION` at the top of the file.
3. Apply it locally with `migrate up`, then check that the Down section works by running `migrate down` followed by `up` again.
4. Never edit a migration once it's merged. Write a new one. CI fails the PR otherwise.
5. Keep each migration compatible with the API version that is currently deployed. For example, to rename a column: add the new one, deploy code that writes to both, backfill, switch reads to the new column, then drop the old one in a later migration.

## Testing

Integration tests are ordinary `go test` tests. They're skipped unless `TEST_DATABASE_URL` points at a Postgres role that can create databases. `internal/pgtest` gives each test its own new database and drops it when the test finishes:

- `pgtest.NewDatabase(t)`: an empty database. Returns its connection string.
- `pgtest.Migrated(t)`: a database with every migration applied. Returns a `*pgxpool.Pool`.
- `pgtest.Provider(t)`: a goose provider for an empty database, used by the migration tests.

CI runs the full suite against a Postgres 17 service container on every PR.
