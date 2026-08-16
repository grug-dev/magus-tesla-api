## Context

Tier 1 (`RM28-telemetry-add-charge-gap-storage`, archived) added `internal/telemetry`'s
`charge_gaps` table, `GapWriter.ReconcileWindow`, and
`SuperchargerReader.SuperchargerSessionsByVehicleBetween`. Tier 2
(`RM28-manualcharge-add-date-range-reader`, archived) added
`manualcharge.Reader.ListEntriesByVehicleBetween`. `telemetry.Reader.SnapshotsByVehicleBetween`
already existed before RM28 (RM8). This tier — the largest of the roadmap's four — is the
derivation itself: `internal/battery` combines all three read ports into one new port
method, `ConsumedByDay`, that computes a corrected per-day battery-consumed percentage
and detects the days whose numbers do not add up.

`internal/battery` already exists in this repo (`battery-add-efficiency-metric`) with one
metric, `RecentEfficiency`. This tier adds a second, independent metric to the same
`Reader` port and the same `reader` struct — no new module (D15: a new
`internal/consumption` module was considered and rejected; `battery` already is that
module and already composes the exact four dependencies this tier's derivation needs a
subset of).

Primary and only module for this worker's dispatch: **`internal/battery/`**. `cmd/poller`
wiring is specified in full below but implemented by the leader (outside this module's
sandbox — `ai/architecture.md`: a worker never edits outside its assigned module).
`internal/telemetry` and `internal/manualcharge` are read-only dependencies; neither's
code is touched here.

## Goals / Non-Goals

**Goals**

- A new `ConsumedByDay` method on the existing `battery.Reader` port, computing D13's
  formula per calendar day over a caller-supplied `[start, end]` window.
- Gap detection (D5/D5a) and inferred-type classification (D7a) as part of the same
  computation — `DayConsumption.Flagged` / `.MissingChargingType` — so `cmd/poller` needs
  no second pass over the same data to build `telemetry.ChargeGap`s.
- Source-specific charge-to-day matching (D12), generalized to cover multi-day spans (D8)
  without special-casing them.
- A precise, fully-specified `cmd/poller` wiring contract (D4/D4a) for the leader to
  implement: exact function calls, ordering, error handling, and the reconciliation
  window.
- `internal/battery/AGENTS.md` updated with the new port method.

**Non-Goals**

- No rendering. No bar chart, no flagged-day visual marker (D10), no multi-day-span
  marker. All `internal/gateway`, tier 4.
- No change to `telemetry.GapWriter`, `charge_gaps`'s schema, or either date-range read
  port's own contract (`SuperchargerSessionsByVehicleBetween`,
  `ListEntriesByVehicleBetween`) — both are consumed exactly as tiers 1–2 shipped them.
- No change to `RecentEfficiency`, `Efficiency`, `deriveEfficiency`, `socReadings`,
  `capacity.go`, or `NewReader`'s signature.
- No caching, no summary table, no new database object of any kind (D2, D15).
- No revival of Supercharger battery-% estimation (D14) — this tier accepts and documents
  D14a's limitation rather than working around it.
- No actual `cmd/poller` code change performed by this worker — the wiring section below
  is a specification for the leader's separate task.

---

## Design Decisions

### D-B1 (D15) — `ConsumedByDay` extends the existing `Reader` port; `NewReader`'s signature does not change

