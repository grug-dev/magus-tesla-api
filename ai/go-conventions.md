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

- **Type names must carry the domain word.** Before naming a new type, write one
  sentence: "this thing does X". The name must contain X's key word. Generic
  suffixes that say nothing about the domain are banned: `Processor`, `Manager`,
  `Handler`, `Helper`, `Data`, `Info`, `Object`, `Thing`. Test: if the type moved
  to another package, would the name still mean something? `app.Processor` fails —
  `app.Processor` fails — it could process anything. `app.VehicleDataCycle` passes.
  `make naming-guard` enforces this: a new type declaration ending in a banned
  suffix fails `make check` (escape hatch: `// naming:allow: <reason>`); the six
  pre-rule names warn only. (The existing `Processor` port in `internal/app`
  predates this rule; renaming it is a separate, explicit decision, not a
  drive-by fix.)
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

## Logging

- **stdlib `log` only — no `slog`, no third-party logger.** A deliberate hold, recorded in
  `internal/telemetry/query_log.go` (roadmap D8).
- **Every log line in a domain module goes through `internal/logging`.** Call
  `logging.Note(typ, method, format, args…)` — never a raw `log.Printf`. It renders the
  platform-wide format:

  ```
  [Type] [Method] message
  ```

  Example: `logging.Note("Processor", "recalculateAnalytics", "gap reconciliation: %s → %s", start, end)`
  logs `[Processor] [recalculateAnalytics] gap reconciliation: 2026-09-01 → 2026-09-14`.
- **`Type`** is the receiver type's name. When the concrete type is unexported, use its
  exported port name (`"Processor"` for `*processor`, `"Reader"` for `*loggingReader`);
  when there is no exported port, the declared name (`"loggingStore"`). For a
  package-level function, use the package name (`"telemetry"`).
- **`Method`** is the enclosing function or method name as declared — unexported names stay
  lowercase, so a log line maps 1:1 to the source.
- **The message keeps a short topic prefix** (`gap reconciliation:`, `telemetry query:`,
  `fleet api:`) after the brackets, so lines stay greppable by topic.
- **`cmd/` may use `log.Printf` / `log.Fatalln` directly** for startup, shutdown, and fatal
  exits — the composition root is exempt.
