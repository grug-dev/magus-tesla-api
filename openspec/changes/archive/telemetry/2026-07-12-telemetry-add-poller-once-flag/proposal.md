## Why

`cmd/poller` is the thin wiring command for the `telemetry` module's nightly collection. Today it
always blocks on `scheduler.Run(ctx)` until the next scheduled tick (default 03:30 local). There is
no way to run a single collection cycle on demand — to exercise the real end-to-end flow (real
`account` + `tesla` ports, real `pgxpool`, real `vehicle_snapshots`/`poll_attempts` writes) outside
the daily window without waiting for the scheduler or duplicating collection logic in a throwaway
script.

This change adds a `--once` flag to `cmd/poller` that runs exactly one immediate
`Collector.CollectAll(ctx)`, logs the cycle outcome with the same formatter the nightly path uses,
and exits — hitting the identical collection code path the scheduled run hits. No collection logic
is duplicated: both the scheduled and `--once` paths call the same `Collector.CollectAll(ctx)` and
share the same report formatter (now exported as `LogCycle`).

This is a developer/operational affordance, not a new collection behavior: it changes only how the
existing capability is *invoked*, never what it collects or where it stores. It is sanctioned by the
Data Access Model (`AGENTS.md` §"Data Access Model") exactly as the nightly anchor snapshot is — a
server-side scheduled/background collection job, just one triggered manually instead of by the
in-app daemon.

## What Changes

- **Export `logCycle` → `LogCycle`** in `internal/telemetry/scheduler.go`. The function body and
  signature are unchanged; only the identifier is exported so `cmd/poller` in one-shot mode calls
  the exact same report formatter that `Scheduler.Run` calls on every nightly tick. The single call
  site inside `Scheduler.Run` (scheduler.go:73) is updated from `logCycle(report, err)` to
  `LogCycle(report, err)`. The doc comment is updated to note it is the shared per-cycle report
  formatter used by both `Scheduler.Run` and `cmd/poller` one-shot mode.
- **Add a `--once` flag** to `cmd/poller/main.go`. At the top of `main()`, before `config.Load()`,
  parse `once := flag.Bool("once", false, "run one collection cycle immediately and exit (skip the daily scheduler)")`
  followed by `flag.Parse()` — mirroring `cmd/explore-tesla-api/main.go:64-65`. `config.Load()` and
  the `cfg.DatabaseURL == ""` guard are kept exactly as today. The schedule-only
  `time.LoadLocation(cfg.PollerTimezone)` block (main.go:33-36) is moved inside an `if !once { ... }`
  guard (a `--once` run does not need a timezone — it never schedules). The pool, `acct`, `tcfg`,
  and `collector := telemetry.NewService(...)` are constructed once (as today); nothing is built
  twice. Then:
  - **Default (`!*once`)**: the existing path, unchanged, inside the `if !once` branch — build
    `scheduler := telemetry.NewScheduler(collector, ...)`, log the "poller started: nightly
    collection..." line, `scheduler.Run(ctx)` with the existing `errors.Is(err, context.Canceled)`
    handling, then `poller stopped`.
  - **`--once` (`*once`)**: `report, err := collector.CollectAll(ctx)` → `telemetry.LogCycle(report,
    err)` → on a whole-cycle error, `log.Fatalf` (exit 1); otherwise exit 0. `NewScheduler` and
    `Run` are NOT called. `signal.NotifyContext(SIGINT, SIGTERM)` is kept before this branch so a
    `--once` wake-wait is cancellable with SIGINT/SIGTERM (the cycle's `ctx` is the signal context).
- The `cmd/poller` package doc comment (main.go:1-4) gains a usage line:
  `// go run ./cmd/poller --once    # one immediate cycle, then exit`.

**Not breaking.** No existing public method, type, table, column, query, or spec scenario is removed
or has its behavior changed. The one renamed identifier, `logCycle` → `LogCycle`, was a
package-private function with a single in-package call site; it has no external callers (no other
module imports `internal/telemetry`'s internals — the scheduler is constructed through the public
`NewScheduler`, and `cmd/poller` only reaches the public surface). The scheduled nightly path is
behavior-identical (same `CollectAll`, same formatter, same shutdown). No production `cmd/web` code
changes.

## No Database Objects Touched

No database objects are modified, added, or removed by this change. The existing `vehicle_snapshots`
and `poll_attempts` tables are written by `CollectAll` as usual; no schema migration is needed. The
`database` design gate (openspec/config.yaml — `design`) does NOT apply: there is no new/changed
table, column, index, constraint, view, or migration.

## Capabilities

### Added Capabilities

- `telemetry`: a new operational mode of the telemetry collection capability — a one-shot immediate
  collection cycle invokable on demand via `cmd/poller --once`, running the identical
  `Collector.CollectAll` code path the nightly scheduler runs, persisting data to the same tables,
  logging the same per-cycle summary, and exiting without ever arming the daily schedule.

## Impact

- **Modified code (module sandbox):**
  - `internal/telemetry/scheduler.go` — rename `logCycle` → `LogCycle` (export); update the one
    call site in `Scheduler.Run`; refresh the doc comment. Body, signature, and `formatFailures`
    helper are untouched.
  - `cmd/poller/main.go` — add `"flag"` import; parse `--once`; guard the schedule-only
    `time.LoadLocation` block behind `if !once`; branch on `*once` between the existing scheduler
    path and the one-shot `CollectAll` + `LogCycle` + exit path; update the package doc comment.
- **No persistence changes** — no migration, no `query.sql` edit, no sqlc regen. `vehicle_snapshots`
  and `poll_attempts` are written by the unchanged `CollectAll`.
- **No spec scenario removed or weakened** — a new requirement/scenario is added for the on-demand
  one-shot mode; the existing "Scheduled Unattended Collection" requirement is unchanged.
- **Read paths affected: none.** This is not a performance-sensitive change. It touches no
  dashboard read path and no database read query. The `--once` path performs the same writes the
  nightly batch performs (off the read hot path by design — `ai/architecture.md` §7).
- **Modules affected:** `telemetry` only (owns `internal/telemetry/` plus its thin wiring command
  `cmd/poller/main.go`, which no other module lays claim to). No other module's files are touched.
- **Operational:** `cmd/poller --once` makes the same paid Tesla API calls (including sanctioned
  wakes of asleep vehicles) the nightly run makes; it runs them on demand, once, then exits. It
  requires the same `DATABASE_URL` the scheduled poller requires.

> Grill-me was run by the leader during plan confirmation. The two binding design decisions
> (D1 — export `LogCycle` as the shared formatter rather than duplicate the logging; D2 — keep
> `signal.NotifyContext` active for `--once` and reuse the existing `ctx` for the one-shot
> `CollectAll` so wake-waits stay cancellable) are recorded in design.md and drive this proposal.