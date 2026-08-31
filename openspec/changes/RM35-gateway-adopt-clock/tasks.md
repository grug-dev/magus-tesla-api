> **Scope.** Adopts `internal/clock` in `internal/gateway` (roadmap `RM35-timezone-
> centralization`, tier 6 of 7, depends on tier 1, already archived): routes the
> `browserLocation`/`browserLocationFromHeader` no-cookie/malformed-cookie fallback through
> `clock.Zone()`, collapses `history.go`'s `startOfDay` into `clock.CalendarDay`, and routes four
> named `handlers.go` `time.Now()` call sites through `clock.Now()`. The `browser_tz` cookie's own
> win path, `startOfDayIn`, `supercharger.go`'s `startOfMonth`/`startOfDay(time.Now().UTC())` call
> sites, and every `cmd/` file are all UNCHANGED. No migration, no schema, no `cmd/` edit.
>
> **Dependencies / parallelism:**
> - T1 (`tz.go` — the 6 fallback-return swaps) has no dependency beyond tier 1 (`internal/clock`,
>   already on disk). Independent of T2/T3 (disjoint file).
> - T2 (`history.go` — `startOfDay`'s delegate-to-`clock.CalendarDay` body) has no dependency
>   beyond tier 1. Independent of T1/T3 (disjoint file).
> - T3 (`handlers.go` — the four `time.Now()` → `clock.Now()` swaps) has no dependency beyond
>   tier 1. Independent of T1/T2 (disjoint file).
> - T4 (repair `history_test.go` + `charges_test.go`) depends on T1 and T2 — it repairs the
>   fallback-default assertions T1/T2 change.
> - T5 (doc-comment sweep: `internal/gateway/AGENTS.md`) depends on T1 — it documents the
>   behavior T1 produces. Disjoint file from T4; MAY run in parallel with it.
> - T6 (OpenSpec artifacts: this file, `proposal.md`, `design.md`, `specs/gateway/spec.md`) —
>   produced alongside implementation per this tier's dispatch (artifacts + implementation
>   together).
> - T7 (verification) depends on T1–T5.
>
> **No leader-integrated step this tier** — no `sqlc generate`, no codegen re-run (unlike tier 3's
> `query.sql` comment fix); this tier touches only `.go` and doc files.

## T1. `internal/gateway/handlers/tz.go` — the 6 fallback-return swaps — depends on tier 1 (`internal/clock`, already archived)

- [x] T1.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
      Acceptance: `internal/clock` is a new import; no import cycle (`clock` imports nothing
      project-local — roadmap D2).
- [x] T1.2 `browserLocation`: all 3 `return time.UTC` sites (nil context/request, missing/empty
      cookie, unparseable IANA name) become `return clock.Zone()`. The cookie parse/win path
      (`c.Cookie`, `time.LoadLocation(cookie)`, `return loc`) is UNCHANGED.
      Acceptance: matches design.md D4/D-gw-4's roadmap restatement — a valid cookie still wins
      unconditionally; only the 3 fallback sites change.
- [x] T1.3 `browserLocationFromHeader`: the same 3-way fallback becomes `clock.Zone()`, mirroring
      T1.2 for the `*http.Request`-only test variant.
      Acceptance: `grep -n "time.UTC" internal/gateway/handlers/tz.go` returns matches only in
      doc comments describing the OLD behavior (historical), never in a `return` statement.
- [x] T1.4 Correct `browserTZCookieName`'s and `browserLocation`'s doc comments (state the
      `clock.Zone()` fallback, not `time.UTC`); correct `browserToday`'s doc comment to state the
      `clock.Zone()` fallback AND explicitly note its own `time.Now()` call is unchanged
      (design.md D-gw-3).
      Acceptance: no doc comment in `tz.go` states `time.UTC` as CURRENT fallback behavior.
- [x] T1.5 Leave `startOfDayIn` and its DST-limitation doc comment byte-for-byte unchanged except
      for one added cross-reference note explaining why it is NOT reimplemented on
      `clock.CalendarDay` (design.md D-gw-2).
      Acceptance: `startOfDayIn`'s function body is identical to before this tier.

## T2. `internal/gateway/handlers/history.go` — `startOfDay` delegates to `clock.CalendarDay` — depends on tier 1

