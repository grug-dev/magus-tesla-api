# Go Conventions — magus-tesla-api

Authoritative Go coding conventions for this project. Any AI assistant (Claude Code,
OpenCode, Cursor, …) or human **must read and follow this file before writing or
editing Go code.** Referenced from `CLAUDE.md`. For the module structure and boundary
rules these conventions operate within, see [`architecture.md`](./architecture.md).

---

## Architecture

Standard Go project layout — modular monolith:

- `cmd/` — thin executable entry points, one per concern. No business logic here.
- `internal/` — private packages. Each package owns exactly one concern.

### Current packages

| Package | Path | Responsibility |
|---|---|---|
| `config` | `internal/config/` | Load `.env`, expose typed config, save tokens back to `.env` |
| `auth` | `internal/auth/` | Tesla OAuth URL builder, authorization code exchange, token refresh |
| `server` | `internal/server/` | Gin HTTP server that catches the OAuth redirect on `:8080/callback` |
| `vehicle` | `internal/vehicle/` | Authenticated Fleet API client + all vehicle data types and calls |

### Current commands

| Command | Path | What it does |
|---|---|---|
| `setup` | `cmd/setup/` | One-time OAuth flow — opens browser, catches callback, saves tokens to `.env` |
| `web` | `cmd/web/` | HTTP gateway serving the per-user vehicle dashboard (multi-tenant, DB-backed) |

---

## Coding Rules

- Every new Tesla API concern gets its own package under `internal/`. Never add charging commands to the vehicle package, never add telemetry to auth, etc.
- `cmd/` files must stay thin — they wire packages together. Zero business logic in `cmd/`.
- Never call `os.Getenv` outside of `internal/config/`. All other packages receive config via function arguments or the `Config` struct.
- All new Fleet API endpoint calls belong in `internal/vehicle/` or a new `internal/<domain>/` package (e.g. `internal/charging/`, `internal/telemetry/`).
- The user wants **modular packages** as a hard requirement — enforce this on every suggestion.
- **Interface-first module contract.** Every module's mandatory, always-present public API is a **Go interface** (its "port") — this is how the gateway and sibling modules call it (in-process, no HTTP). An HTTP `/api/{version}` JSON endpoint is a secondary, optional adapter added per module only when a real external consumer exists (see [`architecture.md`](./architecture.md) §3).
- **Vendor DTOs carry a service-name suffix.** Any struct that mirrors an external service's JSON ends in that service's name (`...Tesla`); our own domain models never carry a vendor suffix. Full rule + rationale in [`architecture.md`](./architecture.md) §6.
- **Vendor adapter DTOs stay in the external service's native units.** Every `...Tesla` struct
  field expressed in a non-metric unit (miles, mph, bar) **must** have a companion value-receiver
  method that returns the platform display unit, following the `OdometerKm()` pattern:
  - Name it `<Field>Km` / `<Field>Kmh` for distance/speed and `<Field>PSI` for pressure (e.g.
    `BatteryRangeKm()`, `ChargeRateKmh()`, `SpeedKmh()`, `TpmsPressureFLPSI()`).
  - Multiply by the package-level `milesToKm` / `barToPSI` constants — never hardcode the factor
    inline. These constants and their companions live in `internal/tesla` only; it is the
    platform's single owner of every conversion factor (`architecture.md` §6).
  - For pointer fields (e.g. `*float64` speed), the method returns a nil-safe pointer (`nil` in →
    `nil` out).
- **Persisted types and columns store display units, converted once on write.** Every domain
  field and database column that carries a unit — outside a vendor adapter DTO — is stored in the
  platform's display unit (kilometres `_km`, km/h `_kmh`, °C `_c`, PSI `_psi`, kWh `_kwh`, kW
  `_kw`, V `_v`, A `_a`, percent `_pct`), converted from the adapter's native unit exactly once, on
  the write path, by calling the adapter's `Km()`/`Kmh()`/`PSI()` companion — never by
  re-deriving the factor. No read path converts; formatting (rounding, symbols) happens only in
  the gateway, on an already-converted, plain unformatted stored number. Two exemptions: vendor
  adapter DTOs (above) and monetary amounts, which take no suffix and must instead be paired with
  a `currency` column. Full rule: `openspec/specs/unit-of-measure/spec.md`.
