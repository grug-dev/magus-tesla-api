## Context

The dashboard history endpoint (`internal/gateway/handlers/history.go`) renders two hand-rolled
SVG bar charts (odometer km/day delta + battery level %) from a per-vehicle snapshot slice fetched
via `telemetry.Reader.SnapshotsByVehicleSince`. The current request flow:

```
GET /ui/dashboard/history?days=N
  → clampHistoryDays(c.Query("days"))               // ∈ {6, 14, 30}, default 6
  → since := startOfDay(time.Now()).AddDate(0, 0, -days)
  → telemetry.Reader.SnapshotsByVehicleSince(ctx, uid, teslaID, since)
  → buildOdometerChart(snaps, days)                  // days+1 snapshots → days deltas
  → buildBatteryChart(snaps, days)                   // last days snapshots → days bars
  → render #dashboard-history fragment
```

RM7 tier 2 (merged to main) already changed the labels: both charts now derive their bar `Label`
and the tooltip date token from `snap.EffectiveDate.Format("01-02")` (MM-DD), the chart carries a
`LabelVertical bool` flag, and `history.templ` renders the labels as an HTML grid row with the
`[writing-mode:vertical-rl]` CSS class for the narrow presets. The view model (`history_vm.go`) is:

```go
type HistoryView struct {
    Days     int          // validated preset
    Presets  []int        // [6, 14, 30]
    Odometer HistoryChart
    Battery  HistoryChart
}
type HistoryChart struct {
    Bars          []HistoryBar
    Empty         bool
    LabelVertical bool // RM7 tier 2: from days preset (labelVerticalFor)
}
type HistoryBar struct {
    HeightPct int
    Tooltip   string
    Label     string // MM-DD from EffectiveDate (RM7 tier 2)
}
```

The dashboard page (`pages/dashboard.templ`) carries two htmx self-refresh surfaces:
- `#dashboard-content` subscribes to `vehicle-changed from:body` and re-fetches `GET /ui/dashboard`
  (the whole bento), which contains `#dashboard-history`.
- `#dashboard-history` self-loads via `hx-trigger="load"` with `hx-get="/ui/dashboard/history?days=6"`
  and swaps its `innerHTML` on preset clicks (each preset button is an `hx-get` with `?days=N`).
The page also has a Refresh `@ui.Button` (`Href: "/dashboard"`, full-page reload) inside the
`@ui.PageHeader` — a vestige from before the htmx self-refresh.

**The MAG-7 problem:** the bar count is driven by the snapshot count the port happens to return, not
by a fixed calendar axis. (a) A missing nightly snapshot shifts every subsequent bar's `EffectiveDate`
label by a day — the axis is *per-snapshot*, not *per-calendar-day*. (b) The odometer chart takes
`days+1` snapshots to produce `days` deltas while the battery chart takes `days` snapshots for `days`
bars, so the two charts' `EffectiveDate` label sets are offset by one snapshot step — the
"odometer 08-01 / battery 08-02" mismatch MAG-7 reports. The fix is a **fixed `[start..end]` date
axis**: one bar per calendar day, identical labels on both charts, missing days = empty labeled
slots, so the axis is calendar-driven and the two charts always align.

Tier 1 of the RM8 roadmap (archived `2026-08-09-RM8-telemetry-between-range-port`) added
`telemetry.Reader.SnapshotsByVehicleBetween(ctx, accountID, teslaID, start, end time.Time)` — a
bounded `[start, end]`-inclusive window read over the existing `(account_id, tesla_id, captured_at)`
ascending index (no DB object added). The port returns snapshots whose **`EffectiveDate` calendar
day** falls in `[start, end]` inclusive, ordered ascending by `EffectiveDate`; it takes **no
lookback parameter** (the gateway supplies any lookback by passing `start-1day` as `start`). This
tier consumes that port.

`Snapshot.EffectiveDate` (added by `telemetry-add-effective-date`, RM7 tier 1) is a full `time.Time`
preserving the time-of-day component, computed once in `rowToSnapshot` as `CapturedAt.AddDate(0,0,-1)`
(`internal/telemetry/mapping.go`). The DB→domain mapper populates it; the gateway reads it as a
plain field, no method call.

## Goals / Non-Goals

**Goals:**
- Switch `GET /ui/dashboard/history` from `?days=N` to `?start=YYYY-MM-DD&end=YYYY-MM-DD` (inclusive
  `end`, default 6-day window, 400 on the four validation failures) — Decision #2.
