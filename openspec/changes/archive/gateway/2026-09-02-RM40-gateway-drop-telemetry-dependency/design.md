# Design — RM40-gateway-drop-telemetry-dependency

## Context

`RM40-gateway-drop-telemetry-dependency` (the roadmap) found, by reading the actual
code, that exactly one production `telemetry.Reader` call site remains in
`internal/gateway/`: `internal/gateway/handlers/history.go:321`
(`h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`),
feeding only the battery-history chart via `buildBatteryChart(ctx, snaps,
start, end)`. Every other `make boundary-guard` hit in the module — the `Deps`/
`Handler` struct fields in `gateway.go` and `handlers.go`, the `telemetry.Snapshot`
parameter/map in `history.go`, and the two `_test.go` fakes — is type leakage from
that one call, not an independent read. Tier 1 of this roadmap
(`RM40-analytics-add-battery-level-read`, implemented, reviewer-approved, archived)
added `analytics.Reader.BatteryLevelByDay(ctx, accountID, teslaID, start, end)
([]analytics.DayBattery, error)`, SELECTing the same three fields
(`EffectiveDate`→`Date`, `BatteryLevelPct`, `BatteryRangeKm`) from `vehicle_metrics`,
the table `internal/analytics` already owns and already serves the gateway's other
two history charts from (`ConsumedByDay`, `OdometerDeltaByDay`).

This tier retargets the one call site and removes every remaining `internal/telemetry`
name from `internal/gateway/`. It reads the actual current state of every file it
touches (`internal/gateway/handlers/history.go`, `handlers.go`,
`internal/gateway/gateway.go`, and both `_test.go` files) rather than assuming from
the roadmap's summary table — see "distancesFromSnaps investigation" below for the
one place that reading paid off with a finding the roadmap did not anticipate.

## Goals / Non-Goals

**Goals:**
- Swap `buildHistoryView`'s battery-chart read from
  `h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)` to
  `h.analyticsReader.BatteryLevelByDay(ctx, uid, teslaID, start, end)` — dropping the
  `readStart := start.AddDate(0, 0, -1)` lookback entirely (roadmap D5).
- Retype `buildBatteryChart` from `(ctx, []telemetry.Snapshot, start, end)` to `(ctx,
  []analytics.DayBattery, start, end)`, bucketing on `DayBattery.Date` verbatim
  (roadmap D4) — never through `effectiveDayUTC`.
- Remove `TelemetryReader`/`telemetryReader` from `gateway.Deps`, `handlers.Deps`,
  and the `Handler` struct, and the `internal/telemetry` import from
  `gateway.go`/`handlers.go`/`history.go`.
- Rework `handlers_test.go` and `history_test.go`'s telemetry-typed fakes onto the
  analytics port and remove `internal/telemetry` from both files (roadmap D7) —
  including resolving the `distancesFromSnaps`/`snapsForDays` question below, which
  is what actually decides whether `history_test.go` can reach zero telemetry
  references.
- Leave `analytics.Reader.ConsumedByDay`/`OdometerDeltaByDay`/`LatestMetricsByAccount`
  and every other gateway read path untouched.
- Verify `make boundary-guard` passes clean (roadmap D8) — this change's own
  definition of done.

**Non-Goals (explicitly deferred, do not implement here):**
- `cmd/web/main.go:58`'s `TelemetryReader: telemetry.NewReader(pool)` injection
  removal — outside `internal/gateway/`, leader-owned (see proposal.md "What
  Changes"). Lines ~72/~86 of the same file, which inject `telemetry.NewReader(pool)`
  for OTHER consumers, are untouched by anyone.
- Renaming `i18n.KeyHistoryNoSnapshotTooltip`. Flagged by the roadmap as a known
  wording nuance ("no snapshot" now technically means "no `vehicle_metrics` row"),
  not actioned — "the owner did not ask for it."
- Backfilling `vehicle_metrics` to close the day-coverage gap with
  `vehicle_snapshots` (roadmap D6) — accepted as self-healing.
- Any database object change — none is needed; tier 1 already added the port and
  its index plan.
- Updating `root README.md`'s "Dependency graph" (the `gateway ─► …, telemetry, …`
  and `handlers ─► …, telemetry, …` lines) and any other root-level doc. This file
  is outside `internal/gateway/` — see tasks.md's "Leader/permission-gated" section.

