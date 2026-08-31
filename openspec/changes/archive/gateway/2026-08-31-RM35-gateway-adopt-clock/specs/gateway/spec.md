## MODIFIED Requirements

### Requirement: Dashboard History Charts

The gateway SHALL render THREE per-vehicle history bar charts in the dashboard bento — an
"Odometer history" chart, a "Battery history" chart, and a "Battery consumed" chart — for the
currently selected vehicle, replacing the "awaiting nightly snapshots" placeholders. **The
Battery chart's data SHALL come exclusively from the `telemetry.Reader` port; the Odometer
chart's data SHALL come exclusively from the `analytics.Reader.OdometerDeltaByDay` port; the
consumed chart's data SHALL come exclusively from the `analytics.Reader.ConsumedByDay` port —
this is a CHANGE from the prior revision of this requirement, under which the odometer chart
was sourced from `telemetry.Reader` alongside the battery chart (roadmap D5: the gateway
computes nothing about the vehicle, only about the chart).** The gateway SHALL NOT read
telemetry or analytics tables directly and SHALL NOT make a live Tesla Fleet API call to render
any of the three charts.

The charts SHALL be served by an authenticated htmx fragment endpoint `GET /ui/dashboard/history`
that accepts **`?start=YYYY-MM-DD&end=YYYY-MM-DD`** — both whole calendar days, UTC-midnight-
bounded, `end` **inclusive** — never a `?days=N` count. The endpoint SHALL resolve the target
vehicle from the session-selected vehicle (never from the URL) and scope the read to that
vehicle's Tesla id and the caller's account. Anonymous requests SHALL be redirected to `/login`
with no history data served.

**"Today" for this endpoint's validation and defaulting is the browser's local calendar day, not
the server's UTC day** (unchanged from the prior revision of this requirement) — derived from the
`browser_tz` cookie, falling back to the platform's default time zone, `clock.Zone()`
(`America/Bogota`), on any failure (absent cookie, empty value, unparseable IANA zone) — **this
fallback default is a CHANGE from the prior revision of this requirement, which fell back to
`time.UTC` (RM35-gateway-adopt-clock, roadmap D1/D4); the browser_tz cookie itself still wins
unconditionally whenever present, unaffected by this change.** See the "Browser-Local Calendar
Day" scenarios below.