- **The platform's default time zone is `America/Bogota`; obtain it, "now", and a calendar day
  only through `internal/clock`.** Get the default zone via `clock.Zone()`, the current moment
  via `clock.Now()`, and a moment's calendar day via `clock.CalendarDay(t, loc)` — never call raw
  `time.Now()`, hardcode a zone name, or hand-roll a UTC-midnight truncation outside
  `internal/clock`. `internal/clock` imports stdlib `time` and nothing else, so leaning on it
  creates no import cycle. Two standing exemptions, neither of which this rule touches: the
  `pgtype.Date` UTC-midnight **storage encoding** (a representation, not a zone) and the
  gateway's per-user `browser_tz` cookie, which still wins over the default for a signed-in
  user's own pages. `cmd/*` is exempt as the composition root — it calls `time.LoadLocation`
  explicitly and on purpose. `make tz-guard` (`RM35-timezone-centralization` tier 7) enforces
  this repo-wide with a grep guard mirroring `money-guard`'s shape — a raw `time.Now()`, a
  hand-rolled midnight-of-a-day `time.Date(...)` construction, or a hardcoded IANA zone string
  outside `internal/clock` fails `make check`, unless marked with a trailing
  `// tz:allow: <reason>` comment for a genuinely deliberate exception. Full rule:
  `internal/clock/AGENTS.md`.

---

## Persistence (Postgres + sqlc + goose)

Conventions established by the `account` module — the project's first DB-backed module.
Every future DB-backed module follows the same shape. 

- **PostgreSQL via `pgx/v5` + `pgxpool`.** UUID primary keys (`gen_random_uuid()`, built into
  Postgres 13+). In generated Go, UUIDs are `github.com/google/uuid.UUID` (via a `sqlc.yaml`
  override). `timestamptz` stays the pgx/v5 default (`pgtype.Timestamptz`); the module converts
  it to/from plain `time.Time` at its DB→domain **mapping boundary** so `pgtype` never leaks into
  a module's public types.
- **Module-scoped DB package.** Each module's queries live in `internal/<module>/db`, generated
  by sqlc into package `<module>db` (e.g. `accountdb`). **No other module imports it** — this is
  how "no cross-module DB access" (`architecture.md` §2) is enforced at the package level.
- **`sqlc.yaml` — one `sql:` entry per module.** Add an entry when a module gains a DB; never
  merge two modules into one package. Keep the `uuid → google/uuid.UUID` override; map nullable
  columns (`pgtype.Text`) and `timestamptz` (`pgtype.Timestamptz`) to plain domain types in the
  module's mapping helpers, not via more overrides.
- **goose migrations are the single schema source.** SQL migrations live in
  `internal/<module>/db/migrations/<timestamp>_<name>.sql` (goose `-- +goose Up/Down`). sqlc's
  `schema:` points at that directory, so there is **no separate `schema.sql`** to keep in sync.
- **`DATABASE_URL` is the single source of truth** for the DSN. Only `internal/config` reads it
  (env access stays in config); the `Makefile` derives the DB name + admin connection from it.
- **Setup is one command:** `make db-setup` (idempotent create-if-missing + `goose up`).
  Regenerate code with `make sqlc` after editing any `query.sql` or migration. **Never run these
  as part of a build** — they are explicit developer/deploy steps.
- **Secrets at rest:** Tesla tokens are stored plaintext for now (local Postgres). Add column
  encryption before any non-local deployment — tracked as an open item on the `account` change.
- **DB tests are `TEST_DATABASE_URL`-gated** and self-skip when it is unset, so `go test ./...` stays
  green without a database. Pure logic (e.g. token-expiry math) is unit-tested without a DB.

### Testing — who writes them, who runs them

