# Tasks — RM67-app-add-monthly-metrics-step

Ownership legend: **[module: app worker]** — inside `internal/app/` only. **[module: app worker,
granted path]** — `kkpa/context/architecture/nightly-cycle.md`, explicitly granted by the leader
for this doc update, the same shape `RM52-app-add-monthly-capacity-step` used for its own KB task.
**[leader]** — cross-module work outside `internal/app`'s sandbox: `cmd/poller/main.go` and root
`README.md`. **[owner]** — the human. See design.md **D1-D5** for the rationale behind each group.

> ## No design gate
>
> This change adds no table, column, index, or migration (design.md header). Work may start
> without an owner confirmation step — there is nothing a database gate would cover.

## Ordering constraints

- **Wave 1 builds the pure period function first**, since everything else calls it and it is the
  piece the Test Contract's Group A pins.
- **Wave 2 wires the step into `Processor`.** 2.1 (`app.go`: constructor parameter) and 2.2
  (`processor.go`: field + step functions) touch disjoint files and may run in parallel. 2.3
  (`app.go`'s package doc comment) touches the same file as 2.1 and must follow it, not run in
  parallel with it.
- **Wave 3's tests cannot compile before Wave 2.** Their expected values are already fixed in
  design.md §Test Contract — **author them against that contract, not against whatever the
  implementation produces.** 3.1 and 3.2 touch disjoint files and may run in parallel.
- **The `cmd/poller` wiring (leader task L1) is a hard dependency for anything outside
  `internal/app` that imports the new signature** — nothing in this tier's own task list needs it,
  since all of this tier's own tests build fakes rather than the real `cmd/poller` composition.
- **This tier has no cross-module compile-fix task beyond L1.** `analytics` and `charging` are
  untouched (proposal.md §Breaking); the only other place this change reaches is `cmd/poller` and
  root `README.md`, both leader-owned.

---

## Wave 1 — the pure period function

- [x] **1.1** **[module: app worker]** Add `monthlyMetricsPeriods(now time.Time, loc
  *time.Location) (current, previous time.Time)` to `internal/app/processor.go` (or a new file,
  worker's choice — `monthly_metrics_step.go` mirrors `scheduler.go`'s one-function-per-concern
  file split, but a single `processor.go` addition is also fine since the function is short),
  verbatim from design.md D2, including its doc comment. `internal/clock` is already imported in
  `processor.go`.
  `depends_on`: — · `parallel_ok`: yes (first task, nothing before it)

---

## Wave 2 — wire the step into `Processor`

- [x] **2.1** **[module: app worker]** `internal/app/app.go`:
  - Add one new parameter to `NewProcessor`, `monthlySyncer analytics.MonthlySyncer`, positioned
    immediately after `gapWriter analytics.GapWriter` (design.md D4 — keeps all four `analytics`
    ports contiguous, `loc` stays last).
  - Assign it to the new `processor` struct field inside the returned literal.
  - Update the constructor's own doc comment to mention the new parameter, following the existing
    style used for `monthlyCapacityCalculator`'s own paragraph.
  - **No other parameter, field, or line in this file's constructor body changes.**
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: app worker]** `internal/app/processor.go`:
  - Add field `monthlySyncer analytics.MonthlySyncer` to the `processor` struct.
  - Inside `ProcessVehicleData`'s `if err == nil { ... }` block, add `p.runMonthlyMetricsStep(ctx)`
    as the new last line, after `p.runMonthlyCapacityStep(ctx)` (design.md Context fact 2: the
    order is load-bearing on the 1st of the month — step 5's previous-month sync must run after
    step 4 has written that month's measured capacity).
  - Add `(*processor) runMonthlyMetricsStep(ctx context.Context)` and `(*processor)
    callMonthlySyncer(ctx context.Context, teslaID int64, period time.Time)`, verbatim from
    design.md D3, including their doc comments. `slices` is already imported in `processor.go`.
  - **`processChargingData`, `recalculateAnalytics` and the step-4 functions are untouched** — same
    signatures, same bodies, same doc comments.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: app worker]** `internal/app/app.go`: update the package doc comment's
  four-step diagram to five steps, adding "Sync Monthly Metrics (analytics)" as the fifth line,
  following the existing diagram's exact formatting (module name in parentheses, aligned columns).
  Also update `Processor`'s own doc comment (the interface method's comment) to mention the fifth
  step and that it runs on every invocation, unlike the fourth.
  `depends_on`: 2.1 · `parallel_ok`: no (same file as 2.1 — run after it, not alongside it)

---

## Wave 3 — tests

Every expected value is fixed in design.md §Test Contract. **Assert that contract.**

