> **Additive change to `internal/battery` only — read design.md before starting.** This
> adds one new `Reader` method (`ConsumedByDay`), one new domain type (`DayConsumption`),
> one new exported constant (`GapReconciliationWindow`), and one new file (`consumed.go`).
> `NewReader`'s signature does NOT change (design D-B1) — `ConsumedByDay` uses only the
> three ports the constructor already wires in. `RecentEfficiency`, `Efficiency`,
> `derive.go`, and `capacity.go` are untouched. **No database change** (design D-B10) — the
> `database` design gate does not apply.
>
> **T0 (done) confirmed `NewReader`'s signature stays unchanged.** The owner's D18 zone
> ruling is satisfied without injecting a `*time.Location`: the bucketing zone arrives
> stamped on the row, as `telemetry.Snapshot.CapturedDate` (design D-B12). Every implementer
> below: the bucket day is `effectiveDay(s) = s.CapturedDate − 1 day`, and
> `telemetry.Snapshot.EffectiveDate` is **not read anywhere in this module**. Test fixtures
> must set `CapturedDate`; leave `EffectiveDate` zero except in T4.10.
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

## T0. DESIGN REVISION — bucket in `Config.Location`, not UTC — BLOCKS T1–T7

Appended by the leader after the owner ruled on the two paused design questions.

- **Roadmap D17** confirms design **D-B3** exactly as written — `DayConsumption.Date` is the
  row's own `EffectiveDate`, which for a multi-day span is the span's **last** calendar day.
  No change needed.
- **Roadmap D18 OVERRULES design D-B7.** Roadmap **D6 stands literally**: calendar-day
  bucketing uses the poller's configured zone (`Config.Location`, `America/Bogota`), not
  UTC. The owner made this call with the consequence stated — `internal/battery` will bucket
  in UTC−5 while the gateway's existing odometer/battery charts still bucket in UTC via
  `effectiveDayUTC`. That mismatch is accepted for this tier and inherited by tier 4.

- [x] T0.1 Rewrite design.md's **D-B7** to record D18: `Config.Location` is the bucketing
      zone. Keep the superseded UTC reasoning visible as a rejected alternative with the
      owner's override noted — do not delete it (decisions are append-only in spirit).
      → D-B7 rewritten; the superseded reasoning is preserved verbatim as "Rejected
      alternative 1", block-quoted, with the owner's override and the reason it fell.
- [x] T0.2 Resolve, in design.md, **where `internal/battery` obtains its `*time.Location`**.
      The module reads no config today. State the mechanism (constructor injection via
      `NewReader`, a `ConsumedByDay` parameter, or another option), with the trade-off, and
      note that both composition roots (`cmd/poller`, `cmd/web`) must supply it.
      → New decision **D-B12**: the module needs NO `*time.Location`. The zone reaches it
      stamped on the row, as `telemetry.Snapshot.CapturedDate` (telemetry computes it in
      `Config.Location` on the write path). Injection is documented as the rejected
      alternative. **`NewReader`'s signature is unchanged — D-B1 survives.** Each
      composition root still resolves a zone to choose the `[start, end]` bounds it asks
      for (`cmd/poller`: `loc` from `POLLER_TIMEZONE`, already loaded at `main.go:63`;
      `cmd/web`: `browserToday(c)`), which is where T0.2's "both roots must supply it"
      lands.
- [x] T0.3 Resolve what zone-aware bucketing **means** given `telemetry.Snapshot.EffectiveDate`
      is derived as a UTC `CapturedAt − 1 day`. A bare date cannot be zone-converted, so state
      explicitly whether the day is re-derived from `CapturedAt` in `Config.Location` or
      `EffectiveDate` is used as-is, and why. This is the substantive part of T0 — get it
      wrong and the formula breaks exactly at the edges D6 exists to protect.
      → **The day is re-derived; `EffectiveDate` is not read by this module at all.**
      `effectiveDay(s) = s.CapturedDate − 1 day`. See D-B7 §"T0.3 answered explicitly" for
      the worked edge cases (four scenarios with arithmetic) and §"Rejected alternative 2"
      for why re-zoning `EffectiveDate` is not even implementable.
- [x] T0.4 Update every affected section — `dayUTC` helper, D-B5/D-B6 matching rules,
      the Go-Level Surface signatures, and the Test Contract's expected values wherever a
      zone shift changes them.
      → `dayUTC` replaced by `calendarDay` + `effectiveDay`; D-B3, D-B4, D-B5, D-B6 updated;
      new **D-B13** derives the widened fetch windows; `Reader.ConsumedByDay` and
      `DayConsumption.Date` doc comments, the `cmd/poller` wiring snippet, the tier-4
      rendering note, the Migration Plan and the Risks section all updated. Test Contract:
      new binding fixture convention (set `CapturedDate`, leave `EffectiveDate` zero),
      scenarios (a)/(g) restated, (j)'s three expected windows changed, and two new
      scenarios (m)/(n) added — see the appended T4.10/T4.11 below.
