# RM62 — Nightly cycle observability

Source ticket: MAG-57 — https://linear.app/magus-monitor/issue/MAG-57/nightly-job-tests

## Intention

After all three tiers land, one `make cmd-poller-once` log shows every database call the
nightly cycle makes, in every module it touches, and you can step through that same cycle
in the VS Code debugger.

## The problem

Today only `internal/telemetry` logs its database calls. It has `query_log.go` with four
decorators. `internal/analytics`, `internal/charging`, `internal/account` and
`internal/tesla` have zero `logging.Note` calls.

So the nightly log shows telemetry reads and writes, and nothing else. The work that
`analytics` and `charging` do in steps 2 and 3 of the cycle is invisible.

Two smaller problems come with it:

- `internal/app/processor.go` prints `gap reconciliation: start → end` once, **before** the
  vehicle loop starts. The telemetry queries that follow that line belong to
  `Recalculator.Reconcile`, not to the gap step. The line reads as if it owns them.
- `.vscode/launch.json` has only a `cmd/web` entry. The poller cannot be debugged from
  VS Code at all.

## Decisions

These were settled with the user before any tier was written. They bind every tier.

1. **No unit tests.** The work is logging decorators and a launch config. The telemetry
   precedent already protects against a missed method with a compile-time assertion, so a
   gap becomes a build error, not a silent hole.
2. **Mirror `internal/telemetry/query_log.go` exactly.** One `query_log.go` per module.
   Every decorator implements its interface **explicitly**, never by embedding. Embedding
   would let a future method satisfy the type by promotion, and that call would never be
   logged. Explicit implementation turns that into a compile error.
3. **All lines go through `internal/logging`.Note**, keeping the platform `[Type] [Method]`
   format. No `slog`, no new logging package.
4. **The gap log fix labels both halves, per vehicle.** `metrics reconciliation: vehicle N`
   before `Reconcile`, and `gap reconciliation: vehicle N: start → end` before the gap work.
   Every query in the log then sits under the step that made it.
5. **Tier order is app → analytics → charging.** The tiers have no technical dependency.
   The user chose to ship the debug setup first, so they can step through the poller while
   the two logging tiers are still being written.

## Out of scope

Named here so no tier picks them up by accident. Each was investigated and closed during
the MAG-57 analysis.

- The wake poll interval (`wakePollInterval`, 5s) and the `ListVehicles` call pattern. The
  repeated calls are the wake loop working as designed.
- A date filter on `ChargingHistory`. The call is account-level with no filter, and the
  real change detection lives in the `UpsertSuperchargerHistory` SQL.
- The gap window length. It is 30 days, which is correct.
- Renaming `internal/app/processor.go`. The name is right.
- Any end-to-end or integration test of the whole cycle.

## Tiers

Status: `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[ ]` | `RM62-app-improve-cycle-step-logging` | `app` | Label both halves of `recalculateAnalytics` per vehicle (Decision 4). Add a VS Code launch configuration that runs `cmd/poller --once` under the debugger, mirroring the existing `cmd/web` entry including its `envFile` handling. | — | Create the OpenSpec artifacts for `RM62-app-improve-cycle-step-logging`. Two pieces. (1) In `internal/app/processor.go`, `recalculateAnalytics` logs its window line once before the vehicle loop, so the telemetry queries that follow appear to belong to the gap step when they belong to `Recalculator.Reconcile`. Move to per-vehicle labels: `metrics reconciliation: vehicle N` before `Reconcile`, `gap reconciliation: vehicle N: start → end` before the gap half. Keep `logging.Note` and the existing message topics so the lines stay greppable. (2) Add a `cmd/poller --once` entry to `.vscode/launch.json`, mirroring the `cmd/web` entry (`envFile` pointing at `.env`). `.vscode/launch.json` is granted to this worker as an explicit extra path; it is outside `internal/`. No tests. Document the new launch entry where a developer will look. |
| `[ ]` | `RM62-analytics-add-query-logging` | `analytics` | A `query_log.go` giving `internal/analytics` the same query logging `internal/telemetry` has. Ports: `Reader`, `Recalculator`, `GapWriter`. | — | Create the OpenSpec artifacts for `RM62-analytics-add-query-logging`. `internal/analytics` has zero `logging.Note` calls, so its work in step 3 of the nightly cycle is invisible in the log. Add `internal/analytics/query_log.go` mirroring `internal/telemetry/query_log.go`: one decorator per port, each implementing its interface EXPLICITLY (never by embedding), with a compile-time assertion per decorator, all lines through `internal/logging`.Note in the `[Type] [Method]` format, keeping an `analytics query:` message topic. Decide in design.md WHICH ports and which methods get logged and say why — the nightly cycle's own read and write path is the priority, because the user's goal is to read one poller log and see which date ranges analytics passes to telemetry. Wire the decorators in at the module's own constructors so `cmd/poller` and `cmd/web` both get them. No tests. |
| `[ ]` | `RM62-charging-add-query-logging` | `charging` | A `query_log.go` giving `internal/charging` the same query logging. It has eight public ports; the tier decides which are in scope. | — | Create the OpenSpec artifacts for `RM62-charging-add-query-logging`. `internal/charging` has zero `logging.Note` calls, so its work in step 2 of the nightly cycle is invisible in the log. Add `internal/charging/query_log.go` mirroring `internal/telemetry/query_log.go` and the shape tier 2 established for `analytics`: explicit interface implementation, never embedding, a compile-time assertion per decorator, all lines through `internal/logging`.Note, message topic `charging query:`. The module has eight public ports (`Writer`, `Reader`, `SessionWriter`, `SessionReader`, `SuperchargerSessionAnalyticsReader`, `SessionVerifier`, `MirrorWatermarkStore`, `MonthlyCapacityCalculator`). Only some are on the nightly path. design.md MUST decide which ports are in scope and justify the cut — logging all eight adds gateway-request noise to every page load, which is not what this ticket asked for. No tests. |

## Future work

None deferred. Everything the MAG-57 analysis found that is not in a tier above is listed
under **Out of scope**, and each item was closed as "working as designed", not postponed.
Nothing was added to `openspec/roadmaps/backlog.md` by this roadmap.