- [x] **3.1** **[module: app worker]** Create `internal/app/monthly_metrics_step_test.go` —
  offline, no DB, package `app`. Cover design.md Test Contract:
  - **A1-A6** (`monthlyMetricsPeriods`'s six cases, including the zone-crossing pair A4/A5 that is
    the load-bearing proof of correctness).
  - **B1-B2** (`callMonthlySyncer` against a new `fakeMonthlySyncer`, satisfying
    `analytics.MonthlySyncer`, recording every `(teslaID, period)` call it received — as a slice,
    not a single value, since this step calls the port multiple times per invocation — and
    returning a configurable per-call `(analytics.VehicleMonthlyMetrics, error)`, e.g. keyed by
    `teslaID` and month, or by call order — worker's choice, as long as it can express "this
    specific call fails, the rest succeed" for C2/C3 below).
  - **C1-C4** (`runMonthlyMetricsStep`'s own vehicle enumeration and per-`(vehicle, period)`
    isolation), using a small fake satisfying whatever narrow slice of `account.Service` this test
    needs (mirroring how `processor_test.go` already fakes `account.Service` for the existing
    step-2/3 tests) plus `fakeMonthlySyncer`. Also cover the duplicate-`TeslaID` dedup case
    described at the end of design.md §Test Contract Group C.
  `depends_on`: 2.2 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: app worker]** `internal/app/processor_test.go`:
  - Declare `fakeMonthlySyncer` in exactly one place — either here or in 3.1's new file, worker's
    choice, but not both, to avoid a duplicate-symbol compile error between the two files.
  - Update `newTestProcessor` to accept and pass an `analytics.MonthlySyncer` (the fake),
    positioned to match `NewProcessor`'s new parameter order (design.md D4).
  - Update every existing call to `newTestProcessor` to pass a `fakeMonthlySyncer`.
  - Extend the existing step-1-whole-cycle-failure test with the Test Contract **C5** assertion:
    `fakeMonthlySyncer`'s recorded call count is `0`.
  - **Add no assertion about the syncer's call count to the success-path test** — the exact number
    of registered vehicles in that fixture decides the count, and pinning it here would couple an
    unrelated fixture's vehicle count to this tier's own test.
  - Any other test in this file constructing a `processor` literal directly (rather than through
    `newTestProcessor`) also needs a `monthlySyncer` field — `fakeMonthlySyncer{}` (a zero-value
    stub) is fine wherever the test does not exercise step 5.
  `depends_on`: 2.2, 3.1 · `parallel_ok`: with 3.1 up to the shared-fake-declaration point; do the
  fake declaration first if both are picked up together

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [x] **4.1** **[module: app worker]** `internal/app/AGENTS.md`:
  - §Responsibility — the diagram/description gains the fifth step, one or two sentences noting it
    runs on every invocation (unlike step 4), and points at `analytics.MonthlySyncer` as the port
    it calls.
  - §Public interface — `NewProcessor`'s parameter list documentation gains the new
    `monthlySyncer analytics.MonthlySyncer` argument, in its actual position.
  - §Allowed / forbidden imports — the existing `internal/analytics` bullet gains `MonthlySyncer`
    to the list of ports this module imports. No new import path is added (design.md Context fact
    4) — say so explicitly, mirroring how the existing bullet already explains why each port is
    safe to import.
  - §Testing notes — add a short paragraph for `monthlyMetricsPeriods` / `callMonthlySyncer` /
    `runMonthlyMetricsStep`, using design.md D3's own covered/partial table (which function is
    pure and tested, which is impure and tested via a fake, which wall-clock read is an accepted
    gap, and why — matching this file's existing style for step 4's own table).
  `depends_on`: 3.2 · `parallel_ok`: no

- [x] **4.2** **[module: app worker, granted path]** `kkpa/context/architecture/nightly-cycle.md` —
  the KB guide for this exact cycle. `CLAUDE.md` §Non-negotiables ("docs track structural change")
  requires the KB to be fixed in the **same** change, and this guide calls the cycle a **four**-step
  orchestration throughout. A stale guide is worse than no guide: `kkpa-context-fetch` presents it
  as authoritative, so an agent trusts it *instead of* reading the code. Update, at minimum:
  - §Glossary and §Component map — "the 4-step orchestration" becomes five; `processor.go`'s row
    gains `runMonthlyMetricsStep` / `callMonthlySyncer` / `monthlyMetricsPeriods` alongside the
    existing four steps' functions.
  - A new **§Step 5 — sync monthly metrics (`internal/analytics`)** section, in the same table
    shape as §Step 4, naming `analytics.MonthlySyncer.SyncMonth`,
    `internal/analytics/monthly_sync.go`, and the note that this step runs every night, not only
    the first of the month.
  - §Port map — add the `app` → `analytics.MonthlySyncer` → `SyncMonth` row (called twice per
    vehicle per cycle — current month, then previous month).
  - The table-effects table — `analytics.vehicle_monthly_metrics` is written by step 5 (C+U, one
    row per vehicle per synced month).
  - §"How maintenance works" — the "four nightly steps" line becomes five; note that step 5, unlike
    step 4, runs every night.
  - §Conventions & gotchas — add the gotcha that matters most for this tier: step 5 runs AFTER
    step 4 on purpose, because on the 1st of the month step 5's previous-month sync copies the
    capacity figure step 4 just measured that same night (design.md Context fact 2).
  - The three local diagrams under `kkpa/docs/diagrams/nightly-job/` and the published Artifact
    named at the bottom of this guide are **out of scope for this task** — the guide's own text
    says "it does not self-update" and names them as pictures for humans, not implementation
    inputs; refreshing them is a documentation nice-to-have, not a requirement this change's own
    doc-tracking rule forces. Leave them as-is unless the owner asks separately.
  **Do not touch any other KB guide**, and do not touch `openspec/changes/archive/`.
  `depends_on`: 2.2, 4.1 · `parallel_ok`: with 4.1 is fine but 4.1 first is simpler