- [x] T0.5 Re-run `openspec validate RM28-battery-derive-consumed-per-day --strict`.
      Acceptance: design.md carries D18's zone rule, names the `*time.Location` source, and
      answers T0.3 explicitly; `openspec validate --strict` passes.

## T1. `internal/battery/battery.go` — `Reader` interface, `DayConsumption` type, `GapReconciliationWindow` const — depends on T0

- [x] T1.1 Add `GapReconciliationWindow = 30 * 24 * time.Hour` as an exported constant,
      with the doc comment from design.md's "Go-Level Surface" section (states: the
      rolling window `cmd/poller` re-derives and reconciles against `charge_gaps` every
      nightly run — D4/D4a/D7b — and why 30 days).
      Acceptance: constant compiles; `gofmt -l internal/battery/battery.go` reports no
      issues.
- [x] T1.2 Add `ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64,
      start, end time.Time) ([]DayConsumption, error)` to the `Reader` interface, with the
      doc comment from design.md's "Go-Level Surface" section (states: D13 formula,
      recomputed-on-read/no-cache D2, sparse-result/absence-is-no-data D-B2, no
      window-size validation of its own).
      Acceptance: `go build ./...` fails (interface method has no implementation yet) —
      expected until T3 lands, mirrors tiers 1–2's own precedent.
- [x] T1.3 Add the `DayConsumption` struct exactly as specified in design.md's "Go-Level
      Surface" section — fields `Date`, `ConsumedPct`, `DistanceKm`, `Flagged`,
      `MissingChargingType telemetry.MissingChargingType`, `DaysSpanned int` — with every
      field's doc comment from design.md.
      Acceptance: struct compiles; `battery.go` now imports `internal/telemetry` (already
      imported by `reader.go` for other types, but `battery.go` itself gains the import
      for `telemetry.MissingChargingType` on the exported type — confirm the import is
      added if not already present at file scope).

## T2. `internal/battery/consumed.go` (new file) — pure derivation — depends on T1

- [x] T2.1 Create `internal/battery/consumed.go` with package `battery` and the file-level
      doc comment mirroring `derive.go`'s style (what this file owns: the D13 per-day
      derivation, fully offline).
- [x] T2.2 Add `calendarDay(t time.Time) time.Time` and `effectiveDay(s telemetry.Snapshot)
      time.Time`, exactly as specified in design.md D-B7/D-B12. **These REPLACE the `dayUTC`
      helper this task originally specified** — the owner's D18 ruling (T0) moved bucketing
      off UTC. `effectiveDay(s) = calendarDay(s.CapturedDate).AddDate(0, 0, -1)`;
      `s.EffectiveDate` is NOT read anywhere in this module. Carry both doc comments from
      design.md verbatim — `calendarDay`'s in particular must state that it is a
      representation normalizer, not a timezone conversion.
- [x] T2.3 Add `const minFlagDistanceKm = 10.0` with the doc comment from design.md D-B8 —
      never referenced as a bare literal anywhere else in this file or `reader.go`.
- [x] T2.4 Add `sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from,
      to time.Time) float64`, exactly as specified in design.md D-B5: half-open `[from,
      to)` on `ChargeStopDateTime`, skips either-nil-percentage sessions (contributes 0).
- [x] T2.5 Add `inferMissingChargingType(sessions []telemetry.SuperchargerSession, from,
      to time.Time) telemetry.MissingChargingType`, exactly as specified in design.md D7a/
      D-B5: `telemetry.MissingChargingTypeSupercharger` when any matched session has
      either percentage nil, else `telemetry.MissingChargingTypeManual`.
- [x] T2.6 Add `sumManualPctBetween(entries []manualcharge.Entry, fromDay, toDay
      time.Time) float64`, exactly as specified in design.md D-B6: exclusive-start/
      inclusive-end date range on `calendarDay(e.ChargedOn)` (was `dayUTC` before T0), skips
      either-nil-percentage entries (contributes 0). `fromDay`/`toDay` are the caller's
      zoned `effectiveDay` values.
