# Tasks — RM29-app-add-process-vehicle-data

Ownership legend: **[module: telemetry worker]** — inside `internal/telemetry/` only.
**[module: app worker]** — inside `internal/app/` only (new module). **[leader]** —
outside every module's sandbox (`cmd/`, `ai/`, root docs, `openspec/`, and the one
one-sentence `internal/charging/AGENTS.md` fix — see task 4.4's note on why that single
line is leader-owned in this tier rather than dispatched to a third module worker). See
design.md D1–D13 for the rationale behind each group.

> **Amended 2026-08-22 for RD8 (supersedes RD5).** `Scheduler` moves
> `internal/telemetry` → **`internal/app`**, not `cmd/poller`, and its four
> `Scheduler`/`nextRun` tests move with it instead of being deleted (design.md **D4**,
> **D13**). Tasks **2.4**, **2.5**, **2.7**, **4.1**, **4.3** and the "Not in this change"
> list were rewritten accordingly, and Wave 3 gained **3.4** (`internal/app/scheduler.go`)
> and **3.5** (`internal/app/scheduler_test.go`). No existing task id was renumbered or
> removed.

**Wave ordering rule (binding):** offline/pure-function work goes EARLY — it compiles
against the widened `Collector` signature the moment Wave 2 lands, with no DB involved.
DB-backed integration work goes in the FINAL wave — it cannot compile until the migration
(Wave 1) and the regenerated sqlc types exist. `internal/app` cannot be built until
`telemetry.Collector`'s widened signature exists (Wave 2), since `Processor` calls it
directly.

**Ordering constraints:**

- Wave 1 (telemetry schema) before Wave 2 (telemetry Go): `sqlc` generates
  `InsertPollAttemptParams` with the two new fields from `query.sql` validated against
  the migration directory, so the columns must exist there first.
- Wave 2 before Wave 3 (`internal/app`): `app.Processor` calls
  `telemetry.Collector.CollectAll(ctx, RunContext)` directly — the widened signature must
  exist first, or `internal/app` does not compile.
- Wave 2's own internal order: 2.1–2.2 (schema-shaped Go: types, signature, stamping)
  before 2.3 (dbStore mapping, needs sqlc's regenerated params) before 2.4–2.6 (file
  reshuffle + test updates, needs the final shape of everything above to exist).