- [x] T2.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock`.
- [x] T2.2 `startOfDay(t time.Time) time.Time`'s body becomes `return clock.CalendarDay(t,
      time.UTC)`, replacing the hand-rolled `t.UTC(); time.Date(...)` truncation.
      Acceptance: matches design.md D-gw-1 — algebraically identical output for every input;
      `supercharger.go:57`'s `startOfMonth` is untouched (different function, different file).
- [x] T2.3 Correct `startOfDay`'s doc comment to name `clock.CalendarDay` and state it is the
      gateway's ONE definition (cross-referencing `startOfMonth` as deliberately separate).

## T3. `internal/gateway/handlers/handlers.go` — 4 `time.Now()` → `clock.Now()` — depends on tier 1

- [x] T3.1 Import `github.com/cristianpena/magus-tesla-api/internal/clock` (alphabetically after
      `charging`, before `gateway/i18n`).
- [x] T3.2 `mapVehicles`: `isStale(snap.CapturedAt, time.Now())` → `isStale(snap.CapturedAt,
      clock.Now())`.
      Acceptance: matches design.md D-gw-4 point 1 — `isStale` consumes only `.Sub()`, unaffected
      by the `Location` swap.
- [x] T3.3 `dashboardFor`: `mapDashboardSnapshot(ctx, &vm, snap, time.Now())` →
      `mapDashboardSnapshot(ctx, &vm, snap, clock.Now())`.
      Acceptance: matches design.md D-gw-4 point 2.
- [x] T3.4 `navHeaderFor`: `now := time.Now()` → `now := clock.Now()`.
      Acceptance: matches design.md D-gw-4 point 3 — both `connectedAt` and `relativeLastSeen`
      consume `now` only via `.Sub()`.
- [x] T3.5 `TeslaCallback`: `AccessExpiresAt: time.Now().Add(...)` → `AccessExpiresAt:
      clock.Now().Add(...)`.
      Acceptance: matches design.md D-gw-4 point 4 — the value round-trips through
      `pgtype.Timestamptz`, discarding `Location`.
      Acceptance (all of T3): `grep -n "time.Now()" internal/gateway/handlers/handlers.go`
      returns no match.

## T4. Repair tests broken by the fallback-default change — depends on T1, T2

- [x] T4.1 `internal/gateway/handlers/history_test.go`: add the `internal/clock` import; repair
      `TestBrowserLocation_Fallbacks` (missing/malformed/empty-cookie assertions now expect
      `clock.Zone()`, not `time.UTC`; the valid-cookie assertion is untouched).
      Acceptance: matches design.md's Test Contract for `browserLocation`/
      `browserLocationFromHeader`.
- [x] T4.2 `internal/gateway/handlers/history_test.go`: rename
      `TestParseHistoryRange_NoCookieFallsBackToUTC` →
      `TestParseHistoryRange_NoCookieFallsBackToPlatformDefault`; repoint `wantEnd` at
      `startOfDayIn(time.Now(), clock.Zone())` and the `Location()` assertion at `clock.Zone()`.
      Acceptance: matches design.md's Test Contract.
- [x] T4.3 `internal/gateway/handlers/history_test.go`: repair
      `TestParseHistoryRange_BothAbsent_DefaultSixDayWindow`'s `wantEnd` computation to
      `startOfDayIn(time.Now(), clock.Zone()).AddDate(0, 0, -1)`.
      Acceptance: matches design.md's Test Contract; `wantStart`'s arithmetic is unchanged.
- [x] T4.4 `internal/gateway/handlers/charges_test.go`: add the `internal/clock` import; rename
      `todayUTCMidnight` → `todayDefaultZoneMidnight`, body becomes
      `startOfDayIn(time.Now(), clock.Zone())`; repoint both call sites
      (`TestChargeCreate_D2_...`, `TestChargeRowUpdate_D3_...`).
      Acceptance: matches design.md's Test Contract; `grep -n todayUTCMidnight
      internal/gateway/handlers/charges_test.go` returns no function reference (a historical
      comment mentioning the old name by way of explaining the rename is acceptable).
- [x] T4.5 `internal/gateway/handlers/charges_test.go`: repair `TestChargePage_DateDefaultsToToday`'s
      `todayDefault` computation to `time.Now().In(clock.Zone()).Format("2006-01-02") + "T00:00"`.
      Acceptance: matches design.md's Test Contract.
      Acceptance (all of T4): `go vet ./internal/gateway/...` compiles every touched test file
      cleanly (this tier does not run the suite — `Test-Execution-Policy`); no test in
      `supercharger_test.go` needed repair (verified — its `startOfDay(time.Now().UTC())` call
      sites pass an already-UTC argument, per design.md's Test Contract closing note).

## T5. Doc-comment sweep — `internal/gateway/AGENTS.md` — depends on T1

- [x] T5.1 Add an "Allowed / forbidden imports" section naming `internal/clock`, describing which
      call sites use it and restating that the `browser_tz` cookie still wins whenever present.
- [x] T5.2 Correct the RD9 (`browser_tz` cookie script) section's graceful-degradation paragraph:
      the JS-failure fallback is `clock.Zone()` (`America/Bogota`), not `time.UTC`.
      Acceptance: `grep -n "falls back to \`time.UTC\`" internal/gateway/AGENTS.md` returns no
      match.

## T6. OpenSpec artifacts — produced alongside T1–T5 per this tier's dispatch

- [x] T6.1 `proposal.md` — header, Why, What Changes, the ticket's "no UTC" scoping note,
      breaking assessment, Capabilities, Impact.
- [x] T6.2 `design.md` — Context (including this tier's out-of-scope findings), Goals/Non-Goals,
      restated roadmap decisions + this tier's own (D-gw-1 through D-gw-4), Test Contract
      authored up front, Risks/Trade-offs, Migration Plan (none), Open Questions (none).
- [x] T6.3 `specs/gateway/spec.md` — the "Dashboard History Charts" requirement restated in full
      (per this project's established MODIFIED-Requirements convention — the delta file replaces
      the whole requirement on sync) with the fallback-default wording and one scenario's example
      values updated from `time.UTC` to `clock.Zone()`/`America/Bogota`; every other sentence
      unchanged.
- [x] T6.4 This file (`tasks.md`), kept checked off live as work proceeds.

## T7. Verification — depends on T1–T5

- [x] T7.1 `go build ./...` passes repo-wide.
- [x] T7.2 `go vet ./...` passes repo-wide.
- [x] T7.3 `gofmt -l` reports no diff for every touched file.
- [x] T7.4 `make i18n-guard` and `make ui-guard` pass (no user-facing markup touched by this
      tier, but run as a cheap signal per dispatch instructions).
- [x] T7.5 Confirm no file outside `internal/gateway/`, this change's own OpenSpec artifacts, and
      no `README.md` edit — no `cmd/`, no other module, no migration.
- [x] T7.6 `openspec validate RM35-gateway-adopt-clock --strict` passes.
- [ ] T7.7 Owner runs the exact suite commands and reports the result, turning every repaired
      test from `awaiting-user-verification` into `done`:
      - `go test ./internal/gateway/...`
      - `go test ./...` (full repo regression net — roadmap D4's "behavior-preserving except the
        documented default" claim for this tier)
