> **Scope.** Adopts `internal/clock` in `internal/app` (roadmap `RM35-timezone-
> centralization`, tier 5 of 7, depends on tier 1, already archived): `NewScheduler`'s
> nil-`loc` fallback moves from `time.Local` to `clock.Zone()`, and `recalculateAnalytics`'s
> hand-rolled UTC-midnight day-truncation delegates to `clock.CalendarDay(clock.Now(),
> p.loc)`, preserving `p.loc` — the poller's own configured zone — unchanged. No migration,
> no schema, no `cmd/` edit.
>
> **Dependencies / parallelism:**
> - T1 (`scheduler.go` — the nil-`loc` fallback swap + doc comment) has no dependency beyond
>   tier 1 (`internal/clock`, already on disk). Must land before T4.
> - T2 (`processor.go` — `recalculateAnalytics`'s day-math delegation) has the same
>   dependency as T1 and is disjoint from it (different file); MAY run in parallel with T1.
> - T3 (`internal/app/AGENTS.md` doc corrections) depends on T1 and T2 — it documents the
>   final behavior they produce.
> - T4 (repair `scheduler_test.go`) depends on T1 — it repairs the one assertion the
>   fallback change breaks.
> - T5 (root `README.md` dependency-graph + adoption-list edit) has no dependency on T1–T4;
>   MAY run at any time. This is the leader's own edit, not this tier's (`proposal.md`
>   Impact) — already landed.
> - T6 (OpenSpec artifacts: this file, `proposal.md`, `design.md`, `specs/app/spec.md`) —
>   produced alongside implementation per this tier's dispatch (artifacts + implementation
>   together).
> - T7 (verification) depends on T1–T4.

## T1. `internal/app/scheduler.go` — the nil-`loc` fallback swap — depends on tier 1 (`internal/clock`, already archived)

- [x] T1.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
      Acceptance: `internal/clock` is a new import; no import cycle (`clock` imports nothing
      project-local — roadmap D2; `app` sits above every domain module in the call graph).
- [x] T1.2 `NewScheduler`'s nil-`loc` fallback changes from `loc = time.Local` to
      `loc = clock.Zone()`.
      Acceptance: matches design.md D-app-3 — a `Scheduler` constructed with `loc == nil`
      now holds `clock.Zone()` (`America/Bogota`) as its own `loc` field, not the host
      process's `time.Local`.
- [x] T1.3 Correct `NewScheduler`'s doc comment, which stated the `time.Local` fallback by
      name, to name `clock.Zone()` instead (`CLAUDE.md`'s "docs track structural change" /
      stale-comment rule).
      Acceptance: no comment in `scheduler.go` names `time.Local` as the fallback.

## T2. `internal/app/processor.go` — `recalculateAnalytics`'s day-math delegation — depends on tier 1

- [x] T2.1 Replace the hand-rolled truncation
      `y, m, d := time.Now().In(p.loc).Date(); end := time.Date(y, m, d, 0, 0, 0, 0,
      time.UTC).AddDate(0, 0, -1)` with `end := clock.CalendarDay(clock.Now(),
      p.loc).AddDate(0, 0, -1)`.
      Acceptance: matches design.md D-app-1/D-app-2 — `end`'s computed value is
      algebraically identical for every input; `p.loc`, the poller's own configured zone,
      is the argument passed to `clock.CalendarDay` unchanged (roadmap D6/D18,
      `RM29-app-add-process-vehicle-data` design.md D-B12 — this composition, not
      `internal/analytics`, owns "which day am I asking about").
- [x] T2.2 Extend the call site's doc comment to explain the delegation and to state
      explicitly that `p.loc` is preserved unchanged (not replaced with `clock.Zone()`).
      Acceptance: the comment names `RM35-app-adopt-clock` design.md D-app-1/D-app-2 and
      makes no claim that the platform default now decides which day
      `recalculateAnalytics` asks about.

## T3. `internal/app/AGENTS.md` — doc corrections — depends on T1, T2

- [x] T3.1 Correct the `Scheduler`/`NewScheduler` port description's "A nil `loc` falls back
      to `time.Local`" line to name `clock.Zone()` instead.
      Acceptance: `grep -n "time.Local" internal/app/AGENTS.md` returns no match describing
      the fallback as current behavior.
- [x] T3.2 Add `internal/clock` to the "Allowed / forbidden imports → May import" list, with
      the two call sites it serves (`recalculateAnalytics`'s day math, `NewScheduler`'s
      nil-`loc` fallback).