- **`make logging-guard` enforces this repo-wide** (mirrors `tz-guard`'s shape): a raw
  stdlib `log` call in `internal/` outside `internal/logging` fails `make check`. Escape
  hatch: a trailing `// log:allow: <reason>` comment on the same line.
- **Never log a credential or a `raw_data` payload** (`internal/telemetry`'s standing rule).
  `logging.Note` changes nothing about that.
- Gold standards: `internal/app/processor.go` (handler/orchestration style),
  `internal/telemetry/query_log.go` (decorator style). Helper: `internal/logging`.

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
| `make lint` / `golangci-lint run ./...` | **Claude** — lints only, runs no test |
| `make build`, `make vet`, `make bins` | **Claude** |
| `make ui-guard`, `make i18n-guard`, `make money-guard`, `tz-guard`, `logging-guard`, `make migration-boundary-guard`, `make boundary-guard`, `make theme-guard`, `make archive-guard` | **Claude** — standalone guards, no tests |
| `go test ./...`, `make test`, `make test-with-db`, `make check` | **Owner only** — Claude never runs them |

Everything on Claude's side is a **cheap deterministic signal**: fails fast, prints a few
lines, needs no human. `go vet` in particular compiles `_test.go` files, so it catches
signature drift and API mistakes in tests that were never executed. Skipping such a signal
saves nothing — it converts it into a round-trip costing more than the output it replaced.

**`make lint` (golangci-lint, config `.golangci.yml`) is the same kind of signal, and it
catches three classes `go vet` cannot see**: an **ignored error return** (`errcheck`), **dead
code** (`unused` — vet never reports it), and a **leaked resource** (`bodyclose`,
`rowserrcheck`, `sqlclosecheck`). Run it before handing work back; a finding there is one a
reviewer would otherwise have to raise by hand.

Keep the two kinds of check apart. `.golangci.yml` holds **Go-correctness** rules that an
off-the-shelf linter already implements. The `make *-guard` targets hold **this project's own**
rules (i18n, boundaries, time zone, units, themes) — rules no public linter knows about. Do not
re-implement a guard as a custom linter, and do not hand-roll a grep guard for something
golangci-lint already checks.

`make check` is `build vet lint ui-guard i18n-guard money-guard tz-guard logging-guard
migration-boundary-guard boundary-guard theme-guard vehicleref-guard tenancy-guard
naming-guard archive-guard
test`; it is owner-only purely because of the trailing `test`. Claude
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

**Do not test migrations. Delete migration tests when you find them.** Never write a test
that runs a migration — rolls it back, re-applies it, and asserts what its backfill wrote.
Never add one when asked to "add tests" broadly, and delete any that already exist rather
than repairing them. **Migrations are verified by the owner, by inspecting the database
directly.** That is the check that matters, and it is the one being done.

The cost is one-sided. A migration is frozen the moment it is applied, but its test fixture
is not: the fixture must keep seeding the schema that migration expected. So every later
change that touches those columns breaks a test of work that already ran, correctly, on every
database that needed it — and the migration itself can no longer even reach a row written by
the new code. The test then measures nothing and still has to be fixed. Precedent:
`TestMigration_TpmsPressureBackfill` seeded `vehicle_snapshots.account_id` so a 2026-09-08
analytics backfill could join on it; MAG-65 re-keyed that table on `tesla_id` and the test
broke. It was deleted, not repaired.

What still belongs in a test is the **behaviour after** a migration — the queries, ports and
derivations that read the new shape. Those are ordinary integration tests against the
provisioned schema, and they are not migration tests.

**Provisioning the test database — which entry point.** `internal/testdb` provisions a
throw-away Postgres (a reachable `TEST_DATABASE_URL` if there is one, otherwise a disposable
`postgres:16-alpine` container) with your migrations applied. It has two entry points, and
picking the wrong one produces a mystifying "relation does not exist" deep inside a test:

| Your package's fixtures touch… | Use | How |
|---|---|---|
| only its own module's tables | `testdb.Provision(ctx, fsys)` | `//go:embed db/migrations/*.sql`, then `fs.Sub`. See `internal/telemetry/testdb_test.go`. |
| more than one module's tables | `testdb.ProvisionDirs(ctx, dirs...)` | Relative migration **directories**, e.g. `"db/migrations"`, `"../telemetry/db/migrations"`. See `internal/analytics/testdb_test.go`. |

**Opt-in: a real local database instead of the testcontainer.** Set
`TEST_DATABASE_URL=postgres://localhost:5432/magus_test?sslmode=disable` (or any other
Postgres you keep around) and `internal/testdb` uses it instead of starting a container.
Run `make db-setup-test` first — and again before any later test run — to create it if
missing, re-own its schemas, and apply pending migrations. This is faster and needs no
Docker daemon, but the database can go stale between runs; `make db-setup-test` is what
keeps it current, and nothing touches it unless `TEST_DATABASE_URL` is set on purpose.

Why the second form takes paths rather than an `fs.FS`: the **`//go:embed` directive may not
contain `..` path elements**, so a package can only ever embed its own migrations. That is a
restriction on the directive, not on the filesystem — and `go test` always runs a test binary
with its own package directory as the working directory, so a relative `../<module>/db/migrations`
resolves reliably from a `_test.go` file.

`ProvisionDirs` applies each directory with its **own goose provider**, into that module's own
version ledger. It deliberately does not merge them into one filesystem: a merged filesystem
puts several modules' files in one sequence, which is the arrangement that lets two modules
collide on a version number and lets one module's migration read another's schema. This is
exactly what the Makefile's `migrate-up` loop and the deploy's `cmd/migrate` do.

### Migrations: one baseline per module, one ledger per module

Each module's migration history starts from a single **baseline** — one file that creates that
module's whole schema and reads nothing. Each module records its applied versions in
**`<module>.goose_db_version`**, inside the Postgres schema it already owns. Everything below
follows from those two facts.

- **Two modules MAY use the same version number.** All four baselines are `20260917000001`.
  There is no cross-module uniqueness rule any more, and no guard for one. Number a new
  migration however you like within your own module; only your module's numbers must increase.
- **The order the directories are applied in does not matter.** Nothing can need another module
  to have run first. `MIGRATION_MODULES` in the `Makefile` keeps a stable order only so two
  runs produce comparable logs.
- **A migration may name only its own module's schema.** This is the same boundary rule that
  binds runtime code: a module that may not read another module's tables through Go must not
  read them in SQL either. `make migration-boundary-guard` fails any migration that does, and
  `COMMENT ON` statements are excluded because a comment cannot read a row. Need another
  module's data in a backfill? Route it through that module's public Go interface, or have the
  owning module write the value itself.
- **Nothing passes goose's allow-missing / out-of-order option.** It used to be required,
  because the shared ledger made a module's own lower version look like a late migration. With
  per-module ledgers a late migration is a real mistake, so goose's own check is on. Do not add
  the flag back to silence a complaint — read the complaint.
- **A baseline's `Down` raises an exception instead of doing nothing.** An empty `Down` would
  mark the baseline un-applied while every object it created still exists, and the next
  `migrate-up` would fail on `relation already exists` — which stops `web` and `poller`,
  because both wait for the migration step to succeed. `make migrate-down` therefore stops at a
  baseline. Recreate the database (`make db-reset`) instead of rolling one back.
- **The module's Postgres schema is created by the runner, not only by the baseline.**
  goose creates its version table before running any migration, and that table lives in the
  module's schema — which on an empty database does not exist yet. So `cmd/migrate`,
  `internal/testdb` and every `Makefile` goose loop run `CREATE SCHEMA IF NOT EXISTS <module>`
  first (`config.MigrationDir.EnsureSchemaSQL`). The baseline keeps its own idempotent
  `CREATE SCHEMA IF NOT EXISTS` so the file still stands alone. This is also why
  `make migrate-up` needs `psql` on PATH now, not just `goose`.
- **A baseline never runs on an existing database.** Dev and prod have the baseline version
  recorded as applied without it ever executing. So a change made **inside a baseline** reaches
  new databases only. Anything that must also reach dev and prod is an ordinary migration after
  the baseline — this is why dropping a column means writing a `DROP COLUMN` migration, not
  deleting the line from the baseline.
- **The runner records it, nobody does it by hand** (`config.MigrationDir.StampBaselineSQL`,
  called from `cmd/migrate` before goose, and from the `Makefile` goose loops via
  `cmd/migrate -stamp-only`). It writes the baseline into the module's ledger only when the
  module's schema already holds a table and the ledger is still empty — a database built
  before the squash. It records nothing on a fresh database and nothing on one already
  migrated, so it runs unconditionally on every deploy. **One-time code**: delete it once no
  pre-squash database is left.

Why this replaced the previous arrangement: all four directories used to share one
`public.goose_db_version`, so a version number used twice was recorded once and the second file
**skipped in silence**, reported as applied. `charging/20260720000001_require_location_kind.sql`
was never run in any database for that reason. Four migrations also read another module's
schema, which worked only while the folder order happened to match the dependency direction —
and when `telemetry` later re-keyed `vehicle_snapshots`, an `analytics` migration joining the
dropped column failed on every fresh database, so the drop was abandoned (MAG-65).

**Seeding another module's tables.** Prefer that module's public writer where one exists (e.g.
`charging.NewWriter(pool).Create`). Where none exists, **direct `INSERT`s from the `_test.go`
file are the right answer** — do not add an exported writer to another module just to seed a
fixture, and never import `internal/tesla` to drive a collector. `internal/analytics`'s
`db_integration_test.go` is the reference: `telemetry` exposes no public writer for a single
snapshot and none at all for a Supercharger session, so its fixtures are seeded with direct
SQL (RM29 decision D19). This is a test-only concession and does not weaken the boundary rule —
production code still reaches another module only through its public port.

**Raw SQL in tests is invisible to `go vet`.** It is a string, so a fixture naming a column
a migration just dropped or renamed still compiles, and `vet` stays green. Two habits:

- When a change drops or renames a column, **scan the SQL strings in `_test.go` too** — and
  scan across line breaks. A table name and its column often sit on different lines, so a
  line-based `grep` finds some hits and misses others.
- **Assert `RowsAffected()` on a fixture `UPDATE` or `DELETE`.** A write that matches no row
  does not error. The fixture then does nothing, and the assertions after it pass or fail for
  a reason that has nothing to do with the code under test.

MAG-65 hit this three times in one change and paid a full test round each time. The last one
reported a bug in `Recalculate` that did not exist: its `UPDATE` still filtered on the removed
`account_id`, matched zero rows, and the re-capture it was meant to simulate never happened.

### Read optimization (project-wide)

This system has an **asymmetric workload** — ~99% reads, ~1% writes (the nightly
telemetry batch at 03:30). See [`architecture.md`](./architecture.md) §7 for the
full rationale. The DB-level conventions below are binding for every DB-backed
module:

- **A multi-tenant table keys on one of three columns — pick by what the row means.**

  | Rule | Applies when | Tables |
  |---|---|---|
  | Key on `tesla_id` | `tesla_id` is `NOT NULL` | `telemetry.vehicle_snapshots`, `analytics.vehicle_metrics`, `analytics.vehicle_metric_watermarks`, `analytics.charge_gaps` |
  | Key on `vin` | `tesla_id` is nullable — the row can exist before the vehicle is known | `telemetry.supercharger_history`, `charging.supercharger_sessions` |
  | Keep `account_id`, demoted to an attribute | the row records who acted, not what the car did | `charging.manual_charge_entries`, `telemetry.poll_attempts` |

  `make tenancy-guard` enforces this: it fails if a module's query file outside
  `internal/account` filters on `account_id` (escape hatch: `-- tenancy:allow: <reason>`, SQL's own comment syntax).

  Do not add an index just because the old table had one. A `UNIQUE (a, b)`
  constraint already builds a btree that serves equality on `a`, point lookups on
  `(a, b)`, range scans on `b` within one `a`, and `ORDER BY b DESC` pinned to one
  `a`. A separate `(a, b DESC)` index next to it repeats work the constraint
  already does. Justify every index against a query that exists today.
- **Unit-bearing columns are named with their unit suffix.** Every column storing a value with a
  unit ends in `_km`, `_kmh`, `_c`, `_psi`, `_kwh`, `_kw`, `_v`, `_a`, or `_pct` — the unit is
  readable from the column name alone, no migration or comment required. Identifiers, timestamps,
  dates, counts, names, states, and flags (`captured_at`, `tesla_id`,
  `max_range_charge_counter`) carry no unit and take **no** suffix. Two categories are exempt from
  the suffix by design: vendor adapter DTOs (native units — see §Coding Rules) and monetary
  amounts, which take no suffix and must instead be paired with a `currency` column. Full rule:
  `openspec/specs/unit-of-measure/spec.md`.
- **A day-over-day delta column or Go field is named `_delta_calc` / `DeltaCalc`, never a bare
  `_calc` / `Calc`.** A column or field holds a "delta" when it stores today's value of a metric
  minus yesterday's value of the *same* metric. Name it so the reader can tell from the name
  alone, without opening the migration. A bare `_calc` / `Calc` name stays valid for a derived
  value that is **not** itself a day-over-day difference — for example `km_per_pct_calc` (a
  same-day rate) and `inferred_capacity_kwh_calc` (a same-row ratio). `make delta-guard` enforces
  this (escape hatch: `-- delta:allow: <reason>` in SQL, `// delta:allow: <reason>` in Go). Full
  rule: `openspec/specs/unit-of-measure/spec.md`. The guard's Go leg **skips
  sqlc-generated files** (a `Code generated by ... DO NOT EDIT.` first line): sqlc emits one
  struct field per column, so a column that already passed the SQL leg would be judged a
  second time in Go, where the escape hatch is unreachable — the marker must sit on the
  same line, and the next `sqlc generate` would erase it.
- **A derived column goes in Postgres as `GENERATED ALWAYS AS ... STORED` when, and only
  when, it reads nothing but its own row.** Two things follow from generating it, and both
  are the reason to prefer it: the formula exists exactly once, so it cannot drift, and the
  migration needs no backfill because Postgres fills every existing row itself. The moment
  the value needs another row — yesterday's figure, a running total — a generated column
  cannot express it, and the derivation belongs in Go like every other one. `ADD COLUMN ...
  GENERATED ... STORED` rewrites the table under an `ACCESS EXCLUSIVE` lock, so schedule it
  away from a writing job. **Never name a generated column in an INSERT or UPDATE**: sqlc
  emits a struct field for it and the code compiles, but Postgres rejects the write at
  runtime. Today the only one is `analytics.vehicle_metrics.tesla_range_100_pct_km_calc`.
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
  Postgres index scan, no N+1. Reference: `LatestSnapshotsByVehicles` in
  `internal/telemetry/db/query.sql`. Write batch reads at the module interface
  level (`LatestSnapshotsByVehicles` — all vehicles in one query), never per-entity
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
