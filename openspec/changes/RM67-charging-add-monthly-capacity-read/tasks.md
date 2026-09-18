# Tasks — RM67-charging-add-monthly-capacity-read

Ownership legend: **[module: charging worker]** — inside `internal/charging/` only. This
change has **no leader-owned task**: it ships no migration, touches no `cmd/`, and no
`sqlc.yaml` edit (the existing `charging` entry already covers the new query). See
design.md D1–D6 for the rationale behind each group.

**Ordering constraints:**

- Wave 1 (query) before Wave 2 (Go): `sqlc` generates
  `chargingdb.EffectiveCapacityForPeriodParams`/the new query method from `query.sql`
  validated against the existing migration directory, so the query must exist first. No
  migration is added or changed in this wave — design.md D5 already establishes the
  existing `monthly_effective_capacity_tesla_id_effective_period_key` constraint serves it.
- Wave 2 before Wave 3 (tests): the `DATABASE_URL`-gated tests cannot compile until
  `charging.MonthlyCapacityReader` exists (`ai/go-conventions.md` §Testing authoring
  order — their expected values are already fixed in design.md §Test Contract).
- `make sqlc` runs once, after task 1.1.

---

## Wave 1 — query (module: charging worker)

- [ ] **1.1** **[module: charging worker]** `internal/charging/db/query.sql` — append
  `-- name: EffectiveCapacityForPeriod :one` exactly as specified in design.md
  §"The Query", including its full doc comment. `WHERE tesla_id = @tesla_id AND
  effective_period = date_trunc('month', @month::date)::date` — do **not** normalize the
  month in Go (design.md D4), and do **not** add or edit any migration file. Run
  `make sqlc` and report the result — this generates
  `chargingdb.EffectiveCapacityForPeriodParams` (fields `TeslaID`, `Month`) and the
  `EffectiveCapacityForPeriod` method against the *existing* `MonthlyEffectiveCapacity`
  model (unchanged — confirm it reappears identically in the diff, since no migration
  changed).
  `depends_on`: — · `parallel_ok`: no (blocks everything)

---

## Wave 2 — the Go port (module: charging worker)

- [ ] **2.1** **[module: charging worker]** `internal/charging/charging.go` — add the
  `MonthlyCapacityReader` interface (one method, `CapacityForMonth`, with the doc comment
  exactly as specified in design.md §"The Go Port" — the three-state contract table from
  design.md D1 belongs in this comment, not only in this tasks file) and the
  forward-declaring constructor `func NewMonthlyCapacityReader(pool *pgxpool.Pool)
  MonthlyCapacityReader`. Follow this file's existing conventions: no `pgtype` anywhere in
  it, `*float64` for the optional capacity, doc comments on every exported symbol. Does
  not compile until 2.2 and 2.3 supply the constructor chain's bodies.
  `depends_on`: 1.1 · `parallel_ok`: with 2.2 (authoring only — they land together)

- [ ] **2.2** **[module: charging worker]** `internal/charging/monthly_capacity_reader.go`
  (new file) — implement the port exactly as specified in design.md §"The Go Port": an
  unexported `monthlyCapacityReader` struct over `*chargingdb.Queries`, an unexported
  `newMonthlyCapacityReader(pool *pgxpool.Pool) *monthlyCapacityReader`, the compile-time
  `var _ MonthlyCapacityReader = (*monthlyCapacityReader)(nil)` assertion, and
  `CapacityForMonth` translating `pgx.ErrNoRows` to `(nil, false, nil)` and a real row to
  `(pgFloat8ToFloat64Ptr(v), true, nil)`. Reuse the existing `pgFloat8ToFloat64Ptr` helper
  from `service.go` — do not duplicate it.
  `depends_on`: 2.1 · `parallel_ok`: with 2.1

- [ ] **2.3** **[module: charging worker]** `internal/charging/query_log.go` — append the
  `loggingMonthlyCapacityReader` decorator exactly as specified in design.md §"The Go
  Port", following this file's existing explicit-implementation pattern (no embedding —
  see the file's own header comment on why). Update `NewMonthlyCapacityReader` in
  `charging.go` (task 2.1) to return
  `newLoggingMonthlyCapacityReader(newMonthlyCapacityReader(pool))`, mirroring
  `NewMirrorWatermarkStore`/`NewMonthlyCapacityCalculator`'s identical shape. Update this
  file's own header comment (currently "five logging decorators... Grouped in one file")
  to say six.
  `depends_on`: 2.2 · `parallel_ok`: no (edits the same constructor task 2.1 touches; land
  after 2.1/2.2)

---