## Decisions

Decisions below carry the roadmap's own D-numbering where they restate a roadmap
decision verbatim, plus this design's own local decisions (D-gw*) for the
gateway-internal shape this tier introduces.

### D1 (roadmap D1) — `internal/analytics` owns the read (settled in tier 1)

Restated only because it is why `BatteryLevelByDay` already exists for this tier to
call: keeping the read on `internal/telemetry` behind a gateway-local interface
would satisfy the guard's letter while leaving the gateway depending on telemetry at
runtime — exactly the loophole the guard exists to close.

### D2 (roadmap D2) — the port's shape (settled in tier 1, consumed here)

`BatteryLevelByDay(ctx, accountID, teslaID, start, end) ([]DayBattery, error)`,
mirroring `ConsumedByDay`/`OdometerDeltaByDay` in signature shape, the
precomputed-and-sparse contract, and the non-nil-empty-slice rule. This tier is the
port's first production caller.

### D4 (roadmap D4) — `DayBattery.Date` is a FINAL bucket key

`buildBatteryChart` buckets on `d.Date` **verbatim** — never `effectiveDayUTC(d.Date)`
— identical to how `buildConsumedChart` already buckets `DayConsumption.Date` and
`buildOdometerChart` already buckets `DayDistance.Date`. Re-applying `effectiveDayUTC`
would shift the day by one, because `metric_date` already had that conversion
applied once, at `Recalculate` time.

**Why this produces byte-identical output to the old bucketing (the characterization
proof this design pins down precisely):** the OLD `buildBatteryChart` bucketed on
`effectiveDayUTC(s.EffectiveDate)`, i.e. `startOfDay(s.EffectiveDate)`. Every
gateway test fixture already constructed `telemetry.Snapshot.EffectiveDate` as
`startOfDay(d)` for some calendar day `d` (see `snapsForDays`,
`history_test.go:315-327`) — `startOfDay` is idempotent, so
`effectiveDayUTC(startOfDay(d)) == startOfDay(d) == d`. The NEW bucket key is
`analytics.DayBattery.Date`, which is `vehicle_metrics.metric_date` — the SAME
already-effective calendar day, read back verbatim. Both old and new bucket keys are
therefore the identical `time.Time` value for the identical underlying day. The
retype changes WHERE the day gets computed (once, at `Recalculate` time, instead of
via a second `effectiveDayUTC` call inside the gateway) — it does not change WHICH
day any bar lands on.

### D5 (roadmap D5) — the 1-day lookback is dropped, and it was ALREADY a no-op for the battery chart specifically

`readStart := start.AddDate(0, 0, -1)` is deleted; `BatteryLevelByDay(ctx, uid,
teslaID, start, end)` is called with `start` verbatim.

