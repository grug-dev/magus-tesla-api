# Tasks — RM52-app-add-monthly-capacity-step

Ownership legend: **[module: app worker]** — inside `internal/app/` only. **[module: app worker,
granted path]** — `cmd/poller/main.go`, explicitly granted by the leader for this one call site.
**[owner]** — the human. See design.md **D1–D4** for the rationale behind each group.

> ## No design gate
>
> This change adds no table, column, index, or migration (design.md header). Work may start
> without an owner confirmation step — there is nothing a database gate would cover.

## Ordering constraints

- **Wave 1 builds the pure gate first**, since everything else calls it and it is the piece the
  Test Contract's Group A pins.
- **Wave 2 wires the step into `Processor`.** 2.1 (`app.go`) and 2.2 (`processor.go`) touch
  disjoint files and may run in parallel; 2.3 (`cmd/poller/main.go`) depends on both, since it
  calls the constructor 2.1 changes and needs `charging.NewMonthlyCapacityCalculator`.
- **Wave 3's tests cannot compile before Wave 2.** Their expected values are already fixed in
  design.md §Test Contract — **author them against that contract, not against whatever the
  implementation produces.** 3.1 and 3.2 touch disjoint files and may run in parallel.
- **This tier has no cross-module compile-fix task beyond 2.3.** `charging` is untouched
  (proposal.md §Breaking); the only other module this change reaches into is `cmd/poller`, and 2.3
  is that task.

---

## Wave 1 — the pure gate

- [x] **1.1** **[module: app worker]** Add `monthlyCapacityPeriod(now time.Time, loc
  *time.Location) (period time.Time, run bool)` to `internal/app/processor.go` (or a new file,
  worker's choice — `monthly_capacity_step.go` mirrors `scheduler.go`'s one-function-per-concern
  file split, but a single `processor.go` addition is also fine since the function is short),
  verbatim from design.md D2, including its doc comment. Import `internal/clock` if not already
  imported in the chosen file.
  `depends_on`: — · `parallel_ok`: yes (first task, nothing before it)

---

## Wave 2 — wire the step into `Processor`

- [x] **2.1** **[module: app worker]** `internal/app/app.go`:
  - Add one new parameter to `NewProcessor`, `monthlyCapacityCalculator charging.
    MonthlyCapacityCalculator`, positioned immediately after `mirrorWatermarks charging.
    MirrorWatermarkStore` (design.md D3 — keeps all three `charging` ports contiguous).
  - Assign it to the new `processor` struct field inside the returned literal.
  - Update `Processor`'s own doc comment: the diagram/description gains a fourth step, "measure
    monthly vehicle capacity", noting it runs only on the first day of the month, for the previous
    month (RD6/RD7). Keep the existing wording style — short, factual, pointing at design.md for
    the full rule.
  - **No other parameter, field, or line in this file changes.**
  `depends_on`: 1.1 · `parallel_ok`: with 2.2