**The `start`/`end` params' default window AND the `end <= X` validation cap now both target
`browser-yesterday`, not `browser-today` — this is a BEHAVIOR CHANGE from the prior revision of
this requirement.** The `start`/`end` params SHALL be validated by a single helper
(`parseHistoryRange`): when both are absent the endpoint SHALL apply the default 6-day window
(`end = browser-yesterday` midnight, `start = end-6`); a missing partner, a malformed non-ISO
date, an `end` earlier than `start`, an `end` later than **browser-yesterday** (this bound moved
by one calendar day — an `end` equal to browser-TODAY is now ALSO rejected, where it was
previously accepted), or a window wider than 90 days SHALL be rejected with HTTP 400. **The
endpoint SHALL compute a 1-day lookback `readStart = start-1day` and fetch the Battery chart's
window via `telemetry.Reader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)`; it
SHALL fetch the Odometer chart's window via `analytics.Reader.OdometerDeltaByDay(ctx, uid,
teslaID, start, end)` (no lookback — the port performs its own internal lookback fetch, mirroring
the consumed chart's port contract); and it SHALL fetch the consumed window via
`analytics.Reader.ConsumedByDay(ctx, uid, teslaID, start, end)` (likewise no lookback). This is a
CHANGE from the prior revision, under which one `telemetry.Reader` call with a lookback fed both
the Odometer and Battery charts — the Battery chart's own fetch is otherwise unaffected by this
change (same call, same lookback, same result for that chart).**

**This closes a previously-documented divergence**: before this change, a direct API call
omitting both `start` and `end` returned a window ending at browser-today, while the dashboard's
own self-load and preset buttons always targeted a window ending at browser-yesterday (because
today's data is not captured until tomorrow's nightly poll). After this change, BOTH paths target
the same browser-yesterday-ending window — the no-params default and every preset now agree.

Both the Odometer and Battery charts SHALL continue to render a **fixed `[start..end]` date
axis** — one bar per calendar day in the inclusive window, identical `MM-DD` labels across all
three charts — so a missing nightly snapshot does not shift the axis. A calendar day with no
data (no stored snapshot for the Battery chart; no `analytics.DayDistance` entry for the
Odometer chart) SHALL render as an **empty labeled bar**: zero height, its own `MM-DD` label
retained, and a "no snapshot" tooltip. **The pre-window `start-1` day (consumed internally by
`analytics.Reader.OdometerDeltaByDay` as the first delta's kilometre basis, and by the Battery
chart's own `telemetry.Reader` lookback fetch, respectively) SHALL NOT be displayed as a bar on
either chart — this is unchanged from the prior revision; only WHICH port performs the lookback
for the Odometer chart has changed.**

**The "Odometer history" bars SHALL represent kilometres driven per day, as already computed and
already floored at zero by `internal/analytics`** (`OdometerDeltaByDay`'s `KmDriven` field) — **the
gateway SHALL NOT compute a delta between two snapshots and SHALL NOT clamp a negative value
itself; both the subtraction and the zero-floor are `internal/analytics`'s responsibility, not
the gateway's (roadmap D5). This is a CHANGE from the prior revision, under which the gateway
computed `cur.OdometerKm - prev.OdometerKm` and clamped it directly.** The "Battery history" bars
SHALL represent the **battery level percentage** at each day's snapshot (absolute 0–100),
unchanged — read directly from `telemetry.Reader`, with no delta and no clamp, exactly as before
this change (the Battery chart was never a roadmap D5 violation). Each bar SHALL carry a hover
tooltip: the odometer bar's tooltip SHALL include the `MM-DD` date, the kilometres driven that
day (`DayDistance.KmDriven`), and the cumulative odometer in kilometres (`DayDistance.OdometerKm`
— both values already supplied by `internal/analytics`, not computed by the gateway); the battery
bar's tooltip SHALL include the `MM-DD` date, the level percentage, and the rated range in
kilometres. A missing-day bar's tooltip SHALL state that no snapshot exists for that date.

**The "Battery consumed" chart's bars SHALL represent the corrected per-day battery-consumed
percentage** returned by `analytics.Reader.ConsumedByDay` — bucketed on each returned
`DayConsumption.Date` value DIRECTLY, never re-derived or re-bucketed through the odometer/
battery charts' `EffectiveDate`-based UTC bucketing. A day with no corresponding
`DayConsumption` entry (no computable value for that calendar day) SHALL render as an empty
labeled bar identical in shape to the odometer/battery "no snapshot" bar, but with a "no data"
tooltip rather than a "no snapshot" tooltip. The gateway SHALL NOT perform any timezone
computation of its own for this chart — the calendar day a value belongs to is decided entirely
by `internal/analytics` before the gateway receives it. **The Odometer chart's bucket day is
likewise the port's own `DayDistance.Date`, bucketed DIRECTLY — never re-derived through
`effectiveDayUTC` — this is a CHANGE from the prior revision, under which the gateway itself
computed each snapshot's `EffectiveDate` UTC bucket for the odometer chart; `internal/analytics`
now performs that bucketing internally, using the identical `effectiveDay`/poller-zone logic
`ConsumedByDay` already used (so the Odometer and Battery charts' bucket days remain in the SAME
reference frame as before this change — only the consumed chart's own, separately-documented,
poller-zone-vs-UTC mismatch is unaffected by this change).** A known, accepted consequence
(unchanged): because `internal/analytics` and the Battery chart bucket calendar days in
different reference frames (the poller's configured zone vs. UTC), the same underlying nightly
poll can label the consumed bar's calendar day one day apart from its battery sibling bar; the
gateway SHALL NOT attempt to reconcile this.

**The "Battery consumed" chart SHALL be scaled RELATIVE to the window's own maximum displayed
value** (the tallest bar occupies 100% of the chart canvas), NOT an absolute 0–100 scale like
the Battery chart. A day whose value is suppressed per the flagged-day rule below SHALL
contribute exactly zero to that maximum, so a flagged day can never compress or distort the
scale other bars are drawn against.

**A "flagged" day** (`DayConsumption.Flagged == true`, indicating the platform's own gap
detection could not reconcile that day's charging records against its battery delta) SHALL
render its bar at ZERO height and SHALL carry a visually distinct warning marker — never the
raw (possibly negative) percentage value, and never omitted from the axis.

**A "multi-day span" day** (`DayConsumption.DaysSpanned > 1`, indicating a missed nightly poll
whose delta covers more than one calendar day) SHALL carry its OWN visually distinct marker,
separate from the flagged-day warning marker, and its tooltip SHALL state how many days it
covers.

**A day that is BOTH flagged AND a multi-day span SHALL carry BOTH markers**, and its tooltip
SHALL state BOTH facts — the day count it spans AND that a charge source is suspected missing —
never only one. This is a deliberate, owner-ruled ("picking one hides a fact that is true")
decision: markers are a SET a bar can carry zero, one, or both of, never a single mutually-
exclusive choice between "flagged" and "spanned."

A **non-spanned** flagged day's tooltip SHALL identify which charge source is suspected missing
(manual entry or Supercharger session) and SHALL NOT include the suppressed numeric value
anywhere. A **spanned** day's tooltip (flagged or not) SHALL always state its real
`ConsumedPct` value — the zero-height rendering that a flagged, non-spanned day gets never
applies to a spanned day's NUMBER; a spanned day's bar height is floored at the chart's zero
axis only because a bar cannot be drawn with negative height (a rendering-mechanics fact,
distinct from the flagged-day value-suppression rule above), never because the value is hidden
from the tooltip.

The date shown in each battery tooltip and per-bar label SHALL be the snapshot's
**`EffectiveDate`** (the calendar day the nightly snapshot represents — `CapturedAt` − 1 day),
formatted **`MM-DD`** by the Go handler; the odometer chart's date SHALL be `DayDistance.Date`
formatted the same way (already the equivalent bucket day, per the bucketing change above); the
consumed chart's date SHALL be `DayConsumption.Date` formatted the same way. The gateway SHALL
NOT show the capture-morning date (`CapturedAt`) and SHALL NOT format dates inside the template.
Each bar on every chart SHALL carry a per-bar **date label** rendered under the bar in an HTML
grid row (one cell per bar), with orientation decided by a single chart-level boolean flag
(`LabelVertical`): **horizontal** for the 6-bar window (wide bars), **rotated vertical** (via the
`[writing-mode:vertical-rl]` CSS class) for windows of 14 bars or more (narrow bars). The handler
SHALL set `LabelVertical` from the number of bars in the fixed window (`labelVerticalFor(numBars)`,
true when `numBars >= 14`) independently for each chart, not from a `days` count; the template
SHALL NOT compare the window size, compute rotation, or call `time.Format`.

The charts SHALL be rendered as **responsive inline SVG** (scaling to the container width) using
no client-side charting library. All numeric values — bar heights, deltas, percentages, tooltip
strings, per-bar label strings, and the consumed chart's marker classification — SHALL be
computed by the Go handler (for the Odometer and consumed charts: received already-computed from
`internal/analytics` and only scaled/formatted by the handler; for the Battery chart: computed by
the handler directly from the raw snapshot, unchanged) before the template renders; the template
SHALL perform no arithmetic, unit conversion, date formatting, or method calls on domain types,
and SHALL select every marker's visual class from a literal written in the template source, never
from a string computed by the handler. Bar colours and marker colours SHALL use DaisyUI semantic
tokens (no hardcoded hex).

The preset selector SHALL keep the 6/14/30 buttons but each button's `hx-get` SHALL emit a
server-rendered absolute `?start=<yesterday-N>&end=<yesterday>` href (computed by the handler at
render time, unchanged by this revision — the preset windows already targeted browser-yesterday
before the default/cap change above); the selector SHALL mark the preset whose `(start, end)`
matches the requested window as active. Changing the preset SHALL re-fetch
`GET /ui/dashboard/history?start=...&end=...` and re-render all three charts AND the selector by
swapping `#dashboard-history`'s `innerHTML` without a full page reload.