**A fact this design pins down that the roadmap did not need to, because it only
affects test assertions, not production behavior:** the lookback day was already
dead weight for the battery chart's OWN rendered bars, even before this change.
`buildBatteryChart`'s loop is `for d := start; !d.After(end); d = d.AddDate(0, 0,
1)` — it starts at `start`, never at `readStart`. The extra `readStart` day, if
present in `snaps`, entered the `byDay` map but was never looked up, because no
iteration of the loop ever queries `byDay[readStart]`. (The lookback existed
because, historically pre-`RM29-analytics-add-vehicle-metrics`, the SAME
`SnapshotsByVehicleBetween` read also fed the odometer chart's own delta
calculation, which DOES need the predecessor day. Once RM29 moved the odometer
chart onto its own `analytics.Reader.OdometerDeltaByDay` port, the lookback kept
being fetched for a battery-only read that never consumed it.) This is why removing
it changes NOTHING about any rendered bar — see the Test Contract's
`TestBuildHistoryView_PassesReadStartLookbackToEndToReader` treatment below, which
is the one place this drop is test-visible at all (as a changed call-argument
assertion, never a changed chart output).

### D6 (roadmap D6) — day-coverage difference accepted, not backfilled

A day with no `vehicle_metrics` row (the nightly `Recalculate`/`Reconcile` watermark
has not reached it yet) yields no `DayBattery` entry — renders as the SAME empty
"no snapshot" bar a missing `vehicle_snapshots` row already produced. This is a
(typically single-day-lagging) SHIFT in which table produces the empty bar, not a
new UI state. No gateway code change is needed to express this — `buildBatteryChart`
already renders an empty bar for any day absent from its input slice, regardless of
why it is absent.

### D7 (roadmap D7) — no new unit tests; existing tests repaired

`handlers_test.go` and `history_test.go` stop compiling the moment `TelemetryReader`
leaves `Deps`. Reworking their fakes is REPAIR — restoring a compiling, passing
suite — never new coverage. No test case is added beyond what is needed to keep the
existing suite's assertions meaningful against the new port. Every fixture value and
test name this document commits to below is either (a) an unchanged value carried
forward, or (b) an explicitly-flagged behavior-contract change (D5's lookback
removal) — never a value invented after the fact to make a test pass.

### D8 (roadmap D8) — zero escape hatches

`make boundary-guard` passes with no `// boundary:allow:` comment added anywhere,
and the guard's grep pattern is not widened, narrowed, or special-cased. See
"Verification signals" below for the exact command and its two-tier (fail on
non-test, warn on test) behavior, and why this change's own goal is stricter than
what the guard mechanically requires (D-gw3).

### D-gw1 — `buildHistoryView`'s call-site swap, exact shape

```go
// Battery chart: analytics.Reader.BatteryLevelByDay (RM40 tier 2) — replaces
// the telemetry.Reader.SnapshotsByVehicleBetween call and its 1-day lookback
// (roadmap D5). No lookback: the port returns exactly [start, end].
batteryDays, err := h.analyticsReader.BatteryLevelByDay(ctx, uid, teslaID, start, end)
if err != nil {
    log.Printf("gateway: history reader error for account %s vehicle %d: %v", uid, teslaID, err)
    v.Battery = fragments.HistoryChart{Empty: true}
} else {
    v.Battery = buildBatteryChart(ctx, batteryDays, start, end)
}
```

Variable name `batteryDays` (not `days` or `snaps`) avoids colliding with the
`days` variable the consumed-chart block already declares two statements later in
the same function — both must remain independently readable in one function body.
The log line's format string is unchanged (still names the read as "history reader
error" — it does not need to say which port, matching the two sibling blocks'
identical wording). Independent-failure behavior (a battery error degrades ONLY
`v.Battery`) is unchanged, because this block's shape — read, branch on `err`,
assign `v.Battery` — is structurally identical to before; only the callee and its
argument list changed.

### D-gw2 — `buildBatteryChart` retype, exact shape

```go
func buildBatteryChart(ctx context.Context, days []analytics.DayBattery, start, end time.Time) fragments.HistoryChart {
    numDays := int(end.Sub(start).Hours()/24) + 1

    if len(days) == 0 {
        return fragments.HistoryChart{Empty: true, LabelVertical: labelVerticalFor(numDays)}
    }

    byDay := make(map[time.Time]analytics.DayBattery, len(days))
    for _, d := range days {
        byDay[d.Date] = d // D4: Date verbatim, never effectiveDayUTC(d.Date)
    }

    bars := make([]fragments.HistoryBar, 0, numDays)
    for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
        label := d.Format("01-02")
        day, ok := byDay[d]
        if !ok {
            bars = append(bars, fragments.HistoryBar{
                HeightPct: 0,
                Tooltip:   fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryNoSnapshotTooltip), label),
                Label:     label,
                Present:   false,
            })
            continue
        }
        tooltip := fmt.Sprintf("%s · %d%% · %s km range", label, day.BatteryLevelPct, formatKmRaw(day.BatteryRangeKm))
        bars = append(bars, fragments.HistoryBar{
            HeightPct: day.BatteryLevelPct,
            Tooltip:   tooltip,
            Label:     label,
            Present:   true,
        })
    }
    return fragments.HistoryChart{
        Bars:          bars,
        Empty:         false,
        LabelVertical: labelVerticalFor(numDays),
        YAxisTicks:    buildYAxisTicks(100, func(v float64) string { return fmt.Sprintf("%d%%", int(math.Round(v))) }),
    }
}
```