**Claude writes tests. The owner runs them.** This is binding in every session, inside the
pipeline or not, and `CLAUDE.md` §"Builds & local checks" states the same rule.

| Command | Who |
|---|---|
| `go build ./...`, `go vet ./...`, `gofmt -l` | **Claude may run these**, unprompted |
| `make build`, `make vet`, `make bins` | **Claude** |
| `make ui-guard`, `make i18n-guard`, `make money-guard`, `tz-guard`, `make migration-guard`, `make boundary-guard`, `make theme-guard`, `make archive-guard` | **Claude** — standalone guards, no tests |
| `go test ./...`, `make test`, `make test-with-db`, `make check` | **Owner only** — Claude never runs them |

Everything on Claude's side is a **cheap deterministic signal**: fails fast, prints a few
lines, needs no human. `go vet` in particular compiles `_test.go` files, so it catches
signature drift and API mistakes in tests that were never executed. Skipping such a signal
saves nothing — it converts it into a round-trip costing more than the output it replaced.

`make check` is `build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
theme-guard archive-guard test`; it is owner-only purely because of the trailing `test`. Claude
runs the other phases individually, so excluding `check` costs no guard coverage.

**Reporting rules — these are the point of the split:**

- Work that is complete but whose tests have not been run is **`awaiting-user-verification`**,
  never `done`. In the pipeline that is a task status; outside it, say so in plain words.
- Keep it distinct from **blocked**, which means stuck and needing intervention. Nothing is
  stuck here — it awaits a signal Claude is not allowed to produce.
- A passing suite is **the owner's report**, recorded as theirs. Claude never claims tests
  pass on its own authority, in a task status, a commit message, or a summary.
- When handing work back, give the **exact commands** to paste.

**Authoring order** (it follows from the above — unexecuted tests need to be right first time):

- **Pure/offline tests** — write them early, TDD-style. `go vet` verifies they compile.
- **`TEST_DATABASE_URL`-gated integration tests** — write them **last**, after the migration and
  the sqlc-generated types exist; they cannot compile before that.
- **But author their expected values up front**, in the change's `design.md`, before the
  implementation exists. A test written after reading the implementation confirms what the
  code does rather than what the design specifies. Contract-first authoring recovers most of
  TDD's benefit for tests that have no fast feedback loop.

**Do not test what a page looks like.** In `internal/gateway` — the only module that
renders HTML — assert what the markup *does* (attributes, htmx wiring, status codes, i18n,
pure functions), never how it *looks* (element order, section placement, CSS class strings,
decoration, copy). The UI changes often and is checked by hand, so an appearance assertion
costs a fix on every redesign and buys nothing. The full banned/required split lives in
`internal/gateway/AGENTS.md` §"Do not test what the page looks like" (MAG-39).

**Provisioning the test database — which entry point.** `internal/testdb` provisions a
throw-away Postgres (a reachable `TEST_DATABASE_URL` if there is one, otherwise a disposable
`postgres:16-alpine` container) with your migrations applied. It has two entry points, and
picking the wrong one produces a mystifying "relation does not exist" deep inside a test:

| Your package's fixtures touch… | Use | How |
|---|---|---|
| only its own module's tables | `testdb.Provision(ctx, fsys)` | `//go:embed db/migrations/*.sql`, then `fs.Sub`. See `internal/telemetry/testdb_test.go`. |
| more than one module's tables | `testdb.ProvisionDirs(ctx, dirs...)` | Relative migration **directories**, e.g. `"db/migrations"`, `"../telemetry/db/migrations"`. See `internal/analytics/testdb_test.go`. |

Why the second form takes paths rather than an `fs.FS`: the **`//go:embed` directive may not
contain `..` path elements**, so a package can only ever embed its own migrations. That is a
restriction on the directive, not on the filesystem — and `go test` always runs a test binary
with its own package directory as the working directory, so a relative `../<module>/db/migrations`
resolves reliably from a `_test.go` file.