- Fetch via `telemetry.Reader.SnapshotsByVehicleBetween` with a 1-day lookback (`readStart =
  start-1day`) for the first odometer delta — Decision #4.
- Render a **fixed `[start..end]` date axis** — one bar per calendar day on both charts, identical
  MM-DD labels, missing days = empty labeled bars — so the MAG-7 odometer/battery axis offset is
  eliminated — Decision #4.
- Keep the 6/14/30 preset selector UX, with each button emitting a server-rendered absolute
  `?start=<today-N>&end=<today>` href — Decision #3.
- Remove the dashboard page Refresh button (Decision #5).
- Record the start/end HTTP convention in `internal/gateway/AGENTS.md` (canonical) + a one-line
  pointer in `ai/go-conventions.md` — Decision #6.
- Preserve the RM7-tier-2 invariants: logic-free template (handler pre-computes all bars/labels),
  hand-rolled SVG (RD7), semantic theme tokens (RD7), `LabelVertical` adaptive orientation.

**Non-Goals:**
- NOT touching the telemetry module, the DB schema, any migration, or any index (the `database`
  design-gate is **not triggered** — tier 1 already handled the DB-adjacent port; this is a
  gateway/UI change only).
- NOT adding a custom calendar picker (`<input type=date>`) — Decision #3 explicitly rejects it
  (would break the "no client JS" gateway invariant and is out of ticket scope).
- NOT localizing dates, adding a year to the label, or changing the MM-DD format (RM7 tier 2 settled
  MM-DD from `EffectiveDate`).
- NOT removing `SnapshotsByVehicleSince` (tier 1 kept it additive; this tier stops using it from the
  history handler but does not touch the port).
- NOT changing the `#dashboard-history` route path or the fragment id — only the query contract and
  the rendered axis.
- NOT touching the charges-list Refresh (`fragments/charges_list.templ`) — different feature
  (manual charge records).

## Decisions

### D1 — HTTP param contract (`parseHistoryRange`)

**Decision:** Replace `clampHistoryDays(raw string) int` with
`parseHistoryRange(c *gin.Context) (start, end time.Time, ok bool)` (Decision #2). Parse `start`
and `end` from `c.Query("start")` / `c.Query("end")` as `time.Parse("2006-01-02", raw)` — whole
calendar days, UTC-midnight-bounded (the parser yields a `time.Time` at 00:00 UTC for a YYYY-MM-DD
string). Validation, in order:

1. **Both absent → default window.** When `start` and `end` are both `""`, return
   `end = startOfDay(time.Now())` (today, UTC midnight) and `start = end.AddDate(0, 0,
   -historyRangeWindowDays)` (`today-6`), `ok = true`. This is the self-load and
   anonymous-vehicle-empty-state default.
2. **Either present → both required and well-formed.** If either is non-empty, both must parse as
   `YYYY-MM-DD`; a malformed or missing partner → `ok = false` (400).
3. **`end >= start`** — `end.Before(start)` → `ok = false` (400).
4. **`end <= today`** — `end.After(startOfDay(time.Now()))` → `ok = false` (400) (no future dates;
   the dashboard history reads stored nightly snapshots, there is no data for "tomorrow").
5. **Window ≤ 90 days** — `end.Sub(start).Hours()/24 > 90` → `ok = false` (400) (hard cap to
   protect the hot read path from an unbounded range scan — Performance-Profile: read-heavy).

`historyRangeWindowDays = 6` replaces `defaultHistoryDays = 6` (the default window in days). The
handler, on `!ok`, renders a 400 (an empty `#dashboard-history` placeholder fragment with the
`dashHistoryEmpty()` component, NOT a 500 — graceful degrade consistent with reader errors; the
preset selector is omitted on a 400 since the request was malformed). On `ok`, `readStart =
start.AddDate(0, 0, -1)` and the call is
`h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`.

**Rejected `days` compat:** keeping `?days=N` as a deprecated alias that internally maps to a
window would have left two contracts live on one endpoint and deferred the convention the ticket
explicitly asks for ("date filters … should be handled by start/end date filters"). The gateway is
the only consumer (internal htmx fragment, no external JSON client on this route), so the break is
clean and lockstep with the htmx rewrite. No deprecated alias.

**Rationale:** the four 400 cases are a closed validation vocabulary — a single named location
(`parseHistoryRange`) that fails fast and cheap. The 90-day cap is the read-path protection
(Performance-Profile); the realistic max return under the cap + 1-day lookback is ≤ 91 rows
(parity with tier 1's `LIMIT 400` safety cap, which stays as the DB-side net).

### D2 — 1-day lookback for the first odometer delta, port stays a clean `Between`

**Decision:** The handler computes `readStart = start.AddDate(0, 0, -1)` and passes `(readStart,
end)` to `SnapshotsByVehicleBetween` (Decision #4). The port is a clean `Between(start, end)` —
tier 1 design D5 — and the lookback is purely a gateway concern: it supplies the snapshot whose
`EffectiveDate == start-1` as the km basis for the first odometer delta (the km driven on `start`
= `odometerKm(snapshot@start) - odometerKm(snapshot@start-1)`). The `start-1` snapshot is **never
displayed as a bar** — it only seeds the delta; the displayed axis is `[start..end]` inclusive.

**Why the lookback is 1 day, not 2:** `EffectiveDate = CapturedAt - 1 day`, so a snapshot whose
`EffectiveDate == start-1` is the most recent snapshot *before* the window. Tier 1's
`SnapshotsByVehicleBetween(readStart, end)` returns snapshots with `EffectiveDate ∈ [readStart,
end]` = `[start-1, end]`, so the `start-1` snapshot is included in the returned slice; the
bucketing step (D3) consumes it as the first delta's basis and excludes it from the displayed bars.

**Rationale:** the gateway owns presentation-axis decisions; the port stays a pure data accessor
(Decision #4 / tier 1 D5). Keeping the lookback out of the port signature means a future read
consumer that does not need a delta (e.g. a battery-only view) is not forced to pay for it.

### D3 — Fixed `[start..end]` date axis: bucket by `EffectiveDate`, one bar/day, missing days = empty labeled slots

**Decision:** `buildHistoryView` constructs a fixed axis of `numDays = int(end.Sub(start).Hours()/24)
+ 1` calendar days (`[start, start+1, ..., end]`, inclusive — `end.Sub(start)` in days + 1 because
`end` is inclusive). It allocates `numDays` bar slots, one per calendar day, each labeled with
`day.Format("01-02")` (MM-DD). It then buckets the returned snapshots by `EffectiveDate`'s calendar
day into a `map[time.Time]telemetry.Snapshot` keyed by UTC midnight:
- **Battery chart:** each day's bar is `HeightPct = snap.BatteryLevelPct` if a snapshot exists for
  that day, else `HeightPct = 0` with `Present = false` (an empty labeled slot). The bar's `Label`
  and `Tooltip` come from the day's calendar date (MM-DD) — for a missing day the tooltip reads
  e.g. `"08-05 · no snapshot"`, so the axis is complete and the empty slot is self-explanatory. The
  empty-state (`HistoryChart.Empty == true`) still fires only when **zero** snapshots exist in
  `[start..end]` (not when some days are missing — a partial axis is NOT an empty chart; the
  `dashHistoryEmpty()` placeholder is reserved for "no data at all").
- **Odometer chart:** each day's bar (for day `d ∈ [start, end]`) is the km delta between the
  snapshot for `d-1` and the snapshot for `d`. The `start-1` snapshot (the lookback) is consumed
  solely as the basis for the `start` bar's delta and is **not** allocated a bar slot. A day `d`
  with no snapshot for either `d` or `d-1` renders as `HeightPct = 0` / `Present = false` with its
  MM-DD label and a "no snapshot" tooltip. The odometer chart's `Empty` fires only when there is
  fewer than one snapshot in `[start-1..end]` (i.e. no delta possible — < 2 snapshots total).

Both charts allocate **exactly `numDays` bar slots**, so the two charts' `Label` slices are
identical by construction — the MAG-7 axis offset is eliminated because the axis is now
calendar-driven, not snapshot-driven.

`LabelVertical` is set from `numBars` (the window's day count), not from a `days` preset:
`labelVerticalFor(numBars)` returns `true` when `numBars >= 14`. The 6-day window (default) →
horizontal labels; 14- and 30-day presets → vertical labels. This retargets RM7 tier 2's
`labelVerticalFor` (which took `days int`) to the new axis while preserving the
adaptive-orientation invariant.

**Empty-day bar representation:** add `Present bool` to `HistoryBar` (false for a missing-day
slot). The template renders a present bar as the existing `<rect>` + `<title>`; a missing-day bar
renders as a `<rect>` with `HeightPct=0` (invisible) plus its `MM-DD` label so the axis stays
complete, and the label cell stays in the grid (the HTML-grid label row from RM7 tier 2 already
derives its columns from `len(chart.Bars)`, so a `numDays`-cell label row is automatic). No new CSS
class is needed — the empty-day bar is just a zero-height rect plus an ordinary label cell.

**Rejected "shift labels to fill missing days":** re-labeling a missing day's bar with the next
available snapshot's date would re-introduce the per-snapshot-shift behavior MAG-7 fixes. The fixed
axis must keep missing-day slots labeled with their own date, even if the bar is empty.

**Rejected "odometer chart gets `numDays+1` bars":** the odometer delta is one per *day*, not one
per *snapshot pair*. The `start-1` snapshot seeds the first delta; the displayed bars are exactly
`[start..end]`, one km-driven value per day. This is what makes the two charts share an axis.

### D4 — Preset selector: server-rendered absolute `start`/`end` hrefs (Decision #3)

**Decision:** `HistoryView.Presets` changes from `[]int` to `[]RangePreset`:

```go
type RangePreset struct {
    Label string    // "6 days", "14 days", "30 days" — the button copy (unchanged UX)
    Start time.Time // today-N, UTC midnight
    End   time.Time // today, UTC midnight
    Active bool     // true when (Start, End) matches the requested window
}
```

The handler builds `Presets` at render time: for each `n ∈ {6, 14, 30}`, `End = startOfDay(now)`,
`Start = End.AddDate(0,0,-n)`, `Active = (reqStart.Equal(Start) && reqEnd.Equal(End))` (the default-
window request, where the params were absent, activates the 6-day preset). `historyDaysSelector`
(history.templ) renders each button's `hx-get` as `fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s",
p.Start.Format("2006-01-02"), p.End.Format("2006-01-02"))`, and marks the active preset with
`btn-primary` (unchanged UX: 6/14/30 buttons, identical copy, identical swap target
`#dashboard-history` `innerHTML`). No client JS, no `<input type=date>`.

**Why `RangePreset` carries `time.Time`, not formatted strings:** the templ formats the href at
render time from the `time.Time`, keeping the "template does no date math" rule (the handler
already computed the absolute dates; the template just formats a `time.Time` it was handed —
mirrors how RM7 tier 2's `Label` is a pre-formatted string, except here the formatting is a trivial
`.Format("2006-01-02")` on an already-computed instant, which is presentation not business logic).
If the "no `time.Format` in template" rule is read strictly, the handler can pre-format
`StartStr`/`EndStr` on `RangePreset` instead — see tasks 1.2 and 6.2; the implementer picks the
stricter option.

**`#dashboard-history` self-load:** the `hx-get` in `pages/dashboard.templ` switches from
`?days=6` to a server-rendered `?start=<today-6>&end=<today>`. The dashboard page handler already
builds a `fragments.DashboardData`; it now also computes the default history window's absolute dates
and passes them into the page template so the `hx-get` is server-rendered (not a Go template
`time.Now()` call inside the `.templ` — the page handler pre-computes the string, the template
emits it verbatim, preserving the logic-free-template invariant).

### D5 — Remove only the dashboard page Refresh button (Decision #5)

**Decision:** Delete the `@ui.Button(ui.ButtonProps{Variant: "ghost", Href: "/dashboard", Class:
"btn-sm"}) { Refresh }` block inside the `@ui.PageHeader` in `pages/dashboard.templ` (currently
lines 24-26). The `@ui.PageHeader` keeps its `Title`/`Subtitle` but no longer carries a trailing
button. The htmx self-refresh surfaces (`#dashboard-content` on `vehicle-changed`, `#dashboard-
history` on load + preset click) already cover every refresh path; the Refresh button was a
full-page reload vestige from before those existed.

**Out of scope:** the charges-list Refresh (`fragments/charges_list.templ`) — it belongs to the
manual-charge-records feature and is not touched by this change.

### D6 — Convention doc: `internal/gateway/AGENTS.md` canonical + one-line pointer in `ai/go-conventions.md` (Decision #6)

**Decision:** Add a new "HTTP date-filter convention" section to `internal/gateway/AGENTS.md` (the
module owning all HTTP endpoints, already in every gateway worker's doc-pack) stating:

> **Every date-filtered gateway HTTP endpoint takes `?start=YYYY-MM-DD&end=YYYY-MM-DD` (both whole
> calendar days, UTC-midnight-bounded, `end` inclusive), never a `?days=N` count.** The default
> window when both are absent is endpoint-specific (the dashboard history default is 6 days →
> `today-6..today`). Validate in the handler via a `parseHistoryRange`-style helper: 400 on
> malformed non-ISO dates, `end < start`, `end > today`, or window > 90 days. The bounded window
> protects the hot read path from an unbounded range scan (read-heavy Performance-Profile). This
> convention is established by `RM8-gateway-history-date-range` (Linear MAG-7); the only endpoint
> that follows it today is `GET /ui/dashboard/history` — every future date-filtered endpoint
> follows the same contract so the convention is discoverable, not re-invented.

Add a one-line pointer in `ai/go-conventions.md` under a new "HTTP date-filter convention (gateway)"
bullet pointing to the gateway AGENTS.md section, for cross-cutting discoverability (an agent who
never reads the gateway AGENTS.md still finds the pointer in the always-loaded conventions file).

**Rejected CLAUDE.md always-loaded (unjustified context cost for a single-module convention) and
module-only (not found by agents who never read the gateway AGENTS.md)** — per Decision #6.

Per CLAUDE.md's "Docs track structural change" rule, this convention doc is added **in the same
change** as the code that establishes it (never as a follow-up).

## Read path (named per the perf rule)

**Affected read path:** `gateway.handlers.DashboardHistoryFragment` →
`telemetry.Reader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)` — the dashboard
history hot path. One call per history-chart render (initial load + every preset click); user-
initiated htmx refresh.

- The port (tier 1) is an **indexed forward range scan** over
  `idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at)` ASC, half-open
  `[start_bound, end_bound)` where `start_bound = readStart+1day` and `end_bound = end+2days`
  (tier 1's `EffectiveDate ∈ [readStart, end]` ⇔ `CapturedAt ∈ [readStart+1, end+2)` translation
  lives inside the `dbStore` impl). No sort step (the index serves the ORDER BY), no new DB object.
  `LIMIT 400` safety cap (tier 1 D3).
- This tier adds the **90-day HTTP window cap** (D1) as the read-path protection BEFORE the DB
  call: a wider request is rejected with 400 and never reaches the port. Under the cap + 1-day
  lookback the realistic max return is ≤ 91 nightly rows — far below `LIMIT 400` — so the bounded
  window is strictly cheaper than the open `Since` scan it replaces (tier 1 design D2/D3).

**Bucketing cost:** the handler maps the returned slice (≤ 91 rows) into a `numDays`-slot fixed
axis via a single `O(n)` pass over the snapshots (`map[time.Time]Snapshot` keyed by
`EffectiveDate` UTC midnight), then a single `O(numDays)` pass to build the bars. `numDays ≤ 91`
under the cap. Negligible — a handful of map ops and `time.Format` calls, all in-handler, no
allocation pressure. The template stays logic-free (all heights/labels pre-computed — RM7 tier 2
invariant preserved).

**No other read path, no write path, no collection path is affected.** `SnapshotsByVehicleSince`
(tier 1 kept it additive) is no longer called from the history handler, but remains on the port
and untouched.

## SSR htmx flow

The preset selector's `hx-get` hrefs change from `?days=N` to `?start=<today-N>&end=<today>`
(server-rendered absolute dates — D4). A preset click fires `GET
/ui/dashboard/history?start=...&end=...`; the handler validates via `parseHistoryRange`, fetches
via `SnapshotsByVehicleBetween(readStart, end)`, builds the fixed-axis view model, and renders the
`#dashboard-history` fragment (`renderFragment(c, http.StatusOK, pages.DashboardHistory(v),
"dashboard-history")`), which htmx swaps into `#dashboard-history`'s `innerHTML` (the `hx-target` +
`hx-swap` on each preset button are unchanged from RM5/RM7). The swap replaces both charts AND the
selector in one round-trip, so the active preset's `btn-primary` marking updates with the new
window. No `HX-Trigger` event is needed — the selector is inside the swapped region.

The `#dashboard-history` self-load (`hx-trigger="load"` in `dashboard.templ`) fires the same
fragment on first dashboard render with the default window's absolute dates — this is the initial
population path. A `vehicle-changed` event re-renders `#dashboard-content` (which contains
`#dashboard-history`), and the self-load fires again for the newly-selected vehicle at the default
window. No client JS at any point.

## Why `historyDayPresets` / `defaultHistoryDays` / `clampHistoryDays` are REPLACED, not kept

A closed vocabulary of *day counts* (`historyDayPresets = [6, 14, 30]`) no longer makes sense once
the HTTP contract takes absolute calendar dates: the API surface is `?start=&end=`, not `?days=`,
so the presets become a **UI-only convenience** that translates to absolute dates server-side at
render time (D4 — `RangePreset{Start, End}`). Keeping `clampHistoryDays` would leave a `?days=N`
parser alive on an endpoint whose contract no longer mentions `days` — a dead code path and a
second contract to keep in sync. `defaultHistoryDays = 6` becomes `historyRangeWindowDays = 6` (the
default window's *size in days*, used to compute the default `(start, end)` when both params are
absent — D1). The `historyRangeWindowDays` constant is the single named location for the default
window size; `parseHistoryRange` is the single named location for param validation (D1). This
preserves the AI-efficiency principle (closed, small vocabularies; one named location per concern)
on the new contract.

## No database design-gate trigger

This change does not touch any table, column, index, constraint, view, or migration. The
`database` design-gate (`openspec/config.yaml` rules.design) is **not triggered**. The bounded-window
read port (`SnapshotsByVehicleBetween`) was added by tier 1 (`RM8-telemetry-between-range-port`),
which carried the DB-adjacent design rationale and index plan. This tier is a gateway/UI change:
handler rewrite, view-model + template rewrite, preset rewrite, Refresh removal, convention doc,
codegen, tests. No DB object is added, renamed, or rescoped.

## Risks / Trade-offs

- **[Risk] Breaking the `?days=N` URL.** The only caller is the dashboard's own htmx (the
  `#dashboard-history` self-load and the preset buttons), which this change rewrites in lockstep.
  No external JSON consumer exists on `/ui/...` (the `/api/{version}` surface is a separate
  adapter that does not expose this route). → **Mitigation:** the htmx rewrite and the handler
  rewrite ship in the same change; a stale browser tab that cached the old `?days=6` href will
  get a 400 and re-fetch on the next dashboard load (the self-load re-renders with the new
  server-side default href). Honest break, no deprecated alias.
- **[Risk] A preset button's `Active` flag mis-marks.** `RangePreset.Active` is computed by exact
  `(Start, End)` equality with the requested window. A request with a custom (non-preset) window
  marks **no** preset active — the selector renders with all-ghost buttons, which is correct (the
  user is viewing a window that does not match any preset). → **Mitigation:** the default-window
  request (params absent) activates the 6-day preset; the only way to reach a non-preset window is
  a hand-crafted URL, where no-preset-active is the honest state.
- **[Risk] Missing-day empty bars confuse a reader.** A zero-height bar with a `MM-DD` label and a
  "no snapshot" tooltip looks like "0 km driven" to a casual reader. → **Mitigation:** the tooltip
  explicitly says "no snapshot" for a missing day (D3), and the bar's `Present=false` flag is
  available to the template if a visual distinction (e.g. a dashed empty slot) is later wanted; for
  this change the empty-day bar is a zero-height rect + its label, consistent with the existing
  `dashHistoryEmpty()` aesthetic.
- **[Risk] `parseHistoryRange` lives in the handler file, growing it.** → **Mitigation:** the helper
  is ~30 lines, colocated with `startOfDay` (already there), and is the single named location for
  the param contract (D1) — the closed-vocabulary principle. Extracting it to a separate file would
  be over-abstraction for a single-endpoint validator (anti-AI-efficiency).
- **[Trade-off] The 90-day cap is a gateway-side protection, not a DB-side one.** Tier 1's `LIMIT
  400` is the DB-side net; this tier's 90-day cap is the HTTP-side protection that rejects the
  request before it reaches the port. The two are defense-in-depth: the cap keeps the hot path
  bounded; the LIMIT catches a runaway caller who bypasses the handler (none today, but the LIMIT
  is cheap insurance).

## Migration Plan

None at the infrastructure level. No DB migration, no route path change, no deploy coordination.
Apply: edit `history.go` + `history_vm.go` + `history.templ` + `dashboard.templ` +
`internal/gateway/AGENTS.md` + `ai/go-conventions.md`; run `make generate` (sqlc + templ + css);
commit `static/app.css` + the generated `*_templ.go`; run `go test ./internal/gateway/...`. The
``?days=N`` URL stops working the moment this change lands — by design (honest break, the htmx is
rewritten in lockstep).

## Open Questions

None. The five gateway-tier decisions (D1 param contract, D2 lookback, D3 fixed axis, D4 preset
rewrite, D5 Refresh removal, D6 convention doc) are settled here against the read patterns and the
binding product decisions #2-#6 from the 2026-08-09 grill-me interview (recorded in the RM8
roadmap). Tier 1 (Decision #1 — bounded port) is archived.