Every line is either a rename (`snaps []telemetry.Snapshot` → `days
[]analytics.DayBattery`, `s.EffectiveDate`/`effectiveDayUTC(...)` → `d.Date`
verbatim, `s.BatteryLevelPct`/`s.BatteryRangeKm` → `day.BatteryLevelPct`/
`day.BatteryRangeKm`) or unchanged (the empty rule, the tooltip format string, the
Y-axis ticks, `LabelVertical`). The `i18n.KeyHistoryNoSnapshotTooltip` key is
reused unchanged (roadmap "Future work": flagged, not renamed).

### D-gw3 — `Deps`/`Handler` field removal, and why this goes further than the guard strictly requires

`gateway.Deps.TelemetryReader`, `handlers.Deps.TelemetryReader`, and
`Handler.telemetryReader` are deleted (not merely left unused) — along with the
`internal/telemetry` import in all three production files. `make boundary-guard`
(`Makefile:560-587`) technically only FAILS on a non-test file's import; a
`_test.go` hit is a non-fatal WARNING. That means, mechanically, leaving
`handlers_test.go`/`history_test.go` importing `internal/telemetry` would NOT fail
`make boundary-guard`. This change goes further anyway, because:

1. The roadmap's own stated intention is unconditional: "the gateway stops naming
   `internal/telemetry` at all — no import, no `telemetry.Reader` field, no
   `telemetry.Snapshot` type" — not "no import in a non-test file."
2. Roadmap D2's findings table says the guard's other 5 hits (which include the 2
   test-file imports) "all vanish once the single call moves" — treating them as
   removed, not merely no-longer-fatal.
3. The dispatch that produced this tier explicitly assigns "rework the two
   `_test.go` fakes onto the analytics port and remove the `internal/telemetry`
   imports" as in-scope work, not optional cleanup.

So this tier's tasks.md holds both test files to the same zero-import bar as the
three production files, even though the Makefile guard alone would tolerate less.

### D-gw-test — the `distancesFromSnaps`/`snapsForDays` investigation (the one genuine unknown)

**Question:** can `history_test.go` drop its `internal/telemetry` import entirely,
given that `distancesFromSnaps`/`snapsForDays` both operate on `[]telemetry.Snapshot`
today?

**Finding: yes, but only by retyping those two helpers off `telemetry.Snapshot`
onto a new, test-file-local fixture struct — they cannot simply be deleted,
because they serve a DIFFERENT chart than the one this tier touches.**

`distancesFromSnaps(snaps []telemetry.Snapshot, start, end time.Time)
[]analytics.DayDistance` (`history_test.go:349-368`) is the ODOMETER chart's test
fixture bridge: it replays the pre-`RM29-analytics-add-vehicle-metrics` delta+clamp
algorithm over a snapshot-shaped fixture, so pre-existing snapshot fixtures
(`snapsForDays`) keep driving the odometer chart's `analytics.DayDistance` fixture
with the same numbers the old, pre-move gateway code produced. This is used by
`fakeAnalyticsReader.OdometerDeltaByDay` (`history_test.go:221-237`, the `snaps
!= nil` branch) — **not by anything battery-related.** Nothing about retargeting the
battery chart onto `BatteryLevelByDay` requires touching `distancesFromSnaps` or the
odometer chart's fixtures.