The dashboard page SHALL NOT carry a Refresh button; the `#dashboard-content` (subscribes to
`vehicle-changed from:body`) and `#dashboard-history` (self-loads on `hx-trigger="load"` with the
default 6-day window's absolute `start`/`end` ending yesterday, and re-renders on preset clicks)
htmx surfaces cover every refresh path. When too few data points exist to draw a chart (**zero
`analytics.DayDistance` entries returned by `OdometerDeltaByDay` for the odometer chart — a
CHANGE from the prior revision's "fewer than two snapshots total in the lookback window"
condition, now equivalent in effect since a day only appears in that result when both it and its
predecessor were computable**, none for the battery chart, or zero `DayConsumption` entries for
the consumed chart), that chart SHALL show the existing empty-state placeholder instead of
fabricated bars; a partially-missing axis (some days empty, some present) is NOT an empty chart
for any of the three.

Every user-facing string introduced or changed by this chart (its title, and every tooltip
clause it composes from — the plain value, the multi-day-span value, and the flagged note)
SHALL resolve through `i18n.T(ctx, key)` against `internal/gateway/i18n/catalog.go`, with both
`ES` and `EN` non-empty. Composing multiple clauses into one tooltip (e.g. for a day that is
both flagged and spanned) SHALL join independently-translated, complete clauses with a
language-neutral separator and SHALL NOT hardcode a connective word from any one language.

#### Scenario: History charts render for the selected vehicle with the default window, now ending yesterday

- **GIVEN** a signed-in user whose selected vehicle has several stored nightly snapshots and
  several computable `analytics.DayConsumption` days
- **WHEN** the history fragment is requested (`GET /ui/dashboard/history`) directly, with no
  `start` and no `end` parameter and no `browser_tz` cookie (a direct API call)
- **THEN** the response renders an "Odometer history" chart, a "Battery history" chart, and a
  "Battery consumed" chart for the selected vehicle using a 6-day window (`end =
  platform-default-yesterday`, `start = platform-default-yesterday-6`, `end` inclusive) — NOT
  `end = platform-default-today` (the prior D11 behavior). `platform-default` is `clock.Zone()`,
  the platform's default time zone `America/Bogota` — this is a CHANGE from the prior revision
  of this requirement, which used `time.UTC` here with no `browser_tz` cookie present
  (RM35-gateway-adopt-clock, roadmap D1/D4)
- **AND** all three charts render exactly 6 bars, one per calendar day in
  `[platform-default-yesterday-6 .. platform-default-yesterday]`
- **AND** the odometer and battery charts display identical `MM-DD` labels under corresponding
  bars; the consumed chart's labels cover the same calendar range (see the bucketing-mismatch
  scenario below for why an individual label can differ by one day)