- [x] **2.2** **[module: app worker]** `internal/app/processor.go`:
  - Add field `monthlyCapacityCalculator charging.MonthlyCapacityCalculator` to the `processor`
    struct.
  - Inside `ProcessVehicleData`'s `if err == nil { ... }` block, add `p.runMonthlyCapacityStep(ctx)`
    as the last line, after `p.recalculateAnalytics(ctx)` (RD6: step 4, same short-circuit).
  - Add `(*processor) runMonthlyCapacityStep(ctx context.Context)` and `(*processor)
    callMonthlyCapacityCalculator(ctx context.Context, period time.Time)`, verbatim from design.md
    D2, including their doc comments.
  - **`processChargingData` and `recalculateAnalytics` are untouched** — same signatures, same
    bodies, same doc comments.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [x] **2.3** **[module: app worker, granted path]** `cmd/poller/main.go`: add one new argument to
  the existing `app.NewProcessor(...)` call, `charging.NewMonthlyCapacityCalculator(pool)`, in the
  same position `app.go`'s new parameter takes (immediately after `charging.
  NewMirrorWatermarkStore(pool)`). **No other line in this file changes** — this is wiring only,
  per this file's own header comment ("cmd/ stays thin, zero business logic").
  `depends_on`: 2.1 · `parallel_ok`: no (needs the constructor's new signature to exist first)

---

- [x] **2.4** **[module: app worker]** `internal/app/processor.go`: warn once when the two zones
  disagree. **Appended after the artifacts, on the owner's decision at the artifact gate.**
  `NewProcessor` takes an injected `loc` (`POLLER_TIMEZONE`), which decides **when** the nightly
  cycle fires. The monthly gate uses `clock.Zone()` (`America/Bogota`), which RD7 and
  `CLAUDE.md`'s time-zone rule both require. They agree in the current deployment but are two
  different knobs.
  - If `p.loc` and `clock.Zone()` name different zones, log **one** warning at step 4, saying
    both zone names and that the monthly gate follows `clock.Zone()`.
  - **Log only. Never fail, never skip the step, never change which zone the gate uses.** RD7 is
    not re-opened: `clock.Zone()` stays the gate's zone.
  - Why: if the two ever diverge, the cycle can fire on a moment that is the 1st in `p.loc` but
    the 31st in `clock.Zone()`. The month is then skipped and nothing reports it. This turns a
    silent skip into a visible one.
  - Follow this module's existing logging shape; add no new dependency.
  `depends_on`: 2.2 · `parallel_ok`: no

## Wave 3 — tests

Every expected value is fixed in design.md §Test Contract. **Assert that contract.**

- [x] **3.1** **[module: app worker]** Create `internal/app/monthly_capacity_step_test.go` —
  offline, no DB, package `app`. Cover design.md Test Contract **A1–A6**
  (`monthlyCapacityPeriod`'s six cases, including the zone-crossing pair A4/A5 that is the
  load-bearing proof of correctness) and **B1–B2** (`callMonthlyCapacityCalculator` against a new
  `fakeMonthlyCapacityCalculator`, satisfying `charging.MonthlyCapacityCalculator`, recording the
  `(ctx, period, teslaID)` it was called with and returning a configurable
  `(charging.MonthlyCapacityReport, error)`).
  `depends_on`: 2.2 · `parallel_ok`: with 3.2

- [x] **3.2** **[module: app worker]** `internal/app/processor_test.go`:
  - Add `fakeMonthlyCapacityCalculator` (from 3.1, or redeclared here if 3.1 places it in the new
    file — worker's choice, but declare it in exactly one place to avoid a duplicate-symbol
    compile error between the two files).
  - Update `newTestProcessor` to accept and pass a `charging.MonthlyCapacityCalculator` (the fake),
    positioned to match `NewProcessor`'s new parameter order (design.md D3).
  - Update every existing call to `newTestProcessor` (Fixtures P3–P5) to pass a
    `fakeMonthlyCapacityCalculator`.
  - Extend the existing step-1-whole-cycle-failure test
    (`TestProcessVehicleData_WholeCycleFailureStillRecordsRow`, Fixture P4) with the Test Contract
    **C1** assertion: `fakeMonthlyCapacityCalculator.calls == 0`.
  - **Add no assertion about the calculator's call count to the success-path test** (Fixture P3,
    Test Contract **C2**) — whether the real test-run date happens to be the 1st of the month is
    not something this suite may depend on.
  - `newMirrorTestProcessor` (used by the T-app-1..3 mirror-watermark tests) also needs a
    `monthlyCapacityCalculator` field in its literal — pass `fakeMonthlyCapacityCalculator{}` (a
    zero-value stub is fine there; those tests exercise `processChargingData` only).
  `depends_on`: 2.2, 3.1 · `parallel_ok`: with 3.1 up to the shared-fake-declaration point; do the
  fake declaration first if both are picked up together

---

## Wave 4 — documentation (`CLAUDE.md` §Non-negotiables: docs track change)

- [x] **4.1** **[module: app worker]** `internal/app/AGENTS.md`:
  - §Responsibility — the diagram/description gains the fourth step, one or two sentences noting
    it runs only on the first day of the month, for the previous month (RD6/RD7), and points at
    `charging.MonthlyCapacityCalculator` as the port it calls.
  - §Public interface — `NewProcessor`'s parameter list documentation gains the new
    `monthlyCapacityCalculator charging.MonthlyCapacityCalculator` argument, in its actual position.
  - §Allowed / forbidden imports — the existing `internal/charging` bullet ("`SessionWriter`...")
    gains `MonthlyCapacityCalculator` to the list of ports this module imports. No new import path
    is added (Context fact 3) — say so explicitly, mirroring how the existing bullet already
    explains why each port is safe to import.
  - §Testing notes — add a short paragraph for `monthlyCapacityPeriod` /
    `callMonthlyCapacityCalculator` / `runMonthlyCapacityStep`, using design.md D2's own
    covered/uncovered table (which function is pure and tested, which is impure and tested via a
    fake, which is accepted as untested wiring, and why — matching this file's existing style for
    `processChargingData`/`recalculateAnalytics`'s own accepted gap).
  `depends_on`: 3.2 · `parallel_ok`: no

- [x] **4.2** **[module: app worker, granted path]** `kkpa/context/architecture/nightly-cycle.md` —
  the KB guide for this exact cycle. **Appended by the leader after the waves 1+2 dispatch.**
  `CLAUDE.md` §Non-negotiables ("docs track structural change") requires the KB to be fixed in the
  **same** change, and this guide calls the cycle a **three**-step orchestration throughout. A
  stale guide is worse than no guide: `kkpa-context-fetch` presents it as authoritative, so an
  agent trusts it *instead of* reading the code. Update, at minimum:
  - §Glossary — "the 3-step orchestration" becomes four.
  - §Component map — `app.go`'s "seven **public ports**" count, and `processor.go`'s "The three
    steps" row, which must name `runMonthlyCapacityStep` / `callMonthlyCapacityCalculator` /
    `monthlyCapacityPeriod` as step 4 the same way it names steps 1–3.
  - A new **§Step 4 — measure monthly capacity (`internal/charging`)** section, in the same table
    shape as §Step 2 and §Step 3.
  - §Port map — add the `app` → `charging.MonthlyCapacityCalculator` → `Calculate` row.
  - §"How do I…" — "bracketing all three steps" becomes four; and note that step 4 is the first
    step that does **not** run every night.
  - §Conventions & gotchas — "steps 2 and 3 never run" and "its three steps" become 2, 3 and 4.
    Add the gotcha that matters most: the gate zone is `clock.Zone()`, **not** the poller's
    `POLLER_TIMEZONE` (`p.loc`); they are two different knobs, and step 4 logs a warning when they
    disagree (RD7, task 2.4).
  - The table-effects table — `charging.monthly_effective_capacity` is written by step 4.
  **Do not touch any other KB guide**, and do not touch `openspec/changes/archive/`.
  `depends_on`: 2.2, 2.4, 4.1 · `parallel_ok`: with 4.1 is fine but 4.1 first is simpler

---

## Wave 5 — signals

- [x] **5.1** **[module: app worker]** Run the cheap deterministic signals the
  `Test-Execution-Policy` allows: `gofmt -l ./internal/app ./cmd/poller`, `go build ./...`, `go vet
  ./...`. Both should be clean repo-wide — this change's only edits outside `internal/app` are the
  one-argument addition in `cmd/poller/main.go` (proposal.md §Breaking). If either fails anywhere
  else, stop and report it rather than assuming it is expected; it is not.
  `depends_on`: 3.1, 3.2, 4.1 · `parallel_ok`: no

---

## Owner verification (`Test-Execution-Policy`: the owner runs the suite)

- [x] **O1** **[owner]** Run the suite. **DONE — the owner reported `make check` PASSED.**
  Recorded as the owner's report, never claimed by the assistant. Nothing above may be reported as `done` on the assistant's
  say-so; work that is complete but unexecuted is **`awaiting-user-verification`**.
  ```bash
  make check
  ```
  (`make check` = `build vet ui-guard i18n-guard money-guard tz-guard migration-guard
  boundary-guard archive-guard test`. No `make migrate-up` needed — this tier adds no migration.)

- [ ] **O2** **[owner]** Optional manual spot-check, since this tier's own step-4 wiring
  (`runMonthlyCapacityStep`) is accepted-untested (design.md D2): run `go run ./cmd/poller --once`
  on a day that is the 1st of the month (or temporarily patch `monthlyCapacityPeriod`'s call site
  to force `run = true`, then revert) and confirm the log line `monthly capacity: period YYYY-MM:
  N vehicle(s) found, M measured, K thin` appears. Not required before archiving — this is a
  verification convenience, not a gate.

## Cross-module tasks the leader owns

- [x] **L1** **[leader]** **DONE — both are green repo-wide**, re-run by the leader after the
  final doc commit, together with `make tz-guard`, `make boundary-guard` and `make archive-guard`.
  No cross-module compile fix beyond task 2.3 was needed, as proposal.md §Breaking predicted.
  Original task text: Confirm `go build ./...`/`go vet ./...` are green **outside**
  `internal/app` and `cmd/poller` once Wave 5 lands. Proposal.md §Breaking states no other
  cross-module compile fix should be needed — verify rather than assume.
- [x] **L2** **[leader]** Confirm the root `README.md` needs no edit.
  **DONE — and the assumption was wrong, again.** `README.md`'s `internal/app` Architecture row
  described the cycle as "three named steps". Step 4 makes that false. The leader edited that one
  row: four steps, plus one sentence on the first-day-of-month gate and the port it calls. The
  dependency graph needed **no** edit — it already shows `app ► charging`, and this change adds no
  new import path. No other `README.md` line changed. Original task text follows.
   This change adds no module
  and no runnable (tier 3 adds `cmd/monthly-capacity`, not this tier) — the "Project Structure"
  tree and the "Architecture" table should already be correct. (L2 on tier 1 turned out wrong once
  already — actually check, do not assume.)
- [x] **L4** **[leader]** `internal/app/AGENTS.md` §Public interface — bring the documented
  `NewProcessor` signature back in sync with `app.go`. **Appended after wave 4, on the owner's
  decision.** The worker found this doc already wrong BEFORE this change, and correctly did not
  fix it inside its assigned scope. Two errors: `mirrorWatermarks charging.MirrorWatermarkStore`
  was missing entirely (added by `RM44-platform-add-mirror-watermark`), and the telemetry port was
  named `superchargerReader telemetry.SuperchargerReader` when the real name has been
  `superchargerHistoryReader telemetry.SuperchargerHistoryReader` since RM39 tier 5. **DONE** —
  the list now matches `app.go` parameter for parameter, ten public ports plus one
  `*time.Location`. Doc text only; no code changed.

- [ ] **L3** **[leader]** When tier 2 archives, update
  `openspec/roadmaps/RM52-vehicle-monthly-metrics.md`'s tier 2 status to `[x]`.