However, `telemetry.Snapshot` was ALSO the type `fakeHistoryReader.historySnaps`
carried into the (now-removed) `SnapshotsByVehicleBetween` call — the SAME
`snapsForDays(...)` fixture was, until this change, doing double duty: driving the
battery chart directly (via `fakeHistoryReader`) AND driving the odometer chart
indirectly (via `distancesFromSnaps`, fed the identical slice through
`newHandlerForHistory`'s `snaps: reader.historySnaps` wiring,
`history_test.go:278`). Once `Deps.TelemetryReader` is deleted, `fakeHistoryReader`
has no method any handler calls anymore — it is not "unused," it is **uncompilable**
(its whole reason to exist was to satisfy `telemetry.Reader` for a field that no
longer exists on `Deps`).

The resolution: **split what `snapsForDays` used to do in one call into two
independent fixture builders**, because the two charts it fed are now driven by
two unrelated ports with two unrelated fixture shapes:

1. **Odometer fixture (unchanged data, new carrier type).** Retype `snapsForDays`
   (or an equivalently-named replacement) from `func(days []time.Time, odometerBase,
   odometerStep float64, batteryBase int) []telemetry.Snapshot` to a version that
   drops the now-irrelevant `batteryBase` parameter and returns a new,
   test-file-local struct carrying ONLY the two fields `distancesFromSnaps` reads
   (`OdometerKm float64`, `EffectiveDate time.Time`) — e.g. (naming is the
   implementer's call, not fixed by this design):
   ```go
   type odometerCapture struct {
       OdometerKm    float64
       EffectiveDate time.Time
   }
   ```
   `distancesFromSnaps`'s parameter type changes from `[]telemetry.Snapshot` to
   `[]odometerCapture`; its body (the delta+clamp+bucket loop) is BYTE-IDENTICAL —
   only the struct literal's type name changes at the two field accesses
   (`s.OdometerKm`, `s.EffectiveDate`). This is a pure rename: `telemetry.Snapshot`
   was never used here as an interface constraint, only as a convenient data
   carrier that happened to have the two needed fields, so swapping it for a
   narrower local struct changes no behavior and breaks no other call site.
2. **Battery fixture (new, direct, simpler than before).** Add a new,
   test-file-local helper building `[]analytics.DayBattery` DIRECTLY in final
   bucket-key form — no `EffectiveDate`/`CapturedAt` distinction needed at all,
   because `DayBattery.Date` is already final (D4), unlike the old
   `telemetry.Snapshot`, which needed a separate `EffectiveDate` field precisely
   because `CapturedAt` (the capture instant) and the effective day were different
   values requiring a translation the port no longer needs:
   ```go
   func batteryForDays(days []time.Time, batteryBase int) []analytics.DayBattery {
       out := make([]analytics.DayBattery, len(days))
       for i, d := range days {
           out[i] = analytics.DayBattery{
               Date:            startOfDay(d),
               BatteryLevelPct: batteryBase + i,
               BatteryRangeKm:  300, // matches snapsForDays' old hardcoded value
           }
       }
       return out
   }
   ```
   This mirrors `snapsForDays`' existing per-day pattern (`batteryBase + i`,
   constant `BatteryRangeKm: 300`) exactly, so every existing test's expected
   `HeightPct`/tooltip values (Test Contract below) transfer unchanged.
3. **`fakeHistoryReader` and its 5 methods are deleted outright** — not
   deprecated, not left as dead code. `fakeAnalyticsReader` gains the fields and
   methods to be the SOLE reader double for the whole history test file:
   `battery []analytics.DayBattery`, `batteryErr error`,
   `gotBattAccount/gotBattTeslaID/gotBattStart/gotBattEnd time.Time`,
   `batteryByDayCalled bool` — mirroring the existing `days`/`err`/`gotAccount`/
   .../`consumedByDayCalled` fields on the SAME struct for `ConsumedByDay`. The
   tier-1 compile-only panic stub for `BatteryLevelByDay`
   (`history_test.go:190-200`) is replaced with a real, call-recording
   implementation of the same shape as `ConsumedByDay`'s (`history_test.go:212-219`).