- **AND** no live Tesla Fleet API call is made and no telemetry or analytics table is read
  directly

#### Scenario: The end<=today cap now rejects end=today; only end<=yesterday is accepted

- **GIVEN** a signed-in user whose browser-local "today" is `2026-08-16`
- **WHEN** the history fragment is requested with `?start=2026-08-10&end=2026-08-16` (`end`
  equal to browser-today)
- **THEN** the endpoint responds with HTTP 400 — this request was ACCEPTED before this change
- **AND** the SAME request with `?end=2026-08-15` (`end` equal to browser-yesterday) is
  accepted (HTTP 200)
- **AND** the empty-state placeholder (no preset selector) is rendered for the rejected request,
  per the existing malformed-request degradation rule

#### Scenario: Direct API default and the dashboard's own preset/self-load windows now agree

- **GIVEN** a signed-in user whose browser-local "today" is `2026-08-16` (so
  browser-yesterday is `2026-08-15`)
- **WHEN** a direct API call omits both `start` and `end`, AND separately the dashboard page's
  own self-load href is inspected
- **THEN** both resolve to the identical window: `end = 2026-08-15`, `start = 2026-08-09`
  (the default 6-day width)
- **AND** this is a change from the prior revision of this requirement, under which the direct
  API default ended at `2026-08-16` (browser-today) while the dashboard's self-load already
  ended at `2026-08-15` (browser-yesterday) — that divergence no longer exists

#### Scenario: The odometer chart's delta and clamp are computed by analytics, not the gateway

- **GIVEN** two consecutive stored snapshots whose odometer readings differ by `-2.0` km (a
  clock-skew/read anomaly)
- **WHEN** `analytics.Reader.OdometerDeltaByDay` is called for the window containing that day
- **THEN** the returned `DayDistance.KmDriven` is `0.0` — the negative value is already floored
  by `internal/analytics`
- **AND** the gateway handler building the odometer chart performs no subtraction between two
  snapshots and no comparison against zero — it renders `DayDistance.KmDriven` directly, scaled
  relative to the window's maximum

#### Scenario: The odometer chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.OdometerDeltaByDay` returns a `DayDistance` entry with
  `Date = 2026-08-10`
- **WHEN** the odometer chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through `effectiveDayUTC` or any other re-bucketing
  step

#### Scenario: The consumed chart bucket day is the port's own Date, never re-derived

- **GIVEN** `analytics.Reader.ConsumedByDay` returns a `DayConsumption` entry with
  `Date = 2026-08-10`
- **WHEN** the consumed chart buckets that entry onto the fixed `[start..end]` axis
- **THEN** the entry's bar is placed at the `2026-08-10` slot using `Date` verbatim
- **AND** the gateway does NOT pass `Date` through the battery chart's `effectiveDayUTC` helper
  or any other re-bucketing step