`ProvisionDirs` applies each directory with its **own goose provider, in the order given**. It
deliberately does not merge them into one filesystem: migration versions are unique within a
module but **not across the repo** (`internal/account` and `internal/charging` both ship a
`20260720000001`), so a merged filesystem dies on the collision. Per-directory sequencing is
also exactly what the Makefile's `migrate-up` loop over `MIGRATIONS_DIRS` already does, and for
the same reason both pass goose's allow-missing/out-of-order option: every module applies its
own directory against ONE shared `goose_db_version` table, so a directory's versions are
routinely lower than versions another module already recorded.

Ordering between directories matters where one module's migration READS another's table. There
are still no cross-module foreign keys ([`architecture.md`](./architecture.md) §2), but since
RM29 tier 6 there is one such read: `internal/charging`'s `20260823000001_add_charge_sessions`
backfills the table it creates — then `public.charge_sessions`, since RM39 tier 3
`charging.supercharger_sessions` — from `telemetry.supercharger_history` (named
`telemetry.supercharger_sessions` until RM39 tier 4 moved and renamed it), so `telemetry` must
precede `charging` for that data to land. The migration filename and its own SQL still say
`charge_sessions`: historic migrations are never edited (RM39 D1), so they keep the names that
were current when they ran. `MIGRATIONS_DIRS` already orders them that way.

This is a **soft** dependency, deliberately. The backfill sits inside a
`to_regclass`-guarded `DO $$ … $$` block, so on a database where telemetry's table is absent it
emits a NOTICE and moves on instead of failing. Ordering therefore affects **data completeness,
never migration success** — a fresh database provisioned in the wrong order still migrates
green, it just backfills nothing.

Ordering is also why a cross-module **DROP** must never share a change with the backfill that
reads the dropped columns: goose walks the directories in `MIGRATIONS_DIRS` order, each to
completion, so version numbers cannot reorder work across modules. A `telemetry` DROP would run
before a `charging` backfill no matter how the two files are numbered. Split the two across
changes — expand first, contract once the expand is confirmed applied.

**Seeding another module's tables.** Prefer that module's public writer where one exists (e.g.
`charging.NewWriter(pool).Create`). Where none exists, **direct `INSERT`s from the `_test.go`
file are the right answer** — do not add an exported writer to another module just to seed a
fixture, and never import `internal/tesla` to drive a collector. `internal/analytics`'s
`db_integration_test.go` is the reference: `telemetry` exposes no public writer for a single
snapshot and none at all for a Supercharger session, so its fixtures are seeded with direct
SQL (RM29 decision D19). This is a test-only concession and does not weaken the boundary rule —
production code still reaches another module only through its public port.

### Read optimization (project-wide)

This system has an **asymmetric workload** — ~99% reads, ~1% writes (the nightly
telemetry batch at 03:30). See [`architecture.md`](./architecture.md) §7 for the
full rationale. The DB-level conventions below are binding for every DB-backed
module:

- **`account_id` is the leading index column** on every multi-tenant table. Every
  dashboard read scopes by account; an index without `account_id` first forces a
  scan when the planner can't pre-filter by tenant. Reference: the
  `(account_id, tesla_id, captured_at)` index in `internal/telemetry/db/migrations/`.
- **Unit-bearing columns are named with their unit suffix.** Every column storing a value with a
  unit ends in `_km`, `_kmh`, `_c`, `_psi`, `_kwh`, `_kw`, `_v`, `_a`, or `_pct` — the unit is
  readable from the column name alone, no migration or comment required. Identifiers, timestamps,
  dates, counts, names, states, and flags (`captured_at`, `tesla_id`,
  `max_range_charge_counter`) carry no unit and take **no** suffix. Two categories are exempt from
  the suffix by design: vendor adapter DTOs (native units — see §Coding Rules) and monetary
  amounts, which take no suffix and must instead be paired with a `currency` column. Full rule:
  `openspec/specs/unit-of-measure/spec.md`.