- [x] T2.7 Add `deriveConsumedByDay(snapshots []telemetry.Snapshot, sessions
      []telemetry.SuperchargerSession, entries []manualcharge.Entry, start, end
      time.Time) []DayConsumption`, exactly as specified in design.md's "Go-Level
      Surface" section: pairwise iteration from `i=1`, D5a skip on nil
      `BatteryUsedPctCalc`, D13 formula, D5 flag rule using `minFlagDistanceKm`, D7a
      inference only when flagged, `DaysSpanned` from `DaysSpannedCalc` (default 1 when
      nil — though design.md notes this is unreachable once `BatteryUsedPctCalc` is
      non-nil, defend anyway), range filter against `[start, end]` via `effectiveDay` (was
      `dayUTC(cur.EffectiveDate)` before T0). The emitted `Date` and the manual-matching
      lower bound both come from `effectiveDay`, never from `EffectiveDate`.
      Acceptance (all of T2): `go build ./...` succeeds for this file in isolation once
      T1 has landed (the file has no dependency on `reader.go`); `gofmt -l
      internal/battery/consumed.go` reports no issues.

## T3. `internal/battery/reader.go` — `ConsumedByDay` implementation — depends on T1, T2

- [x] T3.1 Add `(*reader).ConsumedByDay`, exactly as specified in design.md's "Go-Level
      Surface" section. **All three fetch windows CHANGED in T0** (design D-B13 — the zone
      shift means a UTC-windowed fetch would drop rows the zoned bucketing needs):
      `lookbackStart := start.AddDate(0, 0, -1)` (D9a);
      `r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart,
      end.AddDate(0, 0, 1))`; `r.supercharger.SuperchargerSessionsByVehicleBetween(ctx,
      accountID, teslaID, lookbackStart, end.AddDate(0, 0, 2))`;
      `r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end)`; return
      `deriveConsumedByDay(snapshots, sessions, entries, start, end)`. Every port error
      returned unwrapped (matching `RecentEfficiency`'s existing error-propagation
      convention in the same file).
      Acceptance: `var _ Reader = (*reader)(nil)` (already present in `reader.go`)
      compiles; `go build ./...` succeeds; `go vet ./...` reports no issues.

## T4. `internal/battery/consumed_test.go` (new file) — pure-function test contract — depends on T2 (parallel-safe with T3)

- [x] T4.1 `TestDeriveConsumedByDay_SingleSessionSingleDay_MatchesRoadmapExample` —
      design.md Test Contract (a): snapshots 22→73, one session 18→80, expect exactly one
      entry, `ConsumedPct = 11`, `Flagged = false`.
- [x] T4.2 `TestDeriveConsumedByDay_TwoSessionsSameDay_SumsBoth_Not5` (name reflects the
      regression guard) — design.md Test Contract (b): snapshots 30→75, sessions 20→50 and
      60→80, expect `ConsumedPct = 5`; explicitly assert it is neither −25 (latest-only)
      nor 15 (first-to-last).
- [x] T4.3 `TestDeriveConsumedByDay_NegativeFlagged` — design.md Test Contract (c):
      `BatteryUsedPctCalc = -10`, no charge events, expect `Flagged = true`,
      `MissingChargingType = MissingChargingTypeManual`.
- [x] T4.4 `TestDeriveConsumedByDay_ZeroWithDistanceFlagged` — design.md Test Contract
      (d): `ConsumedPct` computes to 0, `DistanceKm = 50.0`, expect `Flagged = true`.
- [x] T4.5 `TestDeriveConsumedByDay_ZeroWithLowDistanceNotFlagged` — design.md Test
      Contract (e): two subtests, `DistanceKm = 10.0` (boundary, not flagged — `>` is
      strict) and `DistanceKm = 5.0` (not flagged).
- [x] T4.6 `TestDeriveConsumedByDay_NilBatteryUsedPctCalc_Skipped` — design.md Test
      Contract (f): `cur.BatteryUsedPctCalc = nil`, expect an empty returned slice (not an
      entry with a zero/flagged value).
- [x] T4.7 `TestDeriveConsumedByDay_MultiDaySpan_OneEntry` — design.md Test Contract (g):
      3-day-apart snapshots, two charge events inside the span, expect exactly one entry
      dated the later snapshot's day, `DaysSpanned = 3`, `ConsumedPct` summing both
      charges.
- [x] T4.8 `TestSumSuperchargerPctBetween_IntervalBoundary_InclusiveStartExclusiveEnd` —
      design.md Test Contract (h): session at exactly `from` is included, session at
      exactly `to` is excluded. Also exercises `inferMissingChargingType`'s identical
      boundary via a companion assertion or subtest.
- [x] T4.9 `TestDeriveConsumedByDay_Stateless_ResolvesOnRecompute` — design.md Test
      Contract (i): call `deriveConsumedByDay` once with no charge data (flagged), call it
      again with the same snapshots plus a resolving charge entry, assert the second
      call's `Flagged = false` — proves no memory between calls (D2), the precondition
      `cmd/poller`'s reconciliation depends on.
