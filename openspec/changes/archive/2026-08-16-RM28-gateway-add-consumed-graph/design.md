## Context

`internal/gateway`'s `/ui/dashboard/history` fragment already renders two hand-rolled SVG bar
charts (RD7, `internal/gateway/AGENTS.md`) over a fixed `[start..end]` calendar-day axis:
odometer km/day and battery level %. Tier 3 (`RM28-battery-derive-consumed-per-day`, archived
2026-08-16) shipped `battery.Reader.ConsumedByDay(ctx, accountID, teslaID, start, end)
([]DayConsumption, error)` — the corrected per-day battery-consumed percentage, with gap
detection and multi-day-span metadata already computed. This tier's job is narrow: call that
port, and render a third chart panel that behaves correctly for its three special cases
(normal day, flagged day, multi-day span) plus a day with no entry at all.

Two things make this tier more than "copy `buildBatteryChart`":

1. **The bucketing zone mismatch is real and must not be "fixed" here.** `internal/battery`
   buckets `DayConsumption.Date` in the poller's configured zone (`Config.Location`, D6/D18);
   the two existing charts bucket in UTC via `effectiveDayUTC`. The battery module's own port
   doc comment is explicit: "Do NOT re-bucket this method's Date through effectiveDayUTC — it
   is already a final bucket key." This tier follows that instruction literally.
2. **D10 (flagged) and D20 (span) are two independent visual states that can, per the
   `DayConsumption` struct's own field independence, occur on the SAME day** — `Flagged` is
   computed from `ConsumedPct`/`DistanceKm` alone; `DaysSpanned` is a separate field from the
   underlying snapshot row. This overlap was flagged as an open interpretation question in
   this design's first revision; the owner has since ruled directly (roadmap **D21**,
   2026-08-16): a day that is both flagged and a multi-day span carries **both** markers, with
   both facts in the tooltip. Markers are therefore a **set**, not a single-valued enum — see
   D-G4/D-G5 below, both rewritten under D21.

## Goals / Non-Goals

**Goals**
- Render `battery.DayConsumption` as a third `HistoryChart` panel, mirroring the existing
  two panels' pre-computed-by-the-handler, logic-free-template discipline.
- Implement D10 (flagged), D19 (relative scale), D20 (span), D21 (both markers when a day is
  both flagged AND spanned) and D11 (default/cap move) exactly as specified.
- Wire `battery.Reader` into the gateway's `Deps` the same way every prior sibling port was
  wired (`SuperchargerReader`, `ManualChargeReader`) — no new pattern invented.
- Keep every new user-facing string bilingual through `i18n.T` against the single catalogue.

**Non-Goals**
- Re-deriving, re-validating, or caching anything `ConsumedByDay` already computed (D2 stays
  tier 3's — this tier performs zero arithmetic on `ConsumedPct` itself, only presentation
  math: scaling to a 0–100 height and clamping a negative value for **display purposes only**,
  see D-G4).
- Reconciling the UTC-vs-zoned bucketing mismatch between this chart and the other two
  (explicitly out of scope, D18/D18a).
- Writing to `charge_gaps` or calling `telemetry.GapWriter` — read-only at request time,
  unchanged module invariant.
- `cmd/web`'s actual `battery.NewReader(...)` construction — specified here for the leader,
  implemented by the leader (outside `internal/gateway`).

## Design Decisions

### D-G1 (D19, revised under D21) — Relative-scale max is `math.Max(0, ConsumedPct)`, one line, no per-state branch

The window's tallest **displayed** value is 100% of the canvas (D19), never an absolute
0–100 axis:

```
displayVal(d DayConsumption) float64:
    return math.Max(0, d.ConsumedPct)
```

