Source: MAG-26 — https://linear.app/magus-monitor/issue/MAG-26/modular-monolith-refactoring
Roadmap: openspec/roadmaps/RM29-modular-monolith-boundaries.md
Tier: 7 of 8 (`app` (new) gains `ProcessVehicleData`; tiers 1–6 are archived —
`RM29-analytics-rename-from-battery`, `RM29-charging-rename-from-manualcharge`,
`RM29-analytics-add-vehicle-metrics`, `RM29-telemetry-drop-derived-columns`,
`RM29-analytics-own-charge-gaps`, `RM29-charging-add-charge-sessions`; tier 8 is
parked, D9). T7 depends on T1 and T2 (both archived) and is the last unblocked tier.
Design gate: this change trips the built-in **`database`** design gate (`poll_attempts`
gains two columns). The schema was **settled with the owner in the pre-artifacts
interview** (RD7 in design.md) and is transcribed here, not renegotiated.
Unit tests: **mostly none-new, per roadmap D10**. The one genuinely new behavior
(`run_id`/`triggered_by` stamping) extends `internal/telemetry`'s existing offline
`CollectAll` fixtures rather than adding a new suite, plus DB-integration coverage for
the two new columns. `internal/app`'s three orchestration
steps get **no new unit-test suite** — they are pure code relocations from `cmd/poller`,
which has never had tests; see design.md D12 for the honest accounting of that accepted
gap. `internal/app` does ship one test file, `scheduler_test.go`, but its four tests are
**pre-existing coverage moving with the scheduler's code** (design.md D4), not new
coverage. **No test is deleted anywhere in this change.**

## Why

Roadmap violation #3: **there is no application layer.** `cmd/poller`'s
`reconcilingCollector` (plus its two post-cycle helper closures,
`newSessionMirrorer` and `newNightlyReconciler`) is business orchestration living in a
`cmd/` binary because there was nowhere else to put it — it hand-rolls exactly the
`ProcessVehicleData` = sync + process-charging + recalculate split the roadmap asks for.
`cmd/` is supposed to be a thin wiring layer (`ai/go-conventions.md`); this orchestration
belongs in a real, testable module.

Three further, related problems this tier removes:

1. **The nightly scheduler and the future manual-rerun API would otherwise both have to
   re-implement the same three-step cycle**, or one would call the other's
   implementation detail. A shared `Processor` port lets both be thin driving adapters.
2. **`poll_attempts` cannot currently distinguish a nightly run from a future manual
   run.** There is no `triggered_by`, so an operator (or the tier-8 API) has no way to
   tell which invocation wrote a given attempt, nor to correlate every vehicle's row
   from one invocation together.
3. **The roadmap's own D2 decision, revisited at this tier's interview, turned out to be
   wrong about where `poll_attempts` belongs.** See design.md D1 for the full
   supersession: this change does **not** move the table to a new module; it stays in
   `internal/telemetry` and gains two columns instead.

## What Changes

**New module `internal/app`.** Owns **zero schema** (design.md D1/D2 — this is a
deliberate, load-bearing choice, not an oversight) and exposes exactly one port:

- **ADDED** — `internal/app` (new): `Processor` interface (`ProcessVehicleData(ctx,
  triggeredBy) (telemetry.CycleReport, error)`) and `NewProcessor(...)`, composing
  `telemetry.Collector` (sync), a mirror step reading `telemetry.SuperchargerReader` and
  writing `charging.SessionWriter` (moved **wholesale** from `cmd/poller`'s
  `newSessionMirrorer` — design.md D6), and a reconcile step calling
  `analytics.Recalculator` / `analytics.Reader` / `analytics.GapWriter` (moved
  **wholesale** from `cmd/poller`'s `newNightlyReconciler` — design.md D8). No new Go
  type is introduced for the return value: it reuses `telemetry.CycleReport` unchanged
  (design.md D7). **Also ADDED here** — `Scheduler` / `NewScheduler` / `Run`, relocated
  essentially verbatim from `internal/telemetry/scheduler.go` (design.md **D4**, carrying
  **RD8**, which supersedes RD5's `cmd/poller` plan). It stays a *driving adapter* that
  CALLS the port from outside — `Processor` has no `Scheduler` field and
  `ProcessVehicleData` never consults a clock (design.md D3) — it simply now lives in the
  same package. Its one substantive edit is calling
  `ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)` where it used to call
  `Collector.CollectAll(ctx)`. Its constructor keeps the `cfg telemetry.Config` clock
  seam unchanged (design.md **D13**).
- **CHANGED** — `internal/telemetry`: `Collector.CollectAll` widens to
  `CollectAll(ctx, run RunContext) (CycleReport, error)`; new exported types
  `TriggeredBy` (`scheduler` | `api`) and `RunContext{RunID, TriggeredBy}`; `Attempt`
  gains `RunID uuid.UUID` and `TriggeredBy TriggeredBy`, stamped by `record()`.
  `telemetry.Scheduler` / `telemetry.NewScheduler` / `Run` / `nextRun` **leave this
  module** — they are **relocated to `internal/app`** (design.md D4, carrying RD8; see
  the `internal/app` bullet above), not deleted. `LogCycle`/`formatFailures` **stay in
  `telemetry`**, relocated out of `scheduler.go` into a new `report.go` and still
  exported, since `internal/app`'s scheduler now calls `LogCycle` across the boundary
  (design.md D11).
- **CHANGED (schema)** — `poll_attempts` (owned by `internal/telemetry`, unchanged
  ownership) gains `run_id UUID` (nullable, unbackfilled) and
  `triggered_by TEXT NOT NULL DEFAULT 'scheduler'`. **This SUPERSEDES roadmap D2**,
  which said the table moves to `app` as `process_runs` — design.md D1 records why the
  interview reversed that call. **Grain is unchanged**: one row per (vehicle, run);
  `run_id` correlates a run's rows, no new run-level table is added (design.md D2).
- **CHANGED** — `cmd/poller`: becomes **wiring only**, gaining no file and no logic. It
  constructs `app.NewProcessor(...)` and `app.NewScheduler(processor, hour, minute, loc,
  tcfg)` and starts one of them; the `--once` path calls
  `Processor.ProcessVehicleData` directly. `reconcilingCollector`, `newSessionMirrorer`
  and `newNightlyReconciler` are **deleted** from it (their bodies moved into
  `internal/app`, not rewritten), and the scheduler it used to construct from
  `internal/telemetry` it now constructs from `internal/app` — a one-identifier change.
  This is the point of the tier: `CLAUDE.md` requires `cmd/` to stay thin with zero
  business logic, so no orchestration and no timer/date logic may be left behind or
  newly introduced here (design.md D4).
- **MOVED, with no coverage loss (RD8)** — `internal/telemetry/scheduler_test.go`'s four
  `Scheduler`/`nextRun` tests relocate to `internal/app/scheduler_test.go`, assertions
  and expected values unchanged; only their two collector stubs become `Processor` fakes
  (design.md Test Contract group S). RD5 had planned to delete them; **RD8 overturned
  that** — see design.md D4, which records the reversal and the leader's own correction
  of the "coverage would be lost anyway" framing it originally used (Go permits tests in
  a `main` package, so coverage was never the deciding constraint; the `cmd/`-stays-thin
  rule was). `TestFormatFailures` and the three `TestWaitUntilOnline_*` tests in the same
  file also move, but **stay inside `internal/telemetry`** (`report_test.go` /
  `wake_test.go`) — the code they cover does not leave the module.

**Breaking:** the module boundary changes (a new module gains orchestration that used to
sit in `cmd/`), but no external behavior changes: every existing log line, every
per-vehicle/per-account isolation rule, and the whole-cycle short-circuit contract are
preserved verbatim (design.md D3/D6/D8). `telemetry.Collector.CollectAll`'s signature is
a breaking Go API change within the module boundary — its only callers are `internal/app`
(new) and `internal/telemetry`'s own tests, both updated in this change.

**Modules affected:** `app` (new — owns the use case **and**, since RD8, hosts the
scheduled driving adapter that calls it), `telemetry` (schema change + signature
widening + `Scheduler` relocated out), `cmd/poller` (composition root — reduced to
wiring: loses three moved functions and constructs its scheduler from `internal/app`
instead of `internal/telemetry`). `charging` and `analytics` are consumed through
their existing, **unchanged** public ports; neither module's Go code changes.

## Read paths affected

Per `openspec/config.yaml` §proposal:

- **No read path changes.** `internal/app` introduces no new query and no new table.
  The three composed steps run exactly the same reads and writes they ran inside
  `cmd/poller` today, in the same order, with the same isolation.
- **One column pair is added to an existing hot-ish write path** (`poll_attempts`,
  written once per vehicle per nightly cycle — a write, not a read; see design.md
  §Database Changes and §Index Plan for why no index is added).

## Impact

- **Affected specs:** `process-vehicle-data` (new capability, ADDED requirements only).
- **Affected code:** `internal/app/` (new — `app.go`, `processor.go`, `scheduler.go`,
  `scheduler_test.go`, `AGENTS.md`),
  `internal/telemetry/` (`telemetry.go`, `service.go`, `mapping.go`, a new
  `report.go`/`report_test.go`, a new `wake_test.go`, `scheduler.go` and
  `scheduler_test.go` removed — their contents relocated, not discarded —
  `service_test.go`, `db/migrations/`, `db/query.sql`,
  `AGENTS.md`), `cmd/poller/main.go` (**no new file** — the scheduler does not land
  here),
  `internal/charging/AGENTS.md` (one stale sentence — see design.md, its composition
  root reference moves from `cmd/poller` to `internal/app`), root `README.md`,
  `cmd/README.md`.
- **Design gate:** this change trips the built-in `database` gate. design.md carries the
  complete `ALTER TABLE`, both column comments, the rationale for no CHECK and no index,
  and the index plan.
- **Deferred, explicitly NOT in scope:** the manual-rerun HTTP API (tier 8, parked —
  roadmap D9); overlap protection between a nightly run and a manual re-run (same tier as
  the API); any change to `internal/charging` or `internal/analytics` Go code (their
  ports are consumed unchanged).
