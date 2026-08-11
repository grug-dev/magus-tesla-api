# Design: gateway-battery-consumed-chart

## Context

Follow-up to MAG-6 / RM7 (`openspec/roadmaps/RM7-history-graph-improvements.md`). The dashboard's
"Battery history" chart (`buildBatteryChart`, `internal/gateway/handlers/history.go`) currently
plots the **absolute battery level %** of each snapshot. The user wants it to plot **battery %
consumed per day** — the drop from the previous day's level to today's — the same "delta, not
cumulative value" pattern the sibling "Odometer history" chart (`buildOdometerChart`, RD5 of
`2026-07-31-gateway-dashboard-history-charts`) already established.

**Claim verified against the code** (per the dispatch's instruction not to assume): the existing
`DashboardHistoryFragment` handler computes
`since := startOfDay(time.Now()).AddDate(0, 0, -days)` and passes it straight to
`telemetry.Reader.SnapshotsByVehicleSince`. For `days = N` this window covers `[today − N days,
today]` inclusive of both ends, i.e. **N + 1 calendar days**, so the reader already returns up to
`N+1` daily snapshots whenever that much history exists — exactly the "one extra, older baseline
snapshot" `buildOdometerChart` relies on for its first delta. **No reader change, no new query, no
new DB object, and no handler-level windowing change are needed** to get a real delta for the
first bar. This is not a new capability; `buildBatteryChart` was simply not using the extra
snapshot the port was already handing it.

## Goals / Non-Goals

**Goals**
- `buildBatteryChart` bars represent **% battery consumed that day** (a clamped delta), computed
  the same way `buildOdometerChart` computes km driven — mirrored, not reinvented.
- The **first** rendered bar is a real delta against the snapshot immediately before it (the
  `days+1`-th, oldest snapshot in the window), never a stub or a dropped bar.
- The tooltip's **content and wording do not change** — it keeps showing the absolute level % and
  range km of that day's own snapshot.
- The **odometer chart is untouched** — no shared helper is refactored in a way that changes its
  behavior; if a helper is extracted, `buildOdometerChart` calls it with the same arguments it
  uses today and produces byte-identical output.

**Non-Goals**
- Resolving how a charging day should be represented — captured as an explicit Open Question
  below, not decided here.
- Any DB, telemetry, manualcharge, or Supercharger-session change. This change reads nothing new.
- Renaming the "Battery history" card title or changing chart chrome/legends (left to the
  implementer's judgement in tasks.md; not a design decision this document needs to gate).

## Decisions

### D1 — Bar = consumed %, mirroring `buildOdometerChart`'s shape (reversed operand order)

`buildBatteryChart` is rewritten to follow the identical structure as `buildOdometerChart`:

```go
want := days + 1
start := 0
if len(snaps) > want {
    start = len(snaps) - want
}
pts := snaps[start:]

if len(pts) < 2 {
    return fragments.HistoryChart{Empty: true}
}

for i := 1; i < len(pts); i++ {
    d := pts[i-1].BatteryLevelPct - pts[i].BatteryLevelPct // reversed vs. odometer: consumption is a decrease
    if d < 0 {
        d = 0 // provisional clamp — see Open Questions
    }
    ...
}
```

The only semantic difference from `buildOdometerChart`'s delta line (`pts[i].OdometerKm -
pts[i-1].OdometerKm`) is the **operand order**: odometer is monotonically increasing so "today
minus yesterday" is the natural driven-distance delta; battery level *drops* on consumption, so
the natural consumed-% delta is "yesterday minus today." Getting this backwards would make every
normal (non-charging) day render as a negative-clamped-to-zero bar — the opposite of what a
consumption chart should show — so this is called out explicitly rather than left to be inferred
from the sign.

### D2 — `days+1` baseline snapshot (no reader/window change)

As verified in Context above, `since` already spans `days+1` calendar days, so `want := days + 1`
(copied verbatim from `buildOdometerChart`) gets a real baseline for the first bar with **zero**
changes outside `internal/gateway`. This directly satisfies the "first bar must be a real delta,
never a stub" constraint.

### D3 — Height scaling: % of the window's max consumed, not 0–100 absolute

Today, `buildBatteryChart` sets `HeightPct = s.BatteryLevelPct` directly (already 0–100, so no
scaling needed for an absolute-level chart). A consumed-% delta is a different quantity with no
fixed ceiling — a single big driving day could consume anywhere from a few percent to (at the
extreme) close to 100%, but most days will be much smaller. Rendering `HeightPct = delta` directly
would make ordinary days look like flat, unreadable slivers. So this change adopts
`buildOdometerChart`'s existing scaling pattern instead: track `maxPct` (the largest single-day
consumed delta in the window) and set each bar's `HeightPct = round(delta / maxPct * 100)`, so the
window's largest-consumption day is always the full-height reference bar — consistent with how
the odometer chart already scales `HeightPct` against `maxKm`.

### D4 — Empty guard: `len(pts) < 2` (was `len(pts) == 0`)

A delta needs two points; a single snapshot can no longer produce a meaningful bar (it would
either be forced to 0 or fabricated). The guard changes from "empty when zero snapshots" to "empty
when fewer than two snapshots," matching `buildOdometerChart`'s existing guard exactly. This is a
**behavior change** versus today: a vehicle with exactly one stored snapshot showed one (trivially
"honest," but not delta-meaningful) 100%-height bar before this change; after this change it shows
the empty-state placeholder instead, same as the odometer chart already does in that situation.
This is intentional — a lone snapshot cannot express "consumed," only "was at."

### D5 — Tooltip stays exactly as it is today (accepted mismatch)

The tooltip continues to read `fmt.Sprintf("%s · %d%% · %s km range", label, s.BatteryLevelPct,
formatKmRaw(s.BatteryRangeKm))`, built from the **current** snapshot `pts[i]`'s own absolute
fields — unchanged from today's code, and explicitly NOT rebuilt from the delta. This is a direct,
explicit user instruction (see proposal.md "Why").

**Honest trade-off, stated because the codebase's own convention (`ai/htmx-conventions.md`
"Component & fragment rules" / gateway `AGENTS.md` "Charts") is that a chart's visual encoding and
its tooltip describe the same fact** — every other chart in this module (odometer: bar = km
driven, tooltip = km driven + cumulative; Supercharger: bar = kWh that month, tooltip = kWh that
month) keeps that invariant. This change breaks it on purpose: the bar will show, e.g., "24"
(consumed) while the tooltip on the same bar reads "69% · 350 km range" (today's absolute level
and range) — two different quantities on one visual element, with no connecting label ("consumed:
24%") anywhere in the tooltip string. A user unfamiliar with this decision could reasonably read
the tooltip's "69%" as an explanation of the bar's height and be confused. This was raised and the
user chose to accept it rather than change the tooltip; recorded here per this project's decision-
documentation convention, not to relitigate it.

## Open Questions — charging days go negative (NOT resolved by this change)

Battery level **rises** whenever the car charges. Unlike a decreasing odometer (a genuine
data anomaly the existing clamp-to-zero is designed for), a charging day is completely normal —
it will be common, not rare. Clamping every negative delta to 0 (D1's provisional behavior) means
**every day the vehicle charged renders a 0-height "consumed" bar**, and a day that both drove and
charged (e.g. drove 20%, then charged back 15%) under-reports its true 20%-consumed as a
naive 5%-net-delta-clamped-to-zero-only-if-net-negative, or — worse — under D1's actual clamp
logic, a net gain shows 0 even though real consumption happened that day. **Two daily snapshots
alone cannot recover true within-day consumption when both driving and charging occurred.**

This change ships D1's clamp-to-zero as the **provisional, in-scope** behavior (option 1 below) so
the chart has a well-defined, honest-about-its-limits shape today. The three candidates below are
recorded for a follow-up grill-me with the user — **do not silently pick one and build it**:

1. **Clamp negatives to 0 (what this change ships).** Simplest; reuses the exact pattern already
   proven by `buildOdometerChart`'s anomaly clamp; zero new data sources. **Wrong on any charging
   day** — the chart silently under-reports (shows less consumption than actually happened, down
   to zero on a net-charge day), with no visual indication that the number is a floor, not a fact.

2. **Show the signed net change (no clamp).** Honest about what two snapshots can actually tell
   you — the chart becomes "net change in battery %," not "consumed." Trade-offs: (a) it is a
   *different metric* than what the user asked for ("consumed" implies energy spent, not
   net-of-charging change), so the chart's title/tooltip would need to say "net change," not
   "consumed," to avoid misleading; (b) rendering negative bars needs a zero baseline and
   bidirectional bar geometry — a real change to the SVG chart primitive shared with (or forked
   from) `buildOdometerChart`'s single-direction-from-zero bars, which no chart in this module does
   today.

3. **Charge-aware true consumption from stored charge records (the user's preferred direction).**
   `consumed ≈ (previous_level − current_level) + (level added by charging that day)`. This is the
   most correct answer and the most costly.

   **What data actually exists today (verified, not assumed):**
   - `internal/manualcharge` (`Reader.ListEntriesByVehicle(ctx, accountID, teslaID, limit)`)
     returns `[]manualcharge.Entry`, each carrying `ChargedOn time.Time` (the calendar day) and
     `EnergyAddedKWh float64` (required, always present) — **this data exists and is queryable per
     vehicle per day today.**
   - `telemetry.SuperchargerReader` (`SuperchargerSessionsByVehicle(ctx, accountID, teslaID,
     limit)`) returns `[]telemetry.SuperchargerSession`, each carrying `ChargeStartDateTime
     time.Time` and `EnergyKWh *float64` (nullable — derived from fee line items, absent when no
     kWh-billed fee was present) — **this data also exists and is queryable per vehicle today.**
     Contrary to a natural assumption, Supercharger history **is** persisted in this codebase, not
     merely available raw from Tesla.
   - Both energy figures are in **kWh, not %**. Converting kWh added → % level gained requires the
     vehicle's usable pack capacity. `internal/battery/capacity.go` already has exactly this: a
     `car_type → kWh` reference table (`packCapacityKWh`) plus a `capacityFor(carType)
     (kWh float64, known bool)` lookup — used today by `internal/battery.RecentEfficiency`, which
     already performs almost this exact derivation: it sums `SuperchargerSessionsByVehicle` +
     `ListEntriesByVehicle` energy over a window, corrects for pack capacity, and produces a
     derived Wh/km efficiency figure, degrading to `Approximate: true` when the car's capacity is
     unknown rather than fabricating a number (`internal/battery/reader.go`
     `RecentEfficiency`). **This is a direct, working precedent for the exact cross-source
     derivation option 3 needs** — a day-bucketed "energy added" figure is the same shape of
     computation `RecentEfficiency` already does over a rolling window, just re-bucketed by
     calendar day instead of collapsed into one window-wide total.

   **Why this is likely a multi-module change, not a gateway-only one:** the gateway already holds
   `Deps.SuperchargerReader` and `Deps.ManualChargeReader` (both wired in for the Supercharger
   Stats and Charge Log pages), so *technically* nothing new would need to be injected. But this
   project's boundary convention (`internal/gateway/AGENTS.md` "Hard rule: the gateway never owns
   business data... add a method to the owning module instead") treats a multi-source derived
   business metric as domain logic, not presentation logic — and `internal/battery` is precisely
   the module this project created to own that kind of derivation (its own module doc: "owns
   analytics computed FROM other modules' stored data"). The natural implementation of option 3 is
   therefore a **new `battery.Reader` method** (e.g. a day-bucketed consumption series, mirroring
   `RecentEfficiency`'s three-port read + `capacityFor` correction, but returning a per-day series
   instead of one window aggregate), which the gateway would then call — i.e. a change to
   `internal/battery`'s public interface **plus** a change to `internal/gateway` to consume it.
   Per `openspec/config.yaml` ("If the change spans multiple modules, do NOT create one big change
   — create `openspec/roadmaps/<feature-name>.md`... one module-prefixed change per tier"), option
   3 — if chosen — should be scoped as a roadmap with a `battery-*` tier followed by a
   `gateway-*` tier, not folded into this single-module change.

   Additional real complications option 3 would need to resolve (not exhaustive, flagged so the
   follow-up grill-me doesn't rediscover them from scratch): charge sessions don't align cleanly
   to calendar days (a session can start before midnight and end after, or a vehicle can have
   multiple sessions in one day); manual and Supercharger entries can overlap in coverage for the
   same physical charge; and `capacityFor` is model-coarse (trim-exact capacity is a known battery-
   module backlog item), so any kWh→% conversion inherits that same approximation.

**This design does not choose between the three.** Option 1 ships now (D1); options 2 and 3 are
explicitly deferred to a follow-up conversation with the user before either is designed further.

## Data flow (unchanged shape, changed computation inside step 2)

```
days-button click / region load / vehicle-changed → GET /ui/dashboard/history?days=N   [UNCHANGED]
  handler: currentUID → resolveSelectedVehicle(session) → (accountID, teslaID)          [UNCHANGED]
           clamp days → since = startOfDay(now)-days                                    [UNCHANGED]
           telemetry.Reader.SnapshotsByVehicleSince(accountID, teslaID, since)  [1 read] [UNCHANGED]
  buildOdometerChart(snaps, days)  → HistoryChart{Bars: km-driven deltas}                [UNCHANGED — this change touches no line of it]
  buildBatteryChart(snaps, days)   → HistoryChart{Bars: %-consumed deltas}               [CHANGED — was absolute-level bars]
  templ:   #dashboard-history → selector(active=Days) + odometerChart(Odometer) + batteryChart(Battery)  [UNCHANGED template contract]
```

## View model (no field additions required)

`fragments.HistoryBar{ HeightPct int; Tooltip string; Label string }` and
`fragments.HistoryChart{ Bars []HistoryBar; Empty bool; LabelVertical bool }` are unchanged
structurally — this change only changes what values `buildBatteryChart` computes and assigns into
the existing fields. No new view-model field, no `.templ` change is required by this design; the
chart's `<svg>` component already renders whatever `HeightPct`/`Tooltip`/`Label` it is given
generically (verify at implementation time — see tasks.md).

## Risks / Trade-offs

- **[Charging days show a 0-height "consumed" bar]** — accepted for this change (D1's provisional
  clamp); the honest fix (option 3) is deferred, not silently dropped — see Open Questions.
- **[Bar and tooltip describe different quantities on the same element]** — accepted, per explicit
  user direction (D5); flagged as breaking this module's own established chart convention so a
  future reader of this file understands it was deliberate, not an oversight.
- **[A vehicle with exactly one stored snapshot now shows the empty state instead of a bar]** —
  intentional (D4); matches the odometer chart's existing behavior in the same situation.
- **[Height scaling changes visual density]** — with `HeightPct` now relative to the window's max
  consumed day rather than a fixed 0–100 scale, a window with only small, uniform consumption days
  will show more visually "full" bars than the old absolute-level chart did for the same data —
  expected and desired (matches the odometer chart's existing behavior), but worth knowing when
  comparing before/after screenshots.

## Migration Plan

None. No DB, no data. Rollback = revert the gateway commit; `buildBatteryChart` returns to
absolute-level bars.

## Database Changes

**None.** No table, column, index, view, or migration is added, changed, or removed by this
change. The `database` design gate (`openspec/config.yaml`) does not trigger. If implementation
discovers a real need for a DB object while building this, that is out of this design's scope —
stop and raise it for a design-gated follow-up rather than adding one silently.