**This design's first revision had a two-branch version** (`if !span && Flagged: return 0`,
else `math.Max(0, ConsumedPct)`). The leader's review caught that the branch was dead code:
tier 3's D5 defines `Flagged` as `ConsumedPct < 0 OR (ConsumedPct == 0 AND DistanceKm >
minFlagDistanceKm)` — **`Flagged` therefore implies `ConsumedPct <= 0` unconditionally**,
span or not (`battery.go`'s `DayConsumption.Flagged` doc comment states the same rule). So
`math.Max(0, d.ConsumedPct)` already evaluates to `0` for every flagged day, whether or not it
is also a multi-day span — the special-cased branch returned the exact value the general
clamp already produced on every input where it could fire. Removing it changes no behavior
and removes a line a future reader could mistake for load-bearing.

**The D19 guarantee — "a flagged bar must not be allowed to set or distort the scale" — holds
by construction from this one clamp alone**, not from any per-state logic: `max` is the
largest `displayVal` across every entry in `[start..end]`; a flagged day (span or not) always
contributes exactly `0` and therefore can never raise `max`. `HeightPct(d) = 0` when `max == 0`
(every entry flagged, or window empty of computable days), else `round(displayVal(d) / max *
100)`. Two-phase computation (collect `displayVal` + track `max`, then convert to
`HeightPct`), mirroring `buildOdometerChart`'s existing `deltas`/`maxKm` shape — same pattern,
not a new one.

**Do not "restore" a per-state branch here** — see the Test Contract's regression test (i2)
below, added specifically to catch a future edit that reintroduces the dead branch under the
mistaken belief it changes behavior for the flagged+spanned case (D-G4).

### D-G2 (D18/D18a) — Bucket key is `DayConsumption.Date`, verbatim, never `effectiveDayUTC`

`buildConsumedChart` buckets into `byDay := make(map[time.Time]battery.DayConsumption,
len(days)); for _, d := range days { byDay[d.Date] = d }` — no `effectiveDayUTC(d.Date)` call
anywhere. `d.Date` is already UTC-midnight-*represented* (a bare calendar date has no
timezone left to convert — see `battery.go`'s own doc comment) even though the calendar day
it names was decided in the poller's zone, not UTC. This is a deliberate, accepted mismatch:
a capture that lands between 00:00Z and 05:00Z can label the consumed bar one calendar day
apart from the odometer/battery bar for the same underlying row. **Do not "fix" this by
routing `d.Date` through `effectiveDayUTC`** — that would silently shift the bucket to a day
`internal/battery` never claimed the value belonged to, corrupting the one guarantee the port
makes (D18/D18a, tier 3 design.md D-B7).

### D-G3 (D18/D18a) — No new gateway-side timezone handling

The gateway does not read `battery.Config.Location`, does not call `time.LoadLocation` for
this chart, and does not pass a zone into `ConsumedByDay` — the port's signature is
`(ctx, accountID, teslaID, start, end time.Time)`, unchanged by this tier, and `start`/`end`
are the SAME UTC-midnight-represented values `parseHistoryRange` already produces for the
other two charts. The zone lives entirely inside `internal/battery`, stamped into the data
before it ever reaches this module (D18a) — the gateway's existing "no business logic in
templates, no time math beyond what the module already established" posture is fully
preserved.

### D-G4 (D10 + D20 + D21) — A day that is both flagged and a multi-day span carries BOTH markers

**This design's first revision flagged a genuine gap**: roadmap D10 ("a flagged day renders
as a zero-height bar... never omitted") and D20 ("a multi-day span... plots its REAL
`ConsumedPct`... never a zero-height bar") are each stated with no explicit carve-out for the
other, and the overlap is reachable — `Flagged` (tier 3 D5: `ConsumedPct < 0 OR (ConsumedPct
== 0 AND DistanceKm > minFlagDistanceKm)`) is computed independently of `DaysSpanned`, so a
multi-day span's own summed `ConsumedPct` can absolutely be negative (e.g., a 2-day gap where
the vehicle drove far and no charge record exists for either day). The first revision resolved
this by picking one marker to win; that framing was itself wrong.

**The owner has ruled directly — roadmap D21 (2026-08-16), binding:** a day that is both
flagged and a multi-day span carries **BOTH** markers, with **both facts** in the tooltip.
Markers are a **set**, not a single winner.

**The owner's own ruling also dissolves the "which one wins the height" question this
design's first revision spent most of its length on — the height was never actually in
dispute.** `Flagged ⇒ ConsumedPct <= 0` (tier 3 D5, restated in D-G1 above), so
`math.Max(0, d.ConsumedPct)` is `0` under EVERY reading, whether `Flagged` is read as
height-controlling or not, span or not. The only real choice was ever which marker(s) show and
what the tooltip says — D21 answers that: both, when both are true.

**Four rendering states, cleanly enumerated** (`markerFlagged := d.Flagged`, `markerSpan :=
d.DaysSpanned > 1` — two independent booleans, D-G5). Every row's height is the SAME formula,
`displayVal = math.Max(0, d.ConsumedPct)` (D-G1) — there is no per-row height rule to
memorize:

| `markerFlagged` | `markerSpan` | `ConsumedPct`'s sign (D5) | `displayVal` | Marker chip(s) | Tooltip clauses (D-G7) |
|---|---|---|---|---|---|
| false | false | `>= 0` (guaranteed — D5's contrapositive) | `ConsumedPct` unchanged | none | value clause only |
| true | false | `<= 0` (guaranteed — D5) | `0` | warning chip | flagged clause only (value hidden — D10) |
| false | true | `>= 0` (guaranteed — D5's contrapositive) | `ConsumedPct` unchanged | span chip | value+span-count clause (real value, D20) |
| true | true | `<= 0` (guaranteed — D5) | `0` | **both** warning AND span chips (D21) | value+span-count clause, **then** flagged clause (D21 — both facts) |

**One clean global fact worth stating once, not per-row: `math.Max(0, ...)`'s clamp only ever
fires when `Flagged` is true.** D5 defines `Flagged` to include `ConsumedPct < 0` as one of its
two OR'd conditions — so `ConsumedPct < 0` is only reachable on a flagged row, and every
non-flagged row is guaranteed `ConsumedPct >= 0` by construction, never needing the clamp in
practice. The "an SVG `<rect>` cannot have a negative height" rendering-mechanics concern (the
same reason `buildOdometerChart` clamps a negative odometer delta) and D10's "hide the
untrusted value" concern therefore always coincide for this port's data — there is no
reachable case where the two justifications for `math.Max(0, ...)` point at different rows.
This is why D-G1 needs exactly one clamp, not two.

### D-G5 (revised under D21) — Markers are a SET: two independent booleans, not a single-valued enum

**D21 makes a single-valued `BarMarker` field wrong** — a day can carry both the flagged
warning marker and the span marker simultaneously (D-G4's row 4), so a field that can only
ever hold ONE value cannot represent that state. This design's first revision proposed a
3-value string enum (`BarMarkerNone`/`BarMarkerFlagged`/`BarMarkerSpan`); that type is dropped
entirely.

**Chosen: two independent boolean fields on `HistoryBar`** — `MarkerFlagged bool`,
`MarkerSpan bool` — over a `[]BarMarker` slice. Both options keep every CSS class a literal in
`.templ` (a slice iterated with a per-element `if`/`switch` in the template would ALSO resolve
to literal classes, so the Tailwind-content-scan constraint from the first revision does not,
by itself, force this choice — see below for what does). Booleans win on three points, each
small individually but all pointing the same way (AI-efficiency: prefer the option with less
incidental complexity to re-derive later):

1. **The marker vocabulary is closed at exactly two, not open-ended.** A slice's type
   (`[]BarMarker`) signals "an arbitrary-length collection," inviting a future reader to
   wonder about ordering, duplicates, or a third marker sharing a slot — none of which apply
   here. Two named booleans say precisely what is true: exactly two independent conditions,
   each either present or not.
2. **No allocation, no nil-vs-empty ambiguity.** `[]BarMarker` for a "no markers" bar is
   either `nil` or `[]BarMarker{}` depending on construction path — a distinction Go treats as
   meaningfully different in some contexts (`len` doesn't care, `== nil` does) for no reason
   this feature needs. Two `bool` fields have one zero value each, always `false`, always
   comparable, always cheap.
3. **The template stays a flat sequence of independent `if`s, not a loop with a nested
   switch.** `historyBarChart` already reads `chart.LabelVertical` (`if`/`else`, not a loop
   over flags) — two `if bar.MarkerFlagged { ... }` / `if bar.MarkerSpan { ... }` blocks match
   that existing shape exactly; a `for range bar.Markers { switch m { ... } }` would be the
   first loop-over-an-enum construct in this file for no expressiveness gain.

```go
type HistoryBar struct {
	HeightPct int
	Tooltip   string
	Label     string
	Present   bool
	// MarkerFlagged is true when this day was flagged by internal/battery's
	// gap detection (D10) -- the template renders a warning-colored marker
	// chip. Always false for every existing odometer/battery bar (they never
	// set this field).
	MarkerFlagged bool
	// MarkerSpan is true when this day's underlying DayConsumption spans
	// more than one calendar day (D20) -- the template renders an
	// info-colored marker chip, DISTINCT from the warning chip above.
	// Independent of MarkerFlagged: BOTH can be true on the same bar (D21),
	// in which case BOTH chips render and Tooltip states both facts
	// (design.md D-G4/D-G7). Always false for every existing odometer/
	// battery bar.
	MarkerSpan bool
}
```

**The Tailwind-content-scan constraint from the first revision still fully binds** — it is
WHY the fields are booleans (a presence/absence signal) rather than, say, a
`MarkerClass string` field the handler could be tempted to set to a computed `"fill-warning"`
value. `internal/gateway/static/input.css` scopes Tailwind's content scanner to `@source
"../templates/**/*.templ"` only — `.go` files under `handlers/` are **not** scanned. Any
`fill-*`/`text-*` class computed as a Go string in `history.go` and injected as a raw
attribute value would compile, pass `go vet`, and render an **empty/unstyled** marker in
production with no build-time warning — the exact "stale CSS silently ships unstyled markup"
gotcha `internal/gateway/AGENTS.md`'s hot-reload section already documents for a different
case. `historyBarChart`'s template body reads `bar.MarkerFlagged`/`bar.MarkerSpan` and writes
the `fill-warning`/`fill-info` class names as **literals inside the `.templ` file itself**
(see "Go-Level Surface" below) — never receives them as a parameter computed in Go. Every
future marker-style addition to this component must keep this shape: booleans (or, if a third
independent marker is ever needed, a third boolean) in the VM, literal class strings only in
`.templ`.

### D-G6 — A new, symmetric "no data" tooltip key, not reuse of `KeyHistoryNoSnapshotTooltip`

A day absent from `ConsumedByDay`'s sparse result renders exactly like today's missing-day
bar — `Present: false`, `HeightPct: 0` — but its wording must not claim "no snapshot": the
absence reason is broader than the existing key's name implies (no snapshot pair for that
day, OR the account's very first-ever snapshot, which tier 3's D5a deliberately skips without
flagging — see `battery.go`'s `ConsumedByDay` doc comment). Reusing
`KeyHistoryNoSnapshotTooltip` would also inherit an existing bilingual **asymmetry**: its `ES`
value is already the generic "sin dato" ("no data") while its `EN` value is the specific "no
snapshot" — so EN would read slightly wrong for the D5a case while ES happened to read fine.
This tier adds `KeyHistoryConsumedNoDataTooltip` (`ES: "%s · sin dato"`, `EN: "%s · no
data"`) — symmetric, accurate for every absence reason. Unlike the value/span/flagged clause
keys (D-G7, revised under D21), this ONE key stays a whole-sentence, label-included format
string, matching `KeyHistoryDaysPreset`/`KeyHistoryNoSnapshotTooltip`'s existing convention —
there is no second clause a "no data" bar would ever need to compose with, so composability
buys nothing here.

