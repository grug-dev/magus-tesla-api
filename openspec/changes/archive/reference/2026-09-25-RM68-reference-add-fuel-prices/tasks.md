# Tasks — RM68-reference-add-fuel-prices

Each task names its file(s), its `depends_on`, and whether it is `parallel_ok`. A task
marked `[leader-owned]` touches a file outside `internal/reference/` and needs an explicit
grant or the leader's own hand — `sqlc.yaml`, the `Makefile`, and the root `README.md`. Every
other task is `[module: reference worker]`. No unit tests in this change (roadmap D13).

**Ordering constraints:**

- Wave 1 (migrations + shared-file grants) before Wave 2 (query + Go port): `sqlc` validates
  `query.sql` against the migrations directory named in `sqlc.yaml`, so both must exist
  first.
- Wave 2's query (2.1) before its Go port (2.3, 2.4): the port's implementation file imports
  the sqlc-generated types `make sqlc` produces from the query.
- Wave 3 (module docs) after Wave 2: the docs describe the finished port, not a planned one.
- Wave 4 (root `README.md`) can run any time after Wave 1 — it only needs the module and its
  responsibility to be settled, not the finished code.

---

## Wave 1 — schema + shared-file grants

- [x] **1.1** **[module: reference worker]**
  `internal/reference/db/migrations/20260922000001_baseline.sql` — the schema-only baseline
  from `design.md` "Database Changes" §Full schema: `CREATE SCHEMA IF NOT EXISTS reference;`
  then `CREATE TABLE reference.fuel_prices` with all six columns and both constraints, plus
  `COMMENT ON TABLE`/`COMMENT ON COLUMN` statements written fresh from `design.md`'s
  column-by-column table — never copy a decision ID, tier number, or ticket ID into a SQL
  comment. `-- +goose Down` follows every other module's baseline convention exactly
  (`internal/charging/db/migrations/20260917000001_baseline.sql` is the file to mirror): it
  raises a Postgres exception rather than dropping anything, because an empty rollback would
  mark the baseline un-applied while the schema still exists.
  `depends_on`: — · `parallel_ok`: with 1.3, 1.4

- [x] **1.2** **[module: reference worker]**
  `internal/reference/db/migrations/20260922000002_seed_2026_08_price.sql` — one ordinary
  migration, `-- +goose Up` inserting exactly one row: `period = '2026-08-01', currency =
  'COP', price = 16000.00`. `-- +goose Down` deletes that one row by its `period` — this is
  an ordinary migration, not a baseline, so a real reversible `Down` is expected and correct
  here, unlike 1.1's. State in the file's header comment that this is the first of the
  one-migration-per-month price loads the roadmap describes (design.md TD2) — not a special
  case.
  `depends_on`: 1.1 · `parallel_ok`: no (same migrations directory, must not collide on
  filename ordering with 1.1)

- [x] **1.5** **[module: reference worker]**
  `internal/reference/db/migrations/20260922000003_seed_2026_09_price.sql` — one ordinary
  migration, same shape as 1.2: `-- +goose Up` inserts exactly one row, `period =
  '2026-09-01', currency = 'COP', price = 16331.00`. `-- +goose Down` deletes that one row
  by its `period`. This row makes September use its own price, not August's 16000.
  `depends_on`: 1.2 · `parallel_ok`: no (same migrations directory, filename order after
  1.2)

- [x] **1.3** **[leader-owned]** `sqlc.yaml` — append a new `sql:` entry for `reference`,
  mirroring the existing four entries exactly: `schema:
  internal/reference/db/migrations`, `queries: internal/reference/db/query.sql`,
  `gen.go.package: referencedb`, `out: internal/reference/db`, `sql_package: pgx/v5`,
  `emit_json_tags: false`, `emit_interface: false`, the standard `uuid →
  github.com/google/uuid.UUID` override, and one `rename:` line:
  `reference_fuel_price: "FuelPrice"`. Add a header comment above the entry explaining what
  the module owns, matching the other four entries' own comment style.
  Files: `sqlc.yaml`.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.4

