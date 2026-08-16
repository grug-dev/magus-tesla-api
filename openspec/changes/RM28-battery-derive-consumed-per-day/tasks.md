> **Additive change to `internal/battery` only — read design.md before starting.** This
> adds one new `Reader` method (`ConsumedByDay`), one new domain type (`DayConsumption`),
> one new exported constant (`GapReconciliationWindow`), and one new file (`consumed.go`).
> `NewReader`'s signature does NOT change (design D-B1) — `ConsumedByDay` uses only the
> three ports the constructor already wires in. `RecentEfficiency`, `Efficiency`,
> `derive.go`, and `capacity.go` are untouched. **No database change** (design D-B10) — the
> `database` design gate does not apply.
>
> **T6 is LEADER-OWNED and out of this dispatch's sandbox** — `cmd/poller` is not a child
> of `internal/`, so no worker touches it. It is listed here only so the dependency chain
> and the exact contract (design.md's "`cmd/poller` wiring" section) are visible in one
> place.
>
> **Dependencies:**
>
> - T1 (`battery.go`: interface + type + const) has no dependencies.
> - T2 (`consumed.go`: pure derivation) depends on T1 (`DayConsumption` must exist).
> - T3 (`reader.go`: `ConsumedByDay` implementation) depends on T1, T2.
> - T4 (`consumed_test.go`: pure-function tests) depends on T2 only — **parallel-safe with
>   T3** (disjoint files, T4 never touches `reader.go`).
> - T5 (`reader_test.go`: port-wiring tests + un-panic the three `...Between` fakes)
>   depends on T3.
> - T7 (`AGENTS.md`) depends on T1 (interface must be final) — **parallel-safe with T2–T5**
>   (disjoint file).
> - T6 (`cmd/poller` wiring, LEADER-OWNED) depends on T1, T2, T3 (needs `ConsumedByDay` to
>   exist and compile) — run only after this module's Verification (V) passes.
> - Verification (V) depends on T1–T5, T7 (not T6 — a separate module's build).

---

## T1. `internal/battery/battery.go` — `Reader` interface, `DayConsumption` type, `GapReconciliationWindow` const — no dependencies

- [ ] T1.1 Add `GapReconciliationWindow = 30 * 24 * time.Hour` as an exported constant,
      with the doc comment from design.md's "Go-Level Surface" section (states: the
      rolling window `cmd/poller` re-derives and reconciles against `charge_gaps` every
      nightly run — D4/D4a/D7b — and why 30 days).
      Acceptance: constant compiles; `gofmt -l internal/battery/battery.go` reports no
      issues.
- [ ] T1.2 Add `ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64,
      start, end time.Time) ([]DayConsumption, error)` to the `Reader` interface, with the
      doc comment from design.md's "Go-Level Surface" section (states: D13 formula,
      recomputed-on-read/no-cache D2, sparse-result/absence-is-no-data D-B2, no
      window-size validation of its own).
      Acceptance: `go build ./...` fails (interface method has no implementation yet) —
      expected until T3 lands, mirrors tiers 1–2's own precedent.
- [ ] T1.3 Add the `DayConsumption` struct exactly as specified in design.md's "Go-Level
      Surface" section — fields `Date`, `ConsumedPct`, `DistanceKm`, `Flagged`,
      `MissingChargingType telemetry.MissingChargingType`, `DaysSpanned int` — with every
      field's doc comment from design.md.
      Acceptance: struct compiles; `battery.go` now imports `internal/telemetry` (already
      imported by `reader.go` for other types, but `battery.go` itself gains the import
      for `telemetry.MissingChargingType` on the exported type — confirm the import is
      added if not already present at file scope).

## T2. `internal/battery/consumed.go` (new file) — pure derivation — depends on T1

- [ ] T2.1 Create `internal/battery/consumed.go` with package `battery` and the file-level
      doc comment mirroring `derive.go`'s style (what this file owns: the D13 per-day
      derivation, fully offline).
