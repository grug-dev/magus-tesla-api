> **Scope.** Adopts `internal/clock` in `internal/telemetry` (roadmap `RM35-timezone-
> centralization`, tier 3 of 7, depends on tiers 1–2, both already archived): deletes the
> module's own `dateOnly` in favor of `clock.CalendarDay`, and routes `now()`/`location()`'s
> nil-fallbacks through `clock.Now()`/`clock.Zone()`. No migration, no schema, no `cmd/` edit.
> Both test-injection seams (`Config.Clock`, `Config.Location`) are preserved unchanged.
>
> **Dependencies / parallelism:**
> - T1 (`service.go` — the three call-site swaps + `dateOnly` deletion) has no dependency
>   beyond tiers 1–2 (`internal/clock`, `internal/config`, both already on disk). Must land
>   before T2 and T3.
> - T2 (repair `dedupe_test.go` + the five `db_*_integration_test.go` files) depends on T1 — it
>   repairs the compile break `dateOnly`'s deletion causes.
> - T3 (doc-comment sweep: `service.go`, `telemetry.go`, `internal/telemetry/AGENTS.md`,
>   `testdb_test.go`, `query.sql`) depends on T1 — it documents the final behavior T1 produces.
>   Disjoint files from T2; MAY run in parallel with it.
> - T4 (`sqlc generate` to pick up the `query.sql` comment change into `query.sql.go`) depends
>   on T3's `query.sql` edit.
> - T5 (root `README.md` dependency-graph + prose fix) has no dependency on T1–T4; MAY run at
>   any time.
> - T6 (OpenSpec artifacts: this file, `proposal.md`, `design.md`,
>   `specs/telemetry/spec.md`) — produced alongside implementation per this tier's dispatch
>   (artifacts + implementation together).
> - T7 (verification) depends on T1–T5.
>
> **Leader-integrated step:** T4 (`sqlc generate`) — this tier's only codegen re-run; comment-
> only source change, no query/schema/type change.

## T1. `internal/telemetry/service.go` — the three call-site swaps — depends on tiers 1–2 (`internal/clock`, `internal/config`, already archived)

- [x] T1.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
      Acceptance: `internal/clock` is a new import; no import cycle (`clock` imports nothing
      project-local — roadmap D2).
- [x] T1.2 `(*service).now()`: nil-`Config.Clock` fallback changes from `time.Now()` to
      `clock.Now()`. The `Config.Clock` seam itself (the `if s.cfg.Clock != nil` branch) is
      unchanged.
      Acceptance: matches design.md D-tel-2 — `clock.Now()` is `time.Now().In(clock.Zone())`,
      the same instant `time.Now()` returned, so every persisted value this feeds
      (`CapturedAt`, `AttemptedAt`) is unaffected by the swap.
- [x] T1.3 `(*service).location()`: nil-`Config.Location` fallback changes from `time.Local` to
      `clock.Zone()`. The `Config.Location` seam itself is unchanged.
      Acceptance: matches design.md D-tel-3 — `Config{}` (zero value) now returns `clock.
      Zone()` from `location()`.
- [x] T1.4 Delete `dateOnly(t time.Time, loc *time.Location) time.Time` entirely (formerly
      `service.go:556-561`). Update its one call site, inside `snapshotFrom`
      (`CapturedDate: dateOnly(capturedAt, loc)`), to call `clock.CalendarDay(capturedAt,
      loc)` directly.
      Acceptance: `dateOnly` no longer exists anywhere in `service.go`; `grep -n dateOnly
      internal/telemetry/service.go` returns no match; `Snapshot.CapturedDate`'s computation is
      algebraically identical to before (design.md D-tel-1 / tier-1 D7).
- [x] T1.5 Correct `dateFrom`'s doc comment (references `dateOnly` by name) to name
      `clock.CalendarDay` instead.
      Acceptance: no comment in `service.go` names the deleted `dateOnly` function.

## T2. Repair tests broken by `dateOnly`'s deletion — depends on T1

- [x] T2.1 `internal/telemetry/dedupe_test.go`: add the `internal/clock` import; repoint the
      three `dateOnly(...)` calls at `clock.CalendarDay(...)`, keeping the exact same
      arguments and expected values (renamed `TestDateOnly_ComfortablyInsideLocalDay` →
      `TestCalendarDay_ComfortablyInsideLocalDay`, `TestDateOnly_UTCDayAheadOfLocalDay` →
      `TestCalendarDay_UTCDayAheadOfLocalDay`, `TestDateOnly_UTCLocationIsNoOp` →
      `TestCalendarDay_UTCLocationIsNoOp`).
      Acceptance: matches design.md's Test Contract — identical expected `time.Date` literals
      for all three cases; only the called function's name/package changed.
- [x] T2.2 `internal/telemetry/dedupe_test.go`: repair
      `TestService_Location_FallsBackToTimeLocal` → renamed
      `TestService_Location_FallsBackToClockZone`, asserting `s.location() == clock.Zone()`
      (was `== time.Local`).
      Acceptance: matches design.md's Test Contract for `location()`'s repaired fallback.
      `TestService_Location_UsesConfiguredLocation` is left untouched — it was never testing
      the fallback branch.