4. **`newHandlerForHistory`/`newHandlerForHistoryWithAnalytics` collapse to ONE
   constructor** taking only a `*fakeAnalyticsReader` (there is no second reader
   left to wire): `newHandlerForHistory(analyticsReader *fakeAnalyticsReader,
   teslaID int64, vin string) *Handler`, replacing both existing functions. Every
   one of the ~15 call sites that built `&fakeHistoryReader{historySnaps: ...}`
   (see tasks.md for the full list by line) is reworked to build the SAME
   underlying day list once and derive BOTH an odometer fixture (via the retyped
   `snapsForDays`+`distancesFromSnaps`, unchanged numbers) and a battery fixture
   (via the new `batteryForDays`, using the SAME `batteryBase` parameter the old
   single fixture used) — see the Test Contract below for the exact per-call-site
   mapping and the handful of assertions that must change, not just retype.

**Why this is not a "workaround" but the correct fix:** `telemetry.Snapshot` was
never load-bearing for `distancesFromSnaps`'s logic — it was a happenstance carrier
reused because it already existed and already had the two needed fields. Now that
the gateway has zero other reason to reference `telemetry`, keeping that import
alive JUST to shape a test fixture would be exactly the kind of "satisfies the
letter, not the intent" outcome D-gw3 rejects for the Makefile guard's test-file
leniency. A two-field local struct is strictly narrower, self-documents what the
odometer fixture actually needs, and removes the last reason this file imports
`internal/telemetry`.

**`handlers_test.go` needs no such investigation** — its `fakeReader`/
`newHandlerWithReader` are entirely vestigial. Every call site
(`TestDashboardFor_RegisteredEmptyPromptsConnect`,
`TestDashboardFor_AccountReadErrorShowsNotice`,
`TestVehicleSelect_FiresVehicleChangedTrigger`,
`TestHome_SignedInRedirectsToDashboard`) passes a fake `telemetry.Reader` through a
`Deps`/`Handler` field NO code path in that test ever reaches — confirmed by
reading each test body: none calls a dashboard/history code path that touches
`telemetryReader`. They all switch to the pre-existing `newHandler(acct, tsvc)`
helper (`handlers_test.go:187-189`, which already omits `TelemetryReader`) or drop
the field from a direct `Deps{...}` literal. `fakeReader` and
`newHandlerWithReader` are deleted outright; the `internal/telemetry` import is
removed with nothing left to replace it.

## Test Contract (authored before implementation, per `ai/go-conventions.md`)

This is a **characterization change**: every fixture below must produce the SAME
rendered battery-chart output as the pre-change code, with exactly one class of
assertion changing on purpose (the dropped lookback, D5). Where a value below
disagrees with what the implementation happens to produce, THIS document wins — the
implementer traces the disagreement to a genuine mistake, not to a "the test must be
wrong" excuse.

### Unchanged: `buildBatteryChart` unit tests (retype only, same expected values)

- **`TestBuildBatteryChart_EmptyWhenNoSnapshots`** — `nil` and `[]analytics.DayBattery{}`
  (was `nil`/`[]telemetry.Snapshot{}`) both still yield `Empty: true`.
- **`TestBuildBatteryChart_FixedAxis_FullWindow`** — fixture was
  `snapsForDays(calendarDays(start, end), 0, 0, 70)`; becomes
  `batteryForDays(calendarDays(start, end), 70)`. Expected, UNCHANGED: 5 bars, labels
  `08-03`..`08-07`, `Bars[i].HeightPct == 70+i`, every bar `Present: true`.
- **`TestBuildBatteryChart_FixedAxis_MissingDayEmptyLabeledBar`** — fixture was
  `snapsForDays(days, 0, 0, 70)` over a 4-of-5-days list (missing `08-05`); becomes
  `batteryForDays(days, 70)` over the same list. Expected, UNCHANGED: index 2
  (`08-05`) has `Present: false`, `HeightPct: 0`, tooltip containing the literal
  substring `"no snapshot"` (the resolved EN string for
  `KeyHistoryNoSnapshotTooltip`, unchanged key).