- Wave 3's own internal order: 3.1 → 3.2 (the port and its implementation) → 3.4 (the
  scheduler, which holds a `Processor`) → 3.5 (the scheduler's tests) → **3.3 last**
  (`AGENTS.md`, which must describe everything the other four landed). 3.3 keeps its
  original id even though it now lands after 3.4/3.5: **ordering in this file is carried
  by `depends_on`, never by the number**, and renumbering a task the leader already
  tracks would be worse than a non-monotonic id.
- Wave 3 before Wave 4 (`cmd/poller`): the composition root wires **both** things Wave 3
  builds — `app.NewProcessor` and `app.NewScheduler` (design.md D4). `cmd/poller` gains
  no new file of its own in this change.
- `make sqlc` runs **once** in this change, after task 1.1 — this tier adds no new query
  (`InsertPollAttempt` already exists in `query.sql`; only its params struct grows two
  fields as a consequence of the migration, no `query.sql` edit is needed at all unless
  the telemetry worker finds otherwise while implementing 2.3, in which case run `make
  sqlc` again after that edit and note the deviation in the report).

---

## Wave 1 — schema (module: telemetry worker)

- [ ] **1.1** **[module: telemetry worker]** Create
  `internal/telemetry/db/migrations/20260823000002_add_run_id_triggered_by_poll_attempts.sql`,
  transcribing design.md §"Database Changes" **verbatim** — the full `ALTER TABLE
  poll_attempts ADD COLUMN run_id UUID, ADD COLUMN triggered_by TEXT NOT NULL DEFAULT
  'scheduler'`, both `COMMENT ON COLUMN` statements, and the `-- +goose Down` block.
  **Do not simplify or re-word the comments** — they carry the load-bearing rationale
  (why this stays in `telemetry` rather than moving per roadmap D2, why no CHECK, why no
  index, why `run_id` is permanently unbackfilled). Then run `make sqlc` and report the
  result. Confirm the generated `telemetrydb.InsertPollAttemptParams` gained `RunID` and
  `TriggeredBy` fields (their exact generated Go type — plain `uuid.UUID`/`string` vs
  `pgtype.UUID`/nullable — is the one open question design.md D5 flags; report what sqlc
  actually generated for `run_id` specifically, since it is the nullable column).
  `depends_on`: — · `parallel_ok`: no (blocks everything)

---

## Wave 2 — telemetry Go changes (module: telemetry worker)

- [ ] **2.1** **[module: telemetry worker]** `internal/telemetry/telemetry.go` — add
  `TriggeredBy` (type + `TriggeredByScheduler`/`TriggeredByAPI` constants) and
  `RunContext{RunID uuid.UUID; TriggeredBy TriggeredBy}` exactly as design.md **D5**
  specifies. Widen the `Collector` interface's `CollectAll` to
  `CollectAll(ctx context.Context, run RunContext) (CycleReport, error)`, updating its
  doc comment to describe the new parameter and to name `internal/app` as the module that
  generates `RunID`. Add `RunID uuid.UUID` and `TriggeredBy TriggeredBy` fields to
  `Attempt`, with a doc comment noting the column's nullability asymmetry (design.md D5).
  `depends_on`: 1.1 · `parallel_ok`: with 2.2 (both touch the interface/type layer; land
  together)

- [ ] **2.2** **[module: telemetry worker]** `internal/telemetry/service.go` — widen
  `(s *service) CollectAll` to accept `run RunContext`; thread `run` as a plain parameter
  through `collectAccount(ctx, run, accountID, owned, report)` (currently
  `collectAccount(ctx, accountID, owned, report)`) to all four existing `s.record(...)`
  call sites inside it (`service.go:181,198,210,217`); widen `record` to
  `record(ctx context.Context, run RunContext, accountID uuid.UUID, teslaID int64,
  reason Reason, report *CycleReport)` and set `Attempt.RunID = run.RunID`,
  `Attempt.TriggeredBy = run.TriggeredBy` on the `Attempt` it builds. **Do not** store
  `run` as a field on `*service` — design.md D5 explains why (a shared long-lived object,
  concurrent-call safety). `collectVehicle`/`attemptVehicle`/`collectChargingHistory` are
  untouched — none of them calls `record`.
  `depends_on`: 1.1 · `parallel_ok`: with 2.1

- [ ] **2.3** **[module: telemetry worker]** `internal/telemetry/service.go` (or
  `mapping.go`, wherever `insertPollAttempt`'s param-building lives) — update
  `dbStore.insertPollAttempt` to pass the two new fields into
  `telemetrydb.InsertPollAttemptParams`, using whatever type task 1.1 confirmed sqlc
  generated for the nullable `run_id` column (`uuid.UUID` directly if the project's
  existing `uuid → google/uuid.UUID` sqlc override covers it, or a small
  `runIDToPgUUID`-style helper mirroring this file's existing `teslaIDToPgInt8`-style
  round-trip-pair naming if sqlc generated `pgtype.UUID` instead). `Attempt.RunID` is
  never the zero UUID on the write path (design.md D5), so no NULL-handling branch is
  needed for a write — every write supplies a real value.
  `depends_on`: 2.1, 2.2 · `parallel_ok`: no

- [ ] **2.4** **[module: telemetry worker]** Create `internal/telemetry/report.go`:
  relocate `LogCycle` and `formatFailures` out of `scheduler.go` verbatim (design.md
  **D11**) — same doc comments, same behavior, only the file changes. `LogCycle` stays
  **exported** and stays in `internal/telemetry`; its new caller across the module
  boundary is `internal/app`'s relocated scheduler (task 3.4), alongside the `--once`
  path that already calls it. Then remove `internal/telemetry/scheduler.go` entirely:
  `Scheduler`, `NewScheduler`, `Run` and `nextRun` leave this module.
  **They are MOVED, not discarded** — task **3.4** re-creates them, essentially verbatim,
  in `internal/app/scheduler.go` (design.md **D4**, which carries RD8 and supersedes
  RD5's `cmd/poller` plan). Do not reimplement them here, do not leave a copy behind, and
  **read `scheduler.go` before removing it and quote its exact current content in your
  report**, so the app worker's relocation in 3.4 is verbatim rather than reconstructed.
  `depends_on`: 2.1 · `parallel_ok`: with 2.5

- [ ] **2.5** **[module: telemetry worker]** Split
  `internal/telemetry/scheduler_test.go` three ways, per design.md's Test Contract "What
  moves, unchanged" list:
  - `TestNextRun`, `TestScheduler_ShutsDownWithoutRunningWhenCancelled`,
    `TestScheduler_NilLocationDefaultsToLocal`, `TestScheduler_RunsAndLogsOneCycle` —
    **remove from `internal/telemetry`, because they move to `internal/app`** (task 3.5,
    design.md **D4** / Test Contract group S). This is a relocation of pre-existing
    coverage, **not** a coverage drop: all four survive this change intact. Their two
    collector stubs (`stubCollector`, `reportCollector`) go with them and become
    `Processor` fakes there. Do **not** write replacements here, and do **not** put them
    in `cmd/poller`. As with 2.4, **quote their exact current content in your report** so
    task 3.5 relocates rather than reconstructs them.
  - `TestFormatFailures` — **move** to a new `internal/telemetry/report_test.go`,
    unchanged, alongside `report.go` (task 2.4).
  - `TestWaitUntilOnline_OnlineImmediately`, `TestWaitUntilOnline_TimesOutWhenAsleep`,
    `TestWaitUntilOnline_UnauthorizedPropagates` — **move** to a new
    `internal/telemetry/wake_test.go`, unchanged (they test `wake.go`'s
    `waitUntilOnline`, which this change does not touch).
  Delete `scheduler_test.go` once all three groups have been relocated — two within this
  module (`report_test.go`, `wake_test.go`), one to `internal/app` (task 3.5).
  `depends_on`: 2.4 · `parallel_ok`: with 2.4 (coordinate: 2.4 creates `report.go` that
  2.5's `report_test.go` sits beside; land together or in either order as long as both
  land before 2.6)

- [ ] **2.6** **[module: telemetry worker]** `internal/telemetry/service_test.go` —
  implement Test Contract **group A** (A1–A3). Add a small package-level test helper
  (e.g. `func testRun() RunContext { return RunContext{RunID: uuid.New(), TriggeredBy:
  TriggeredByScheduler} }`) and update every existing `svc.CollectAll(context.Background())`
  call site (~24, per design.md's count) to `svc.CollectAll(context.Background(),
  testRun())`, changing **no other line** of any existing test (A1: identical output,
  the literal characterization bar). Then extend one existing single-vehicle-success
  fixture's assertions (or add a minimal new test right next to it, whichever reads more
  naturally against that fixture's existing shape) to cover A2 (stamped `RunID`/
  `TriggeredBy` match the call's own `RunContext`) and A3 (two calls with two different
  `RunID`s stamp their own, never the other's).
  `depends_on`: 2.2, 2.3 · `parallel_ok`: no

- [ ] **2.7** **[module: telemetry worker]** `internal/telemetry/AGENTS.md` — update for
  this tier (docs-track-structural-change): §Public Interface — `Collector.CollectAll`'s
  new signature, `RunContext`/`TriggeredBy` as new exported types, `Scheduler`/
  `NewScheduler` **no longer part of this module's surface** — state where they went:
  **`internal/app`** (design.md **D4**, carrying RD8), **with their four tests preserved
  and moved alongside them** (Test Contract group S). Do not describe this as a deletion
  or as a coverage loss — it is a relocation and nothing is lost. Note also that
  `LogCycle`/`formatFailures` stay here (now in `report.go`) and that `LogCycle` is now
  called from `internal/app` across the boundary, which is why it remains exported. §Data Ownership — `poll_attempts` gains
  `run_id`/`triggered_by`, and state explicitly that this **supersedes roadmap D2**
  (the table did not move) with a one-line pointer to this change's design.md D1 for the
  full reasoning. §Testing Notes — note the new `report.go`/`report_test.go` and
  `wake_test.go` files, that `scheduler.go`/`scheduler_test.go` no longer exist in this
  module, and that the four scheduler tests now live in `internal/app/scheduler_test.go`
  so a reader looking for them knows where they went.
  `depends_on`: 2.6 · `parallel_ok`: with 3.x (docs only; does not block `internal/app`)

---

## Wave 3 — `internal/app` (module: app worker)

- [ ] **3.1** **[module: app worker]** Create `internal/app/app.go`: the package doc
  comment (the three-step diagram from design.md's Context, attributed to the owner),
  the `Processor` interface (design.md **D3**, **D10**) with its full doc comment
  describing the three named steps and the whole-cycle short-circuit, and
  `NewProcessor(...)` exactly as design.md **D10** specifies — seven ports plus `loc
  *time.Location`, **no** `*pgxpool.Pool` parameter anywhere (design.md D1/D2: this
  module owns no table). Forward-declares only; task 3.2 supplies the implementation.
  `depends_on`: 2.1, 2.2 (needs the widened `telemetry.Collector`/`RunContext` to compile
  against) · `parallel_ok`: no (blocks 3.2)

- [ ] **3.2** **[module: app worker]** Create `internal/app/processor.go`: the concrete
  `*processor` type implementing `Processor`, plus the compile-time
  `var _ Processor = (*processor)(nil)` assertion. `ProcessVehicleData` follows
  design.md **D8** exactly: generate `run := telemetry.RunContext{RunID: uuid.New(),
  TriggeredBy: triggeredBy}`; call `p.collector.CollectAll(ctx, run)`; on a non-nil
  error, return `(report, err)` immediately — steps 2 and 3 do not run; on success, call
  the mirror step then the reconcile step, then return `(report, nil)`.
  - The mirror step (design.md **D6**) is `newSessionMirrorer`'s **entire body**,
    relocated from `cmd/poller/main.go` (currently lines ~219–275) with field names,
    log-line prefixes (`"session mirror:"`) and per-account isolation **unchanged**.
    Reproduce it as an unexported method (e.g. `(p *processor) processChargingData(ctx
    context.Context)`) reading `p.superchargerReader`/writing `p.sessionWriter`,
    enumerating via `p.acct.AllRegisteredVehicles(ctx)`.
  - The reconcile step (design.md **D8**) is `newNightlyReconciler`'s **entire body**,
    relocated from `cmd/poller/main.go` (currently lines ~307–370) unchanged — same two
    ordered halves (`analytics.Recalculator.Reconcile` per vehicle, then the gap-window
    read/write), same "skip step 2 if step 1 failed for that vehicle" rule, same
    "yesterday resolved in `p.loc`" zone math, same log-line prefixes
    (`"metrics reconciliation:"`, `"gap reconciliation:"`). Reproduce as an unexported
    method (e.g. `(p *processor) recalculateAnalytics(ctx context.Context)`).
  Preserve every doc comment's substance from the two moved functions — they explain
  *why* the ordering and isolation rules exist, and that reasoning does not change by
  moving.
  `depends_on`: 3.1 · `parallel_ok`: no

- [ ] **3.3** **[module: app worker]** Create `internal/app/AGENTS.md` — `Agent-Name:
  app` header, `## Doc-Pack (module)` section (may be empty — no module-specific docs
  beyond the base pack), §Responsibility (the three-step use case, design.md D3),
  §Public Interface (`Processor`, `NewProcessor`, design.md D10), §Allowed/Forbidden
  Imports (**allowed**: `internal/telemetry`, `internal/charging`, `internal/analytics`,
  `internal/account` — all through their public ports only, design.md **D9**; **forbidden**:
  `internal/gateway`, `internal/tesla` directly (telemetry already wraps it), any
  module's `db` sub-package, `*pgxpool.Pool` anywhere in this module's own code),
  §Data Ownership (**none** — design.md D1/D2, the load-bearing fact that distinguishes
  this module from every other domain module in the project), §Testing Notes.
  **This task lands LAST in Wave 3** (see the Ordering constraints preamble: its id is
  lower than 3.4/3.5 only because RD8 appended those later; `depends_on` carries the
  order). It must therefore also reflect the **scheduler's arrival** (design.md D4/D13):
  - §Responsibility — the module now holds the **scheduled driving adapter** next to the
    port it drives, and why that does not break the peer-adapter rule (mirror design.md
    D3's reconciliation: `Processor` has no `Scheduler` field, `ProcessVehicleData` never
    consults a clock, `cmd/poller` still constructs and starts the scheduler).
  - §Public interface — add `Scheduler`, `NewScheduler`, `Run` alongside `Processor`/
    `NewProcessor`.
  - §Allowed imports — `telemetry.Config` (the clock seam kept by design.md **D13**) and
    stdlib `time`.
  - §Testing Notes — **rewrite**: this module is no longer test-free. It ships
    `scheduler_test.go`'s four relocated tests (D4, Test Contract group S), while the
    three orchestration steps remain deliberately uncovered (D12). Keep the two facts
    visibly distinct so a reviewer can tell an accepted gap from a violation.
  §Data Ownership stays **none**.
  `depends_on`: 3.2, 3.4, 3.5 · `parallel_ok`: with 2.7

- [ ] **3.4** **[module: app worker]** Create `internal/app/scheduler.go` (`package app`)
  — relocate `internal/telemetry/scheduler.go`'s `Scheduler` struct, `NewScheduler`,
  `Run` and the pure `nextRun` **essentially verbatim** (design.md **D4**, carrying RD8;
  the file is removed from `telemetry` by task 2.4, whose report quotes its exact prior
  content — relocate from that, do not rewrite from memory). Permitted differences, and
  **no others**:
  - `package telemetry` → `package app`; `Collector`/`CycleReport` references become
    `Processor` / `telemetry.CycleReport`.
  - The `collector Collector` field becomes `processor Processor`, and the constructor is
    `NewScheduler(processor Processor, hour, minute int, loc *time.Location, cfg
    telemetry.Config) *Scheduler` — **the `cfg telemetry.Config` clock seam is kept
    deliberately**; design.md **D13** records why (minimum diff, unchanged `cmd/poller`
    wiring, unchanged test construction sites) and rejects the narrower
    `now func() time.Time` alternative.
  - The one substantive change, in `Run`'s tick handler:
    `report, err := s.processor.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)`
    in place of `s.collector.CollectAll(ctx)`, followed by `telemetry.LogCycle(report,
    err)` in the same position as before (design.md D11).
  Everything else is byte-for-byte intent-preserving: the `time.NewTimer`-not-`Sleep`
  choice, the `select` on `ctx.Done()`, the `return ctx.Err()` on cancellation, the
  "a per-cycle error is not fatal" behavior, the nil-`loc` → `time.Local` default, and
  `nextRun`'s in-`loc` strictly-after math. **Preserve every doc comment**, adjusting only
  the words that name the port (they explain why the timer, the graceful shutdown and the
  purity of `nextRun` exist, and none of that reasoning changed by moving).
  `depends_on`: 3.1, 3.2 · `parallel_ok`: no (3.5 tests exactly this file; and 3.2 must
  already have produced `Processor`'s implementation for the wiring to make sense)

- [ ] **3.5** **[module: app worker]** Create `internal/app/scheduler_test.go`
  (`package app`, same-package — `TestScheduler_NilLocationDefaultsToLocal` reads the
  unexported `loc` field, and `TestNextRun` calls the unexported `nextRun`) — relocate
  the four tests removed from `internal/telemetry/scheduler_test.go` by task 2.5,
  **assertions and expected values unchanged**. Design.md Test Contract **group S** is
  the authority and states each test's expected values up front; implement to it, not to
  whatever `scheduler.go` happens to do. Required edits, and no others:
  - `package telemetry` → `package app`; `Config{…}` → `telemetry.Config{…}`;
    `CycleReport`/`Reason`/`ReasonAPIError` → their `telemetry.`-qualified forms.
  - Convert `stubCollector` and `reportCollector` from `Collector` stubs into
    **`Processor` fakes**: one method
    `ProcessVehicleData(ctx context.Context, triggeredBy telemetry.TriggeredBy)
    (telemetry.CycleReport, error)` that keeps the same `calls int` counter and returns
    the **same canned values** the collector stub returned (zero report + `nil` for the
    shutdown stub; the caller-supplied `report`/`err` pair for the reporting stub), so
    `TestScheduler_RunsAndLogsOneCycle` drives the identical report through the identical
    `telemetry.LogCycle` call and logs the identical output.
  Do **not** import `internal/tesla` here — the three `TestWaitUntilOnline_*` tests that
  needed it stayed in `internal/telemetry` (task 2.5) and must not follow the scheduler.
  Do **not** add new scheduler tests in this tier; this is a relocation (roadmap D10).
  `depends_on`: 3.4 · `parallel_ok`: no

---

## Wave 4 — composition root + project docs (leader)

- [ ] **4.1** **[leader]** `cmd/poller/main.go` — rewire the composition root to
  **wiring only** (design.md D3, D4, D6, D8, D11, D13). After this task `cmd/poller`
  holds **no business logic at all** — that is the point of the tier
  (`CLAUDE.md` §Non-negotiables: "`cmd/` stays thin (zero business logic)"):
  - Build `processor := app.NewProcessor(telemetryCollector, superchargerReader,
    chargingSessionWriter, acct, recalculator, analyticsReader, gapWriter, loc)` in
    place of the current `reconcilingCollector` literal. Delete
    `reconcilingCollector`, `newSessionMirrorer`, and `newNightlyReconciler` from this
    file — their bodies now live in `internal/app` (task 3.2), not here.
  - Daemon path: replace `telemetry.NewScheduler(collector, cfg.PollerScheduleHour,
    cfg.PollerScheduleMinute, loc, tcfg)` with `scheduler := app.NewScheduler(processor,
    cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)` and call
    `scheduler.Run(ctx)` exactly as today. The `tcfg` argument is unchanged — design.md
    **D13** deliberately kept the `telemetry.Config` clock seam, so this is a
    one-identifier edit (`telemetry.NewScheduler` → `app.NewScheduler`, `collector` →
    `processor`) and the existing comment above `tcfg` ("One telemetry.Config drives both
    the collector … and the scheduler") stays true.
  - **Create NO new file here.** There is no `cmd/poller/scheduler.go` in this change —
    the scheduler's source lives in `internal/app/scheduler.go` (task 3.4). If a
    `cmd/poller/scheduler.go` exists at review, the RD8 reversal was not applied.
  - Update the `--once` branch: replace `collector.CollectAll(ctx)` with
    `processor.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)`; keep its
    existing `telemetry.LogCycle(report, err)` call unchanged.
  - Rewrite the file's package-level doc comment to describe the new shape: `cmd/poller`
    now only **composes** `internal/app`'s `Processor` and `Scheduler` and starts one of
    them (daemon vs `--once`); it owns neither the three-step orchestration nor the
    scheduling logic (design.md D3/D4).
  **Read this task's diff at review — do not trust the signals.** `cmd/poller` has no
  tests; RM29 tier 3's and tier 6's reviews both caught exactly this class of failure
  (a call site that silently never fires while everything still compiles). That remains
  this task's acceptance signal, together with the owner's `go run ./cmd/poller --once`.
  `depends_on`: 3.2, 3.4 · `parallel_ok`: no

- [ ] **4.2** **[leader]** Root `README.md` (docs-track-change, `CLAUDE.md`
  §Non-negotiables):
  - "Project Structure" tree — add `internal/app/` alongside the other domain modules.
  - "Architecture" table — add a row for `internal/app`: owns no data, exposes
    `Processor.ProcessVehicleData` (sync fleet data → process charging data → recalculate
    analytics), called by `cmd/poller` and, later, the parked tier-8 API.
  - "Dependency graph" section — `cmd/poller` now calls `app`, which calls `telemetry`,
    `charging`, `analytics`, `account` — update the diagram/prose accordingly.
  - "Database tables by module" — **no new row**; `internal/app` owns no table
    (design.md D1/D2) and `poll_attempts` stays listed under `internal/telemetry`, gaining
    a one-line mention of the two new columns in its existing description.
  `depends_on`: 1.1, 3.3 · `parallel_ok`: with 4.1, 4.3

- [ ] **4.3** **[leader]** `cmd/README.md` — update the `cmd/poller` row: it is now
  **wiring only**. It composes `internal/app`'s `Processor` and `Scheduler` and starts
  one of them; it no longer owns the three-step orchestration **nor the scheduling
  logic** — the scheduler moved `internal/telemetry` → `internal/app` (design.md D4), not
  into this binary. Mention that `--once` now calls `ProcessVehicleData` directly.
  `depends_on`: 4.1 · `parallel_ok`: with 4.2

- [ ] **4.4** **[leader]** `internal/charging/AGENTS.md` — fix the one sentence design.md
  **D6** flags as going stale: its forbidden-imports section says the mirror's data
  "arrives already mapped, from `cmd/poller` (the composition root)" — change this to
  name `internal/app` instead, since that is where `newSessionMirrorer`'s body now lives.
  This is a single-sentence, doc-only correction inside another module's `AGENTS.md`;
  handled here by the leader (rather than a dispatched `charging` worker) because it is
  one line of documentation, not a code or contract change to `charging` itself — no
  `charging` Go file, test, or public port is touched. While here, **grep
  `internal/analytics/AGENTS.md`** for any claim that `cmd/poller` is the sole/production
  caller of `Recalculator.Reconcile` or `GapWriter` (design.md's Risks section flags this
  as a possible second stale reference that was not verified while producing this
  change's artifacts) and correct it to name `internal/app` if found.
  `depends_on`: 4.1 · `parallel_ok`: with 4.2, 4.3

- [ ] **4.5** **[leader]** `openspec/roadmaps/RM29-modular-monolith-boundaries.md` — flip
  the **T7** row from `[ ]` to `[~]` when this change's artifacts are created, and to
  `[x]` at archive; update §Status. Mirror the same status into
  `RM29-modular-monolith-boundaries.progress.json`. **Do not** edit the D1–D10 decision
  text or any tier's scope — this change's own design.md is where D2's supersession is
  recorded (design.md D1); the roadmap file's D2 entry itself is left as the historical
  record of what was originally decided, exactly as archived tiers' own supersessions
  (e.g. tier 4 vs. tier 6's D1 amendment) were handled.
  `depends_on`: — · `parallel_ok`: with everything

---

## Not in this change — do not do these

Listed so no worker "completes the pattern" and so review can reject them fast:

- **Moving `poll_attempts` to a new table or module.** Roadmap D2's literal text says
  this; design.md D1 supersedes it with the owner's own reversed call from this tier's
  interview. Do not "restore" the original plan.
- **Adding a CHECK constraint on `triggered_by`.** The typed Go constant is the guard
  (design.md D7/D8 in the migration's own comment) — a CHECK is deliberately omitted.
- **Adding any index on `run_id` or `triggered_by`.** Speculative surface with no reader
  in this tier (design.md's Index Plan).
- **Leaving a `Scheduler` (or any copy of `nextRun`) behind in `internal/telemetry`, or
  creating one in `cmd/poller`.** Exactly **one** copy of this logic exists after this
  change: `internal/app/scheduler.go` (design.md **D4**, carrying RD8, which superseded
  RD5's `cmd/poller` plan). A `cmd/poller/scheduler.go` would put business logic back
  into `cmd/`, violating `CLAUDE.md` §Non-negotiables in the very tier that exists to
  take it out. Equally: do **not** delete `TestNextRun`/`TestScheduler_*` — they are
  relocated to `internal/app/scheduler_test.go` (task 3.5), not dropped.
- **Writing a new unit-test suite for `internal/app`'s three orchestration steps.**
  Design.md D12 explains why not, and flags it as a legitimate *future* backlog item
  instead. This does **not** cover `internal/app/scheduler_test.go`, which is required
  (task 3.5) because it is pre-existing coverage relocating with its code, not new
  coverage. Do not add scheduler tests beyond those four either — roadmap D10 keeps this
  tier characterization-only.
- **Touching `internal/charging` or `internal/analytics` Go code, tests, migrations, or
  ports.** Both are consumed unchanged through their existing public interfaces; only
  their callers move.
- **The manual-rerun HTTP API, its auth, or overlap protection.** Tier 8, parked
  (roadmap D9).