- [x] T2.3 Repoint every `dateOnly(t, time.UTC)` fixture call site (identical arguments,
      identical expected values) to `clock.CalendarDay(t, time.UTC)` in:
      `db_integration_test.go`, `db_preceding_snapshot_integration_test.go`,
      `db_read_integration_test.go`, `db_sourcea_integration_test.go`,
      `db_tpms_integration_test.go`. Add the `internal/clock` import to each file that lacked
      it.
      Acceptance: `grep -rn dateOnly internal/telemetry/*.go` returns no match in any
      non-comment, non-historical-migration context; `go vet ./internal/telemetry/...`
      compiles every touched test file cleanly (this tier does not run the suite —
      `Test-Execution-Policy`).

## T3. Doc-comment sweep — depends on T1

- [x] T3.1 `internal/telemetry/telemetry.go`: correct `Config.Location`'s doc comment — states
      telemetry's own nil-fallback (`clock.Zone()`) accurately, and notes `internal/app`'s
      `Scheduler` still falls back to `time.Local` pending `RM35-app-adopt-clock` (tier 5) —
      without claiming `app` has changed.
      Acceptance: comment makes no claim about `app`'s current behavior beyond "not yet
      migrated"; accurately states telemetry's own new fallback.
- [x] T3.2 `internal/telemetry/telemetry.go`: correct the second stale `dateOnly` reference
      (in `SnapshotPrecedingDay`'s doc comment) to name `clock.CalendarDay`.
- [x] T3.3 `internal/telemetry/AGENTS.md`: correct the three stale `dateOnly` references (in
      the `SnapshotPrecedingDay` port description, the `vehicle_snapshots` data-ownership
      section, and the Testing Notes section) to name `clock.CalendarDay`; add `internal/clock`
      to the "Allowed / forbidden imports → May import" list.
      Acceptance: `grep -n dateOnly internal/telemetry/AGENTS.md` returns no match.
- [x] T3.4 `internal/telemetry/testdb_test.go`: correct the one stale `dateOnly` comment
      reference to name `clock.CalendarDay`.
- [x] T3.5 `internal/telemetry/db/query.sql`: correct the two stale `dateOnly` comment
      references (in `InsertVehicleSnapshot`'s and the preceding-snapshot query's doc
      comments) to name `clock.CalendarDay`. No query text, parameter, or return-type change.

## T4. Regenerate sqlc output — depends on T3.5

- [x] T4.1 Run `sqlc generate` (or `make sqlc`) to propagate T3.5's comment-only `query.sql`
      change into `internal/telemetry/db/query.sql.go`.
      Acceptance: `grep -n dateOnly internal/telemetry/db/query.sql.go` returns no match; no
      other module's generated output changes (comment-only source edit, deterministic
      codegen).

## T5. Root `README.md` — dependency graph + prose fix — no dependency on T1–T4

- [x] T5.1 §Architecture dependency graph: `telemetry`'s row gains `clock` —
      `telemetry ──────────► account, tesla, clock, telemetry/db`.
      Acceptance: `internal/clock` stays a LAYER 0 leaf (its own row is unchanged — it has no
      internal dependencies of its own); only `telemetry`'s outgoing edge changes.
- [x] T5.2 `internal/clock`'s prose row (§Architecture table) — extend the adoption list to
      name `telemetry` alongside `config` (tier 2's adoption note), distinguishing it from
      `analytics`/`app`/`gateway` (tiers 4–6, still pending).
      Acceptance: the row accurately states which tiers have landed as of this change (a
      review finding on tier 2 flagged this exact omission — not repeated here).

## T6. OpenSpec artifacts — produced alongside T1–T5 per this tier's dispatch

- [x] T6.1 `proposal.md` — header, Why, What Changes, reachability analysis for the nil-
      `Config.Location` path, breaking assessment, Capabilities, Impact.
- [x] T6.2 `design.md` — Context, Goals/Non-Goals, restated roadmap decisions + this tier's own
      (D-tel-1/2/3), Test Contract authored up front, Risks/Trade-offs, Migration Plan (none),
      Open Questions (none).
- [x] T6.3 `specs/telemetry/spec.md` — one ADDED requirement, GIVEN/WHEN/THEN, describing the
      default-time-zone behavior (previously unspecified) rather than restating
      implementation.
- [x] T6.4 This file (`tasks.md`), kept checked off live as work proceeds.

## T7. Verification — depends on T1–T5

- [x] T7.1 `go build ./...` passes repo-wide.
- [x] T7.2 `go vet ./...` passes repo-wide.
- [x] T7.3 `gofmt -l` reports no diff for every `internal/telemetry/*.go` file touched.
- [x] T7.4 Confirm no file outside `internal/telemetry/`, `README.md`, and this change's own
      OpenSpec artifacts was touched — no `cmd/`, no other module.
- [ ] T7.5 `openspec validate RM35-telemetry-adopt-clock --strict` passes (owner/leader step).
- [ ] T7.6 Owner runs the exact suite commands and reports the result, turning every repaired
      test from `awaiting-user-verification` into `done`:
      - `go test ./internal/telemetry/...`
      - `go test ./...` (full repo regression net — roadmap D4's "behavior-preserving except
        the documented default" claim for this tier)
