# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-data-hub.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    `2026-08-31`
Status: APPLIED 2026-09-01

Derived from the four requirements merged by change `RM36-telemetry-add-poll-runs`
(ticket MAG-35, archived `2026-08-31-RM36-telemetry-add-poll-runs`): Run-Level Poll
Summary Storage, Tesla API Call Counting, Account-Level Attempt And Outcome Counts,
and Nightly Cycle Log Summary.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `telemetry module`, `vehicle snapshots`, `nightly collection`, `telemetry hub`, `who reads telemetry`, `poll run`, `run summary`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables `vehicle_snapshots`, `supercharger_sessions`, `poll_attempts`, `poll_runs`

## [guide] ## How maintenance works — APPEND

- **Record a new run-level fact:** add the field to `telemetry.CycleReport` (populated inside `CollectAll`), add the column to `poll_runs` via a migration, extend `telemetry.PollRun` + the `InsertPollRun` query, and map it in `RunWriter.RecordRun`. The caller in `internal/app` passes the whole `CycleReport` — it gains no pool and no table.
- **Read `poll_runs`:** there is **no read port yet**. Direct SQL is the only way to see a row today; adding a `Reader`-style method is deferred backlog work, not an existing surface.

## [guide] ## Conventions & gotchas — APPEND

- **A poll run is recorded exactly once, and a duplicate is an error — never an upsert.** A second summary for a run identity that already has one is rejected and leaves the first record unchanged; recording a run twice is never a legitimate outcome. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **A run that fails before touching a single vehicle still records a summary.** Its account and vehicle counts are all zero, but its start/finish times and duration are real. This is the whole point of the table: before it, a failed run left no trace at all, because zero `poll_attempts` rows were written. _Source: spec telemetry — Requirement: Run-Level Poll Summary Storage._
- **Every Tesla Fleet API request counts, including the ones that fail.** A rejected request still consumes a request against the vendor's quota, so the counter increments before the error is checked — never after. _Source: spec telemetry — Requirement: Tesla API Call Counting._
- **"Whole-account failure" has exactly two causes.** An account counts as failed only when it cannot obtain a usable Tesla access token, or when its account-wide vehicle-list request is rejected as unauthorized. No other failure mode marks an account failed — per-vehicle failures never do. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **Succeeded accounts is derived, not counted:** attempted minus failed. Do not increment it independently or the two will drift. _Source: spec telemetry — Requirement: Account-Level Attempt And Outcome Counts._
- **The cycle log line mixes two grains, so every label says which.** Vehicle-grain counts are labelled as vehicles (`vehicles_attempted`/`vehicles_succeeded`); account-grain counts and the Tesla API call count are reported alongside them. The unlabelled `attempted`/`succeeded` pair was the exact ambiguity MAG-35 was filed about. _Source: spec telemetry — Requirement: Nightly Cycle Log Summary._

## [index] ## Architecture topics — ADD ROWS

| `poll run` (one `poll_runs` row per collection-cycle invocation: trigger, timing, account/vehicle outcome counts, Tesla API call count) | `architecture/telemetry-data-hub.md` |
| `run summary` | synonym of `poll run` → `architecture/telemetry-data-hub.md` |
| `poll_runs` | `telemetry.PollRun` / `telemetry.RunWriter.RecordRun` → `architecture/telemetry-data-hub.md` |
| `Tesla API call count` | `CycleReport.TeslaAPICalls` (counting decorator inside `internal/telemetry`) → `architecture/telemetry-data-hub.md` |
