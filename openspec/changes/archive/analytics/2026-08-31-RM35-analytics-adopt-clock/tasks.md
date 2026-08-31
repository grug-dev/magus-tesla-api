> **Scope.** Adopts `internal/clock` in `internal/analytics` (roadmap `RM35-timezone-
> centralization`, tier 4 of 7, depends on tier 1, already archived): deletes the module's own
> `calendarDay` in favor of `clock.CalendarDay(t, time.UTC)` at its two call sites, and routes
> `Reconcile`'s bare `time.Now()` through `clock.Now()` while deliberately keeping the bucketing
> zone at `time.UTC` (design.md D-ana-1 — the central decision this tier makes). No migration,
> no schema, no `cmd/` edit. This tier is fully behavior-preserving (design.md).
>
> **Dependencies / parallelism:**
> - T1 (`consumed.go` — the two `calendarDay` call-site swaps + deletion) has no dependency
>   beyond tier 1 (`internal/clock`, already on disk). Must land before T3.
> - T2 (`recalculate.go` — `Reconcile`'s instant-source swap, `loc` unchanged) has the same
>   dependency as T1 and is independent of T1 (disjoint files). Must land before T3.
> - T3 (repair `consumed_test.go`, `recalculate_test.go`, `db_integration_test.go`) depends on
>   T1 and T2 — it repairs the compile break `calendarDay`'s deletion causes and corrects stale
>   doc-comment references.
> - T4 (doc-comment sweep: `internal/analytics/AGENTS.md`) depends on T1/T2 — it documents the
>   final behavior they produce. **NOT yet done — see T4.1 below.**
> - T5 (root `README.md` dependency-graph edit) is leader-owned, applied together with tiers 5/6
>   — not made by this tier's worker. Already present on disk as of this artifact pass.
> - T6 (OpenSpec artifacts: this file, `proposal.md`, `design.md`, `specs/analytics/spec.md`) —
>   produced alongside implementation per this tier's dispatch.
> - T7 (verification) depends on T1-T4.
>
> **Leader-integrated step:** none — this tier has no codegen re-run (no `sqlc generate`, no
> migration).

## T1. `internal/analytics/consumed.go` — the two `calendarDay` call-site swaps — depends on tier 1 (`internal/clock`, already archived)

- [x] T1.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
      Acceptance: `internal/clock` is a new import; no import cycle (`clock` imports nothing
      project-local — roadmap D2). Verified: `consumed.go` imports it at line 19.
- [x] T1.2 `effectiveDay(s telemetry.Snapshot) time.Time`: body changes from
      `calendarDay(s.CapturedDate).AddDate(0, 0, -1)` to
      `clock.CalendarDay(s.CapturedDate, time.UTC).AddDate(0, 0, -1)`.
      Acceptance: matches design.md — algebraically identical output for every input (tier-1
      design D7's reproduction guarantee). Verified at `consumed.go:100-102`.
- [x] T1.3 `sumManualPctBetween`: the per-entry `day := calendarDay(e.ChargedOn)` becomes
      `day := clock.CalendarDay(e.ChargedOn, time.UTC)`.
      Acceptance: identical output for every input. Verified at `consumed.go:151`.
- [x] T1.4 Delete `calendarDay(t time.Time) time.Time` entirely.
      Acceptance: `calendarDay` no longer exists anywhere in `consumed.go`; `grep -rn
      calendarDay internal/analytics/` returns no match anywhere in the module. Verified.
- [x] T1.5 Correct `effectiveDay`'s doc comment (previously referenced `calendarDay` by name;
      now explains why `clock.CalendarDay` is called with `time.UTC` rather than `clock.Zone()`
      — the zone/representation distinction this tier's design documents) and
      `sumManualPctBetween`'s doc comment likewise.
      Acceptance: no comment in `consumed.go` names the deleted `calendarDay` function.
      Verified at `consumed.go:69-102` and `consumed.go:139-160`.

## T2. `internal/analytics/recalculate.go` — `Reconcile`'s instant-source swap — depends on tier 1

- [x] T2.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock` (shared with T1;
      already present once per file).
      Acceptance: verified at `recalculate.go:23`.
- [x] T2.2 `Reconcile`'s `yesterday := calendarDay(time.Now()).AddDate(0, 0, -1)` becomes
      `yesterday := clock.CalendarDay(clock.Now(), time.UTC).AddDate(0, 0, -1)` — instant
      source swapped, bucketing zone deliberately kept at `time.UTC` (design.md D-ana-1, the
      central decision).
      Acceptance: matches design.md D-ana-1 — provable no-op for every input, because
      `CalendarDay`'s `loc` parameter re-converts unconditionally and `loc` is unchanged.
      Verified at `recalculate.go:277`. A doc comment immediately above the line (lines
      269-276) records the reasoning and points at design.md.
- [x] T2.3 The three `widen(calendarDay(...))` calls inside `Reconcile` (over
      `s.EffectiveDate`, `s.ChargeStopDateTime`, `e.ChargedOn`) become
      `widen(clock.CalendarDay(..., time.UTC))` — mechanical, algebraically identical swap.
      Acceptance: identical output for every input. Verified at `recalculate.go:291,298,305`.

## T3. Repair tests broken by `calendarDay`'s deletion — depends on T1, T2

- [x] T3.1 `internal/analytics/consumed_test.go`: no call-site change required (this file never
      called `calendarDay` directly — it exercises `deriveVehicleMetrics` end to end). No stale
      `calendarDay` reference remains.
      Acceptance: `grep -n calendarDay internal/analytics/consumed_test.go` returns no match.
      Verified.
- [x] T3.2 `internal/analytics/recalculate_test.go`: the `wantChargeStart` fixture's inline doc
      comment corrected to read `effectiveDay(2026-08-01) = clock.CalendarDay(2026-08-01,
      time.UTC) - 1` (was: naming `calendarDay`). Expected value (`day(2026, 7, 31)`)
      unchanged.
      Acceptance: matches design.md's Test Contract — same expected value, comment names the
      function that now performs the computation. Verified at `recalculate_test.go:121`.
- [x] T3.3 `internal/analytics/db_integration_test.go`: repoint the three direct
      `calendarDay(t)` fixture call sites (all computing a fixture's "N days before today"
      anchor) to `clock.CalendarDay(t, time.UTC)`, identical arguments and identical expected
      values. Add the `internal/clock` import.
      Acceptance: matches design.md's Test Contract; `grep -rn calendarDay
      internal/analytics/*.go` returns no match anywhere in the module (only `clock.CalendarDay`
      remains); `go vet ./internal/analytics/...` compiles every touched test file cleanly (this
      tier does not run the suite — Test-Execution-Policy). Verified: three call sites at
      `db_integration_test.go:938,999,1089`, plus a stale-reference-corrected comment at line
      1248; `internal/clock` imported at line 66.

## T4. Doc-comment sweep — `internal/analytics/AGENTS.md` — depends on T1, T2

- [x] T4.1 Add `internal/clock` to `AGENTS.md`'s "Allowed / forbidden imports → May import"
      list, and correct the `effectiveDay`/`ConsumedByDay` port description(s) to name
      `clock.CalendarDay` instead of the deleted `calendarDay`.
      **NOT DONE.** Verified by direct read: `grep -n "clock" internal/analytics/AGENTS.md`
      returns only an unrelated line ("no injectable clock" — RM29 D13 prose, not an import).
      `internal/clock` does not appear in the "May import" list (`AGENTS.md:171-187`, which
      lists `internal/telemetry`, `internal/charging`, `internal/account`,
      `github.com/google/uuid`, and stdlib `context`/`time` only). No `calendarDay` reference
      remains to correct (the port descriptions never named the function by name), so only the
      import-list addition is outstanding. This is a genuine gap between this tier's own
      `proposal.md` (which states in "What Changes" and "Impact" that this edit was made) and
      the file on disk — out of this dispatch's sandbox (`internal/analytics/AGENTS.md` is
      inside the module and was in scope for the implementation wave, but this artifact-only
      dispatch was directed to touch no file outside
      `openspec/changes/RM35-analytics-adopt-clock/`). Flagged for the leader to either
      re-dispatch as a one-line follow-up task or correct directly.

## T5. Root `README.md` — dependency graph edit — leader-owned, no dependency on T1-T4

- [x] T5.1 §Architecture dependency graph: `analytics`'s row gains `clock` —
      `analytics ──────────► account, charging, telemetry, clock`.
      Acceptance: verified at `README.md:232`.
- [x] T5.2 `internal/clock`'s prose row (§Architecture table) — extends the adoption list to
      name `analytics` alongside `config`/`telemetry`.
      Acceptance: verified at `README.md:205` — "Adopted by `config`, `telemetry`, `analytics`
      and `app` (`RM35` tiers 2-5)".

## T6. OpenSpec artifacts — produced alongside T1-T3

- [x] T6.1 `proposal.md` — header, Why, What Changes, the central decision, breaking assessment,
      Capabilities, Impact. Present on disk.
- [x] T6.2 `design.md` — Context, Goals/Non-Goals, No database change, restated roadmap
      decisions + this tier's own (D-ana-1/D-ana-2), full behavior-preservation argument, Test
      Contract authored to match the implemented tests' actual expected values, Risks/
      Trade-offs, Migration Plan (none), Open Questions (none).
- [x] T6.3 `specs/analytics/spec.md` — two ADDED requirements (the `Reconcile` UTC-cutoff
      behavior, and the module's no-time-zone-configuration invariant), each with `SHALL` on the
      first line of its body and at least one `#### Scenario:` block, per `openspec/config.yaml`
      GIVEN/WHEN/THEN rule.
- [x] T6.4 This file (`tasks.md`), documenting work already complete — genuinely-done boxes
      ticked, T4.1 (the AGENTS.md gap) and every T7 owner/leader step left unticked.

## T7. Verification — depends on T1-T4

- [x] T7.1 `go build ./...` passes repo-wide (run this pass, exit 0).
- [x] T7.2 `go vet ./internal/analytics/...` passes (run this pass, exit 0; repo-wide `go vet
      ./...` was not re-run in this artifact-only pass but was clean as of the implementation
      wave per the leader's own note that `go test ./internal/analytics/...` already reported
      ok).
- [x] T7.3 `gofmt -l internal/analytics/*.go` reports no diff.
- [x] T7.4 Confirm no file outside `internal/analytics/`, `README.md` (leader-owned, T5), and
      this change's own OpenSpec artifacts was touched by the implementation wave.
- [x] T4.1 above (`AGENTS.md`) — DONE by the leader: `internal/clock` added to the May-import list, with a note that the D-B12 no-`*time.Location` invariant still holds because every bucketing call passes `time.UTC` explicitly.
- [x] T7.5 `openspec validate RM35-analytics-adopt-clock --strict` passes. Verified in this
      artifact pass: `openspec validate RM35-analytics-adopt-clock --strict` → "Change
      'RM35-analytics-adopt-clock' is valid".
- [ ] T7.6 Owner runs the exact suite commands and reports the result, turning every repaired
      test from `awaiting-user-verification` into `done` (already reported green once by the
      leader for `go test ./internal/analytics/...` prior to this artifact pass — re-run only if
      further changes land, e.g. T4.1's follow-up):
      - `go test ./internal/analytics/...`
      - `go test ./...` (full repo regression net — roadmap D4's "behavior-preserving" claim for
        this tier)