- [x] T4.10 `TestDeriveConsumedByDay_BucketsInPollerZone_NotEffectiveDate` — **APPENDED BY
      T0**; design.md Test Contract (m). The single binding regression test for the owner's
      D18 ruling: a `cur` captured `2026-08-14T01:00:00Z` (20:00 Bogota Aug 13) with
      `CapturedDate = 2026-08-13` and `EffectiveDate` deliberately set to the UTC value
      `2026-08-13T01:00:00Z`. Assert `Date == 2026-08-12` **and** `Date != 2026-08-13` — the
      negative assertion is the point, since the two coincide for every nominal 03:30
      fixture. Also assert the D-B12 identity (`effectiveDay(cur) − effectiveDay(prev) ==
      DaysSpanned` whole days), which fails under the overruled UTC rule on the same
      fixtures. Do not weaken either assertion.
- [x] T4.11 `TestDeriveConsumedByDay_ZoneShiftedRowAtWindowEnd_Emitted` — **APPENDED BY T0**;
      design.md Test Contract (n): a row whose zoned effective day is exactly `end` while its
      UTC `EffectiveDate` day would be `end+1` is emitted, not filtered out — the derivation
      half of the widened snapshot fetch T5.4 asserts.
      Acceptance (all of T4): all tests compile and are pure (no fakes, no I/O — mirrors
      `derive_test.go`'s own zero-fakes style); `go vet ./...` passes (compiles
      `_test.go` files). **Per the Test-Execution-Policy, these tests are written but NOT
      run by the worker — status is `awaiting-user-verification`, not `done`, until the
      owner runs `go test ./internal/battery/...` (or `make test`) and reports the
      result.**

## T5. `internal/battery/reader_test.go` (extended) — port-wiring tests + un-panic three fakes — depends on T3

- [x] T5.1 Change `fakeTelemetryReader.SnapshotsByVehicleBetween` from a panicking stub to
      a functional one: record `accountID`/`teslaID`/`start`/`end` on new fields, return
      `(f.snapshots, f.err)` when set (reuse or add a dedicated fixture field so
      `RecentEfficiency`'s own existing tests, which populate `f.snapshots` for the
      `...Since` path, are unaffected — confirm no existing test relies on
      `SnapshotsByVehicleBetween` panicking).
- [x] T5.2 Change `fakeSuperchargerReader.SuperchargerSessionsByVehicleBetween` from a
      panicking stub to a functional one, same shape as T5.1.
- [x] T5.3 Change `fakeManualReader.ListEntriesByVehicleBetween` from a panicking stub to
      a functional one, same shape as T5.1.
      Acceptance (T5.1–T5.3): every existing `RecentEfficiency` test in this file still
      compiles and passes unmodified (none of them exercise the three `...Between`
      methods, per design.md's own note) — a diff of this file shows no existing test
      function's body changed, only the three fake method bodies.
- [x] T5.4 `TestConsumedByDay_FetchWindows` — design.md Test Contract (j). **Expected values
      CHANGED in T0** (design D-B13): assert `SnapshotsByVehicleBetween` is called with
      `(start-1, end+1)`, `SuperchargerSessionsByVehicleBetween` with `(start-1, end+2)`, and
      `ListEntriesByVehicleBetween` with `(start-1, end)`, for a fixed `start`/`end` pair.
- [x] T5.5 `TestConsumedByDay_AccountIDScoping_PassedToEveryPort` — design.md Test
      Contract (k): mirrors `TestRecentEfficiency_AccountIDScoping_PassedToEveryPort`'s
      existing pattern for the three ports `ConsumedByDay` calls.
- [x] T5.6 `TestConsumedByDay_TelemetryError_Propagates`,
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
      `[yesterday-GapReconciliationWindow+1, yesterday]` — where **"yesterday" is computed in
      the poller's own `loc`** (already loaded at `main.go:63`), not in UTC (T0/design D-B12;
      a UTC "today" is the wrong day for 5 hours out of every 24) — build `[]telemetry.ChargeGap`
      from the flagged entries, call `gapWriter.ReconcileWindow`. Exact code in design.md's
      "`cmd/poller` wiring" section — implement verbatim.
      Acceptance: per-vehicle error isolation (one vehicle's failure does not abort
      another's reconciliation, mirroring `CollectAll`'s own documented isolation); a
      failure to enumerate vehicles is a whole-cycle failure, logged, not fatal to the
      scheduler; `go build ./...` succeeds for `cmd/poller`.

## T7. `internal/battery/AGENTS.md` — Public Interface documentation — depends on T1

- [x] T7.1 Update the "Public interface (the port)" section to include `ConsumedByDay`'s
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
