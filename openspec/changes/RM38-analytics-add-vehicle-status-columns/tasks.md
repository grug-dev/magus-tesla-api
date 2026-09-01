# Tasks — RM38-analytics-add-vehicle-status-columns

Ownership legend: **[module: analytics worker]** — inside `internal/analytics/`, the
sandboxed worker's own territory (this tier touches no other module). **[leader]** —
outside the module sandbox (root `README.md`, `sqlc.yaml` if it ever needed a change —
it does not, since the `analytics` entry already exists from RM29 tier 3). See
design.md D1–D8 for the rationale behind each task.

**Hard ordering constraints:**
- Wave 1 (migration) must land before Wave 2 (sqlc queries + regeneration) — sqlc's
  `schema:` reads the migrations directory directly; the new columns must exist there
  before `LatestVehicleMetricsByAccount`/the extended `UpsertVehicleMetric` can
  reference them.
- Wave 2 must land before Wave 3 (Go domain code) — `vehicleMetricRow`'s new fields,
  `upsertVehicleMetricParamsFrom`'s extension, and the new `reader.go` method all
  reference the generated `analyticsdb` types Wave 2 produces.
- Wave 4 (offline derivation tests) needs Wave 3's `consumed.go` change to compile
  against, but is authored from design.md's Test Contract (Fixtures RM38-A/B, already
  fixed) — it can be drafted in parallel with Wave 3 and wired in once Wave 3 lands.
- Wave 5 (DB-integration tests) needs Wave 2's generated types AND Wave 3's code —
  cannot compile before both exist. This is the **migration → sqlc generate → Go
  code → DB-integration test** serial chain the dispatch calls out explicitly; Wave 5
  is therefore the final code wave, not parallelizable with anything before it.
- Wave 6 (docs) describes the POST-change state, so it runs after Wave 3 lands
  (content, not compile, dependency) — it does not need Wave 4/5 to be describable.
- Wave 7 (verification) runs after every other wave.

## Wave 1 — migration (module: analytics worker)

- [ ] **1.1** `internal/analytics/db/migrations/<timestamp>_add_vehicle_status_columns.sql`
  — goose migration: `ALTER TABLE vehicle_metrics ADD COLUMN` for all eight columns
  (`locked BOOLEAN`, `sentry_mode BOOLEAN`, `car_version TEXT`, `inside_temp_c DOUBLE
  PRECISION`, `outside_temp_c DOUBLE PRECISION`, `charging_state TEXT`,
  `charge_limit_soc_pct INTEGER`, `captured_at TIMESTAMPTZ`), all nullable, no
  `DEFAULT` (design D2/D7) — exact DDL and every `COMMENT ON COLUMN` body in
  design.md "Database Changes" § "Schema: `vehicle_metrics` (ALTER, not CREATE)" is
  authoritative; copy it verbatim, do not paraphrase the `sentry_mode` ambiguity
  comment (design D8 depends on its exact wording surviving into the DB). `-- +goose
  Down` drops all eight columns (`ALTER TABLE vehicle_metrics DROP COLUMN ...` ×8).
  **Same migration also creates `idx_vehicle_metrics_latest ON vehicle_metrics
  (account_id, tesla_id, metric_date DESC)`** — added at the database design gate by the
  owner's explicit instruction, revising design.md's original "no new index" conclusion;
  rationale and accepted write cost are in design.md "Index Plan". `-- +goose Down` drops
  the index as well as the eight columns.
  Timestamp must sort after the module's latest existing migration
  (`20260828000001_migrate_vehicle_metric_watermarks_source.sql`) — use today's date
  at implementation time, not a placeholder.
  `depends_on`: — · `parallel_ok`: no (single task, nothing else in this wave)

## Wave 2 — sqlc queries + regeneration (module: analytics worker)