- **`TestBuildBatteryChart_TooltipUsesEffectiveDateMMDD`** (candidate rename:
  `TestBuildBatteryChart_TooltipUsesDateMMDD` — `analytics.DayBattery` has no
  separate `CapturedAt` field to distinguish from, so the old name's contrast no
  longer applies; renaming is allowed, not required) — fixture was a single
  `telemetry.Snapshot{BatteryLevelPct: 80, BatteryRangeKm: 300, CapturedAt:
  2026-08-08T03:30, EffectiveDate: 2026-08-07T03:30}`; becomes a single
  `analytics.DayBattery{Date: 2026-08-07, BatteryLevelPct: 80, BatteryRangeKm:
  300}`. Expected, UNCHANGED: `Bars[0].Label == "08-07"`, tooltip contains `"08-07"`
  and does NOT contain `"2026-08-08"`.

### Unchanged: `buildHistoryView`/`DashboardHistoryFragment` battery-chart assertions

- **`TestBuildHistoryView_AnalyticsReaderOdometerError_DegradesOdometerChartOnly`**,
  **`TestBuildHistoryView_ConsumedReaderError_LeavesOtherChartsIntact`** — both
  assert the Battery chart stays populated (`v.Battery.Empty == false`,
  `len(v.Battery.Bars) > 0`) when a DIFFERENT port fails. Fixture source changes
  (`fakeHistoryReader{historySnaps: snaps}` → `fakeAnalyticsReader{battery:
  batteryForDays(...)}`), assertions and their pass/fail outcome do not.
- **`TestDashboardHistoryFragment_ReaderErrorDegradesBothEmpty`** (candidate
  rename: the name predates even the pre-this-tier reality that only ONE chart
  ever degraded from this read — not required to rename, but the misleading name
  is a pre-existing issue this tier may fix opportunistically) — the fixture's
  error field moves from `fakeHistoryReader{historyErr: errTestHistory}` to
  `fakeAnalyticsReader{batteryErr: errTestHistory}`. Expected, UNCHANGED: HTTP 200,
  body contains the Spanish empty-state placeholder text
  `"Esperando los datos nocturnos"`.
- **`TestBuildHistoryView_TelemetryReaderError_DegradesBatteryChartOnly`**
  (candidate rename: `TestBuildHistoryView_BatteryReaderError_DegradesBatteryChartOnly`)
  — same error-field move. Expected, UNCHANGED: `v.Battery.Empty == true`,
  `len(v.Presets) > 0`.
