## Context

`cmd/poller/main.go` wires the telemetry daily `Scheduler` (`internal/telemetry/scheduler.go`). Its
only mode today is the blocking nightly path:

```go
scheduler := telemetry.NewScheduler(collector, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)
log.Printf("poller started: nightly collection at %02d:%02d %s (wake timeout %s)", ...)
if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) { ... }
log.Println("poller stopped")
```

`Scheduler.Run(ctx)` (scheduler.go:55) loops forever: it computes the next 03:30-local tick via the
pure `nextRun`, waits on a timer, runs one `collector.CollectAll(ctx)`, calls `logCycle(report, err)`
(scheduler.go:73 → scheduler.go:82), and reschedules. It only returns when `ctx` is cancelled.
`logCycle` is package-private (`internal/telemetry/scheduler.go:82`); it emits a one-line
`telemetry cycle: attempted=N succeeded=N failures={reason=count ...}` summary (plus a separate
whole-cycle error line) using `formatFailures`, and is the only per-cycle operational visibility an
unattended poller has.

There is no way to run a single collection cycle on demand. To exercise the real flow outside the
03:30 window today, an operator would have to either wait for the scheduler or copy the
`CollectAll` + `LogCycle` calls into a throwaway main — duplicating logic and risking drift from the
nightly path.

`cmd/explore-tesla-api/main.go:64-65` is the in-repo precedent for a `flag.Bool` + `flag.Parse()` at
the top of a `main`/`run`, parsed before `config.Load()`. `cmd/poller` already imports `signal`
(`os/signal`, `syscall`) for its `signal.NotifyContext(context.Background(), syscall.SIGINT,
syscall.SIGTERM)` context (main.go:40), and the collector/`CollectAll` honor that context for
graceful cancellation of in-flight work (including wake-waits).

