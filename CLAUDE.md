# warehouse-service

Go inventory API in `api/` (one module); a TypeScript frontend in `web/` is planned. Read these before changing code:

- `docs/conventions.md` — package layout, dependency rules, HTTP, config, logging and testing conventions
- `docs/database.md` and `docs/adr/` — database setup, migrations and recorded decisions
- `.github/copilot-instructions.md` — the review checklist (security, correctness, architecture, migrations, telemetry)

Everything runs through Docker Compose; Go isn't required locally. Run the test suite with `docker compose run --rm test`.

## Before pushing or opening a PR

1. Commit your work on a branch. Branches must start with `feature/` or `bugfix/`. Don't push yet.
2. Run the `code-reviewer` agent on the branch. It runs the CI checks on the changed packages and reviews the diff against `docs/conventions.md` and the API contract.
3. Address the findings:
   - **blocker** — fix it.
   - **warning** — fix it, or explain in the PR description why it stays.
   - **suggestion** — optional.
4. Re-run the reviewer after fixing. Stop after two review rounds: push, and list any findings still open in the PR description.
5. A subagent that made the change reports back after committing; the session that started it runs the review and does the push.

If the `code-reviewer` agent isn't available, run `docker compose run --rm test` and review the diff against `.github/copilot-instructions.md` yourself before pushing.

## Rules for agents working here

- Never edit a merged migration in `api/migrations/`; add a new one.
- A change that alters documented behavior updates the relevant doc in the same PR.
- Breaking changes to `/api/v1` go in a new `httpapi/v2`, not in v1.