## Wave 3 — tests + module docs (module: charging worker)

- [ ] **3.1** **[module: charging worker]**
  `internal/charging/db_monthly_capacity_reader_integration_test.go` (new file, package
  `charging_test`) — implement Test Contract **T1–T7** (including **T4b**) exactly as
  design.md states them, with those expected `(capacityKWh, found)` values. Seed
  `monthly_effective_capacity` rows with direct `INSERT`s (this module's established
  precedent for a table row shape no public writer targets one-at-a-time — see design.md
  §Test Contract's own note). Assert against a plain `*float64` and `bool` only — **no
  `pgtype` in any assertion** (`internal/charging/AGENTS.md` §Testing Notes). Use a fresh,
  distinct `tesla_id` per test group so T5/T6/T7's isolation fixtures cannot collide with
  T1–T4b's.
  `depends_on`: 2.2, 2.3 · `parallel_ok`: with 3.2

- [ ] **3.2** **[module: charging worker]** `internal/charging/AGENTS.md` — update for the
  module's new read surface (docs-track-structural-change, `CLAUDE.md` §Non-negotiables):
  - §Public Interface — add a block for `MonthlyCapacityReader`/`CapacityForMonth`/
    `NewMonthlyCapacityReader`, mirroring how `MirrorWatermarkStore` is already
    documented there, and stating plainly why it is a second, separate read from
    `packCapacityKWh`'s own seam (design.md D2).
  - §Allowed Imports — add `monthly_capacity_reader.go` to **both** the `pgtype` list and
    the `chargingdb` list, per this file's own instruction: "When you add a file that
    owns a query, add it to both lists in the SAME change." Also add `query_log.go` to
    neither list — it already imports neither `pgtype` nor `chargingdb` today, and this
    change does not change that (it only adds a decorator calling through the port
    interface).
  - §Testing Notes — note that `monthly_effective_capacity` now has a second read-port
    test file (`db_monthly_capacity_reader_integration_test.go`) alongside the existing
    `db_monthly_capacity_integration_test.go` (RM52's estimator/write tests), and that
    this new one asserts only against plain `*float64`/`bool`, never `pgtype`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1

---

- [ ] **3.3** **[leader-owned]** `kkpa/context/workflows/vehicle-monthly-metrics.md` — update
  the KB guide. It is outside the charging worker's sandbox, so the leader edits it.
  `CLAUDE.md` §Non-negotiables requires it in the SAME change, and this guide's file map is
  now wrong in three places:
  - the intro line says the read side is `packCapacityKWh`. There are now two read sides.
  - the `query.sql` row calls `LatestMeasuredCapacity` "the read". It is now one of two;
    add `EffectiveCapacityForPeriod` and say which question each answers.
  - the file map has no row for `internal/charging/monthly_capacity_reader.go`. Add it.

  Say plainly that the new port exists for a caller outside `charging`, and that the table
  is still never read directly by another module. Do not touch
  `kkpa/context/pending-spec-to-sync/applied/` or anything under
  `openspec/changes/archive/`.
  `depends_on`: 2.2 · `parallel_ok`: with 3.1, 3.2

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Any migration file, new or edited.** design.md D5 established the existing unique
  constraint already serves this query; adding an index anyway would trip the `database`
  design gate without the owner's sign-off.
- **Any edit to `LatestMeasuredCapacity`, `packCapacityKWh`, `MonthlyCapacityCalculator`,
  `estimateEffectiveCapacity`, or `median`.** All are unrelated seams this tier does not
  touch (design.md Context fact 1, D2).
- **Exposing `candidate_count` or `sample_count` on `CapacityForMonth`'s return, or on a
  new struct.** design.md D3 argues this case explicitly and declines it — do not add
  them "for completeness."
- **Editing `monthly_effective_capacity`'s `COMMENT ON TABLE`.** design.md D6 is a
  considered decision to leave it as-is — its claim is still true at the level it
  guards (no other module reads the table's rows directly; only its own port).
- **Wiring `charging.NewMonthlyCapacityReader` into `cmd/web` or `internal/app`.** That is
  a later tier's leader-owned integration step — this port has no caller until then, and
  that is expected, not a gap (proposal.md).
- **Root `README.md` edits.** Its existing charging-module description does not claim
  `monthly_effective_capacity` has only one read, so nothing in it goes stale from this
  change.
- **A `vehicleref.Ref` parameter on `CapacityForMonth`.** This port's caller is the
  nightly cycle, not a user request — it needs no proof of ownership, mirroring
  `SuperchargerSessionAnalyticsReader`'s identical no-`Ref` shape.