- [ ] **2.1** `internal/analytics/db/query.sql` — add `LatestVehicleMetricsByAccount`
  exactly per design.md D6 (`SELECT DISTINCT ON (tesla_id) tesla_id,
  battery_level_pct, battery_range_km, odometer_km, inside_temp_c, outside_temp_c,
  locked, sentry_mode, car_version, charging_state, charge_limit_soc_pct, captured_at
  FROM vehicle_metrics WHERE account_id = @account_id ORDER BY tesla_id,
  metric_date DESC`) — a `:many` query, doc comment citing the Index Plan (design.md
  "Index Plan" #1 — served by `vehicle_metrics_account_tesla_date_unique`, no new
  index, mirroring `telemetry`'s `LatestSnapshotsByAccount`).
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [ ] **2.2** `internal/analytics/db/query.sql` — extend `UpsertVehicleMetric`'s
  column list, `VALUES`, and `ON CONFLICT ... DO UPDATE SET` clause with all eight
  new columns (design D1/D3 — every one of the eight is part of the SET clause,
  refreshed on every re-derivation exactly like every other non-`created_at` column
  already is, per the existing query's own "a stale value here must not survive a
  re-derivation" doc comment). Do not add the eight to `created_at`'s exclusion list
  — they are ordinary refreshed columns, not first-write-only ones.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [ ] **2.3** Run `make sqlc` (Claude may run — allowed codegen command,
  `CLAUDE.md` "Builds & local checks") to regenerate
  `internal/analytics/db/*.go` from 2.1/2.2's queries against 1.1's migration.
  Confirm `UpsertVehicleMetricParams` gained the eight new fields and
  `LatestVehicleMetricsByAccountRow` was generated with all twelve selected columns.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

## Wave 3 — analytics domain code (module: analytics worker)

- [ ] **3.1** `internal/analytics/analytics.go` — add the `VehicleStatus` domain type
  (design D4, exact field list and doc comments as specified there) and
  `LatestMetricsByAccount(ctx context.Context, accountID uuid.UUID)
  ([]VehicleStatus, error)` to the `Reader` interface (design D5, doc comment
  verbatim from design.md — including the "never `telemetry.Snapshot`" and
  "unspecified order" contract statements).
  `depends_on`: 2.3 · `parallel_ok`: with 3.2

- [ ] **3.2** `internal/analytics/consumed.go` — `vehicleMetricRow` gains the eight
  new fields (`Locked *bool`, `SentryMode *bool`, `CarVersion *string`,
  `InsideTempC *float64`, `OutsideTempC *float64`, `ChargingState *string`,
  `ChargeLimitSocPct *int`, `CapturedAt *time.Time` — pointer-typed on the Go struct
  even though several source fields on `telemetry.Snapshot` are non-pointer, because
  the DB column is nullable and this struct is the pre-mapping shape).
  `deriveVehicleMetrics` populates all eight from `cur` in **both** the
  predecessor-exists and no-predecessor branches (design D3 — this is the task that
  implements D3's core decision; do not gate these eight behind the `prev == nil`
  check the five `_calc` columns are gated behind).
  `depends_on`: 2.3 · `parallel_ok`: with 3.1

- [ ] **3.3** `internal/analytics/mapping.go` — add the pg-conversion helpers the
  eight new columns need: `pgBoolFromPtr(*bool) pgtype.Bool`, `pgTextFromPtr(*string)
  pgtype.Text` (generic — do not conflate with the existing `pgTextFromMissingType`,
  which encodes a different zero-value rule), `pgTimestamptzFromPtr(*time.Time)
  pgtype.Timestamptz`, plus the reverse direction for `LatestMetricsByAccount`'s
  read side: `ptrBoolFromPg(pgtype.Bool) *bool`, `ptrStringFromPg(pgtype.Text)
  *string`, `ptrFloat64FromPg(pgtype.Float8) *float64` (reuse `pgFloat8FromPtr`'s
  existing nil-check idiom, mirrored for the reverse direction),
  `ptrIntFromPg(pgtype.Int4) *int`, `ptrTimeFromPg(pgtype.Timestamptz) *time.Time`.
  `depends_on`: 2.3 · `parallel_ok`: with 3.1, 3.2

- [ ] **3.4** `internal/analytics/recalculate.go` — `upsertVehicleMetricParamsFrom`
  extends its returned `analyticsdb.UpsertVehicleMetricParams` with the eight new
  fields, each mapped through 3.3's `pg*FromPtr` helpers (design D1/D3).
  `depends_on`: 3.2, 3.3 · `parallel_ok`: no

- [ ] **3.5** `internal/analytics/reader.go` — `vehicleMetricsStore` interface gains
  `LatestVehicleMetricsByAccount(ctx context.Context, accountID uuid.UUID)
  ([]analyticsdb.LatestVehicleMetricsByAccountRow, error)` (design D6 — `*analyticsdb.
  Queries` satisfies it automatically, no adapter needed, mirroring this interface's
  existing two methods). `reader` implements `LatestMetricsByAccount`: calls the
  store method, maps each row to a `VehicleStatus` via 3.3's `ptr*FromPg` helpers —
  no derivation logic, pure row-to-domain mapping, mirroring `ConsumedByDay`'s own
  "no derivation logic here" convention.
  `depends_on`: 3.1, 3.3 · `parallel_ok`: no

## Wave 4 — offline derivation tests (module: analytics worker)

- [ ] **4.1** `internal/analytics/consumed_test.go` — extend (or add alongside the
  existing Fixture A/C-equivalent cases) assertions proving `deriveVehicleMetrics`
  populates all eight new `vehicleMetricRow` fields from `cur` in **both** branches:
  a predecessor-exists case (design.md Test Contract Fixture RM38-A's field values)
  and a no-predecessor case (Fixture RM38-B's field values, including `SentryMode ==
  nil` when `cur.SentryMode` is nil). This is the offline test that would fail if a
  future edit folded the eight new fields into the `_calc` columns' nil-on-no-
  predecessor branch (design D3's own stated regression risk).
  `depends_on`: 3.2 · `parallel_ok`: yes (can be drafted against design.md's Test
  Contract in parallel with Wave 3, wired in once 3.2 lands)

## Wave 5 — DB-integration tests (module: analytics worker — final wave, DB-gated)

- [ ] **5.1** `internal/analytics/db_integration_test.go` — extend (or add) a
  `Recalculate` integration test asserting the persisted `vehicle_metrics` row for
  Fixture RM38-A (predecessor exists) carries all eight new column values exactly as
  design.md's Test Contract specifies, read back via a direct `SELECT` (mirroring
  this file's existing fixture-assertion pattern, e.g. its Fixture A/B/C tests).
  `depends_on`: 2.3, 3.4 · `parallel_ok`: with 5.2

- [ ] **5.2** `internal/analytics/db_integration_test.go` — add a `Recalculate`
  integration test for Fixture RM38-B (no predecessor) asserting the eight new
  columns are populated (not NULL) on that row while the five `_calc` columns and
  `consumed_pct` remain NULL and `flagged` is `false` — the DB-level proof of design
  D3, complementing Wave 4's offline proof.
  `depends_on`: 2.3, 3.4 · `parallel_ok`: with 5.1

- [ ] **5.3** `internal/analytics/db_integration_test.go` — add a
  `LatestMetricsByAccount` integration test covering: (a) a single vehicle's latest
  row returned as one fully-populated `VehicleStatus`; (b) two vehicles on one
  account, each returning its own latest day, never the other's (the multi-vehicle
  `DISTINCT ON` case, design.md Test Contract); (c) a pre-migration row (all eight
  new columns NULL at the DB level, seeded directly via SQL since no writer predates
  this migration) mapping to a `VehicleStatus` with every one of the eight pointer
  fields `nil` — Fixture RM38-C; (d) an account with no `vehicle_metrics` rows at all
  returning an empty, non-nil slice and no error.
  `depends_on`: 2.3, 3.5 · `parallel_ok`: with 5.1, 5.2

## Wave 6 — documentation (module: analytics worker, except 6.2)

- [ ] **6.1** `internal/analytics/AGENTS.md` — under "Public interface (the port)",
  add the `LatestMetricsByAccount`/`VehicleStatus` entry (mirroring this file's
  existing per-method bullet style, e.g. the `OdometerDeltaByDay` bullet added by
  `RM29-analytics-add-vehicle-metrics`); under "Data ownership" → `vehicle_metrics`,
  note the eight new columns, their all-nullable/no-backfill status (roadmap D2),
  and `sentry_mode`'s ambiguous-NULL caveat (design D2/D8) with a pointer to this
  change's design.md rather than restating the full rationale inline (mirrors this
  file's existing convention of citing `design.md D<n>` rather than duplicating it).
  Add a one-line "Testing" section note for the two new DB-integration cases (5.1–5.3).
  `depends_on`: 3.1, 3.5 · `parallel_ok`: yes

- [ ] **6.2** **[leader]** Root `README.md` — check the `internal/analytics` row of
  the "Architecture" table (§189 area, "Derived vehicle metrics computed over stored
  telemetry..."). This tier does not change what analytics computes (no new
  derivation, D3 is a copy-verbatim rule) — update the row's wording only if leaving
  it unchanged would misstate the module after this tier lands (e.g. if it implies
  `vehicle_metrics` still holds only the RM29 column set). No `internal/analytics/
  README.md` exists to update — `internal/tesla/` is the only module with one in this
  repo; this module's doc-of-record is `AGENTS.md` (6.1), per its own "Doc-Pack
  (module)" section.
  `depends_on`: 6.1 · `parallel_ok`: no

## Wave 7 — verification (assistant-run signals, then owner-run suite)

- [ ] **7.1** Run and report: `go build ./...`, `go vet ./...`, `gofmt -l`, `make
  build`, `make vet`, `make bins`, `make migration-guard` (the one guard this tier's
  new migration file must pass — `make ui-guard`/`i18n-guard`/`money-guard`/
  `tz-guard`/`boundary-guard` are no-ops: no gateway code, no user-facing string, no
  monetary or raw-time-zone code, no `internal/telemetry` import touched by this
  tier). Per the Test-Execution-Policy, never `go test ./...`, `make test`,
  `make test-with-db`, or `make check`.
  `depends_on`: 1.1–6.2 (every prior wave) · `parallel_ok`: no

- [ ] **7.2** Hand off to the owner the exact command to run and report:
  `go test ./internal/analytics/...` (covers both the offline Wave 4 tests, which
  run with `DATABASE_URL` unset, and the `DATABASE_URL`-gated Wave 5 tests). Until
  the owner reports a pass, this tier's implementation status is
  **awaiting-user-verification**, never "done" (`CLAUDE.md` "Builds & local checks").
  `depends_on`: 7.1 · `parallel_ok`: no