- **Always store raw `JSONB` when ingesting external API responses — it is the
  insurance policy, not an optional companion.** Any table that persists a response
  from an external API (Tesla Fleet API, any third-party) MUST include a
  `raw_data JSONB NOT NULL` column holding the lossless, unmodified payload. This is
  not a read-optimization choice — it is a schema-drift hedge:
  - If the external API renames or reshapes a field, only the extraction code (the
    `...Tesla` DTO JSON tags in `internal/tesla`) needs updating — the table schema
    and all historical rows stay valid.
  - If you later want a field you weren't extracting, you backfill from `raw_data`
    with a one-time SQL `UPDATE` — no re-calling the API (paid, rate-limited,
    wakes the car), no lost history.
  - The raw column is write-once-read-never-unless-backfilling; it is never the hot
    read path.
  Reference: `vehicle_snapshots.raw_data` stores the full `vehicle_data` payload.
- **Extract typed columns for hot reads alongside the raw `JSONB`.** In addition to
  the mandatory `raw_data JSONB` (above), extract the fields dashboards need into
  typed, indexed columns. Dashboards read the typed columns — never extract from
  JSONB on the hot path. Reference: `vehicle_snapshots.battery_level`, `odometer`,
  etc. alongside `raw_data`.
- **`DISTINCT ON (x) ... ORDER BY x, time DESC` for "latest per X" queries.** One
  Postgres index scan, no N+1. Reference: `LatestSnapshotsByAccount` in
  `internal/telemetry/db/query.sql`. Write batch reads at the module interface
  level (`LatestSnapshotsByAccount` — all vehicles in one query), never per-entity
  helpers (`LatestSnapshotForVehicle`) that the caller must loop over.
- **Append-only inserts for historical event tables.** No `UPDATE`/`DELETE` on
  snapshot/history tables — they are immutable. Writes are cheap (blind `INSERT`);
  reads are indexed. Reference: `vehicle_snapshots`, `poll_attempts`.
- **Pre-compute dashboard summaries during the nightly batch.** If a dashboard
  needs an aggregation (daily distance, weekly energy, monthly cost), compute it in the nightly
  `Collector` batch and write it to a summary table (or materialized view). Dashboard reads do
  `SELECT ... FROM summary_table`, not a runtime `GROUP BY` over months of raw rows. Summary
  tables live in the owning module's `db/` package, written by `Collector`, read by `Reader`.
- **HTTP date-filter convention (gateway).** Every date-filtered gateway HTTP endpoint takes
  `?start=YYYY-MM-DD&end=YYYY-MM-DD` (both whole calendar days, UTC-midnight-bounded, `end`
  inclusive), never a `?days=N` count. Canonical rule + the bounded-window rationale (90-day
  cap protecting the read-heavy hot path): see `internal/gateway/AGENTS.md` "HTTP date-filter
  convention". Reference: `GET /ui/dashboard/history`
  (`RM8-gateway-history-date-range`, Linear MAG-7).

---

## Error-handling pattern (401 / auto-refresh)

- `tesla.ErrUnauthorized` is the sentinel error returned by the adapter on 401 — use `errors.Is()` to detect it in any caller that needs the pattern.
- **Token refresh is owned by the `account` module** (`internal/account`): `account.AccessTokenFor` proactively refreshes an expired (or near-expiry) stored token before handing it out, atomically rotating the single-use refresh token inside a `FOR UPDATE` transaction. Callers wrap the resulting token in `tesla.Credentials` before calling the adapter.
- A *reactive* refresh-on-401 retry (recovering from a token Tesla revoked despite it not being time-expired) is a planned follow-up, also to live in the `account` module.
- Reuse this sentinel + `errors.Is()` shape in any new code that calls the Fleet API. The token *lifetimes* and operational behavior are documented in `CLAUDE.md` → "Token Behavior".

---

## Running

```bash
# Install / update dependencies (first time or after go.mod changes)
go mod tidy

# One-time OAuth setup — single-user smoke path; saves tokens to .env.
# Re-run only when the refresh token expires (every 3 months).
go run ./cmd/setup

# Multi-tenant web gateway — serves the per-user vehicle dashboard.
# Requires DATABASE_URL and SESSION_SECRET in .env.
go run ./cmd/web
```