- **`TestBuildBatteryChart` byte-identity via D4's proof above** — every test that
  builds a multi-day fixture spanning a gap (e.g.
  `TestBuildHistoryView_BothChartsShareFixedAxis`'s gapped 08-05 day) keeps
  identical `Label`/`Present`/`HeightPct` output once its fixture is rebuilt via
  `batteryForDays` instead of `snapsForDays`, because both produce the same
  `Date`/`BatteryLevelPct`/`BatteryRangeKm` triples for the same day list.

### Changed on purpose (D5 — the lookback drop): call-argument assertions only, never chart output

- **`TestBuildHistoryView_PassesReadStartLookbackToEndToReader`** (candidate rename:
  `TestBuildHistoryView_PassesStartToEndToReader`, since there is no more lookback
  to pass) — OLD: asserted `reader.gotStart.Equal(start.AddDate(0, 0, -1))`. NEW:
  assert `analyticsReader.gotBattStart.Equal(start)` (verbatim, no lookback) and
  `analyticsReader.gotBattEnd.Equal(end)`.
- **`TestBuildHistoryView_BothChartsShareFixedAxis`** — drops its trailing "Lookback
  was passed to Between" assertion block entirely (there is no lookback to assert);
  every other assertion in this test (bar counts, matching labels across charts) is
  unchanged.
- **`TestDashboardHistoryFragment_DefaultWindowPassedToReader`** — OLD:
  `wantStart := yesterday.AddDate(0, 0, -7)` (lookback 1 + default window 6). NEW:
  `wantStart := yesterday.AddDate(0, 0, -6)` (no lookback; default window 6 only) —
  asserted against `analyticsReader.gotBattStart`, not `reader.gotStart`.
  `wantEnd := yesterday` is unchanged.
- **`TestDashboardHistoryFragment_DaysParamIsIgnored`** — same `-7` → `-6` change,
  same reasoning, asserted against the renamed field.

No other existing history_test.go assertion is expected to change value — every
odometer-chart and consumed-chart test in this file is untouched by this tier
(they exercise `analytics.Reader.OdometerDeltaByDay`/`ConsumedByDay`, neither of
which this tier modifies).

### Tenant/argument-forwarding proof (mirrors `TestHandler_AnalyticsReaderDepsForwarding`)

That existing test already proves `ConsumedByDay`/`OdometerDeltaByDay` are called on
the SAME `AnalyticsReader` instance `New(Deps{...})` was given. Extend it (or add a
sibling assertion in the same test) to prove `BatteryLevelByDay` is now ALSO called
on that same instance, with `(accountID, teslaID, start, end)` forwarded verbatim —
mirroring the existing `consumedByDayCalled`/`odometerByDayCalled` assertions with a
new `batteryByDayCalled` one.

## Risks / Trade-offs

- **This is the second and final tier — after this lands, `make boundary-guard`
  must show zero hits of any kind** (not just zero non-test failures). If a hit
  remains in a file this tier's tasks.md did not anticipate, the leader
  investigates before archiving; this design does not expect one, based on the
  exhaustive `grep -rn "internal/telemetry" internal/gateway` performed while
  writing this document (five files: `gateway.go`, `handlers.go`, `history.go`,
  `handlers_test.go`, `history_test.go` — matching this tier's assigned scope
  exactly, plus two AGENTS.md prose mentions that are documentation, not code).
- **`cmd/web/main.go` will not compile between this tier's artifacts landing and
  the leader's own fix landing**, because `gateway.Deps.TelemetryReader` disappears
  out from under its line 58 literal. This is expected and intentional — the
  proposal names this as leader-owned integration work in the SAME wave, not a
  follow-up change; `go build ./...` for the WHOLE repository is not green until
  both land together.
- **Root `README.md`'s "Dependency graph" goes stale the moment this lands** (the
  `gateway ─►` and `handlers ─►` lines still list `telemetry`). This file is
  outside `internal/gateway/`; per `CLAUDE.md`'s "docs track structural change"
  rule this MUST be fixed in the same overall change, but by whoever holds
  permission for that path (leader-owned or an explicitly granted path — see
  tasks.md).
- **No new test coverage — by design (D7).** Every fixture rework in this tier
  either preserves an existing assertion's value or changes it for the single,
  named, D5-caused reason above. A future reviewer diffing test expectations
  against this document should find no unexplained delta.

## Verification signals

Per the Test-Execution-Policy: the implementing worker runs and reports `go build
./...`, `go vet ./...`, `gofmt -l`, `make build`, `make vet`, `make bins`, and the
standalone guards — **`make boundary-guard` is this change's own definition of
done** (must print "boundary-guard: internal/gateway/ does not import
internal/telemetry" with no `WARNING` block above it, meaning BOTH the fatal
non-test check and the non-fatal test-file check come back clean, per D-gw3's
stricter-than-the-guard bar). `make ui-guard`/`make i18n-guard`/`make money-guard`/
`make tz-guard`/`make migration-guard` are no-ops for this tier (no new user-facing
string, no monetary or raw-time-zone code, no migration file). Never run `go test
./...` / `make test` / `make test-with-db` / `make check`. The owner runs `go test
./internal/gateway/...` (and, since `cmd/web` only compiles once the leader's
companion fix lands, eventually `go test ./...`) and reports results; until then
this tier's implementation status is **awaiting-user-verification**, never "done."
