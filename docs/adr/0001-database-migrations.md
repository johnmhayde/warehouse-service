# ADR 0001: Database migrations with goose, run as a dedicated step

- **Status:** Accepted
- **Date:** 2026-10-20
- **Tickets:** WHS-106 (tool spike), WHS-107 (workflow)

## Context

The API stores inventory in PostgreSQL, and the schema will change often while the project is young. We need a way to change it that:

- keeps an ordered, reviewable history of every schema change,
- handles changes an "auto-migrate from structs" approach can't express (renames, backfills, type changes, drops, index builds),
- gives every environment (laptops, CI, staging, production) the same schema by the same steps, and
- never runs schema changes as a side effect of an API instance starting.

## Decision

1. **Tool: [goose](https://github.com/pressly/goose)** (v3, Provider API).
   - Migrations are plain SQL files with `-- +goose Up` / `-- +goose Down` sections.
   - Go-coded migrations are available if a data migration ever needs them.
   - The files are embedded into a binary, so the migration image needs nothing else.
   - We also looked at golang-migrate (similar, but separate up/down files and a weaker library API) and Atlas (declarative schema diffs; powerful, but more concepts than this project needs).

2. **Migrations run as their own step, never at API startup.**
   - `api/cmd/migrate` is a separate binary and image (`--target migrate` in `api/Dockerfile`).
   - **Locally:** compose runs the `migrate` service to completion before starting `api` (`depends_on: service_completed_successfully`).
   - **Deployed:** a Kubernetes Job runs `migrate up` before the API Deployment rolls out, for example as a Helm pre-upgrade hook or an Argo CD PreSync hook.
   - The API has no code path that changes the schema. In deployed environments it connects with a role that can't run DDL (see [database.md](../database.md#secrets-and-roles)).

3. **Rules for migration files.**
   - They're numbered sequentially (`00001_name.sql`) and live in `api/migrations/`.
   - A merged migration is never edited, renamed or deleted. CI enforces this.
   - Every migration has a working Down section. CI rolls everything back and forward again.
   - A breaking change is split into backward-compatible steps (expand/contract), so the old and new API versions can run against the same schema during a rollout.

4. **CI checks every pull request.**
   - All migrations apply to an empty database.
   - The newest migration applies on top of the previous version.
   - Every Down works, and the migrations re-apply afterwards.
   - Merged migrations are unchanged.

## Consequences

- Deploys have an explicit ordering: migrate first, then the API. A migration must leave the schema compatible with the API version that is still running.
- Two concurrent runs (two Job pods, or a developer and CI against a shared database) are serialized by a Postgres advisory lock that goose takes.
- Rolling back an API deploy doesn't roll back the schema. Down migrations exist for development and emergencies, not as the normal rollback path.
- Schema changes are reviewed as SQL in pull requests, so whoever reviews has to be comfortable reading it.