- [x] **1.4** **[leader-owned]** `Makefile` — append `reference` to `MIGRATION_MODULES`:
  `account telemetry charging analytics reference` (design.md "Makefile / codegen check" —
  appended at the end, position has no correctness effect, kept for a stable log order and a
  minimal diff).
  Files: `Makefile`.
  `depends_on`: — · `parallel_ok`: with 1.1, 1.3

---

## Wave 2 — the query and the Go port

- [x] **2.1** **[module: reference worker]** `internal/reference/db/query.sql` (new file) —
  the `PricesForMonths` query exactly as specified in `design.md` §"The SQL", with its full
  doc comment. Then run `make sqlc` and report the result: it must generate
  `internal/reference/db/{models.go,query.sql.go,db.go}`, including a `FuelPrice` struct (via
  1.3's `rename:` entry) and a `PricesForMonths` method taking `PricesForMonthsParams{
  StartPeriod, EndPeriod pgtype.Date }` (or the pgx/v5 equivalent sqlc emits for two
  `sqlc.arg` date parameters — report the exact generated signature, since this task fixes
  what 2.3/2.4 must call).
  `depends_on`: 1.1, 1.3 · `parallel_ok`: no (blocks 2.2–2.4)

- [x] **2.2** **[module: reference worker]** `internal/reference/reference.go` (new file) —
  the package doc comment (this module's responsibility, one paragraph, no decision IDs), the
  `Reader` interface and `MonthPrice` struct exactly as specified in `design.md` §"The Go
  Port" (the sparse-result contract belongs in `PricesForMonths`'s own doc comment, not only
  in this tasks file), and the forward-declaring constructor `func NewReader(pool
  *pgxpool.Pool) Reader`. Does not compile until 2.3 supplies `newReader`'s body — this
  mirrors every sibling module's `<module>.go`/`<port>.go` split (e.g.
  `charging.go`/`monthly_capacity_reader.go`).
  `depends_on`: 2.1 · `parallel_ok`: with 2.3 (authoring only — they land together)

- [x] **2.3** **[module: reference worker]** `internal/reference/price_reader.go` (new
  file) — implement the port exactly as specified in `design.md` §"The Go Port": a narrow
  store interface over the one generated `PricesForMonths` method (mirroring
  `internal/analytics/monthly_reader.go`'s `monthlyMetricsStore` seam), an unexported
  `priceReader` struct, `newReader(pool *pgxpool.Pool) *priceReader`, the compile-time `var _
  Reader = (*priceReader)(nil)` assertion, and a `numericToFloat64` helper converting
  `pgtype.Numeric` to `float64` via `Float64Value()` — mirror
  `internal/analytics/mapping.go`'s `float64FromPgNumeric` exactly, kept local to this
  module (no shared helper package). `PricesForMonths` maps each returned row to a
  `MonthPrice`, propagating any mapping error rather than skipping the row (mirrors
  `monthlyReader.MonthlyMetricsBetween`'s identical "a bad row aborts the read" stance — a
  wrong price used silently is worse than a failed render). No logging decorator (design.md
  TD5) — do not add one.
  `depends_on`: 2.1 · `parallel_ok`: with 2.2

---

## Wave 3 — module docs

- [x] **3.1** **[module: reference worker]** `internal/reference/AGENTS.md` (new file) — an
  `Agent-Name: reference` header, a `## Doc-Pack (module)` section stating this module needs
  nothing beyond the base pack (mirroring `internal/clock/AGENTS.md`'s identical empty
  section, since this module has no htmx/UI concern of its own), `## Responsibility` (what
  this module owns and does not own — restate roadmap D1/D3/D6 in plain words, no decision
  IDs), `## Public Interface` (the `Reader` port, `PricesForMonths`'s sparse-result contract),
  `## Allowed Imports` / `## Data Ownership` (mirroring `internal/charging/AGENTS.md`'s
  shape: which files may import `pgtype` and `referencedb` — expected to be exactly
  `price_reader.go`, since `reference.go` declares only the interface), and `## Testing
  Notes` stating plainly that this tier ships no tests (roadmap D13) and that a future
  test, if ever written, must not assert against `pgtype` (the project's standing rule for
  every module).
  `depends_on`: 2.3 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: reference worker]** `internal/reference/README.md` (new file) — a
  short human-facing summary: what this module does, why it exists (one paragraph, mirroring
  `internal/tesla/README.md`'s "What this module does" opening), and a pointer to
  `AGENTS.md` for the full brief. Do not duplicate `AGENTS.md`'s content — this file is the
  short version.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1

---

## Wave 4 — root README

- [x] **4.1** **[leader-owned]** Root `README.md` — three edits, per `CLAUDE.md`'s
  "docs track structural change" rule:
  - "Project Structure" tree: add a `reference/` line under `internal/`, one sentence,
    matching the existing entries' style (e.g. `internal/reference/  # External reference
    values (gasoline price by month) — owned by no vehicle, no user`).
  - "Architecture" table: add one row for `internal/reference`, describing the module and
    its one table, mirroring the existing rows' length and tone.
  - Dependency graph: add `reference` under `LAYER 1 — domain modules & config`, as `reference
    ──────────────► reference/db` — a leaf with its own `db` package and no internal
    dependencies, exactly like `charging`'s line. Do not add it under `cmd/web`'s import
    list yet — this tier ships no `cmd/web` wiring (tier 2 is the first caller); the
    dependency graph must not claim a wire that does not exist.
  Files: `README.md`.
  `depends_on`: 3.1, 3.2 (the module's finished shape must be known before it is described)
  · `parallel_ok`: yes, relative to Wave 3

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any `region` or `product` column, on the table or in the query.** Rejected at the
  roadmap level (D3) and restated with rationale in `design.md`.
- **Any command, HTTP form, or poller step that writes a `fuel_prices` row.** Every price is
  a migration, by hand (roadmap D4). Do not add a `Writer` port of any kind.
- **Storing a computed km-per-gallon figure anywhere, including
  `analytics.vehicle_monthly_metrics`.** Rejected at the roadmap level (D6); design.md TD1's
  rationale restates why. This tier stores only the price, nothing derived from it.
- **Any `cmd/web` (or other `cmd/`) wiring that constructs `reference.NewReader`.** No
  caller exists until tier 2. Adding it here would be dead code with nothing to review it
  against.
- **The `/vehicle-stats` tile, its view model, or any i18n catalogue entry.** All of tier 2
  (`RM68-gateway-add-km-per-gallon-tile`). This tier's `gateway` folder is untouched.
- **Displaying the gasoline price anywhere.** Roadmap D5 — only the derived km-per-gallon
  figure is ever shown, and that figure is not this tier's concern either.
- **A `query_log.go` logging decorator for `Reader`.** Design.md TD5 — this port serves a
  live gateway request; it stays a silent pass-through, mirroring
  `analytics.MonthlyReader`'s identical choice.
- **A `vehicleref.Ref` parameter on `PricesForMonths`.** A gasoline price belongs to no
  vehicle, so there is nothing to authorize against — mirrors
  `charging.MonthlyCapacityReader.CapacityForMonth`'s identical no-`Ref` shape.
- **Any unit test file.** Roadmap D13. `design.md`'s Worked Example replaces the usual Test
  Contract — it is a hand-check target, not something to transcribe into a `_test.go` file.
- **Touching `kkpa/context/`.** No existing guide names `internal/reference` (it does not
  exist before this change), so nothing goes stale. Tier 2 is the first change that needs a
  KB entry, once the tile itself exists.