`ConsumedByDay` is added as a second method on `battery.Reader`, implemented on the same
`*reader` struct `RecentEfficiency` already uses. It needs exactly three of the four
dependencies `NewReader` already wires in — `telemetry.Reader`, `telemetry.SuperchargerReader`,
`manualcharge.Reader` — and **none** of the fourth (`vehicleLookup`/`account`, used only
by `RecentEfficiency`'s pack-capacity lookup). No new dependency is introduced and
`NewReader`'s parameter list is unchanged. This is a direct instance of the project's
AI-efficiency "closed vocabulary" principle: a second metric on an already-composed module
should not force every caller of `NewReader` (currently none — `battery` is not yet wired
into any `cmd/`, per its own `AGENTS.md`) to learn a second constructor or a widened one.

**Rejected alternative:** a second constructor, `NewConsumedReader(telemetry, supercharger,
manual)`, taking only the three needed ports. Rejected — it would create two constructors
for the same concrete `*reader` type and two ports (`Reader` would need splitting, or the
new constructor would return an unexported type), for a savings of one unused struct field
reference. Not worth the surface-area increase.

### D-B2 — `DayConsumption`'s "no data" signal is absence from the slice, not a field

`ConsumedByDay` returns a **sparse** `[]DayConsumption`: one entry per calendar day in
`[start, end]` that has a computable value, and **no entry at all** for a day that does
not (no snapshot exists for that day, or the day is skipped per D5a). There is no
`NoData`/`HasData` boolean field on `DayConsumption`.

**Why:** this mirrors the *exact* existing convention `internal/gateway/handlers/history.go`
already uses for the odometer and battery-level charts — `buildOdometerChart` and
`buildBatteryChart` both bucket the returned snapshot slice into a `map[time.Time]telemetry.Snapshot`
and treat a day's absence from that map as "no snapshot," emitting an empty labeled bar
(`Present: false`). Tier 4 will do the identical thing with `ConsumedByDay`'s result:
bucket into `map[time.Time]battery.DayConsumption` and treat absence as no-data. Adding a
`NoData` field would make the port's own contract disagree with the presence/absence
signal every caller already checks first — two ways to say the same thing, with the risk
they drift. See "How tier 4 renders a flagged day and a no-data day" below for the exact
consumption pattern this is designed to support with zero re-derivation.

**Rejected alternative:** a dense `[]DayConsumption` with exactly `end-start+1` entries,
carrying `HasData bool`. Rejected — it does not match the established gold-standard
pattern (`buildOdometerChart`/`buildBatteryChart`), and it would force every entry's other
fields to have a meaningless zero value on no-data days that callers must remember to
ignore, rather than the entry simply not existing.

### D-B3 (D1, D8) — `DayConsumption.Date` is the row's own `EffectiveDate`, never shifted; resolving the roadmap's "start day" wording

`DayConsumption.Date` is set to `dayUTC(cur.EffectiveDate)` for the snapshot row `cur`
that carries the (possibly multi-day) delta — the same value D1 already establishes as
final and non-negotiable ("do NOT shift, re-derive, or 'fix' the stored
`battery_used_pct_calc`"; "the five derived columns stay exactly where they are, on the
row that carries them today").

This resolves an apparent tension with roadmap D8's own wording: "plot a single bar on
the **start day**." Read literally against calendar dates, `cur.EffectiveDate` is
chronologically the *last* day of a multi-day span (the day the delayed poll's row
represents), not the first. This design deliberately does **not** re-attribute the bar to
`predecessor.EffectiveDate + 1` (the chronological first day of the gap) — doing so would
be exactly the kind of shift D1's correction explicitly overturned RM28's original tier 1
for. The reading this design adopts: "start day" means "the single day *at which the bar
starts appearing*" (as opposed to spreading the value across the span, or omitting it) —
i.e., the one real data point the derivation has, placed where D1 already says it belongs.
Every intervening calendar day between `predecessor.EffectiveDate` and `cur.EffectiveDate`
has **no snapshot row at all** (the poll was missed), so it is automatically absent from
`ConsumedByDay`'s output (D-B2) — exactly matching D8's "intervening days rendered as
no-data" with zero special-case code.

### D-B4 (extends D5a) — A row whose predecessor lies outside the fetched window is skipped, not guessed

`deriveConsumedByDay` iterates fetched, `EffectiveDate`-ascending snapshots pairwise:
`for i := 1; i < len(snapshots); i++`, treating `snapshots[i-1]` as `snapshots[i]`'s
predecessor for the purpose of computing the charge-matching interval's lower bound
(`prev.CapturedAt`, D12). `snapshots[0]` — the earliest row in the fetched
`[start−1, end]` window — is **never** emitted as an output day, even if its own
`EffectiveDate` already falls inside `[start, end]` (which happens only when no snapshot
exists at exactly `start−1`, e.g. a pre-existing gap crossing the window's own boundary).

**Why:** `telemetry.Snapshot.BatteryUsedPctCalc` is computed by `telemetry` against that
row's own true DB-adjacent predecessor, wherever it actually is — but this module has no
way to learn *that* predecessor's `CapturedAt` unless it also happens to fall inside the
fetched window (D9a's one-day lookback assumes the common case: no pre-existing gap
crossing the boundary). Guessing an interval lower bound (e.g., "no lower bound at all," or
"assume exactly one day back") would either double-count a charge already attributed to an
earlier reconciliation window, or silently narrow the window and drop a real charge —
either way, a fabricated interval, not an observed one. D5a already establishes the
governing principle for exactly this shape of problem ("no predecessor means no claim
either way; flagging would be a fabricated finding") — this decision applies the same
principle one layer up, to the *charge-matching bound* rather than the *delta itself*.

**Consequence:** this is a narrow, boundary-only edge case (a gap immediately preceding
`start−1`), and its effect is that one day's `ConsumedByDay` entry is missing from *this*
window's output even though `telemetry` has a valid `BatteryUsedPctCalc` for it. A
different `[start, end]` window whose `start−1` does have a snapshot will emit it
normally. This is consistent with D14a's own accepted-limitation philosophy: `ConsumedByDay`
is not required to be complete at every possible window boundary, only to never fabricate.

### D-B5 (D9a, D12) — Supercharger matching: DB-narrow, then Go-side precise interval filter, with a one-day tail over-fetch

For each row pair `(prev, cur)`, a Supercharger session belongs to `cur`'s day when
`prev.CapturedAt <= session.ChargeStopDateTime < cur.CapturedAt` (D12's rule, generalized
from single-day to any pair — see the roadmap's own D12 language: "`snapshot[D].captured_at`"
and "`snapshot[D+1].captured_at`" index by *capture* day, which is exactly `predecessor`/
`current` in this module's own pairing, not by `EffectiveDate`).

`ConsumedByDay` fetches sessions once for the whole call via
`SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, start−1, end+1)` — one day
of **tail over-fetch** past `end`, because the last row's `CapturedAt` (whose
`EffectiveDate` is `end`) lands in the early hours of the *next* calendar day (the nightly
poll runs ~03:30, well after midnight), which the bounded-window port's own half-open
`[start, end+1day)` contract would otherwise exclude. The over-fetch costs nothing beyond
one extra day's rows: `sumSuperchargerPctBetween`/`inferMissingChargingType` re-filter
every fetched session against each row-pair's own precise `[prev.CapturedAt, cur.CapturedAt)`
interval in Go, so a session outside every real interval is simply never matched to any
row, regardless of what the initial DB fetch included.

**Rejected alternative:** one `SuperchargerSessionsByVehicleBetween` call per row pair,
scoped exactly to that pair's interval. Rejected — this module's own established
`RecentEfficiency` precedent already issues "exactly one call per consumed port... never a
per-snapshot or per-session lookup" (`reader.go`'s own doc comment, `ai/architecture.md`
§7 read-heavy profile); an N-call pattern here would be new, unjustified N+1-style cost
for a window bounded at ~90 rows.

### D-B6 (D8, D12) — Manual-entry matching: `(predecessor.EffectiveDate, current.EffectiveDate]`, generalized for spans

A manual entry belongs to row `cur` (with predecessor `prev`) when
`dayUTC(prev.EffectiveDate) < dayUTC(entry.ChargedOn) <= dayUTC(cur.EffectiveDate)` — an
exclusive-start, inclusive-end calendar-day range. For the ordinary single-day case
(`DaysSpannedCalc == 1`), `prev.EffectiveDate + 1 day == cur.EffectiveDate`, so this
collapses to exactly `entry.ChargedOn == cur.EffectiveDate` — the literal D12 rule. For a
multi-day span, it naturally covers every day in the span without a second code path,
matching D8's "charges inside the span are summed regardless."

`ConsumedByDay` fetches manual entries once via `ListEntriesByVehicleBetween(ctx, accountID,
teslaID, start, end)` — no lookback/tail adjustment needed, because `charged_on` is a plain
`DATE` (tier 2's own D5) and the union of every row pair's matching range across the whole
call is exactly `(start−1, end] = [start, end]`.

### D-B7 (reconciles D1 and D6) — `EffectiveDate` (UTC calendar-day) remains the single bucketing key; no Bogota-zone conversion is introduced here

Roadmap D6 ("calendar-day bucketing uses the poller's configured zone... charges and
snapshots must bucket by identical rules or the formula breaks at the edges") was written
against the *original* D1, before D1 was corrected on 2026-08-15. This design does **not**
introduce a second, Bogota-zone-aware calendar-day computation for `EffectiveDate` or
`ChargedOn` comparisons. `EffectiveDate` is computed once, in `internal/telemetry/mapping.go`,
as `CapturedAt.AddDate(0, 0, -1)` in UTC — already the single bucketing key the gateway's
own `effectiveDayUTC` helper uses for the odometer/battery charts today. `ChargedOn` is a
plain `DATE` the user picked directly (zone-agnostic by construction — there is no
time-of-day component to convert). Comparing both as UTC-midnight calendar values (this
design's `dayUTC` helper, mirroring the gateway's `effectiveDayUTC`) is therefore the
correct, already-established precedent — introducing a *second*, Bogota-zone-shifted
comparison here would recreate exactly the "two clocks disagreeing at the edges" risk D6
warned against, not prevent it.

This is safe in practice because the poller's schedule (`~03:30` local, well after
midnight in both UTC and `America/Bogota`, a UTC−5 zone) means `EffectiveDate`'s "−1 day"
UTC arithmetic and an equivalent Bogota-zone "−1 day" computation agree for every capture
this platform produces today; a schedule change that moved the poll to run *near* midnight
in either zone would need to revisit this, but that is an existing risk of `EffectiveDate`
itself (`mapping.go`), not one this tier introduces.

### D-B8 (D5) — `minFlagDistanceKm` is a named constant

```go
// minFlagDistanceKm is the D5 gap-detection threshold: a day whose consumed pct
// computes to exactly zero is flagged only when the vehicle demonstrably drove
// more than this many km that day. Below it, "zero consumed, zero driven" is a
// plausible parked day, not a data gap.
const minFlagDistanceKm = 10.0
```

Never inlined as a literal in the comparison, per the leader's dispatch instruction and
D5's own wording.

### D-B9 (D7a) — `telemetry.MissingChargingType` is reused directly; no duplicate vocabulary

`DayConsumption.MissingChargingType` is typed `telemetry.MissingChargingType` (the exact
type + the two constants `telemetry.MissingChargingTypeManual` /
`telemetry.MissingChargingTypeSupercharger`, both already shipped in tier 1) — `battery`
already imports `internal/telemetry`, so this costs no new import and creates no second
type a caller must map between. `cmd/poller` passes `DayConsumption.MissingChargingType`
straight through into `telemetry.ChargeGap.MissingChargingType` with no conversion.

### D-B10 — Database Changes: none

**Explicit statement, as required even when the `database` design gate does not trip:**
this change adds no migration, no table, no column, no index. `internal/battery` owns no
database (D15, its own `AGENTS.md`'s "Data ownership" section). `ConsumedByDay` issues
three reads against tables and indexes tiers 1–2 already built and indexed for exactly
these access patterns; it writes nothing. The `database` design gate therefore does not
apply to this change.

### D-B11 (D14, D14a) — Carrying forward the known detection blind spot

Unchanged from the roadmap: `Flagged` catches only a *negative* corrected figure, or a
*zero* figure alongside `DistanceTraveledKmCalc > minFlagDistanceKm`. A charge that only
*partially* offsets a day's driving (e.g., a Supercharger session with NULL percentages,
D14) leaves a positive, plausible-looking, silently understated `ConsumedPct` that
`Flagged` never catches — `internal/telemetry`'s `charge_gaps` ledger this tier's flagged
days feed into is a **lower bound** on days needing attention, never the complete set.
This tier does not attempt to close that gap (out of scope, D14) and does not obscure it:
this section exists so a future reader of this design does not rediscover the limitation
and mistake it for an oversight.

---

## `cmd/poller` wiring (specification for the leader's task, not implemented here)

`cmd/poller/main.go` currently wires only `telemetry.Collector` (see the file's own
header comment: "all collection logic lives in internal/telemetry"). This tier's
detection work adds, to the **`--once` and nightly scheduled paths alike**, a step that
runs **after** `collector.CollectAll(ctx)` (or, in scheduled mode, after each cycle the
scheduler runs) succeeds — never before, since detection needs the night's freshly
written snapshot to exist (D4: "after snapshots are written").

**Additional constructors to wire** (all already exist; none of these are added by this
tier):

```go
superchargerReader := telemetry.NewSuperchargerReader(pool)
manualReader        := manualcharge.NewReader(pool)
gapWriter           := telemetry.NewGapWriter(pool)
batteryReader       := battery.NewReader(telemetry.NewReader(pool), superchargerReader, manualReader, acct, battery.DefaultWindow)
```

(`battery.NewReader`'s `window` argument is required by its signature but unused by
`ConsumedByDay` — only `RecentEfficiency` reads it. Passing `battery.DefaultWindow` here
is a harmless, honest placeholder; `cmd/poller` never calls `RecentEfficiency`.)

**The per-cycle reconciliation step**, run once per vehicle after collection:

```go
// GapReconciliationWindow (battery.go, this tier): 30 days, matching
// battery.DefaultWindow — generous enough to catch a manual-entry backfill
// days after the fact, cheap enough to recompute nightly (≤30 snapshot rows
// + a handful of charge rows per vehicle, per D2's read-heavy tolerance).
end := dayUTC(time.Now()).AddDate(0, 0, -1) // "yesterday" — today's EffectiveDate
                                              // is not captured until TOMORROW's poll
                                              // (same reasoning as roadmap D11)
start := end.AddDate(0, 0, -int(battery.GapReconciliationWindow.Hours()/24)+1)

vehicles, err := acct.AllRegisteredVehicles(ctx)
if err != nil {
    log.Printf("gap reconciliation: listing vehicles: %v", err)
    return // whole-cycle failure, mirrors CollectAll's own enumeration-failure handling
}

for _, v := range vehicles {
    days, err := batteryReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)
    if err != nil {
        log.Printf("gap reconciliation: vehicle %d: consumed-by-day: %v", v.TeslaID, err)
        continue // per-vehicle isolation, mirrors CollectAll's own per-vehicle error handling
    }

    var flagged []telemetry.ChargeGap
    for _, d := range days {
        if !d.Flagged {
            continue
        }
        flagged = append(flagged, telemetry.ChargeGap{
            AccountID:            v.AccountID,
            TeslaID:              v.TeslaID,
            VIN:                  v.VIN,
            Date:                 d.Date,
            MissingChargingType:  d.MissingChargingType,
        })
    }

    if err := gapWriter.ReconcileWindow(ctx, v.AccountID, v.TeslaID, start, end, flagged); err != nil {
        log.Printf("gap reconciliation: vehicle %d: reconcile window: %v", v.TeslaID, err)
        continue
    }
}
```

Every `flagged` element's `Date` is guaranteed inside `[start, end]` by construction
(`deriveConsumedByDay` never emits a day outside its caller-supplied window, D-B4/D-B3),
so `ReconcileWindow`'s own defense-in-depth window-membership check (tier 1's design)
never rejects a well-formed call from this wiring.

**Error handling:** per-vehicle isolation (one vehicle's failure does not abort another's
reconciliation), mirroring `telemetry.Collector.CollectAll`'s own documented isolation
pattern (`main.go`'s own comment: "CollectAll returns nil for per-vehicle failures"). A
failure to enumerate vehicles at all (`AllRegisteredVehicles` erroring) is treated as a
whole-cycle failure, also mirroring `CollectAll`'s existing shape. Reconciliation failures
are logged, never fatal — a missed reconciliation self-heals on the next nightly run
(D7b: `ReconcileWindow` is always safe to retry from scratch, since `flagged` is freshly
recomputed every time).

---

## Go-Level Surface

### `battery.go` additions

```go
// GapReconciliationWindow is the rolling window cmd/poller re-derives and
// reconciles against telemetry's charge_gaps ledger every nightly run (D4/D4a,
// D7b) — 30 days: generous enough to catch a manual-entry backfill days after
// the fact, cheap enough to recompute nightly (bounded by ~30 snapshot rows
// and a handful of charge rows per vehicle, per the read-heavy Performance-
// Profile's tolerance for off-hours write-path cost).
const GapReconciliationWindow = 30 * 24 * time.Hour

type Reader interface {
    RecentEfficiency(ctx context.Context, accountID uuid.UUID, teslaID int64) (Efficiency, bool, error)

    // ConsumedByDay returns the corrected per-day battery-consumed percentage
    // (D13) for the given vehicle over [start, end], both whole UTC-midnight-
    // bounded calendar days, end inclusive (matching this platform's HTTP
    // date-filter convention). Recomputed on every call -- no cache, no
    // stored state (D2). The result is SPARSE: it contains one entry per
    // calendar day that has a computable value, and NO entry for a day that
    // does not (no snapshot exists for that day, or the day is the vehicle's
    // very first-ever snapshot -- D5a). Absence from the returned slice IS
    // the "no data" signal (design.md D-B2) -- callers bucket by Date exactly
    // like internal/gateway/handlers/history.go's existing
    // buildOdometerChart/buildBatteryChart already do for the odometer and
    // battery-level charts.
    //
    // This port performs no window-size validation or capping of its own
    // (mirrors telemetry.Reader.SnapshotsByVehicleBetween's identical
    // stance) -- keeping a window reasonable is the caller's job (the HTTP
    // handler in tier 4, the fixed GapReconciliationWindow in cmd/poller).
    ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error)
}

// DayConsumption is one calendar day's corrected battery-consumed result --
// our own domain model, no vendor suffix (ai/architecture.md §6).
type DayConsumption struct {
    // Date is the calendar day this entry describes -- the underlying
    // telemetry.Snapshot's own EffectiveDate, UTC-midnight, NEVER shifted or
    // re-attributed (design.md D-B3, roadmap D1). For a multi-day span
    // (DaysSpanned > 1), this is the single day the one available row
    // represents; every day between the predecessor and this one has no
    // entry at all (design.md D-B2/D-B3, roadmap D8).
    Date time.Time
    // ConsumedPct is battery_used_pct_calc + sum(end_battery_pct -
    // start_battery_pct) across every matched charge event from BOTH
    // sources (roadmap D13). Raw and unrounded; MAY be negative or exactly
    // zero -- callers decide how to render that (design.md D10 is a
    // gateway/tier-4 concern; this port never clamps or hides it).
    ConsumedPct float64
    // DistanceKm is the row's DistanceTraveledKmCalc (0 when telemetry
    // stored NULL for it -- see telemetry.go: only possible when
    // BatteryUsedPctCalc is also NULL, which this port already skips via
    // D5a, so DistanceKm is effectively always populated on an emitted
    // entry).
    DistanceKm float64
    // Flagged is the D5/D5a gap-detection result: true when ConsumedPct < 0,
    // or when it is exactly 0 while DistanceKm > minFlagDistanceKm.
    Flagged bool
    // MissingChargingType is the D7a inferred source, valid only when
    // Flagged is true (zero value "" otherwise). Reuses
    // telemetry.MissingChargingType directly -- no duplicate vocabulary
    // (design.md D-B9).
    MissingChargingType telemetry.MissingChargingType
    // DaysSpanned is the row's own DaysSpannedCalc (never re-derived) -- 1
    // for a normal night-to-night poll, >1 signals a multi-day span (D8) a
    // caller may want to mark distinctly.
    DaysSpanned int
}
```

### `consumed.go` (new file)

```go
package battery

import (
    "time"

    "github.com/cristianpena/magus-tesla-api/internal/manualcharge"
    "github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// minFlagDistanceKm -- see design.md D-B8.
const minFlagDistanceKm = 10.0

// dayUTC truncates t to its UTC calendar-day midnight, mirroring the
// gateway's own effectiveDayUTC bucketing (internal/gateway/handlers/history.go)
// so a day boundary computed here and one computed on the dashboard side
// always agree (design.md D-B7).
func dayUTC(t time.Time) time.Time {
    t = t.UTC()
    return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// deriveConsumedByDay is the pure D13 derivation, fully offline: no I/O, only
// plain telemetry.Snapshot / telemetry.SuperchargerSession / manualcharge.Entry
// values in, []DayConsumption out. snapshots MUST be ordered EffectiveDate
// ascending and MUST include the one-day lookback row at start-1 when it
// exists (design.md D9a; ConsumedByDay's caller, reader.go, guarantees both).
func deriveConsumedByDay(snapshots []telemetry.Snapshot, sessions []telemetry.SuperchargerSession, entries []manualcharge.Entry, start, end time.Time) []DayConsumption {
    out := make([]DayConsumption, 0, len(snapshots))
    for i := 1; i < len(snapshots); i++ {
        prev, cur := snapshots[i-1], snapshots[i]
        day := dayUTC(cur.EffectiveDate)
        if day.Before(start) || day.After(end) {
            continue
        }
        if cur.BatteryUsedPctCalc == nil {
            continue // D5a: no predecessor claim for this row, skip
        }

        chargePct := sumSuperchargerPctBetween(sessions, prev.CapturedAt, cur.CapturedAt) +
            sumManualPctBetween(entries, dayUTC(prev.EffectiveDate), day)

        consumed := float64(*cur.BatteryUsedPctCalc) + chargePct

        var distanceKm float64
        if cur.DistanceTraveledKmCalc != nil {
            distanceKm = *cur.DistanceTraveledKmCalc
        }

        flagged := consumed < 0 || (consumed == 0 && distanceKm > minFlagDistanceKm)

        var missingType telemetry.MissingChargingType
        if flagged {
            missingType = inferMissingChargingType(sessions, prev.CapturedAt, cur.CapturedAt)
        }

        daysSpanned := 1
        if cur.DaysSpannedCalc != nil {
            daysSpanned = *cur.DaysSpannedCalc
        }

        out = append(out, DayConsumption{
            Date:                 day,
            ConsumedPct:          consumed,
            DistanceKm:           distanceKm,
            Flagged:              flagged,
            MissingChargingType:  missingType,
            DaysSpanned:          daysSpanned,
        })
    }
    return out
}

// sumSuperchargerPctBetween sums (EndBatteryPct - StartBatteryPct) across
// every session whose ChargeStopDateTime falls in [from, to) -- D12's
// interval rule, generalized to any predecessor/current CapturedAt pair
// (design.md D-B5). Sessions with either percentage NULL (D14: always true
// today) contribute 0 -- their absence is what inferMissingChargingType
// flags below, not something this function should estimate.
func sumSuperchargerPctBetween(sessions []telemetry.SuperchargerSession, from, to time.Time) float64 {
    var total float64
    for _, s := range sessions {
        if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
            continue
        }
        if s.StartBatteryPct != nil && s.EndBatteryPct != nil {
            total += float64(*s.EndBatteryPct - *s.StartBatteryPct)
        }
    }
    return total
}

// inferMissingChargingType implements D7a: SUPERCHARGER when a session in
// [from, to) exists with either battery percentage NULL (the exact record
// needing a fill is already known); MANUAL otherwise. Only called once a day
// is already known to be Flagged.
func inferMissingChargingType(sessions []telemetry.SuperchargerSession, from, to time.Time) telemetry.MissingChargingType {
    for _, s := range sessions {
        if s.ChargeStopDateTime.Before(from) || !s.ChargeStopDateTime.Before(to) {
            continue
        }
        if s.StartBatteryPct == nil || s.EndBatteryPct == nil {
            return telemetry.MissingChargingTypeSupercharger
        }
    }
    return telemetry.MissingChargingTypeManual
}

// sumManualPctBetween sums (EndBatteryPct - StartBatteryPct) across every
// entry whose ChargedOn falls in (fromDay, toDay] -- D12's date match,
// generalized the same way as sumSuperchargerPctBetween for D8 spans
// (design.md D-B6). Entries with either percentage nil (optional fields on
// manualcharge.Entry) contribute 0, symmetric with the Supercharger
// nil-handling above.
func sumManualPctBetween(entries []manualcharge.Entry, fromDay, toDay time.Time) float64 {
    var total float64
    for _, e := range entries {
        d := dayUTC(e.ChargedOn)
        if !d.After(fromDay) || d.After(toDay) {
            continue
        }
        if e.StartBatteryPct != nil && e.EndBatteryPct != nil {
            total += float64(*e.EndBatteryPct - *e.StartBatteryPct)
        }
    }
    return total
}
```

### `reader.go` addition

```go
func (r *reader) ConsumedByDay(ctx context.Context, accountID uuid.UUID, teslaID int64, start, end time.Time) ([]DayConsumption, error) {
    lookbackStart := start.AddDate(0, 0, -1) // D9a

    snapshots, err := r.telemetry.SnapshotsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end)
    if err != nil {
        return nil, err
    }

    // Tail over-fetch by one day -- design.md D-B5: the last row's CapturedAt
    // can land in the early hours of end+1; deriveConsumedByDay re-filters
    // every session against each row-pair's own precise CapturedAt interval,
    // so the extra day is harmless.
    sessions, err := r.supercharger.SuperchargerSessionsByVehicleBetween(ctx, accountID, teslaID, lookbackStart, end.AddDate(0, 0, 1))
    if err != nil {
        return nil, err
    }

    entries, err := r.manual.ListEntriesByVehicleBetween(ctx, accountID, teslaID, start, end)
    if err != nil {
        return nil, err
    }

    return deriveConsumedByDay(snapshots, sessions, entries, start, end), nil
}
```

No change to `NewReader`, the `reader` struct fields, or any existing method.

### How tier 4 renders a flagged day and a no-data day (for the next tier's design, not built here)

Mirroring `buildOdometerChart`/`buildBatteryChart` exactly: bucket `ConsumedByDay`'s
result into `byDay := make(map[time.Time]battery.DayConsumption, len(days))`, then iterate
`d := start; !d.After(end); d = d.AddDate(0, 0, 1)`. A day **absent** from `byDay` renders
as today's existing "no snapshot" empty labeled bar (`Present: false`) — no-data. A day
**present** with `Flagged == false` renders a normal bar at `ConsumedPct`. A day present
with `Flagged == true` renders per D10 — a zero-height bar with a distinct warning marker,
using `MissingChargingType` to pick the bilingual tooltip ("possible missing Supercharger
record" vs. "possible missing manual entry"), regardless of `ConsumedPct`'s actual sign. A
day present with `DaysSpanned > 1` additionally carries its own span marker (independent
of `Flagged` — D8's multi-day case is not itself a data-quality flag unless its own
`ConsumedPct` also fails D5's test).

---

## Test Contract (authored before implementation, binding)

Per `ai/go-conventions.md`'s Test-Execution-Policy, these are written but not run by the
worker; `go vet ./...` compiles them as a signature-drift signal. All of this module's
tests are offline (no `DATABASE_URL`, no Docker — `AGENTS.md`'s own "Testing" section).

### Pure-function tests (`consumed_test.go`) — no fakes needed

**(a) Roadmap's own verified case 1 — single session, single day → 11**
- **Given** `prev` (EffectiveDate D0, CapturedAt `T0`), `cur` (EffectiveDate D1,
  CapturedAt `T1`, `BatteryUsedPctCalc = 22 - 73 = -51`, `DaysSpannedCalc = 1`).
- **And** one Supercharger session, `StartBatteryPct = 18`, `EndBatteryPct = 80`,
  `ChargeStopDateTime` inside `[T0, T1)`.
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, nil, D0, D1)` is
  called.
- **Then** exactly one entry is returned, `Date = D1`, `ConsumedPct = -51 + (80-18) = 11`,
  `Flagged = false`.

**(b) Roadmap's own verified case 2 — two sessions, single day → 5 (not −25, not 15)**
- **Given** `prev`/`cur` as above but `BatteryUsedPctCalc = 30 - 75 = -45`.
- **And** two Supercharger sessions inside `[T0, T1)`: session1 `20→50`
  (`ChargeStopDateTime` earlier), session2 `60→80` (`ChargeStopDateTime` later).
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = -45 + (30 + 20) = 5`. Also assert, as a regression guard: this is
  **not** `-45 + 20 = -25` (latest-session-only) and **not** `-45 + 60 = 15`
  (first-start-to-last-end) — the sum-both-sessions formula (D13) is the one under test.

**(c) A day flagged negative**
- **Given** `BatteryUsedPctCalc = 50 - 60 = -10`, no charge events matched (chargePct = 0).
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = -10`, `Flagged = true`, `MissingChargingType = MANUAL` (no
  Supercharger session present in the interval).

**(d) A day flagged zero-with-distance**
- **Given** `BatteryUsedPctCalc = -20`, one manual entry inside `(prevDay, curDay]` with
  `StartBatteryPct = 30`, `EndBatteryPct = 50` (contributes +20), `DistanceTraveledKmCalc =
  50.0`.
- **When** `deriveConsumedByDay` is called.
- **Then** `ConsumedPct = 0`, `50.0 > minFlagDistanceKm (10)`, so `Flagged = true`,
  `MissingChargingType = MANUAL`.

**(e) Zero-with-distance ≤ threshold must NOT flag (boundary at exactly 10)**
- Two subcases, same `ConsumedPct = 0` setup as (d): `DistanceTraveledKmCalc = 10.0`
  (exactly the threshold — `>` is strict, so **not** flagged) and `= 5.0` (well under).
- **Then**, in both subcases, `Flagged = false`.

**(f) NULL `BatteryUsedPctCalc` is skipped, never flagged**
- **Given** `cur.BatteryUsedPctCalc = nil` (the account's first-ever snapshot).
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, nil, nil, start, end)` is
  called (`prev` present so the loop still reaches `i=1`; `prev` itself is index 0, never
  emitted regardless).
- **Then** the returned slice is empty — no entry for `cur`'s day at all, not an entry
  with `Flagged = false` and a zero `ConsumedPct`.

**(g) Multi-day span (D8) — one bar, intervening days absent, charges summed regardless**
- **Given** `prev` (EffectiveDate 2026-08-10, CapturedAt `T0`), `cur` (EffectiveDate
  2026-08-13, CapturedAt `T1`, `DaysSpannedCalc = 3`, `BatteryUsedPctCalc = -30`).
- **And** two charge events inside `[T0, T1)` — one Supercharger session (+15) and one
  manual entry dated 2026-08-12 with real percentages (+25) — summing to +40.
- **When** `deriveConsumedByDay([]telemetry.Snapshot{prev, cur}, sessions, entries,
  2026-08-01, 2026-08-31)` is called.
- **Then** exactly **one** entry is returned (not three): `Date = 2026-08-13`,
  `DaysSpanned = 3`, `ConsumedPct = -30 + 40 = 10`, `Flagged = false`. No entry exists for
  2026-08-11 or 2026-08-12 (no snapshot rows for those days in the input at all — nothing
  in `deriveConsumedByDay` needs to special-case them).

**(h) Supercharger session boundary — just inside vs. just outside the interval (D12)**
- **Given** `prev.CapturedAt = T0`, `cur.CapturedAt = T1`.
- **And** session A with `ChargeStopDateTime == T0` (exactly the lower bound) and session B
  with `ChargeStopDateTime == T1` (exactly the upper bound).
- **When** `sumSuperchargerPctBetween(sessions, T0, T1)` is called directly.
- **Then** session A's delta **is** included (`[from, to)` is lower-inclusive) and session
  B's delta is **not** (upper-exclusive — it belongs to the *next* row pair instead).

**(i) Statelessness / the D7b delete-on-resolve precondition**
- **Given** the same `prev`/`cur` pair with `BatteryUsedPctCalc = -10` and no charge data
  (→ `Flagged = true`, per (c)).
- **When** `deriveConsumedByDay` is called again with the identical snapshots but now a
  manual entry contributing +10 added to `entries` (the user backfilled the missing
  record).
- **Then** the second call's `Flagged = false`, `ConsumedPct = 0` (assuming distance ≤ 10)
  or a legitimately non-negative value — proving the derivation is a pure function of its
  inputs with no memory of the first call (D2). This is the precondition that makes
  `telemetry.GapWriter.ReconcileWindow`'s own delete-on-resolve lifecycle (D7b, already
  tested in tier 1) work for free at the `cmd/poller` layer: the next nightly run's
  `flagged` slice simply omits this day, and `ReconcileWindow` deletes the row.

### Port-wiring tests (`reader_test.go`, extended) — fakes, no arithmetic re-verification

These prove `ConsumedByDay` fetches the right windows and propagates arguments/errors
correctly; the arithmetic itself is already proven by (a)–(i) above, so these use trivial
fixtures.

**(j) Fetch windows: lookback on snapshots, tail over-fetch on sessions, plain window on entries**
- **Given** `start = 2026-08-10`, `end = 2026-08-20`.
- **When** `(*reader).ConsumedByDay(ctx, accountID, teslaID, start, end)` is called.
- **Then** the fake `telemetry.Reader` recorded `SnapshotsByVehicleBetween` called with
  `(2026-08-09, 2026-08-20)`; the fake `SuperchargerReader` recorded
  `SuperchargerSessionsByVehicleBetween` called with `(2026-08-09, 2026-08-21)`; the fake
  `manualcharge.Reader` recorded `ListEntriesByVehicleBetween` called with
  `(2026-08-10, 2026-08-20)`.

**(k) accountID/teslaID scoping reaches all three ports** — mirrors
`TestRecentEfficiency_AccountIDScoping_PassedToEveryPort`'s existing pattern, for the
three ports `ConsumedByDay` actually calls.

**(l) Error propagation** — one subtest per port (`telemetry`, `supercharger`, `manual`):
an error from any one port is returned via `errors.Is`, unwrapped, matching the existing
`TestRecentEfficiency_*Error_Propagates` pattern.

**Fake updates required (same file, not new fakes):** `fakeTelemetryReader.SnapshotsByVehicleBetween`,
`fakeSuperchargerReader.SuperchargerSessionsByVehicleBetween`, and
`fakeManualReader.ListEntriesByVehicleBetween` currently `panic()` (added defensively by
tiers 1–2's own cross-module fake-update tasks, since `RecentEfficiency` never calls
them). This tier changes all three from a panicking stub to a functional one (record
args, return canned data/err) — safe, because no existing `RecentEfficiency` test
exercises any of the three `...Between` methods, so none of tiers 1–2's tests observe the
change.

---

## Migration Plan (implementation order for this module's worker(s))

1. `internal/battery/battery.go` — `GapReconciliationWindow` const,
   `Reader.ConsumedByDay` method addition, `DayConsumption` type. No dependencies. Breaks
   `go build ./...` until step 3 lands (expected, mirrors tiers 1–2's own precedent).
2. `internal/battery/consumed.go` (new file) — `dayUTC`, `minFlagDistanceKm`,
   `deriveConsumedByDay`, `sumSuperchargerPctBetween`, `inferMissingChargingType`,
   `sumManualPctBetween`. Depends on step 1 (`DayConsumption` type must exist).
3. `internal/battery/reader.go` — `(*reader).ConsumedByDay`. Depends on steps 1–2.
4. `internal/battery/consumed_test.go` (new file) — test-contract scenarios (a)–(i).
   Depends on step 2 (pure functions, no fakes).
5. `internal/battery/reader_test.go` (extended) — un-panic the three `...Between` fakes;
   add scenarios (j)–(l). Depends on step 3.
6. `internal/battery/AGENTS.md` — document `ConsumedByDay`/`DayConsumption` in the
   module's "Public interface" section. Depends on step 1 (interface must be final).
7. Verification: `go build ./...`, `go vet ./...`, `gofmt -l`,
   `openspec validate RM28-battery-derive-consumed-per-day --strict`.

**LEADER-OWNED, separate task, after this module's work lands:** `cmd/poller/main.go`
wiring per the "`cmd/poller` wiring" section above — outside this module's sandbox.

Steps 1→2→3 are a strict chain (same reason tiers 1–2's own migration plans chain their
type/port additions before their implementations: the type must exist before the pure
functions can reference it, and the pure functions must exist before the port method can
call them). Steps 4 and 6 can run in parallel with each other once their own single
dependency (2, 1 respectively) lands, since they touch disjoint files. Step 5 depends on 3
only.

## Risks / Trade-offs

- **D-B4's window-boundary skip is a narrow, accepted precision gap**, not a bug: a
  pre-existing data gap crossing exactly `start−1` causes one day to be silently absent
  from that specific call's output, self-correcting for any window whose `start−1` does
  have a snapshot. Accepted for the same reason D5a itself is accepted — never fabricate
  an interval bound this module cannot actually observe.
- **The Supercharger tail over-fetch (D-B5) reads one extra day's rows on every call.**
  Negligible: Supercharger session volume per vehicle is bounded by real-world charging
  frequency, not polling cadence (tier 1's own design.md makes the identical argument for
  why that port carries no defensive `LIMIT`).
- **D14a's blind spot is unchanged and explicitly not addressed here** — see D-B11. Any
  future ticket that wants to close it needs the Supercharger battery-% verification UI
  (backlog entry 11 item 1), out of scope for this entire roadmap.