- [ ] T2.2 Add `dayUTC(t time.Time) time.Time`, exactly as specified in design.md (UTC
      calendar-day truncation, mirrors the gateway's `effectiveDayUTC` — design D-B7).
- [ ] T2.3 Add `const minFlagDistanceKm = 10.0` with the doc comment from design.md D-B8 —
      never referenced as a bare literal anywhere else in this file or `reader.go`.
- [ ] T2.4 Add `sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from,
      to time.Time) float64`, exactly as specified in design.md D-B5: half-open `[from,
      to)` on `ChargeStopDateTime`, skips either-nil-percentage sessions (contributes 0).
- [ ] T2.5 Add `inferMissingChargingType(sessions []telemetry.SuperchargerSession, from,
      to time.Time) telemetry.MissingChargingType`, exactly as specified in design.md D7a/
      D-B5: `telemetry.MissingChargingTypeSupercharger` when any matched session has
      either percentage nil, else `telemetry.MissingChargingTypeManual`.
- [ ] T2.6 Add `sumManualPctBetween(entries []manualcharge.Entry, fromDay, toDay
      time.Time) float64`, exactly as specified in design.md D-B6: exclusive-start/
      inclusive-end date range on `dayUTC(e.ChargedOn)`, skips either-nil-percentage
      entries (contributes 0).
- [ ] T2.7 Add `deriveConsumedByDay(snapshots []telemetry.Snapshot, sessions
      []telemetry.SuperchargerSession, entries []manualcharge.Entry, start, end
      time.Time) []DayConsumption`, exactly as specified in design.md's "Go-Level
      Surface" section: pairwise iteration from `i=1`, D5a skip on nil
      `BatteryUsedPctCalc`, D13 formula, D5 flag rule using `minFlagDistanceKm`, D7a
      inference only when flagged, `DaysSpanned` from `DaysSpannedCalc` (default 1 when
      nil — though design.md notes this is unreachable once `BatteryUsedPctCalc` is
      non-nil, defend anyway), range filter against `[start, end]` via `dayUTC`.
      Acceptance (all of T2): `go build ./...` succeeds for this file in isolation once
      T1 has landed (the file has no dependency on `reader.go`); `gofmt -l
      internal/battery/consumed.go` reports no issues.

## T3. `internal/battery/reader.go` — `ConsumedByDay` implementation — depends on T1, T2

- [ ] T3.1 Add `(*reader).ConsumedByDay`, exactly as specified in design.md's "Go-Level
      Surface" section: `lookbackStart := start.AddDate(0, 0, -1)` (D9a);
      `r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart,
      end)`; `r.supercharger.SuperchargerSessionsByVehicleBetween(ctx, accountID,
      teslaID, lookbackStart, end.AddDate(0, 0, 1))` (D-B5's one-day tail over-fetch);
      `r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, start, end)`; return
      `deriveConsumedByDay(snapshots, sessions, entries, start, end)`. Every port error
      returned unwrapped (matching `RecentEfficiency`'s existing error-propagation
      convention in the same file).
      Acceptance: `var _ Reader = (*reader)(nil)` (already present in `reader.go`)
      compiles; `go build ./...` succeeds; `go vet ./...` reports no issues.

## T4. `internal/battery/consumed_test.go` (new file) — pure-function test contract — depends on T2 (parallel-safe with T3)

- [ ] T4.1 `TestDeriveConsumedByDay_SingleSessionSingleDay_MatchesRoadmapExample` —
      design.md Test Contract (a): snapshots 22→73, one session 18→80, expect exactly one
      entry, `ConsumedPct = 11`, `Flagged = false`.
- [ ] T4.2 `TestDeriveConsumedByDay_TwoSessionsSameDay_SumsBoth_Not5` (name reflects the
      regression guard) — design.md Test Contract (b): snapshots 30→75, sessions 20→50 and
      60→80, expect `ConsumedPct = 5`; explicitly assert it is neither −25 (latest-only)
      nor 15 (first-to-last).
- [ ] T4.3 `TestDeriveConsumedByDay_NegativeFlagged` — design.md Test Contract (c):
      `BatteryUsedPctCalc = -10`, no charge events, expect `Flagged = true`,
      `MissingChargingType = MissingChargingTypeManual`.
- [ ] T4.4 `TestDeriveConsumedByDay_ZeroWithDistanceFlagged` — design.md Test Contract
      (d): `ConsumedPct` computes to 0, `DistanceKm = 50.0`, expect `Flagged = true`.
- [ ] T4.5 `TestDeriveConsumedByDay_ZeroWithLowDistanceNotFlagged` — design.md Test
      Contract (e): two subtests, `DistanceKm = 10.0` (boundary, not flagged — `>` is
      strict) and `DistanceKm = 5.0` (not flagged).
- [ ] T4.6 `TestDeriveConsumedByDay_NilBatteryUsedPctCalc_Skipped` — design.md Test
      Contract (f): `cur.BatteryUsedPctCalc = nil`, expect an empty returned slice (not an
      entry with a zero/flagged value).
- [ ] T4.7 `TestDeriveConsumedByDay_MultiDaySpan_OneEntry` — design.md Test Contract (g):
      3-day-apart snapshots, two charge events inside the span, expect exactly one entry
      dated the later snapshot's day, `DaysSpanned = 3`, `ConsumedPct` summing both
      charges.
- [ ] T4.8 `TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd` —
      design.md Test Contract (h): session at exactly `from` is included, session at
      exactly `to` is excluded. Also exercises `inferMissingChargingType`'s identical
      boundary via a companion assertion or subtest.
- [ ] T4.9 `TestDeriveConsumedByDay_Stateless_ResolvesOnRecompute` — design.md Test
      Contract (i): call `deriveConsumedByDay` once with no charge data (flagged), call it
      again with the same snapshots plus a resolving charge entry, assert the second
      call's `Flagged = false` — proves no memory between calls (D2), the precondition
      `cmd/poller`'s reconciliation depends on.
      Acceptance (all of T4): all tests compile and are pure (no fakes, no I/O — mirrors
      `derive_test.go`'s own zero-fakes style); `go vet ./...` passes (compiles
      `_test.go` files). **Per the Test-Execution-Policy, these tests are written but NOT
      run by the worker — status is `awaiting-user-verification`, not `done`, until the
      owner runs `go test ./internal/battery/...` (or `make test`) and reports the
      result.**

## T5. `internal/battery/reader_test.go` (extended) — port-wiring tests + un-panic three fakes — depends on T3

- [ ] T5.1 Change `fakeTelemetryReader.SnapshotsByVehicleBetween` from a panicking stub to
      a functional one: record `accountID`/`teslaID`/`start`/`end` on new fields, return
      `(f.snapshots, f.err)` when set (reuse or add a dedicated fixture field so
      `RecentEfficiency`'s own existing tests, which populate `f.snapshots` for the
      `...Since` path, are unaffected — confirm no existing test relies on
      `SnapshotsByVehicleBetween` panicking).
- [ ] T5.2 Change `fakeSuperchargerReader.SuperchargerSessionsByVehicleBetween` from a
      panicking stub to a functional one, same shape as T5.1.
- [ ] T5.3 Change `fakeManualReader.ListEntriesByVehicleBetween` from a panicking stub to
      a functional one, same shape as T5.1.
      Acceptance (T5.1–T5.3): every existing `RecentEfficiency` test in this file still
      compiles and passes unmodified (none of them exercise the three `...Between`
      methods, per design.md's own note) — a diff of this file shows no existing test
      function's body changed, only the three fake method bodies.
- [ ] T5.4 `TestConsumedByDay_FetchWindows` — design.md Test Contract (j): assert
      `SnapshotsByVehicleBetween` is called with `(start-1, end)`,
      `SuperchargerSessionsByVehicleBetween` with `(start-1, end+1)`, and
      `ListEntriesByVehicleBetween` with `(start, end)`, for a fixed `start`/`end` pair.
- [ ] T5.5 `TestConsumedByDay_AccountIDScoping_PassedToEveryPort` — design.md Test
      Contract (k): mirrors `TestRecentEfficiency_AccountIDScoping_PassedToEveryPort`'s
      existing pattern for the three ports `ConsumedByDay` calls.
- [ ] T5.6 `TestConsumedByDay_TelemetryError_Propagates`,
      `TestConsumedByDay_SuperchargerError_Propagates`,
      `TestConsumedByDay_ManualChargeError_Propagates` — design.md Test Contract (l):
      mirrors the existing `TestRecentEfficiency_*Error_Propagates` pattern.
      Acceptance (T5.4–T5.6): all compile (`go vet ./...` passes); **written but NOT run
      by the worker**, same Test-Execution-Policy note as T4.

## T6. `cmd/poller` wiring — LEADER-OWNED, NOT this module's sandbox — depends on T1, T2, T3

- [ ] T6.1 Construct `telemetry.NewSuperchargerReader(pool)`, `manualcharge.NewReader(pool)`,
      `telemetry.NewGapWriter(pool)`, and `battery.NewReader(telemetry.NewReader(pool),
      superchargerReader, manualReader, acct, battery.DefaultWindow)` in
      `cmd/poller/main.go`, alongside the existing `telemetry.Collector` wiring.
- [ ] T6.2 After `collector.CollectAll(ctx)` succeeds (both the `--once` path and each
      scheduled nightly cycle), loop `acct.AllRegisteredVehicles(ctx)` and for each
      vehicle: call `batteryReader.ConsumedByDay` for
      `[yesterday-GapReconciliationWindow+1, yesterday]`, build `[]telemetry.ChargeGap`
      from the flagged entries, call `gapWriter.ReconcileWindow`. Exact code in design.md's
      "`cmd/poller` wiring" section — implement verbatim.
      Acceptance: per-vehicle error isolation (one vehicle's failure does not abort
      another's reconciliation, mirroring `CollectAll`'s own documented isolation); a
      failure to enumerate vehicles is a whole-cycle failure, logged, not fatal to the
      scheduler; `go build ./...` succeeds for `cmd/poller`.

## T7. `internal/battery/AGENTS.md` — Public Interface documentation — depends on T1

- [ ] T7.1 Update the "Public interface (the port)" section to include `ConsumedByDay`'s
      signature and a short description (mirrors the existing `RecentEfficiency` bullet's
      style), plus a new bullet for `DayConsumption` (mirrors the existing `Efficiency`
      bullet's style) and one for `GapReconciliationWindow` alongside the existing
      `DefaultWindow` bullet.
      Acceptance: the interface code shown in `AGENTS.md` and the real `Reader` interface
      in `battery.go` never drift — a diff of the two shows identical method signatures.

## Verification — depends on T1–T5, T7 (not T6 — a separate module's build)

- [ ] V.1 `go build ./...` succeeds. — leader-run, exit 0, no output.
- [ ] V.2 `go vet ./...` succeeds (compiles all `_test.go` files, including T4's and T5's
      new tests, catching any signature drift). — leader-run, exit 0 repo-wide.
- [ ] V.3 `gofmt -l` reports no files needing formatting across every file touched by this
      change. — leader-run.
- [ ] V.4 `openspec validate RM28-battery-derive-consumed-per-day --strict` passes. —
      leader-run.
- [ ] V.5 Hand off to the owner: exact command to run and report on —
      `go test ./internal/battery/...` (or `make test` / `make test-with-db` for the full
      suite). Not run by the worker per the Test-Execution-Policy.