The `telemetry` module's public port (`internal/telemetry/AGENTS.md`) is `Collector.CollectAll(ctx)
(CycleReport, error)`. `Scheduler`, `NewScheduler`, `CycleReport`, `Config`, and the domain types are
already public; `logCycle` is not. This change exports `logCycle` as `LogCycle` so the one-shot
command shares the exact report formatter with the nightly path rather than duplicating it.

## Goals / Non-Goals

**Goals:**
- Let an operator run one immediate telemetry collection cycle on demand, hitting the exact same
  collection code path (`Collector.CollectAll`) and the exact same report formatter the nightly
  scheduler uses, then exit.
- Keep zero collection-logic duplication: the scheduled and `--once` paths call the identical
  `Collector.CollectAll(ctx)` and the identical `LogCycle(report, err)`.
- Keep the scheduled nightly path behavior-identical (same setup, same `Run`, same shutdown).
- Keep `--once` wake-waits cancellable via SIGINT/SIGTERM like every other long call in the repo.

**Non-Goals:**
- No new collection behavior, no new stored field, no new metric, no new table — `CollectAll` and
  its persistence are untouched.
- No scheduling change — the in-app daily daemon and `nextRun` are untouched; `--once` simply never
  builds or runs the scheduler.
- No "catch-up on startup" / back-fill of missed nightly runs (still future work, as in the original
  tier-3 design).
- No HTTP/JSON surface, no dashboard read change, no read-path optimization (out of scope — this
  change touches neither reads nor the schema).

## No Database Objects Touched

No database objects are modified, added, or removed by this change. The existing `vehicle_snapshots`
and `poll_attempts` tables are written by `CollectAll` as usual; no schema migration is needed. The
`database` design gate does NOT apply: there is no new/changed table, column, index, constraint,
view, or migration to justify against the project's read patterns.

## Decisions

### D1 — Export `logCycle` as `LogCycle` (shared formatter) instead of duplicating the logging

The one-shot path needs the same per-cycle operational line (`telemetry cycle: attempted=…
succeeded=… failures={…}`) the nightly path emits. Two options:

1. **Export `logCycle` → `LogCycle`** and have `cmd/poller --once` call it. One formatter, one call
   site in `Scheduler.Run`, one new call site in `cmd/poller`. The two paths provably share the
   exact same report rendering — a future change to the line format (e.g. adding a timestamp) lands
   in one place and both paths update. (CHOSEN.)
2. Duplicate the `log.Printf(...)` formatting into `cmd/poller`. Rejected: it duplicates logic in
   `cmd/`, which `ai/go-conventions.md` keeps thin ("`cmd/` files must stay thin — they wire
   packages together. Zero business logic in `cmd/`"), and the two renderers would drift the next
   time the report shape changes — precisely the silent-divergence risk the change exists to avoid.

The export is a rename only: `func logCycle(report CycleReport, err error)` becomes
`func LogCycle(report CycleReport, err error)`. The body (the `err != nil` whole-cycle-error line,
the `attempted/succeeded/failures` line via `formatFailures`) and the signature are identical. The
single existing call site in `Scheduler.Run` (scheduler.go:73) is updated
`logCycle(report, err)` → `LogCycle(report, err)`. The doc comment gains a sentence noting it is
exported so `cmd/poller` (one-shot mode) and `Scheduler.Run` share the exact same report formatter.

`logCycle` is package-private today and has no callers outside `internal/telemetry` (no other module
imports `internal/telemetry` internals — `Scheduler` is reached only through the public
`NewScheduler` constructor, and `cmd/poller` touches only the public surface). So the rename is
non-breaking: it widens visibility without removing or renaming anything the module's consumers
depend on.

### D2 — Keep `signal.NotifyContext` active for `--once`; reuse that `ctx` for the one-shot `CollectAll`

`cmd/poller` already builds `ctx, stop := signal.NotifyContext(context.Background(),
syscall.SIGINT, syscall.SIGTERM); defer stop()` at main.go:40, before the pool/collector are built.
A `--once` run reuses that same `ctx` for `collector.CollectAll(ctx)`.

- A `--once` run can spend minutes in a bounded wake-wait (D4 of the original design — up to the
  configured `WakeTimeout`, ~90s per asleep vehicle). Reusing the signal context means a SIGINT/SIGTERM
  during that wait cancels the in-flight cycle gracefully — the same cancellation contract the
  nightly `Run(ctx)` honors. (CHOSEN.)
- The alternative — building a fresh `context.Background()` for `--once` — would make a `--once`
  wake-wait ignore SIGINT/SIGTERM and have to be killed with `kill -9`, which is worse operator UX
  and inconsistent with the rest of the repo. Rejected.

`signal.NotifyContext` must be set up before the `if !once / *once` branch (both paths need it), so
its placement is unchanged from today: it stays where it is, after the `DATABASE_URL` guard and
before the pool is built.

### D3 — The schedule-only `time.LoadLocation` block is guarded by `if !once`, not deleted

`time.LoadLocation(cfg.PollerTimezone)` (main.go:33-36) is only needed to build the `Scheduler` —
a `--once` run never schedules, so it never needs a timezone. The block is moved inside
`if !once { ... }` rather than left to run unconditionally, so `cmd/poller --once` does not fatal on
a bad `POLLER_TIMEZONE` when the operator only asked for an immediate cycle. It is NOT deleted: the
nightly path still validates and loads the timezone exactly as today (including the
`log.Fatalf("invalid POLLER_TIMEZONE %q: %v", ...) ` on a bad IANA name). The `loc` variable is
declared inside the `if !once` block and used only there to build the scheduler.

### D4 — One construction path for pool/account/tesla/collector; the branch is only at the tail

The pool, `acct := account.NewService(pool, ...)`, `tcfg := telemetry.Config{...}`, and
`collector := telemetry.NewService(pool, acct, tesla.NewClient(), tcfg)` are built once, before the
`if !once / *once` branch — identical to today. The branch is only at the tail:

- `!*once` (default): inside `if !once {` — build `scheduler := telemetry.NewScheduler(collector,
  cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)`, log "poller started: nightly
  collection…", `scheduler.Run(ctx)` with the existing `errors.Is(err, context.Canceled)` handling,
  then `log.Println("poller stopped")`.
- `*once` (`--once`): `report, err := collector.CollectAll(ctx)` → `telemetry.LogCycle(report, err)`
  → on `err != nil`, `log.Fatalf("one-shot collection: %v", err)` (exit 1, only on a whole-cycle
  enumeration failure — never on an individual vehicle failure, because `CollectAll` returns `nil`
  for per-vehicle failures by D5/D9 of the original design); otherwise the function returns and the
  process exits 0. `NewScheduler` and `Run` are NOT called.

This keeps the setup DRY (no duplicate pool/collector construction — a duplicate pool would risk a
double `pgxpool` against the same `DATABASE_URL`, doubling connection count for no reason) and makes
the only meaningful difference between the two modes the tail: schedule-and-block vs.
collect-once-and-exit.

### D5 — `--once` exit code: 0 on a completed cycle (even with per-vehicle failures), 1 only on whole-cycle failure

`Collector.CollectAll` returns `(CycleReport, error)` where `err != nil` means a *whole-cycle*
failure (e.g. the account enumeration itself failing) and is `nil` even when every individual
vehicle failed (per-vehicle isolation — D5/D9 of the original design). `--once` mirrors that
contract:

- `err == nil` → `LogCycle(report, err)`, then exit 0. A cycle that ran to completion but captured
  zero vehicles successfully is still a *completed* cycle; its failures are visible in the logged
  `CycleReport` and in `poll_attempts`. Exit 0 matches "the on-demand run succeeded as an
  operation," not "every vehicle was captured."
- `err != nil` → `LogCycle(report, err)` (which prints the whole-cycle error line), then
  `log.Fatalf("one-shot collection: %v", err)` → exit 1.

This is the same severity split the nightly `Scheduler.Run` uses internally — a per-cycle
`CollectAll` error there is logged and the daemon waits for the next day (it is never fatal); only
a `ctx` cancellation ends `Run`. `--once` has no "next day," so the whole-cycle error becomes a
non-zero exit instead.

## Risks / Trade-offs

- **`--once` cost.** Like the nightly run, `cmd/poller --once` makes paid Tesla API calls and may
  wake asleep vehicles (sanctioned bounded wake, `AGENTS.md` §"Data Access Model"). An operator who
  runs it repeatedly in a loop pays per cycle — but that is the operator's explicit, on-demand
  choice, never the platform's automatic behavior. The flag defaults to `false`, so the nightly
  scheduler remains the default and the cost profile is unchanged absent an explicit `--once`.
- **Shared formatter is now public.** Exporting `LogCycle` widens the `telemetry` package's public
  surface by one function. It is a leaf formatter (takes `CycleReport, error`, calls only `log` and
  `formatFailures`); it has no DB or port coupling, so the exposure cost is low and the function is
  safe to call from `cmd/`. Accepted to avoid duplicating the logging in `cmd/poller` (D1).
- **No new tests required.** `LogCycle` is already covered by the existing scheduler tests
  (`scheduler_test.go` — `TestScheduler_RunsAndLogsOneCycle` exercises the logging path through
  `Run`'s call to `logCycle`, now `LogCycle`). `cmd/poller` is a thin `main` with no test harness
  by repo convention (`cmd/setup`, `cmd/web`, `cmd/explore-tesla-api` are all test-free), and the
  `--once` branch adds no business logic — it wires `CollectAll` + `LogCycle` + exit. A build/vet
  pass plus the existing telemetry suite is the verification bar. (If the reviewer believes a
  test seam is warranted, it can be requested as a follow-up; no `cmd/` test precedent exists.)
- **No read-path or schema impact.** This change touches no DB read query, no index, no migration.
  The performance/read-heaviness profile (`ai/architecture.md` §7) is unaffected.

## Migration Plan

1. **scheduler.go** — rename `logCycle` → `LogCycle` (export); update the call site in
   `Scheduler.Run` (scheduler.go:73); refresh the doc comment. No other change in that file.
2. **cmd/poller/main.go** — add `"flag"` to the import block; at the top of `main()`, before
   `config.Load()`, add the `once := flag.Bool("once", false, …)` + `flag.Parse()` pair; move the
   `time.LoadLocation(cfg.PollerTimezone)` block inside `if !once {`; keep `config.Load()` + the
   `DATABASE_URL` guard + `signal.NotifyContext` before the branch; build pool/acct/tcfg/collector
   once; add the `if !once { … existing scheduler path … } else { … CollectAll → LogCycle →
   exit … }` tail; update the package doc comment with the `--once` usage line.
3. **Verify** — `go build ./...`, `go vet ./...`, `go test ./...` (the existing telemetry suite
   covers `LogCycle` via the scheduler test; `cmd/poller` has no test by repo convention). Confirm
   `cmd/poller --once` builds and that the nightly `cmd/poller` (no flag) path is unchanged source-
   for-source except for the rename and the `if !once` guard. No sqlc, no migration, no `make`
   target changes.