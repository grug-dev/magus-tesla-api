# Running the web server with the latest changes

Day-to-day runbook for `cmd/web` (the multi-tenant server). The mental model:
**generated code and the DB schema are not automatic.** Templ templates and sqlc
queries are compiled *into* the binary, and migrations must be applied to Postgres,
so after certain changes you must regenerate / migrate **before** rebuilding and
restarting. There is **no hot reload** — the server is a static binary; every change
needs a rebuild + restart.

For first-time app registration and OAuth setup, see
[`post-registration-setup.md`](post-registration-setup.md),
[`layer1-app-registration.md`](layer1-app-registration.md), and
[`layer2-user-vehicle-access.md`](layer2-user-vehicle-access.md). For prod, see
[`deployment.md`](deployment.md).

## TL;DR — just run this

```bash
make up
```

`make up` is the single entrypoint. It runs, in order:

1. `make generate` — regenerate **all** codegen (`sqlc generate` + `go tool templ generate ./...`),
2. `make migrate-up` — apply any pending goose migrations (every module, each into its own
   `<module>.goose_db_version` ledger),
3. `go build -o bin/web ./cmd/web` and run `./bin/web` (listens on `$PORT`, default `8080`).

Because it always regenerates and migrates, `make up` is safe after **any** change —
you don't have to remember which step a change needs. Stop the server with `Ctrl-C`,
re-run `make up`.

Prereqs on the host: a running PostgreSQL, and the `sqlc` and `goose` binaries on
`PATH` (`make up` invokes both). The server reads config from `.env`.

## What each kind of change needs (if you'd rather run steps individually)

| You changed… | Minimal commands | Why |
|---|---|---|
| Go code only (handlers, services) | `make up` (or `go build ./... && ./bin/web`) | just recompile |
| A `.templ` template | `make templ` → restart | `*_templ.go` is generated + compiled in; stale = old HTML |
| `queries.sql` (sqlc) | `make sqlc` → restart | regenerates the typed `db/*.go` your services call |
| A DB migration (new schema) | `make migrate-up` → usually `make sqlc` → restart | apply schema first; regenerate queries if they changed |
| `go.mod` dependencies | `make tidy` (or `go mod download`) → restart | sync the module graph |
| Not sure | `make up` | it does generate + migrate + build + run |

`make generate` runs both code generators at once (`sqlc` + `templ`). `make check`
(build + vet + test) is a good gate before running if you want one.

## Adding a *new* module with its own database (one extra manual step)

Migrations live per module under `internal/<module>/db/migrations`, and each module keeps
its own version ledger in `<module>.goose_db_version`. So when a new module gains a DB:

1. Add the module's name to `MIGRATION_MODULES` in the `Makefile` — **position does not
   matter**, and it is the only list to edit. `MIGRATIONS_DIRS` is derived from it, and so
   is the `-table <module>.goose_db_version` every goose call passes. Note this is the list
   to override, not `MIGRATIONS_DIRS`: overriding the dirs alone no longer changes the goose
   CLI loops.
2. Write the module's first migration as a **baseline** — one file creating its whole schema,
   reading nothing outside its own Postgres schema. Any version number is fine; it need not
   be unique across modules.
3. Then `make up` (or `make migrate-up` + `make sqlc`) picks it up.

Two consequences worth knowing:

- `goose run: error: found N missing migrations before current version` now means what it
  says: a migration in **that module** arrived behind that module's current version. Nothing
  passes `-allow-missing` any more, on purpose — within one module, a late migration is a
  real mistake. Renumber it above the module's highest applied version.
- `make migrate-down` stops at a module's baseline, which refuses to roll back: reversing it
  would mean dropping the whole module schema and its data. Recreate the database with
  `make db-reset` instead.

Current list: `account`, `telemetry`, `charging`, `analytics`.

## First-time / fresh environment

```bash
make env-setup   # bootstrap .env (DATABASE_URL, auto-generated SESSION_SECRET, Tesla/Google creds)
make db-setup    # create the app role + database + apply all migrations (one command, idempotent)
make up          # generate + migrate + build + run
```

Helpers: `make db-url` prints the derived DSN / DB name (sanity check, no changes);
`make migrate-status` shows which migrations are applied.

## Gotchas

- **Never** run a bare `go run .../templ generate` — always `make templ` (the pinned
  `go tool`). A bare `go run` pulls the templ CLI's transitive deps into `go.mod` as
  accidental `// indirect` requires.
- Generated files (`*_templ.go`, sqlc `db/*.go`) are committed, but editing their
  **source** (`.templ` / `queries.sql`) without regenerating means the build silently
  uses **stale** generated code. When in doubt, `make up` (or `make generate`).
- No hot reload: template / query / schema / code changes all require a **restart**.
- `make up` needs Postgres reachable (it runs `migrate-up`); if the DB is down it fails
  before building. `make migrate-down` / `make db-reset` are **destructive** (dev only).
- Secrets: `.env` holds the DB password, session secret, and Tesla/Google credentials —
  it is git-ignored; don't commit it. `make db-url` prints the DSN by design (local use).

## Other run targets

| Target | Purpose |
|---|---|
| `make up` | **The web server** — generate + migrate + build + run (`cmd/web`). |
| `make cmd-setup` | One-shot Tesla OAuth token capture (`cmd/setup`). |
| `make cmd-poller-once` | Run one telemetry collection cycle and exit (`cmd/poller --once`). May wake cars — real API calls. |
| `make cmd-explore-tesla` | Explore raw Fleet API JSON (`cmd/explore-tesla-api`). Costs a real API call; wakes the car. |