- **AND** a known, accepted consequence is that this bar's calendar day can differ by one day
  from the battery bar for the same underlying nightly poll, because `internal/analytics` buckets
  in the poller's configured zone while the Battery chart buckets in UTC — this mismatch is NOT
  corrected by the gateway

#### Scenario: The consumed chart scales relative to its own window maximum, not absolute 0-100

- **GIVEN** a selected vehicle whose consumed-chart window contains entries with `ConsumedPct`
  values `8.0`, `20.0`, and one flagged entry
- **WHEN** the consumed chart is rendered
- **THEN** the bar heights are computed relative to `20.0` (the window's maximum displayed
  value), so the `8.0` entry renders at 40% of the chart canvas and the `20.0` entry at 100%
- **AND** the flagged entry contributes zero toward that maximum regardless of its own
  (suppressed) underlying value
- **AND** this differs from the "Battery history" chart, whose bars remain an absolute 0–100
  scale

#### Scenario: A flagged, non-spanned day renders as a zero-height bar with a warning marker and no numeric value

- **GIVEN** a `DayConsumption` entry with `Flagged = true`, `DaysSpanned = 1`, and
  `MissingChargingType = "MANUAL"`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar's height is zero
- **AND** the bar carries a visually distinct warning marker, separate from the normal bar fill
  color and from the multi-day-span marker
- **AND** the bar's tooltip identifies a possible missing manual charge record for that date
- **AND** the tooltip does NOT contain the entry's underlying (suppressed) numeric percentage
  anywhere
- **AND** the bar is NOT omitted from the axis — its calendar-day slot and label remain present

- **GIVEN** the same entry but with `MissingChargingType = "SUPERCHARGER"` instead
- **WHEN** the consumed chart renders that day's bar
- **THEN** the tooltip identifies a possible missing Supercharger session instead of a manual
  entry, otherwise identically to the manual case

#### Scenario: A multi-day span renders its real value with its own marker, distinct from a flagged day

- **GIVEN** a `DayConsumption` entry with `DaysSpanned = 3`, `Flagged = false`, and a positive
  `ConsumedPct`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar's height reflects the entry's real `ConsumedPct`, scaled relative to the
  window's maximum — NOT a zero-height bar
- **AND** the bar carries a visually distinct span marker, different from the flagged-day
  warning marker
- **AND** the bar's tooltip states the real percentage value AND that it covers 3 days

#### Scenario: A multi-day span that is also flagged carries BOTH markers and states both facts

- **GIVEN** a `DayConsumption` entry with `DaysSpanned = 2`, `Flagged = true`,
  `ConsumedPct = -3.0`, and `MissingChargingType = "MANUAL"`
- **WHEN** the consumed chart renders that day's bar
- **THEN** the bar carries BOTH the multi-day-span marker AND the flagged-day warning marker,
  simultaneously and independently visible — neither marker is suppressed in favor of the
  other (roadmap D21: "picking one hides a fact that is true")
- **AND** the bar's height is zero — for this entry that outcome is unambiguous either way: the
  chart's zero-axis floor (a negative height cannot be drawn) and the flagged-day
  value-suppression rule agree, because a flagged day's `ConsumedPct` is always `<= 0` by
  construction (tier 3 D5)
- **AND** the tooltip states the real, signed percentage value (`-3.0%`) and that the entry
  covers 2 days, AND separately notes a possible missing manual charge record — both facts
  present, neither omitted in favor of the other
- **AND** this is the roadmap's own worked overlap case: `Flagged` and `DaysSpanned > 1` are
  independent conditions on `DayConsumption`, so this combination is reachable in production,
  not merely a hypothetical fixture

#### Scenario: A day absent from ConsumedByDay renders as a "no data" bar, distinct wording from "no snapshot"

- **GIVEN** a calendar day within the requested window for which `analytics.Reader.ConsumedByDay`
  returned no entry (no computable value for that day)
- **WHEN** the consumed chart renders that day's slot
- **THEN** the bar renders at zero height with its own `MM-DD` label retained
- **AND** the tooltip states that no data exists for that date, using wording distinct from the
  battery chart's "no snapshot" tooltip (the absence reason for the consumed chart is
  broader than "no snapshot exists")
- **AND** a window with zero computable days across its entire range renders the consumed
  chart's empty-state placeholder instead of an all-empty bar row

