> **Additive, non-breaking change.** Adds a `--once` flag to `cmd/poller` that runs one immediate
> telemetry collection cycle and exits instead of blocking on the daily scheduler. No collection
> logic is duplicated: both the scheduled and `--once` paths call the identical
> `Collector.CollectAll(ctx)` and the identical `LogCycle(report, err)` (the renamed, exported
> `logCycle`). No database objects are modified, added, or removed — the existing `vehicle_snapshots`
> and `poll_attempts` tables are written by the unchanged `CollectAll`. No migration, no sqlc
> regen, no read-path change. Only the `telemetry` module's `scheduler.go` and the thin wiring
> command `cmd/poller/main.go` are touched.
>
> **Two sub-tasks, disjoint files, ordered by one dependency:**
> - **Task 1** — export `logCycle` → `LogCycle` in `internal/telemetry/scheduler.go`. **No deps.**
>   Touches only `scheduler.go`. The rename is source-for-source; the body, signature, and
>   `formatFailures` are untouched; the one in-package call site in `Scheduler.Run` is updated.
> - **Task 2** — add `--once` to `cmd/poller/main.go` and branch on `*once`. **Depends on task 1**
>   (`telemetry.LogCycle` must be exported for the `--once` branch to call it). Touches only
>   `main.go`.
>
> The two tasks touch disjoint files (`internal/telemetry/scheduler.go` vs `cmd/poller/main.go`),
> so they are safe to implement in parallel by separate agents EXCEPT that task 2's `--once` branch
> will not compile until task 1's export lands. Run them in order: task 1 first, then task 2.

## 1. Export `logCycle` → `LogCycle` in `internal/telemetry/scheduler.go` — no dependencies

- [ ] 1.1 Rename the package-private function `logCycle` (scheduler.go:82) to `LogCycle` (export
      it). The signature `func LogCycle(report CycleReport, err error)` and the body (the
      `err != nil` whole-cycle-error `log.Printf` line, then the `attempted/succeeded/failures`
      `log.Printf` line via `formatFailures`) stay identical. The `formatFailures` helper is
      untouched.
- [ ] 1.2 Update the single in-package call site in `Scheduler.Run` (scheduler.go:73) from
      `logCycle(report, err)` to `LogCycle(report, err)`. No other call site exists.
- [ ] 1.3 Update the `logCycle` doc comment (scheduler.go:78-81) to note it is exported so
      `cmd/poller` (one-shot mode) and `Scheduler.Run` share the exact same report formatter; keep
      the existing explanation of what the line prints and that it uses the stdlib `log` package.
- [ ] 1.4 Verify `go build ./internal/telemetry/...` and `go vet ./internal/telemetry/...` pass,
      and the existing `scheduler_test.go` (`TestScheduler_RunsAndLogsOneCycle`, which exercises
      the logging path through `Run`) still passes — confirming the rename is source-for-source
      and the in-package caller is updated.

**Depends on:** none.
**Parallel-ok:** yes (disjoint file from task 2; but task 2 compiles only after this lands).

## 2. Add `--once` flag to `cmd/poller/main.go` and branch on `*once` — depends on task 1

- [ ] 2.1 Add `"flag"` to the import block of `cmd/poller/main.go` (alphabetical order within the
      stdlib group, after `"errors"`). Do not remove or reorder the existing imports.
- [ ] 2.2 At the top of `main()`, before `cfg, err := config.Load()`, add:
      `once := flag.Bool("once", false, "run one collection cycle immediately and exit (skip the daily scheduler)")`
      followed immediately by `flag.Parse()`. Mirror the `flag.Int`/`flag.Parse` pair at
      `cmd/explore-tesla-api/main.go:64-65`. Keep `config.Load()` and the `cfg.DatabaseURL == ""`
      guard exactly as today (both run unconditionally, for both modes).
- [ ] 2.3 Move the schedule-only `time.LoadLocation(cfg.PollerTimezone)` block (today main.go:33-36,
      including the `log.Fatalf("invalid POLLER_TIMEZONE %q: %v", ...) `) inside an `if !once { ... }`
      guard. Declare `loc` inside that block. A `--once` run must NOT load or validate the timezone
      (it never schedules). Do not delete the block — the nightly path keeps it.
- [ ] 2.4 Keep `ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT,
      syscall.SIGTERM); defer stop()` before the branch (both modes need it). Build the pool, `acct`,
      `tcfg`, and `collector := telemetry.NewService(...)` once, as today — do NOT construct
      anything twice.
- [ ] 2.5 Add the tail branch:
      - `if !once {` — the existing nightly path, unchanged: build
        `scheduler := telemetry.NewScheduler(collector, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)`,
        log `"poller started: nightly collection at %02d:%02d %s (wake timeout %s)"`,
        `if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) { log.Fatalf("scheduler: %v", err) }`,
        `log.Println("poller stopped")`.
      - `} else {` — the one-shot path: `report, err := collector.CollectAll(ctx)`,
        `telemetry.LogCycle(report, err)`, then `if err != nil { log.Fatalf("one-shot collection: %v", err) }`
        (exit 1 only on a whole-cycle failure; `CollectAll` returns `nil` for per-vehicle failures).
        Do NOT call `NewScheduler` or `Run`. On `err == nil` the function returns and the process
        exits 0.
- [ ] 2.6 Update the `cmd/poller` package doc comment (main.go:1-4) with the `--once` usage line:
      `// go run ./cmd/poller --once    # one immediate cycle, then exit` (alongside the existing
      description of the nightly command).
- [ ] 2.7 Verify `go build ./cmd/poller` and `go vet ./cmd/poller` pass. Confirm `cmd/poller` (no
      flag) still builds and the nightly branch is source-for-source the existing path (minus the
      `if !once` wrapping and the `flag` plumbing). Confirm `cmd/poller --once` compiles against
      the exported `telemetry.LogCycle` from task 1.

**Depends on:** task 1 (`telemetry.LogCycle` must be exported).
**Parallel-ok:** no (compiles only after task 1 lands; same module's public surface).

## 3. Verification — depends on 1, 2

- [ ] 3.1 `go build ./...` and `go vet ./...` pass with no changes to any other module.
- [ ] 3.2 `go test ./...` green; the existing telemetry suite (which covers `LogCycle` via
      `scheduler_test.go`) passes; no Tesla API call fires from the test run; no new test is
      required for `cmd/poller` (the thin `main` follows the repo's `cmd/` test-free convention —
      `cmd/setup`, `cmd/web`, `cmd/explore-tesla-api` are all test-free).
- [ ] 3.3 `openspec validate telemetry-add-poller-once-flag --strict` passes and every tasks.md
      checkbox reflects real completion.
- [ ] 3.4 Boundary check: `internal/telemetry` gains no new import and no new cross-module
      dependency (only an identifier is exported); `cmd/poller` adds only the stdlib `"flag"`
      import and its existing `internal/telemetry` import (now calling `LogCycle`); no other
      module's files are touched; no DB migration, no sqlc regen, no `Makefile` change.
- [ ] 3.5 Confirm the scheduled nightly path is behavior-identical: same setup, same
      `NewScheduler`, same `Run`, same `errors.Is(err, context.Canceled)` handling, same
      `"poller started"`/`"poller stopped"` log lines; the only differences are the `if !once`
      guard around the `time.LoadLocation` block + scheduler path, the `flag` plumbing, and the
      `else` one-shot branch.