### D-G7 (revised under D21) — Three composable clause keys joined by the existing " · " separator, not four whole-sentence keys

**This design's first revision rejected clause composition** in favor of one whole-sentence
catalogue key per rendering state, reasoning that Go-side concatenation would force a
language's clause order onto every locale. **D21 exposes why that reasoning does not survive
contact with a genuine set of markers**: a fixed-order 4th key
(`KeyHistoryConsumedSpanFlaggedTooltip`, baking "value+span, then flagged" into one literal
per language) is really just concatenation with the seams hidden inside the catalogue instead
of in Go — it still hardcodes ONE global ordering, just in a harder-to-see place, and it does
not extend if a third independent marker is ever added (D-G5 already generalizes to "a third
boolean" — the tooltip composition should generalize the same way, not require a new
whole-sentence key for every new marker *combination*, which is combinatorial in the number of
markers).

**Resolution: each marker (and the plain value) gets its OWN independently-translated clause,
and the Go handler joins whichever clauses apply with `" · "`** — the same punctuation
separator this codebase's tooltips already use internally to join facts within a single
catalogue string (`"%s · %s%% · covers %d days"` in the first revision was itself already
multiple `" · "`-joined facts inside ONE literal). Promoting that separator to a Go-side join
is not the rejected pattern from the first revision: `" · "` is punctuation, not a word — it
carries no language-specific grammar or word-order commitment the way gluing two clauses with
a hardcoded connective like `"and"`/`"plus"` would. What DOES stay rejected: a hardcoded
English (or any single language's) connective word, or any per-language reordering logic in
Go. Neither happens here — every clause is a complete, independently-translated
`fmt.Sprintf(i18n.T(ctx, key), ...)` result; Go only decides WHICH clauses apply (from the two
independent booleans, D-G5) and glues them with the same neutral bullet already used
throughout this file.

**The clause-vs-sentence split also matches D-G4's table exactly**: the "value clause" differs
between a plain day (percentage alone) and a spanned day (percentage + day count) because
those two facts are inherently ONE observation, not decomposable further without becoming
clunky (`"11.0% consumed"` vs `"15.0% · covers 3 days"` are each a single natural clause per
language) — so they stay TWO separate whole clauses, not decomposed into three. The flagged
note, in contrast, is a genuinely independent fact ("something is amiss with the charge
records") that composes cleanly onto EITHER value clause or stands alone, so it is its own
clause, appended when `MarkerFlagged` is true.

**Final catalogue key list (seven, down from the first revision's eight — one fewer, and the
flagged+span combination needs no dedicated key at all)**:

- `KeyHistoryConsumedTitle` — chart title (unchanged from the first revision).
- `KeyHistoryConsumedPctClause` — `{ES: "%s%% consumida", EN: "%s%% consumed"}` — the plain
  value clause (percentage only), used when `!MarkerSpan && !MarkerFlagged`.
- `KeyHistoryConsumedSpanClause` — `{ES: "%s%% · abarca %d días", EN: "%s%% · covers %d
  days"}` — the spanned-day value clause (percentage + day count as one fact), used whenever
  `MarkerSpan` is true, regardless of `MarkerFlagged`.
- `KeyHistoryConsumedFlaggedClause` — `{ES: "posible registro de carga faltante (%s)", EN:
  "possible missing charge record (%s)"}` — the flagged note, appended whenever
  `MarkerFlagged` is true, regardless of `MarkerSpan`. No label, no leading separator (the
  join adds it).
- `KeyHistoryConsumedNoDataTooltip` — unchanged from the first revision (D-G6): the ONE key
  that stays a whole sentence, because the no-data case has no other clause to join with.
- `KeyHistoryChargeTypeManual`, `KeyHistoryChargeTypeSupercharger` — unchanged (standalone
  nouns, substituted into the flagged clause's `%s`).

**Composition** (`buildConsumedChart`, per present day — see "Go-Level Surface" for the full
function):

```go
label := d.Format("01-02")
var clauses []string
switch {
case markerSpan:
	clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedSpanClause), pctStr, day.DaysSpanned))
case !markerFlagged:
	clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedPctClause), pctStr))
	// markerFlagged && !markerSpan: no value clause at all -- D10 hides the number.
}
if markerFlagged {
	clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedFlaggedClause), chargeTypeLabel(ctx, day.MissingChargingType)))
}
tooltip := label
for _, c := range clauses {
	tooltip += " · " + c
}
```

For row 4 (both markers), this produces exactly D-G4's table entry: `"<label> · <span
clause> · <flagged clause>"` — value-and-span-count first, then the flagged note. That
ordering (span fact before gap fact) is a single global STRUCTURAL choice applied identically
regardless of language — analogous to a table always listing its columns in the same order —
not a per-language grammar decision, so it does not reintroduce the pattern this decision
rejects.

### D-G8 — `formatPctRaw`: one decimal place, mirrors `formatKmRaw`'s shape exactly

`ConsumedPct` is documented as "raw and unrounded... typically 6–20%/day" (roadmap D19
rationale). Rounding to a whole percent (as `buildBatteryChart` does for the absolute
0–100 battery level) would collapse most of that range's meaningful variation into a handful
of integers. `formatPctRaw(pct float64) string` returns `strconv.FormatFloat(pct, 'f', 1,
64)` — no thousands grouping (three-digit values don't occur at this magnitude), no `%`
suffix (the catalogue format string supplies it, exactly like `formatKmRaw` omits " km").
Placed in the existing `format.go`, next to `formatKmRaw` — no new file for one function
(AI-efficiency: don't fragment a five-function file over one addition).

### D-G9 (D11) — `parseHistoryRange` computes `yesterday` once, internally; signature unchanged

```go
func parseHistoryRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
	yesterday := today.AddDate(0, 0, -1)
	rawStart := c.Query("start")
	rawEnd := c.Query("end")
	if rawStart == "" && rawEnd == "" {
		end = yesterday                          // was: end = today
		start = end.AddDate(0, 0, -historyRangeWindowDays)
		return start, end, true
	}
	// ... parse s, e as today ...
	if e.Before(s) { return zero, zero, false }
	if calendarDateAfter(e, yesterday) { return zero, zero, false }   // was: calendarDateAfter(e, today)
	if e.Sub(s).Hours()/24 > float64(historyRangeMaxDays) { return zero, zero, false }
	return s, e, true
}
```

`today` stays the function's one input — resolved once per request by the caller
(`browserToday(c)`), exactly as today's doc comment describes (removes a theoretical
day-boundary race). Only what the default/cap compare against shifts by one day, computed
with the SAME `AddDate(0,0,-1)` idiom `buildHistoryPresets`/`defaultHistoryHref` already use
for "yesterday" — no new time-math primitive introduced. `calendarDateAfter` itself is
untouched (it already takes two `time.Time` and compares calendar dates in each side's own
frame — a caller-supplied `yesterday` instead of `today` needs no change to that function).

**Every doc comment referencing "today" in this function's numbered validation steps must be
updated to "yesterday"** — leaving stale prose here is exactly the kind of drift
`ai/go-conventions.md`'s "docs track structural change" rule exists to catch, and this
function's own comment is dense enough that a future reader will trust it over the code if
they diverge.

### D-G10 — `buildHistoryView`'s battery-reader error is independent of the telemetry-reader error

Today, a `telemetryReader.SnapshotsByVehicleBetween` error degrades BOTH existing charts to
empty via one early `return v`. The new `batteryReader.ConsumedByDay` call is a SEPARATE read
against a SEPARATE port — its own error must degrade ONLY `v.Consumed`, not wipe out an
odometer/battery chart that already succeeded. Concretely: the new call sits AFTER the
existing early-return point, with its own `if err != nil { log...; v.Consumed =
fragments.HistoryChart{Empty: true}; return v }`-shaped branch that still returns the
already-populated `v.Odometer`/`v.Battery`. This mirrors the existing resilience posture
(`buildHistoryView`'s doc comment: "degrades... rather than panicking or returning a 500")
extended to a third independent data source instead of introducing a new failure mode.

### D-G11 — `Deps.BatteryReader` follows the `SuperchargerReader` wiring precedent exactly

`gateway.Deps.BatteryReader battery.Reader` → forwarded verbatim into
`handlers.Deps.BatteryReader` → `Handler.batteryReader` via `New()`. No new pattern: this is
the fourth sibling-module read port wired this way (`TelemetryReader`, `SuperchargerReader`,
`ManualChargeReader`, now `BatteryReader`), each added by a prior tier/change with an
identical three-file touch (`gateway.go`, `handlers.go`, the calling handler). `cmd/web`'s
actual construction of the concrete `battery.NewReader(...)` value is the ONLY piece outside
`internal/gateway` — see "cmd/web wiring" below.

## `battery.Reader` import boundary (module-boundary reminder, not a new rule)

`internal/gateway` may only call `battery.Reader.ConsumedByDay` — the public port. `internal/
battery` owns no database (tier 3 D15), so there is no `batterydb` package this rule could
even be tempted to import; the boundary reminder here is purely "the interface, nothing
deeper," matching every other sibling-module dependency this module already holds.

## Go-Level Surface

### `templates/fragments/history_vm.go` additions

**No `BarMarker` type** — see design.md D-G5 (revised under D21): a single-valued enum
cannot represent "both markers on one bar" (D21), so this tier adds two independent boolean
fields to `HistoryBar` instead. `HistoryBar` gains two fields (all existing fields unchanged):

```go
type HistoryBar struct {
	HeightPct int
	Tooltip   string
	Label     string
	Present   bool
	// MarkerFlagged is true when this day was flagged by internal/battery's
	// gap detection (D10) -- the template renders a warning-colored marker
	// chip. Always false for every existing odometer/battery bar (they never
	// set this field).
	MarkerFlagged bool
	// MarkerSpan is true when this day's underlying DayConsumption spans
	// more than one calendar day (D20) -- the template renders an
	// info-colored marker chip, DISTINCT from the warning chip above.
	// Independent of MarkerFlagged: BOTH can be true on the same bar (D21),
	// in which case BOTH chips render and Tooltip states both facts
	// (design.md D-G4/D-G7). Always false for every existing odometer/
	// battery bar.
	MarkerSpan bool
}
```

`HistoryView` gains one field:

```go
type HistoryView struct {
	Start    time.Time
	End      time.Time
	Presets  []RangePreset
	Odometer HistoryChart
	Battery  HistoryChart
	// Consumed contains the battery-consumed-%/day bars over the SAME fixed
	// [start..end] axis as Odometer/Battery, bucketed on
	// battery.DayConsumption.Date DIRECTLY (never effectiveDayUTC — D18/D18a,
	// design.md D-G2). Scaled RELATIVE to the window's max displayed value
	// (D19), not absolute 0-100 like Battery. HeightPct is math.Max(0,
	// ConsumedPct) scaled against that max for every bar (design.md D-G1) —
	// a flagged day (MarkerFlagged) always lands at 0 because Flagged implies
	// ConsumedPct <= 0 (tier 3 D5); a multi-day-span day (MarkerSpan) shows
	// its real value. A bar can carry BOTH markers (D21, design.md D-G4).
	Consumed HistoryChart
}
```

### `templates/fragments/history.templ` changes

`historyBarChart` gains TWO independent per-bar marker blocks (not a switch — a bar can hit
both), appended after the existing bar-rect loop (inside the same `<svg>`, so markers layer on
top of already-drawn bars in paint order) — literal class strings, per D-G5. Each marker has
its OWN fixed vertical strip so both remain independently visible when a bar carries both
(D21) rather than one overpainting the other:

```templ
for i, bar := range chart.Bars {
    if bar.MarkerFlagged {
        <rect x={ strconv.Itoa(i) } y="1" width="0.8" height="4" class="fill-warning">
            <title>{ bar.Tooltip }</title>
        </rect>
    }
    if bar.MarkerSpan {
        <rect x={ strconv.Itoa(i) } y="6" width="0.8" height="4" class="fill-info">
            <title>{ bar.Tooltip }</title>
        </rect>
    }
}
```

Fixed `y="1"`/`y="6"`, `height="4"` each — two thin chips stacked near the TOP of the
`viewBox="0 0 N 100"` canvas, independent of `bar.HeightPct` and independent of each other's
presence. This is deliberate: a flagged-only bar's main `<rect>` is height-0 (invisible), so
its marker MUST NOT depend on bar height to be visible; a spanned bar's height varies with its
value, and fixed-position chips avoid computing a height-dependent Y offset in the template
(which would be presentation arithmetic on an already-precomputed number — acceptable per the
existing `y={ 100 - bar.HeightPct }` precedent for the bar itself, but avoidable here, and
avoiding it is simpler). Each chip sits at the SAME `y` whether it appears alone or alongside
the other — a flagged-only bar's chip is always at `y="1"`, a span-only bar's chip is always
at `y="6"`, so a viewer who has learned one chip's position never has to relearn it when the
other marker is also present. No new `historyBarChart` parameter — the function signature
`historyBarChart(chart HistoryChart, colorClass string)` is unchanged; `colorClass` still
selects the MAIN bar's fill exactly as today, marker chips are independent of it.

`DashboardHistoryContent` gains a third `ui.Card`, after the existing Battery card:

```templ
@ui.Card(ui.CardProps{Title: i18n.T(ctx, i18n.KeyHistoryConsumedTitle)}) {
    @historyBarChart(v.Consumed, "fill-accent")
}
```

`"fill-accent"` is a literal at the `.templ` call site (D-G5) — the third chart's normal-bar
color, distinct from `fill-primary` (odometer) and `fill-secondary` (battery), and distinct
from the marker chips' `fill-warning`/`fill-info`.

### `handlers/format.go` addition

```go
// formatPctRaw renders a percentage value to one decimal place with no "%"
// suffix (the caller's i18n format string supplies it) — e.g. 11.0 -> "11.0",
// -3.4 -> "-3.4". Mirrors formatKmRaw's "no unit, caller appends it" shape.
// One decimal because the consumed chart's typical 6-20%/day range needs
// sub-integer precision to be readable (roadmap D19 rationale) — unlike
// formatKm's whole-number odometer/battery values.
func formatPctRaw(pct float64) string {
	return strconv.FormatFloat(pct, 'f', 1, 64)
}
```

### `handlers/history.go` changes

`parseHistoryRange` — see D-G9 above for the full diff; doc comment's numbered steps update
"today" → "yesterday" for steps 1 and 4.

New function, same file, mirroring `buildOdometerChart`/`buildBatteryChart`'s shape and
two-phase (collect + convert) structure:

```go
// buildConsumedChart computes battery-consumed-%/day bars over the FIXED
// [start..end] calendar-day axis (matching the other two charts' window),
// bucketed on battery.DayConsumption.Date DIRECTLY — never effectiveDayUTC
// (D18/D18a, design.md D-G2: the port's Date is already a final, zoned
// bucket key). Scaled RELATIVE to the window's max displayed value (D19),
// mirroring buildOdometerChart's maxKm pattern, not buildBatteryChart's
// absolute 0-100 — every bar's height is math.Max(0, ConsumedPct) (design.md
// D-G1; the same clamp for every bar, no per-state branch). A day absent
// from days renders as the existing empty labeled "no data" bar
// (Present=false). A day present carries MarkerFlagged when Flagged (D10)
// and/or MarkerSpan when DaysSpanned > 1 (D20) — BOTH markers, independently,
// when both conditions hold (D21, design.md D-G4/D-G5). The tooltip is
// composed from independently-translated clauses joined by " · " (design.md
// D-G7) — never a value clause when MarkerFlagged && !MarkerSpan (D10 hides
// the number), always the real signed value otherwise. The chart's Empty
// fires only when days is empty (mirrors buildBatteryChart: a single
// computable day is enough to draw, unlike buildOdometerChart's need for a
// delta pair).
func buildConsumedChart(ctx context.Context, days []battery.DayConsumption, start, end time.Time) fragments.HistoryChart {
	numDays := int(end.Sub(start).Hours()/24) + 1

	if len(days) == 0 {
		return fragments.HistoryChart{Empty: true, LabelVertical: labelVerticalFor(numDays)}
	}

	byDay := make(map[time.Time]battery.DayConsumption, len(days))
	for _, d := range days {
		byDay[d.Date] = d // D-G2: d.Date verbatim, never effectiveDayUTC(d.Date)
	}

	type entry struct {
		label         string
		present       bool
		displayVal    float64
		markerFlagged bool
		markerSpan    bool
		tooltip       string
	}
	entries := make([]entry, 0, numDays)
	maxVal := 0.0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		label := d.Format("01-02")
		day, ok := byDay[d]
		if !ok {
			entries = append(entries, entry{label: label, present: false})
			continue
		}
		e := entry{
			label:         label,
			present:       true,
			displayVal:    math.Max(0, day.ConsumedPct), // D-G1: one clamp, no per-state branch
			markerFlagged: day.Flagged,                  // D10
			markerSpan:    day.DaysSpanned > 1,           // D20
		}

		// D-G7: compose independently-translated clauses, joined by " · ".
		pctStr := formatPctRaw(day.ConsumedPct) // the REAL signed value, never displayVal
		var clauses []string
		switch {
		case e.markerSpan:
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedSpanClause), pctStr, day.DaysSpanned))
		case !e.markerFlagged:
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedPctClause), pctStr))
			// markerFlagged && !markerSpan: no value clause at all — D10 hides the number.
		}
		if e.markerFlagged {
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedFlaggedClause), chargeTypeLabel(ctx, day.MissingChargingType)))
		}
		tooltip := label
		for _, c := range clauses {
			tooltip += " · " + c
		}
		e.tooltip = tooltip

		entries = append(entries, e)
		if e.displayVal > maxVal {
			maxVal = e.displayVal
		}
	}

	bars := make([]fragments.HistoryBar, 0, numDays)
	for _, e := range entries {
		if !e.present {
			bars = append(bars, fragments.HistoryBar{
				HeightPct: 0,
				Tooltip:   fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedNoDataTooltip), e.label),
				Label:     e.label,
				Present:   false,
			})
			continue
		}
		pct := 0
		if maxVal > 0 {
			pct = int(math.Round(e.displayVal / maxVal * 100))
		}
		bars = append(bars, fragments.HistoryBar{
			HeightPct:     pct,
			Tooltip:       e.tooltip,
			Label:         e.label,
			Present:       true,
			MarkerFlagged: e.markerFlagged,
			MarkerSpan:    e.markerSpan,
		})
	}
	return fragments.HistoryChart{Bars: bars, Empty: false, LabelVertical: labelVerticalFor(numDays)}
}

// chargeTypeLabel resolves the bilingual charge-type noun used inside a
// flagged-day tooltip. telemetry.MissingChargingType is a closed 2-value
// enum (tier 1); this is the gateway's own closed mapping to a catalogue
// key, kept here rather than in internal/telemetry because it is
// presentation vocabulary, not domain vocabulary.
func chargeTypeLabel(ctx context.Context, t telemetry.MissingChargingType) string {
	if t == telemetry.MissingChargingTypeSupercharger {
		return i18n.T(ctx, i18n.KeyHistoryChargeTypeSupercharger)
	}
	return i18n.T(ctx, i18n.KeyHistoryChargeTypeManual)
}
```

`buildHistoryView` extension (after the existing early-return on `telemetryReader` error —
design.md D-G10):

```go
	v.Odometer = buildOdometerChart(ctx, snaps, start, end)
	v.Battery = buildBatteryChart(ctx, snaps, start, end)

	days, err := h.batteryReader.ConsumedByDay(ctx, uid, teslaID, start, end)
	if err != nil {
		log.Printf("gateway: consumed-chart reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Consumed = fragments.HistoryChart{Empty: true}
		return v
	}
	v.Consumed = buildConsumedChart(ctx, days, start, end)
	return v
```

New imports in `history.go`: `"math"` (already imported), `"github.com/cristianpena/
magus-tesla-api/internal/battery"`.

### `handlers/handlers.go` changes

```go
// Deps additions:
	// BatteryReader is the battery module's read port; injected at
	// construction (mirrors TelemetryReader/SuperchargerReader/
	// ManualChargeReader — the gateway calls ConsumedByDay once per history
	// fragment render). NEVER construct an internal/battery internal type
	// here — internal/battery owns no database, so there is no db package
	// this could even accidentally import.
	BatteryReader battery.Reader

// Handler struct addition:
	batteryReader battery.Reader

// New() addition:
		batteryReader: d.BatteryReader,
```

New import: `"github.com/cristianpena/magus-tesla-api/internal/battery"`.

### `gateway.go` changes

Same three-line addition pattern as `SuperchargerReader`: `Deps.BatteryReader
battery.Reader` field with a doc comment, forwarded into `handlers.Deps{..., BatteryReader:
d.BatteryReader}` at `NewEngine`'s handler-construction call site.

## `cmd/web` wiring (specification for the leader's integration task, not implemented here)

`internal/gateway` is not the composition root — `cmd/web/main.go` is, and it lies outside
this dispatch's sandbox (`internal/` is the pipeline's Modules-Root; `cmd/` is not a module).
The exact wiring, mirroring `cmd/poller/main.go`'s already-shipped identical construction
(tier 3's `newGapReconciler` call site):

```go
// cmd/web/main.go, alongside the existing telemetry.NewReader(pool) /
// telemetry.NewSuperchargerReader(pool) / manualcharge.NewReader(pool) construction
// (main.go ~line 57-60) and the pre-existing `acct := account.NewService(...)` (~line 49):

engine, err := gateway.NewEngine(gateway.Deps{
	// ... existing fields unchanged ...
	TelemetryReader:    telemetry.NewReader(pool),
	SuperchargerReader: telemetry.NewSuperchargerReader(pool),
	ManualChargeWriter: manualcharge.NewWriter(pool),
	ManualChargeReader: manualcharge.NewReader(pool),
	// NEW:
	BatteryReader: battery.NewReader(
		telemetry.NewReader(pool),
		telemetry.NewSuperchargerReader(pool),
		manualcharge.NewReader(pool),
		acct,
		battery.DefaultWindow, // unused by ConsumedByDay — required by NewReader's
		                        // signature; only RecentEfficiency reads it, and the
		                        // gateway never calls that method (mirrors cmd/poller's
		                        // own comment at cmd/poller/main.go:84-86).
	),
	// ... remaining existing fields unchanged ...
})
```

New import in `cmd/web/main.go`: `"github.com/cristianpena/magus-tesla-api/internal/
battery"`. This is a THIRD separate `telemetry.NewReader(pool)`/
`telemetry.NewSuperchargerReader(pool)`/`manualcharge.NewReader(pool)` construction in the
process (`cmd/web` already builds one set for its own `Deps` fields, `cmd/poller` builds
another) — each is a cheap stateless wrapper over the shared `*pgxpool.Pool`, so duplicating
the constructor calls costs nothing at runtime and matches the existing `cmd/poller` pattern
exactly; do not attempt to share a single `battery.Reader` instance across `cmd/web`'s other
`Deps` fields, since none of them currently need one.

## Test Contract (authored before implementation, binding)

Per `ai/go-conventions.md`'s Test-Execution-Policy, these are written but not run by the
worker; `go vet ./...` compiles them as a signature-drift signal. All tests below are offline
(fakes for `battery.Reader`/`telemetry.Reader` — no `DATABASE_URL`).

**Fixture convention.** A `battery.DayConsumption` fixture sets exactly the fields
`buildConsumedChart` reads: `Date`, `ConsumedPct`, `DistanceKm` (unused by this tier's
rendering logic but kept for fixture realism), `Flagged`, `MissingChargingType`,
`DaysSpanned`. `Date` is always UTC-midnight (`time.Date(Y,M,D,0,0,0,0,time.UTC)`) — matching
what the real port returns (design.md D-G2).

### `buildConsumedChart` pure-function tests (`history_test.go`, extended)

**(a) Normal day, relative scale.** `days = [{Date: 08-10, ConsumedPct: 11.0}, {Date: 08-11,
ConsumedPct: 20.0}]`, window `[08-10..08-11]`. Expect: `maxVal = 20.0`; bar for 08-10:
`Present=true, MarkerFlagged=false, MarkerSpan=false, HeightPct=55` (round(11/20*100)),
`Tooltip = "08-10 · 11.0% consumed"` (EN: label + `i18n.KeyHistoryConsumedPctClause`'s
`"11.0% consumed"` joined by `" · "`); bar for 08-11: `HeightPct=100`.

**(b) Flagged day, non-span — MANUAL.** `days = [{Date: 08-11, ConsumedPct: -5.0, Flagged:
true, MissingChargingType: telemetry.MissingChargingTypeManual, DaysSpanned: 1}]`. Expect:
`Present=true, MarkerFlagged=true, MarkerSpan=false, HeightPct=0`, `Tooltip = "08-11 ·
possible missing charge record (manual)"` (label + `i18n.KeyHistoryConsumedFlaggedClause`
only — NO value clause) — the tooltip does NOT contain "-5" or "-5.0" anywhere (D10: the raw
value is never shown, not even in the tooltip).

**(c) Flagged day, non-span — SUPERCHARGER, zero-with-distance case.** `days = [{Date: 08-12,
ConsumedPct: 0, DistanceKm: 42, Flagged: true, MissingChargingType:
telemetry.MissingChargingTypeSupercharger, DaysSpanned: 1}]`. Expect: `HeightPct=0`, `Tooltip
= "08-12 · possible missing charge record (Supercharger)"`.

**(d) Multi-day span, not flagged.** `days = [{Date: 08-13, ConsumedPct: 15.0, Flagged: false,
DaysSpanned: 3}]`, window max (from a sibling entry) `= 20.0`. Expect: `Present=true,
MarkerFlagged=false, MarkerSpan=true, HeightPct=75` (round(15/20*100)), `Tooltip = "08-13 ·
15.0% · covers 3 days"` (label + `i18n.KeyHistoryConsumedSpanClause` only).

**(e) Multi-day span AND flagged — D21's resolved case, BOTH markers.** `days = [{Date: 08-14,
ConsumedPct: -3.0, Flagged: true, MissingChargingType: telemetry.MissingChargingTypeManual,
DaysSpanned: 2}]`, no other entries in window (so `maxVal = max(0, -3.0) = 0`). Expect:
`Present=true, MarkerFlagged=true, MarkerSpan=true` (BOTH — D21, not one-or-the-other),
`HeightPct=0` (both because `displayVal` clamps to 0 for a negative value AND because `maxVal`
is 0 here — this fixture deliberately cannot distinguish the two causes; see test (f) for a
fixture that isolates the clamp from the max), `Tooltip = "08-14 · -3.0% · covers 2 days ·
possible missing charge record (manual)"` — label + `KeyHistoryConsumedSpanClause` +
`KeyHistoryConsumedFlaggedClause`, in that order (design.md D-G7). The tooltip DOES contain
the real signed value "-3.0" (contrast with (b)/(c): D20's value clause is never suppressed by
a concurrent flag, D-G4's table row 4).

**(f) Multi-day span AND flagged, isolated from a zero max.** `days = [{Date: 08-14,
ConsumedPct: -3.0, Flagged: true, MissingChargingType: telemetry.MissingChargingTypeManual,
DaysSpanned: 2}, {Date: 08-15, ConsumedPct: 10.0, Flagged: false, DaysSpanned: 1}]`. Expect:
`maxVal = 10.0` (the negative span's clamped `0` never raises it); bar for 08-14:
`MarkerFlagged=true, MarkerSpan=true, HeightPct=0` (clamped to the axis floor, distinct this
time from "max was 0"); bar for 08-15: `MarkerFlagged=false, MarkerSpan=false, HeightPct=100`.

**(g) No-data day.** `days = []` for date `08-16` inside a window that has entries on other
days (so the chart itself is NOT `Empty`). Expect: bar for 08-16: `Present=false,
MarkerFlagged=false, MarkerSpan=false, HeightPct=0, Tooltip = "08-16 · no data"`
(`i18n.KeyHistoryConsumedNoDataTooltip` — NOT the existing `KeyHistoryNoSnapshotTooltip`'s "no
snapshot" wording).

**(h) Empty chart.** `days = []` for the ENTIRE window. Expect: `HistoryChart{Empty: true,
LabelVertical: ...}` — no bars at all (mirrors `buildBatteryChart`'s zero-entries rule, NOT
`buildOdometerChart`'s "fewer than 2" rule).

**(i) Bucketing uses `Date` verbatim — regression guard for D-G2.** `days = [{Date:
time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), ConsumedPct: 11.0}]`, window `[08-10..08-10]`.
Assert the returned bar's `Label == "08-10"` and `Present == true` WITHOUT the fixture ever
setting an `EffectiveDate`-shaped field (the type has none — this test exists to catch a
future regression that tries to re-derive a bucket day from something other than `Date`
itself, e.g. by accidentally piping it through `effectiveDayUTC`, which would be a no-op here
only by coincidence and must not be relied on).

**(i2) `math.Max(0, ConsumedPct)` alone excludes a flagged day from the scale — regression
guard for the dead branch D-G1 removed.** `days = [{Date: 08-17, ConsumedPct: -8.0, Flagged:
true, MissingChargingType: telemetry.MissingChargingTypeManual, DaysSpanned: 1}, {Date: 08-18,
ConsumedPct: 12.0, Flagged: false, DaysSpanned: 1}]`. Expect: `maxVal = 12.0` (the flagged
day's `math.Max(0, -8.0) == 0` never raises it — no separate per-state exclusion is involved);
bar for 08-17: `MarkerFlagged=true, MarkerSpan=false, HeightPct=0`; bar for 08-18:
`HeightPct=100`. **This test exists specifically to fail if a future edit reintroduces a
`if !markerSpan && markerFlagged { return 0 }`-shaped branch believing it is load-bearing** —
it is not (design.md D-G1): the general clamp already produces the same `0` for every flagged
entry, span or not, because `Flagged ⇒ ConsumedPct <= 0` (tier 3 D5). A reintroduced branch
would not change this test's outcome (it's semantically a no-op), so this test alone cannot
catch a *reintroduced-but-harmless* branch — its value is pinning the CORRECT single-clamp
behavior so a reviewer comparing implementation to design.md sees the one-liner is sufficient,
not that a branch was silently dropped without a behavioral check.

### `parseHistoryRange` D11 tests (`history_test.go`, existing tests UPDATED)

**(j) Both-absent default now ends yesterday.** `today = 2026-08-16` (UTC). Call
`parseHistoryRange(c, today)` with no query params. Expect: `end = 2026-08-15`
(`today.AddDate(0,0,-1)`), `start = 2026-08-09` (`end - 6`), `ok = true`. — REPLACES
`TestParseHistoryRange_BothAbsent_DefaultSixDayWindow`'s current assertion of `end = today`.

**(k) Cap now rejects `end = today`.** `today = 2026-08-16`. Call with `?start=2026-08-10&
end=2026-08-16` (end == today). Expect: `ok = false` (400) — this is the **new** rejection;
previously `ok = true`. — REPLACES `TestParseHistoryRange_EndCapUsesBrowserToday`'s "end=today
(browser): want ok=true" assertion; that scenario's title itself becomes misleading post-D11
and should be renamed to reflect "end=yesterday is now the accepted boundary" (see tasks.md).

**(l) Cap still accepts `end = yesterday`.** Same `today`. Call with `?start=2026-08-10&
end=2026-08-15` (end == yesterday). Expect: `ok = true`.

**(m) `TestParseHistoryRange_EndAfterToday`'s existing fixture must be re-examined**: if its
"after today" date was already ≥ today (e.g. `2099-12-31` per the 400-cases table), it stays
`ok=false` unchanged — no update needed there. Only tests whose fixture equals exactly `today`
(not "after") need the (j)/(k)/(l) treatment above.

**(n) `TestDashboardHistoryFragment_DefaultWindowPassedToReader`,
`TestDashboardHistoryFragment_DaysParamIsIgnored`,
`TestDashboardHistoryFragment_DefaultWindowActivatesSixDayPreset`** — each currently computes
its expected `readStart`/window from `today`; update each to compute from `yesterday :=
today.AddDate(0,0,-1)` instead, per (j). `DefaultWindowActivatesSixDayPreset`'s existing
comment ("The API default (both-absent → end=today) is unchanged... NOT ?days=6") describes
exactly the divergence D11 removes — its assertion should change from "the API default
differs from the preset window" to "the API default NOW MATCHES the preset window" (both end
at yesterday) — see tasks.md for the precise rewrite.

### `Deps`/`Handler` wiring test (`handlers_test.go` or `history_test.go`, new)

**(o) `New(Deps{BatteryReader: fakeReader}).batteryReader` is the same instance.** A trivial
construction-forwarding test, mirroring however the existing suite verifies
`SuperchargerReader`/`ManualChargeReader` forwarding (grep the existing pattern before writing
this — do not invent a new verification shape for one more field).

## Migration Plan (implementation order for this module's worker(s))

Grouped into independent sub-tasks per `openspec/config.yaml`'s tasks rule — see tasks.md for
the full dependency graph with file-level detail. High-level order:

1. **VM + catalogue** (`history_vm.go`, `catalog.go`) — no dependents yet, can start
   immediately, touches files nothing else in this list touches.
2. **Template** (`history.templ`) — depends on (1)'s `HistoryBar.MarkerFlagged`/
   `MarkerSpan`/`HistoryView.Consumed` fields existing; regenerate with `make templ`.
3. **Format helper** (`format.go`) — independent, no dependency on (1)/(2).
4. **Deps wiring** (`gateway.go`, `handlers.go`) — independent of (1)-(3), can run in
   parallel; needs `internal/battery` to already exist (it does, tier 3 archived).
5. **Handler logic** (`history.go`: `parseHistoryRange` D11 change, `buildConsumedChart`,
   `buildHistoryView` extension) — depends on (1) types, (3) `formatPctRaw`, (4)
   `batteryReader` field all existing first.
6. **Tests** (`history_test.go` new + updated) — depends on (5) existing to compile against;
   D11 test updates (j)-(n) can be written as soon as (5)'s `parseHistoryRange` change lands,
   independently of the new consumed-chart tests (a)-(i2)/(o).
7. **`AGENTS.md` update** — independent, can happen any time after (4) is decided.
8. **LEADER-OWNED, separate**: `cmd/web/main.go` wiring — depends on (4)'s `Deps.BatteryReader`
   field existing; cannot start before this module's worker finishes step 4.

## Risks / Trade-offs

- **D-G4's flagged+span overlap is now settled (roadmap D21, owner ruling, 2026-08-16) — no
  longer an open risk.** This design's first revision carried a risk entry here about the
  interpretation being wrong; it is superseded by D21's direct ruling (both markers, both
  facts) and removed. The one residual, genuinely cosmetic risk: a bar carrying both marker
  chips (D-G5's stacked `y="1"`/`y="6"` strips) is visually busier than a single-marker bar —
  accepted as the correct trade-off per D21's own rationale ("picking one hides a fact that is
  true"); not a design gap, just a UI density note for whoever eyeballs the rendered chart.
- **Four reads per render instead of one.** `ConsumedByDay`'s three internal reads plus this
  chart's own is a real increase in query count for this endpoint. All four remain bounded and
  indexed (tier 1-3's read ports), so this is accepted per the Performance-Profile's explicit
  tolerance for reads that stay bounded — flagged here only so a future performance review has
  the number on record, not because it is believed to be a problem.
- **The UTC-vs-zoned label mismatch (D18/D18a) is user-visible**, not just an internal
  bookkeeping detail: a capture landing 00:00Z-05:00Z produces a consumed bar labeled one day
  off from its odometer/battery sibling bar. This tier does not soften that (e.g., with a
  disclaimer or footnote) because none was requested — recorded here so a future ticket that
  notices the offset finds the explanation immediately instead of re-investigating from
  scratch.