#### Scenario: Consumed chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the consumed chart
- **WHEN** it obtains per-day consumption data
- **THEN** it does so exclusively through `analytics.Reader.ConsumedByDay`
- **AND** it imports no package other than `internal/analytics`'s public port for this data (there
  is no `analytics` database package the gateway may import — `internal/analytics` owns
  `internal/analytics/db`, but that package is imported only inside `internal/analytics` itself)
- **AND** an `analytics.Reader` error degrades only the consumed chart to its empty state; the
  battery chart, sourced from the separate `telemetry.Reader` call, is unaffected by an
  `analytics.Reader` failure

#### Scenario: Odometer chart data comes exclusively through analytics.Reader

- **GIVEN** the gateway handler that builds the odometer chart
- **WHEN** it obtains per-day odometer distance data
- **THEN** it does so exclusively through `analytics.Reader.OdometerDeltaByDay`
- **AND** it imports no package other than `internal/analytics`'s public port for this data
- **AND** an `analytics.Reader` error building the odometer chart degrades only the odometer
  chart to its empty state; the battery chart, sourced from the separate `telemetry.Reader`
  call, is unaffected

#### Scenario: Browser-Local Calendar Day, with a platform-default fallback

- **GIVEN** a signed-in user whose browser sent a `browser_tz` cookie with a valid IANA zone —
  for any zone, negative or positive UTC offset
- **WHEN** the history fragment is requested with no `start`/`end` parameter
- **THEN** the default window's `end` is midnight of browser-yesterday IN THAT ZONE, not the
  platform default zone's midnight — the cookie always wins whenever present, unchanged by this
  revision (RM35-gateway-adopt-clock, roadmap D1)
- **AND** on a missing, empty, or unparseable `browser_tz` cookie, the gateway falls back to the
  platform's default time zone, `clock.Zone()` (`America/Bogota`) — silently, no error surfaced,
  no caller special-casing required. **This fallback default is a CHANGE from the prior revision
  of this requirement, which fell back to `time.UTC`** (RM35-gateway-adopt-clock, roadmap D1/D4)

#### Scenario: Charts contain no business logic in templates

- **GIVEN** the history chart and selector templates, including the new consumed-chart marker
  rendering
- **WHEN** they render
- **THEN** the bar heights, per-day deltas/percentages, tooltip strings, per-bar `MM-DD` label
  strings, the `Present` flag, the flagged-marker and multi-day-span-marker flags (independent
  of each other — a bar can carry both), and the `LabelVertical` orientation flag have all been
  computed by the Go handler before the template receives the view model
- **AND** the templates use only presentation logic (if/for/display) — no arithmetic, no unit
  conversion, no date formatting, no method calls on domain types
- **AND** every marker's CSS class (e.g. the warning color for a flagged bar, the info color for
  a span bar) is a literal string written in the `.templ` source, never a string value computed
  in a `.go` handler file and passed through as an attribute
- **AND** the SVG scales to the container width (responsive) and bar/marker colours use DaisyUI
  semantic tokens with no hardcoded hex
- **AND** the per-bar label is a real DOM text cell in an HTML grid (not an SVG `<text>`), using
  a muted semantic token — no client-side library

#### Scenario: Gateway never imports telemetrydb or an analytics database package for history

- **GIVEN** the gateway handler that builds all three history charts
- **WHEN** it obtains the vehicle's snapshot history, its per-day odometer distance, and its
  per-day consumption
- **THEN** it does so exclusively through the `telemetry.Reader` and `analytics.Reader` public
  interfaces
- **AND** it imports no package from `internal/telemetry/db` (`telemetrydb`) and no package from
  `internal/analytics/db` (`analyticsdb`)
- **AND** no `pgtype` type appears in any gateway file involved

#### Scenario: History fragment is not served to anonymous callers

- **GIVEN** an unauthenticated request to `GET /ui/dashboard/history`
- **WHEN** the handler resolves the session
- **THEN** the request is redirected to `/login` and no history data (including the consumed
  chart) is served

#### Scenario: All new consumed-chart strings are bilingual

- **GIVEN** the consumed chart's title, and every clause its tooltip composes from (the plain
  percentage value, the multi-day-span value, and the flagged note)
- **WHEN** the catalogue is inspected
- **THEN** every corresponding key has both an `ES` and an `EN` value, neither empty
- **AND** no consumed-chart string is a hardcoded literal bypassing `i18n.T`
- **AND** a tooltip composed from more than one clause (e.g. a day that is both flagged and
  spanned) joins the independently-translated clauses with a language-neutral separator, never
  a connective word hardcoded from one language