- [x] T3.3 Rename the stale `TestScheduler_NilLocationDefaultsToLocal` test-name reference to
      `TestScheduler_NilLocationDefaultsToClockZone` (Testing notes section).
      Acceptance: `grep -n NilLocationDefaultsToLocal internal/app/AGENTS.md` returns no
      match.

## T4. Repair `internal/app/scheduler_test.go` — depends on T1

- [x] T4.1 Rename `TestScheduler_NilLocationDefaultsToLocal` to
      `TestScheduler_NilLocationDefaultsToClockZone` and repoint its assertion from
      `sched.loc == time.Local` to `sched.loc == clock.Zone()`, adding the `internal/clock`
      import.
      Acceptance: matches design.md's Test Contract — this is the one existing test the
      fallback change breaks; no other value in the test changes.
- [x] T4.2 Confirm `TestNextRun`, `TestScheduler_ShutsDownWithoutRunningWhenCancelled`, and
      `TestScheduler_RunsAndLogsOneCycle` are unchanged — all three pass an explicit
      non-nil `loc` (`time.UTC`) and do not exercise the fallback branch.
      Acceptance: no expected `time.Date` literal in any of the three moves.

## T5. Root `README.md` — dependency graph + adoption-list edit — no dependency on T1–T4

- [x] T5.1 §Architecture dependency graph: `app`'s row gains `clock` —
      `app ────────────────► telemetry, charging, analytics, account, clock`.
      Acceptance: verified present at `README.md:230`.
- [x] T5.2 `internal/clock`'s prose row (§Architecture table): the adoption list names
      `config`, `telemetry`, `analytics` and `app` (tiers 2–5); `gateway` stays listed as
      tier 6, still pending.
      Acceptance: verified present at `README.md:205`.
      Note: this is the leader's own edit, not this tier's (`proposal.md` Impact) — landed
      already; no further action needed from this tier.

## T6. OpenSpec artifacts — produced alongside T1–T4 per this tier's dispatch

- [x] T6.1 `proposal.md` — header, Why, What Changes, reachability analysis for the nil-`loc`
      path, breaking assessment, Capabilities, Impact.
- [x] T6.2 `design.md` — Context, Goals/Non-Goals, restated roadmap decisions + this tier's
      own (D-app-1/2/3), Test Contract authored up front, Risks/Trade-offs, Migration Plan
      (none), Open Questions (none).
- [x] T6.3 `specs/app/spec.md` — one ADDED requirement, GIVEN/WHEN/THEN, describing the
      scheduler's default-time-zone behavior (previously unspecified — the module had no
      spec.md before this tier) rather than restating implementation. Does NOT describe
      `recalculateAnalytics`'s day math as a spec-level behavior change, because it is not
      one (D-app-1/D-app-2: algebraically identical, `p.loc` unchanged).
- [x] T6.4 This file (`tasks.md`), kept checked off live as work proceeds.

## T7. Verification — depends on T1–T4

- [x] T7.1 `go build ./internal/app/...` passes.
- [x] T7.2 `go vet ./internal/app/...` passes.
- [x] T7.3 `gofmt -l` reports no diff for `internal/app/scheduler.go`,
      `internal/app/processor.go`, `internal/app/scheduler_test.go`.
- [x] T7.4 Confirm no file outside `internal/app/`, `README.md`, and this change's own
      OpenSpec artifacts was touched — no `cmd/`, no other module (`internal/telemetry`'s
      stale cross-module comment is explicitly left unedited — outside this tier's sandbox,
      flagged in `proposal.md` for the leader).
- [x] T7.5 `go test ./internal/app/...` — run by the leader (outside this tier's own
      Test-Execution-Policy authority): reported green.
- [x] T7.6 `openspec validate RM35-app-adopt-clock --strict` passes (owner/leader step).
      Verified by the leader on 2026-08-31: "Change 'RM35-app-adopt-clock' is valid".
- [x] T7.7 Owner runs the exact suite commands and reports the result, turning this tier's
      work from `awaiting-user-verification` into `done`:
      - `go test ./internal/app/...`
      - `go test ./...` (full repo regression net — roadmap D4's "behavior-preserving except
        the documented default" claim for this tier)
        Owner ran both and reported GOOD on 2026-08-31. Recorded as the OWNER's
        verification, not the assistant's; every T1-T4 task moved
        awaiting-user-verification -> done on the strength of that report.