---

## Wave 5 — signals

- [x] **5.1** **[module: app worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/app`, `go build ./...`, `go vet ./...`. Both
  should be clean for `internal/app` itself — the rest of the repo will not build until leader task
  L1 (`cmd/poller` wiring) lands, since `app.NewProcessor`'s signature changes in this tier. If
  `go build ./...`/`go vet ./...` fail ONLY inside `cmd/poller` with a missing-argument error, that
  is expected until L1 runs — report it as such rather than as a finding. Any other failure,
  anywhere, is a real finding — stop and report it.
  `depends_on`: 3.1, 3.2, 4.1 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [ ] **O1** **[owner]** Run the suite once L1 (leader) has landed the `cmd/poller` wiring.
  Recorded as the owner's report, never claimed by the assistant. Nothing above may be reported as
  `done` on the assistant's say-so; work that is complete but unexecuted is
  **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`make check` = `build vet lint ui-guard i18n-guard money-guard tz-guard migration-boundary-guard
  boundary-guard theme-guard vehicleref-guard tenancy-guard naming-guard archive-guard logdir-guard
  delta-guard test`. No `make migrate-up` needed — this tier adds no migration.)

- [ ] **O2** **[owner]** Optional manual spot-check, since `runMonthlyMetricsStep`'s own
  `clock.Now()`/`clock.Zone()` read is accepted-untested (design.md D3): run
  `go run ./cmd/poller --once` and confirm the log lines `callMonthlySyncer: vehicle N: period
  YYYY-MM: synced` appear twice per registered vehicle (current and previous month). Not required
  before archiving — this is a verification convenience, not a gate. **This wakes the real car and
  makes paid Fleet API calls** — the owner's call, never run unprompted.

## Cross-module tasks the leader owns

- [x] **L1** **[leader]** `cmd/poller/main.go`: add one new argument to the existing
  `app.NewProcessor(...)` call, `analytics.NewMonthlySyncer(pool, charging.NewMonthlyCapacityReader(pool),
  chargingReader, sessionAnalyticsReader)`, in the same position `app.go`'s new parameter takes
  (immediately after `analytics.NewGapWriter(pool)`, before `loc`). `chargingReader` and
  `sessionAnalyticsReader` already exist as local variables in this file, built for
  `analytics.NewRecalculator` — reuse them unchanged; only `charging.NewMonthlyCapacityReader(pool)`
  is a new construction. **No other line in this file changes** — this is wiring only, per this
  file's own header comment ("cmd/ stays thin, zero business logic").
  `depends_on`: 2.1 · `parallel_ok`: no (needs the constructor's new signature to exist first)

- [x] **L2** **[leader]** Confirm `go build ./...`/`go vet ./...` are green repo-wide once L1 lands
  and Wave 5 has run. Proposal.md §Breaking states no cross-module compile fix beyond L1 should be
  needed — verify rather than assume.

- [x] **L3** **[leader]** Root `README.md`: the `internal/app` Architecture table row (currently
  naming "four named steps" and describing step 4) becomes five, with one added sentence on step 5
  — that it runs every night, syncing the current and previous month's metrics through
  `analytics.MonthlySyncer`. Check whether the module dependency graph elsewhere in `README.md`
  needs an edit too — this change adds no new import path (`app` already imports `analytics`), so
  it likely does not, but verify rather than assume (the same check on tier 2's own L2 task once
  turned out wrong).

- [ ] **L4** **[leader]** When this tier archives, update
  `openspec/roadmaps/RM67-vehicle-monthly-metrics-table.md`'s tier 4 status to `[x]`. Since this is
  the roadmap's last tier, also check whether the roadmap itself is now fully complete and should
  move to `openspec/roadmaps/archive/` per that flow's own rules.
