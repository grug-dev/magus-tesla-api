# Design — RM33-gateway-add-entries-dashboard

Source ticket: MAG-18 · Roadmap: `openspec/roadmaps/RM33-manual-record-status.md`, tier 3 of 3.
Roadmap decisions **D9, D10, D11, D13, D14** are binding, plus the 2026-08-29 tier-3 interview
decisions **D-RM33-9..12**. This document does not re-open any of them; where it fills a gap
none of them covers, it says so explicitly (**D-Range**, **D-Dot**, **D-Tiles**, **D-Empty**,
**D-Include**, **D-Refresh**, **D-Colspan**).

No database object is added, changed, or removed by this tier — the `database` design gate
(`CLAUDE.md` §Pipeline config) does not apply. This design.md exists to fix the Test Contract,
the date-range semantics, and the post-write refresh mechanics before implementation, per
`ai/go-conventions.md` §Testing "author their expected values up front" and the module's own
RD8 convention for any new client-side-vs-server-side interaction decision.

---

## Context

Facts read directly out of the repository, not recalled.

1. **`internal/charging.Reader.ListEntriesByVehicleBetween(ctx, accountID, teslaID, from, to)
   ([]Entry, error)`** (`internal/charging/charging.go:211`) already exists, built by tier 1
   ahead of this need: `[from, to]` inclusive of both bounds, ordered `charged_on DESC`
   (matching `ListEntriesByVehicle`'s own order — no re-sort needed, unlike
   `ListSessionsByVehicleBetween`, which is ASC and must be reversed by the caller), no `limit`
   parameter, always a non-nil empty slice on no match. **This is the read this tier switches
   the filtered list onto** — no new port method (D13).
2. **`charging.RequiredFieldsFor(status) []Field`** (`internal/charging/validation.go:24`,
   consumed by tier 2 already) returns `[ChargedOn, LocationKind]` for `IN_PROGRESS` and
   `[ChargedOn, LocationKind, EndedAt, EndBatteryPct]` for `DONE`. `ChargedOn` is a non-pointer
   `time.Time` (DB `NOT NULL`) and `LocationKind` is enforced unconditionally required by the
   gateway's own form validation (tier 2) — in practice both are always present on any entry the
   gateway itself wrote. The two fields that can genuinely be absent are `EndedAt` and
   `EndBatteryPct` (`*T`, nil when not yet supplied).
3. **`charging.Entry.Price` is `float64`, NOT `*float64`** (`internal/charging/charging.go:89`)
   — `NUMERIC(14,2) NOT NULL DEFAULT 0`. Tier 2's `parseChargeForm` coerces an empty submitted
   price to `0`. There is therefore **no way to distinguish "the user left price blank" from
   "the user typed 0"** once persisted — both are the stored value `0`. D9's completeness
   criterion ("price present") is interpreted here as `Price > 0`, not "Price is not the zero
   value" — see §D-Dot.
4. **`GET /ui/charges/list` (`ChargesListFragment`, `internal/gateway/handlers/charges.go:73`)
   takes no query parameters today** and is the Refresh button's only caller
   (`charges_list.templ:18-28`). This is the route D-RM33-10 repurposes into the date-filter
   endpoint — same path, same handler function name, new query contract.
5. **`ChargeRowDelete` (`charges.go:362`) targets `#charge-row-{id}` on both success
   (`ChargeRowEmpty`) and failure (`ChargeRowError`, `<td colspan="8">`).** Neither path touches
   `#charges-list`. Once this tier adds aggregation tiles computed over the SAME rows the table
   shows, a delete that does not refresh those tiles desynchronizes them from the table on the
   very first delete after this ships — see §D-Refresh.
6. **`ChargeCreateSuccessOOB` (`charge_create_form.templ:142`) already OOB-refreshes
   `#charges-list`** via `<div id="charges-list" hx-swap-oob="outerHTML:#charges-list">
   @ChargesList(d)</div>` alongside its primary `#charges-create-form` swap — a proven, shipped
   pattern this tier extends to `ChargeRowUpdate` rather than inventing a second mechanism.
7. **`buildSuperchargerPresets`/`superchargerMonthsSelector`
   (`internal/gateway/handlers/supercharger.go:267`,
   `internal/gateway/templates/fragments/supercharger_stats.templ:19`)** is the literal pattern
   D-RM33-10 says to mirror: `ui.Join` wrapping one `ui.Button` per preset, `Variant: "primary"`
   when `RangePreset.Active`, else `"ghost"`, each button's `hx-get` a server-built absolute
   `?start=&end=` href. **`fragments.RangePreset` (`history_vm.go:93`) is already exported and
   reused by both `history.go` and `supercharger.go`** — `{Label, StartStr, EndStr, Active}` fits
   this tier's two presets with no change, so this tier reuses the type rather than adding a
   third. `browserToday(c)` (`handlers/tz.go:89`) is the browser-local-day helper D-RM33-10
   requires ("never UTC") — already used by `parseHistoryRange` and `buildChargesPage`'s
   existing form-default day, not by `parseSuperchargerRange` (which is deliberately plain UTC,
   design.md D9a of `RM30-gateway-read-supercharger-stats-from-charging` — an intentional
   divergence this tier does NOT inherit; charges' filter follows history's browser-local
   convention instead).
8. **`supercharger_row.templ`'s `windowStartStr`/`windowEndStr` threading**
   (`supercharger.go:193` `superchargerWindowStrs`, `supercharger_row.templ:34,49`) is the
   literal pattern D-RM33-11 says to mirror for the row-level Edit action URL: re-derive the
   window as pre-formatted strings and append `?start=&end=` to the row's own action link(s).
9. **`ChargeEntryVM`/`ChargesPageData` (`charges_vm.go`) and `chargeEntryVMFromEntry`
   (`charges.go:596`)** already carry `Status`/`RawStatus`, `RequiredEndedAt`/
   `RequiredEndBatteryPct`, and the `"—"` empty-placeholder convention for `EnergyKWh` (tier 2).
   `CostPerKWhLabel`, `BatteryDelta`, `DurationLabel` still fall back to `""` (not `"—"`) — this
   tier's D14 scope extends the em-dash convention to them.
10. **`ui.Badge{Kind, Text, Class}`** (`templates/ui/badge.templ`) exists with
    `Kind ∈ {neutral, primary, success, warning, error, ghost}`. **No small colored-dot
    primitive exists in `ui/` today** — see §D-Dot for the new `ui.Dot` addition.
11. **`ui.StatTile{Label, Value, Desc, Class}`** (`templates/ui/stat_tile.templ`) is the exact
    component `buildSuperchargerTiles`/`superchargerTiles` already compose in a
    `grid grid-cols-2 md:grid-cols-4 gap-3` — this tier's four tiles reuse the identical
    component and layout, per the dispatch's explicit "mirror the superchargers stats-tile
    component" instruction.
12. **The i18n catalogue's own documented precedent** (`internal/gateway/i18n/catalog.go`, the
    comment immediately above `KeyChargesListHeaderDate`): "a table column header is a different
    semantic role with different length constraints than a form label or card title" — table
    headers get their OWN keys even when the English word coincides with an existing form-label
    or stat-tile key. This tier's new table headers, status-badge text, and tile labels follow
    that precedent: new keys, never reuses of `KeySupercharger*` or `KeyChargesForm*`.

---

## Goals / Non-Goals

**Goals:**
- A two-preset (`last 7 days` / `this month`) date filter on `GET /ui/charges/list`, following
  the platform's `?start=&end=` convention and the Supercharger Stats preset-button idiom
  exactly.
- Four aggregation tiles, provably summed from the same rows the table renders.
- A Status column carrying a two-state completeness dot + a lifecycle badge, both
  handler-computed.
- A battery-range column alongside the existing delta column; the Vehicle column and Refresh
  button removed.
- Every blank table cell renders `—`.
- The active filter window survives a create, an edit, or a delete — no silent reset to the
  default.
- No-vehicle collapses to the existing "no entries" empty state, no account-wide fallback read.

**Non-goals:**
- Any change to `internal/charging` (tier 1's territory, archived).
- Any further change to the two charge FORMS beyond the one window-threading addition this
  tier needs (tier 2's territory, archived).
- A chart for the entries dashboard — the roadmap tier row names only tiles + a table, unlike
  Supercharger Stats.
- A general-purpose "restore filter across every region" mechanism beyond the one `hx-include`
  wiring this tier's create form needs (§D-Include) — no speculative generality.
- Per-currency cost splitting — manual entries are COP by construction (D13); this tier does not
  add multi-currency support to charging.

---

## Decisions

### D-Range — `parseChargesRange`: a THIRD `?start=&end=` shape, deliberately divergent from history/supercharger in two ways

A new `parseChargesRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool)`,
mirroring `parseHistoryRange`/`parseSuperchargerRange`'s shape (both absent → default; either
present → both required and well-formed; `end.Before(start)` → reject; window-width cap →
reject) but diverging on two points, both forced by D-RM33-10's literal requirements:

1. **No "future end" rejection.** `parseHistoryRange` rejects `end` after browser-yesterday;
   `parseSuperchargerRange` rejects `end` after UTC-today. Neither bound works here: D-RM33-10
   requires "This month" to be the FULL calendar month (1st through last day), which for any day
   before the last of the current month has an `end` that is **in the future relative to
   `today`**. Manual entries are also user-asserted, direct writes (no nightly-batch capture
   lag the way telemetry has), so there is no "the data for that day doesn't exist yet" reason
   to reject a future `end` the way history does. `parseChargesRange` therefore has NO
   after-today rejection at all — a caller-supplied `?end=` further in the future than "this
   month" is still accepted (bounded only by the width cap below), which is intentionally
   permissive: nothing about the domain forbids logging a charge dated in the future, and
   inventing a narrower rule than the one preset this tier itself renders would create a
   filter button that could 400 on its own generated href under a stale `today` (a request
   issued right at a day boundary).
2. **Default end is `today`, not `today - 1`.** `parseHistoryRange`'s default window ends at
   browser-yesterday because the nightly batch captures today's telemetry tomorrow — an
   `end=today` window's last bar would always be empty. No such lag exists here: an entry the
   user just saved is visible immediately. The default ("last 7 days") is therefore
   `today.AddDate(0,0,-6) .. today`, an inclusive 7-day window ending today.

Width cap: `chargesRangeMaxDays = 400`, reusing `superchargerRangeMaxDays`'s exact value and its
stated rationale (`internal/gateway/AGENTS.md` §"HTTP date-filter convention": the cap is set by
the source table's row density, not copied by default). `manual_charge_entries` is, if anything,
SPARSER than `charge_sessions` (hand-typed by the user vs. Tesla-billed and Tesla-reported), so
the same 400-day cap that already protects the comparably-sparse Supercharger table is not an
under-estimate here; there is no live density measurement available to this artifact-only
dispatch, so this is a reasoned analogy, not a measured number — flag for revisit if a future
change finds it wrong in practice.

```go
const chargesRangeDefaultDays = 7
const chargesRangeMaxDays     = 400

func parseChargesRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
    rawStart := c.Query("start")
    rawEnd := c.Query("end")
    if rawStart == "" && rawEnd == "" {
        end = today
        start = end.AddDate(0, 0, -(chargesRangeDefaultDays - 1))
        return start, end, true
    }
    s, err := time.Parse("2006-01-02", rawStart)
    if err != nil { return time.Time{}, time.Time{}, false }
    e, err := time.Parse("2006-01-02", rawEnd)
    if err != nil { return time.Time{}, time.Time{}, false }
    if e.Before(s) { return time.Time{}, time.Time{}, false }
    if e.Sub(s).Hours()/24 > float64(chargesRangeMaxDays) { return time.Time{}, time.Time{}, false }
    return s, e, true
}
```

**Rejected alternative:** reuse `parseHistoryRange` or `parseSuperchargerRange` directly.
Rejected because both encode a "no future end" rule this tier's own "this month" preset would
routinely violate — reusing either verbatim would make the SHIP button 400 on its own href for
most days of most months.

### D-Presets — two named presets via the existing `RangePreset` type

```go
func endOfMonth(t time.Time) time.Time {
    return startOfMonth(t).AddDate(0, 1, 0).AddDate(0, 0, -1)
}

func buildChargesPresets(ctx context.Context, start, end, today time.Time) []fragments.RangePreset {
    last7Start := today.AddDate(0, 0, -(chargesRangeDefaultDays - 1))
    monthStart := startOfMonth(today)
    monthEnd := endOfMonth(today)
    return []fragments.RangePreset{
        {
            Label:    i18n.T(ctx, i18n.KeyChargesRangeLast7Days),
            StartStr: last7Start.Format("2006-01-02"),
            EndStr:   today.Format("2006-01-02"),
            Active:   start.Equal(last7Start) && end.Equal(today),
        },
        {
            Label:    i18n.T(ctx, i18n.KeyChargesRangeThisMonth),
            StartStr: monthStart.Format("2006-01-02"),
            EndStr:   monthEnd.Format("2006-01-02"),
            Active:   start.Equal(monthStart) && end.Equal(monthEnd),
        },
    }
}
```

`startOfMonth` is the EXISTING package-level helper in `supercharger.go:57` (`package handlers`,
already unexported and reusable — no duplicate). `endOfMonth` is new, the one date-math primitive
this tier adds. Reuses `fragments.RangePreset` unchanged — no third preset type (Context fact 7).
The buttons render via a new `chargesRangeSelector(presets []fragments.RangePreset) templ`
component in `charges_list.templ`, structurally identical to `superchargerMonthsSelector` (`ui.Join`
+ `ui.Button` per preset, `hx-get` built from `p.StartStr`/`p.EndStr`, `hx-target="#charges-list"`,
`hx-swap="outerHTML"`, `Variant` switched on `p.Active` — D-RM33-10, verbatim).

### D-Dot — the completeness signal: predicate, colour, and a new `ui.Dot` primitive

**Predicate** (Context fact 3 — `Price > 0`, not merely non-zero-typed, is the only honest
reading of "price present" given the column has no NULL state):

```go
// entryComplete reports whether e satisfies D9's completeness bar: every
// field RequiredFieldsFor(StatusDone) would require, PLUS energy present
// AND price present. Evaluated identically regardless of e.Status itself —
// an IN_PROGRESS entry is correctly yellow until it acquires everything a
// DONE entry needs, which is the whole point of the signal (D9: "makes
// 'DONE but missing a price' visible").
func entryComplete(e charging.Entry) bool {
    return e.EndedAt != nil &&
        e.EndBatteryPct != nil &&
        e.EnergyAddedKWh != nil &&
        e.Price > 0
}
```

`ChargedOn`/`LocationKind` are omitted from the predicate: `ChargedOn` is a non-pointer
`time.Time` the DB itself makes `NOT NULL` (always present), and `LocationKind` is unconditionally
required by the gateway's own form (tier 2) — including either in the check can never turn a
green dot yellow for any entry the gateway itself wrote, so it is deliberately left out rather
than adding two checks that are always true.

**Colour** (D-RM33-12, two-state, no red): `entryComplete(e)` → `"success"` (green); else
`"warning"` (yellow) — DaisyUI semantic tokens, never hex, per the standing styling rule.

**`ui.Dot` — a new `ui/` primitive**, not an inline `<span>`. The status column needs a small,
tooltip-bearing colour indicator that `ui.Badge` cannot express (`Badge` always renders text
alongside its colour; this needs colour ALONE, plus a native-tooltip `title` attribute).
Following the SAME reasoning tier 2's design.md §D-Suffix applied to `ui.InputProps.Suffix`
("a single new capability is a smaller diff than a bespoke component, and the codebase needs
exactly one instance of it today") — but here the shape (a standalone element, not an existing
component's variant) makes a small new component the better fit rather than growing `Badge` with
a text-less mode:

```go
// DotProps configures a Dot — a small colored circle with a native hover
// tooltip, used where a Badge's mandatory text would be redundant with an
// adjacent label (e.g. the charges table's completeness signal, which sits
// next to its own text badge).
type DotProps struct {
    Variant string // "success"|"warning"|"error"|"neutral" (DaisyUI semantic token)
    Tooltip string // native title attribute; empty renders no tooltip
    Class   string
}

templ Dot(p DotProps) {
    <span
        class={ "inline-block w-2.5 h-2.5 rounded-full", dotClass(p.Variant), p.Class }
        if p.Tooltip != "" {
            title={ p.Tooltip }
        }
    ></span>
}
```

`dotClass` maps `Variant` to a `bg-{token}` class exactly like `badgeClass` already does for
`Badge` — same lookup shape, same file convention (`ui/badge.templ`'s `badgeClass` is the
pattern to mirror in a new `ui/dot.templ`). This keeps every DaisyUI-adjacent background token
routed through `ui/`, consistent with "never inline a DaisyUI component class" — though `rounded-full`/
`w-2.5`/`h-2.5` are themselves plain Tailwind utilities, not a DaisyUI component class, the
`bg-{token}` mapping is the one piece worth centralizing (a future third variant or a size prop
is then a one-file change, not a grep-and-replace across every call site).

**Rejected alternative:** inline the dot directly in `charge_row.templ` as a raw `<span
class={"inline-block w-2.5 h-2.5 rounded-full", "bg-" + variant}>`. Rejected because
string-concatenating a Tailwind class from a handler-supplied variant is exactly the kind of
ad-hoc value construction the `ui/` kit exists to prevent (`CLAUDE.md` §AI-efficiency: "closed,
small vocabularies... so output stays consistent across sessions and agents") — a future second
use of a colour dot (there is no shortage of candidate future uses: telemetry freshness,
connection status) would either duplicate the string-building or retroactively extract this same
component anyway.

Status badge (unrelated to the dot, the SECOND signal in the Status column, D9): reuses
`vm.RawStatus` (already computed by tier 2) to pick `ui.Badge{Kind: "primary", Text: ...}` for
`DONE` and `ui.Badge{Kind: "ghost", Text: ...}` for `IN_PROGRESS` — deliberately NOT `"success"`/
`"warning"` for the badge, so its colour vocabulary never collides with the adjacent dot's
completeness colours (a green badge next to a yellow dot could read as "the badge is also
warning you about something," which it is not).

### D-Tiles — `buildChargeTiles`, mirroring `buildSuperchargerTiles`'s shape, single-currency

```go
func buildChargeTiles(entries []charging.Entry) fragments.ChargeTiles {
    var energySum, costSum float64
    var energyCount int
    for _, e := range entries {
        if e.EnergyAddedKWh != nil {
            energySum += *e.EnergyAddedKWh
            energyCount++
        }
        costSum += e.Price // always COP by construction (D13) — no per-currency map
    }
    avg := "—"
    if energyCount > 0 {
        avg = fmt.Sprintf("%.1f kWh", energySum/float64(energyCount))
    }
    return fragments.ChargeTiles{
        Sessions: strconv.Itoa(len(entries)),
        Energy:   fmt.Sprintf("%.1f kWh", energySum),
        Cost:     formatMoney(costSum, "COP"),
        AvgKWh:   avg,
    }
}
```

Called over the SAME `[]charging.Entry` slice `ListEntriesByVehicleBetween` returned — before
the `chargeEntryVMFromEntry` mapping loop — so the tiles and the table are provably the same
data (D13). `Sessions`/`Energy`/`Cost` render `"0"`/`"0.0 kWh"`/`"0.00 COP"` on an empty slice
(D14's "0", not the em-dash — these are sums, not "value absent" fields); only `AvgKWh` uses
`—` (division-guarded, mirroring `buildSuperchargerTiles`'s identical `avgLabel` pattern
verbatim).

### D-Empty — three empty states, not one, deliberately diverging from Supercharger Stats' single collapse

Supercharger Stats collapses tiles+chart+table into ONE empty-state paragraph whenever the
session slice is empty (`v.Empty = len(sessions) == 0`), regardless of whether that emptiness
came from a genuinely empty window or a reader error. **This tier does NOT copy that collapse**
for the "valid vehicle, valid window, zero rows" case, because D13 is explicit: "Empty range
renders `0` and `—`" — the tiles must show real zero/em-dash values, not disappear. Three
distinct states instead:

1. **No vehicle resolved, OR a malformed `?start=&end=`** → `NoFilterChrome = true`: no
   presets, no tiles, no table — render ONLY the existing `ChargesEmptyState()` message
   (`i18n.KeyChargesListEmpty`, reused verbatim, D-RM33-9's literal "the existing empty state").
   Both conditions collapse to the identical render; they are reached by different codepaths
   (no-vehicle is HTTP 200 exactly as today; a malformed window is HTTP 400 per the platform's
   own "on 400, render the empty-state placeholder... and return no preset selector"
   convention, `internal/gateway/AGENTS.md` §"HTTP date-filter convention"). No read is
   attempted in either case.
2. **A reader error** → presets STILL shown (so the user can try a different window), tiles
   zero, `Error` set (rendered via the EXISTING `ui.Alert` inside `ChargesList` — unchanged
   mechanism, mirrors the pre-existing "Charge List Fragment" spec scenario "Reader failure
   degrades the list gracefully," which already required the create form and the page shell to
   stay usable on a reader error).
3. **Valid vehicle, valid window, reader succeeds, zero rows** → presets shown, tiles shown
   (all `0`/`—`, per D13), the table's body replaced by `ChargesEmptyState()` (same component
   as state 1, reused for its message, not its "hide everything" behavior) — this is
   `ChargesPageData.EmptyState`, the field that already exists.

**Rejected alternative:** copy Supercharger Stats' single `Empty` collapse verbatim (the
dispatch's "mirror the superchargers stats-tile component" instruction). Rejected because
D13's literal text ("Empty range renders `0` and `—`") is the MORE specific, MORE recently
confirmed instruction (2026-08-29 roadmap decision vs. an unrelated sibling tier's
already-shipped behavior) and the two cannot both be honored — the dispatch's "mirror" language
is read here as "reuse the STAT-TILE COMPONENT and button-selector idiom" (Context fact 11),
not "reuse Supercharger's empty-state collapse policy," which D13 overrides for this module.

### D-Include — the create form's window survives a filter click with NO new client-side JS

D-RM33-11's create-form half is harder than the edit-row half: the create form
(`#charges-create-form`) is a SIBLING of `#charges-list`, not a descendant, and D-RM33-10 pins
the filter buttons' `hx-target` to `#charges-list` ONLY (verbatim) — so a filter click never
re-renders the create form, meaning any window value baked into the create form's OWN markup at
its last render goes stale the moment the user picks a different preset.

The fix uses htmx's own `hx-include` attribute — verified via Context7
(`/bigskysoftware/htmx`, 2026-08-29 query) against the current htmx docs: `hx-include` accepts a
plain CSS selector that is evaluated GLOBALLY (not scoped to the closest form or the triggering
element's subtree) at REQUEST TIME, and any matched `<input>`/`<select>`/`<textarea>` elements'
current values are folded into the request — the documented "Include Global Elements" example
(`<div hx-get="/submit" hx-include="#my-form">`) is exactly this shape. Two stable-id hidden
inputs are rendered ONCE, as part of `ChargesList` itself:

```templ
<input type="hidden" id="charges-window-start" value={ d.WindowStartStr }/>
<input type="hidden" id="charges-window-end" value={ d.WindowEndStr }/>
```

and the create form's `<form>` gains:

```
hx-include="#charges-window-start, #charges-window-end"
```

Because these two inputs live INSIDE `#charges-list` (which the filter buttons DO refresh),
their values are always current at the moment htmx re-queries the DOM for the create form's
`hx-include` targets — which happens at SUBMIT time, not at the create form's own last-render
time. No polling, no JS, no new RD entry: `hx-include` is a declarative htmx attribute, not
client-side script, so it does not touch the module's "no client-side JS init" rule
(`ai/htmx-conventions.md` §Styling) at all — it is the SAME category of attribute-only wiring as
every other `hx-*` binding already in this codebase.

`ChargeCreate` reads the included values with a small best-effort helper (never a validation
gate — an absent or malformed pair only affects which window the SUCCESS OOB refresh re-renders,
never whether the write itself succeeds):

```go
// windowFromForm resolves the active start/end for a write handler's
// post-write OOB refresh, from POSTed form values (design.md §D-Include).
// Falls back to the default window's own strings on an absent or malformed
// pair — this is cosmetic threading only (D-RM33-11), never a 400 gate.
func windowFromForm(c *gin.Context, today time.Time) (start, end time.Time) {
    s, errS := time.Parse("2006-01-02", c.PostForm("start"))
    e, errE := time.Parse("2006-01-02", c.PostForm("end"))
    if errS != nil || errE != nil || e.Before(s) {
        end = today
        start = end.AddDate(0, 0, -(chargesRangeDefaultDays - 1))
        return start, end
    }
    return s, e
}
```

`ChargeRowDelete`'s GET-query sibling `windowFromQuery` is the SAME best-effort helper reading
`c.Query` instead of `c.PostForm`; both delegate to one shared fallback so the "never a gate"
behaviour cannot drift between them:

```go
// bestEffortWindow parses a start/end pair from whichever source the caller
// reads, falling back to the default window. Cosmetic threading only
// (D-RM33-11) — never a validation gate, in either direction.
func bestEffortWindow(rawStart, rawEnd string, today time.Time) (start, end time.Time) {
    s, errS := time.Parse("2006-01-02", rawStart)
    e, errE := time.Parse("2006-01-02", rawEnd)
    if errS != nil || errE != nil || e.Before(s) {
        end = today
        start = end.AddDate(0, 0, -(chargesRangeDefaultDays - 1))
        return start, end
    }
    return s, e
}

func windowFromForm(c *gin.Context, today time.Time) (start, end time.Time) {
    return bestEffortWindow(c.PostForm("start"), c.PostForm("end"), today)
}

func windowFromQuery(c *gin.Context, today time.Time) (start, end time.Time) {
    return bestEffortWindow(c.Query("start"), c.Query("end"), today)
}
```

This is deliberately NOT `parseChargesRange`: that one REJECTS a malformed window (the caller
400s, D-Range), which is right for the filter route the user drives directly, and wrong for a
write's post-write refresh — a malformed window must never turn a successful delete into an
error.

**Rejected alternative:** widen the filter buttons' `hx-target` to a region containing BOTH the
create form and the list (e.g. `#charges-content`, mirroring `ChargesContentFragment`).
Rejected because it directly contradicts D-RM33-10's verbatim `hx-target="#charges-list"`
instruction, and because it would re-render (and reset any in-progress typing in) the create
form on every filter click for no benefit — the create form's OWN fields are unrelated to the
date filter.

**Rejected alternative:** a client-side JS listener keeping a hidden create-form field in sync
with whichever preset button is currently `Active` (a fifth zero-JS exception, RD14).
Rejected because `hx-include` solves the exact same problem declaratively, with no JS at all —
introducing JS here would violate the module's own "prefer a CSS-only/attribute-only pattern
before any JS" rule for a problem that does not need one.

### D-Refresh — extending D-RM33-11's "create/update" wording to delete, and how each write's OOB shape works

**Update (success only).** Mirrors `ChargeCreateSuccessOOB` (Context fact 6) rather than
inventing a second mechanism:

```go
templ ChargeRowUpdateSuccessOOB(vm ChargeEntryVM, csrfToken, windowStartStr, windowEndStr string, list ChargesPageData) {
    @ChargeRow(vm, csrfToken, windowStartStr, windowEndStr)
    <div id="charges-list" hx-swap-oob="outerHTML:#charges-list">
        @ChargesList(list)
    </div>
}
```

The primary swap (`hx-target="#charge-row-{id}"`, unchanged) and the OOB swap
(`#charges-list`, its ancestor) both target elements that exist in the DOM when htmx processes
this response: htmx applies `hx-swap-oob` elements first, and because the OOB-refreshed
`#charges-list` itself contains a freshly-rendered `#charge-row-{id}` for the SAME entry (it is
one of the rows in the just-updated window), the primary swap still finds a live target
afterward and replaces it with byte-identical content — a harmless redundant re-paint, not a
DOM error. This is the same pattern `ChargeCreateSuccessOOB` already ships with (there, the two
targets are siblings, not ancestor/descendant, but htmx's OOB mechanism does not distinguish the
two cases — it only requires the target id to be resolvable at swap time, which it is here in
both orders). The validation-ERROR path is UNCHANGED (still targets only `#charge-row-{id}`,
still `renderError`) — nothing about a rejected save changes the tiles, so no refresh is needed
there.

**Delete (success AND failure) — target retargeted, no OOB needed.** Rather than reproducing the
same ancestor/descendant OOB shape for a case where the primary target is BEING REMOVED (a
harder edge case — see the rejected alternative below), the delete button's `hx-target` changes
from `#charge-row-{id}` to `#charges-list`, `hx-swap="outerHTML"` (matching the filter buttons'
own target exactly). `ChargeRowDelete` becomes structurally identical to `ChargesListFragment`,
preceded by the actual `Writer.Delete` call:

```go
func (h *Handler) ChargeRowDelete(c *gin.Context) {
    // ...auth, CSRF, id-parse unchanged...
    today := browserToday(c)
    start, end := windowFromQuery(c, today) // ?start=&end= on the row's own Delete URL

    entryTeslaID, entryChargedOn, hadEntry := h.fetchEntryTeslaIDAndChargedOn(...)
    err := h.chargingWriter.Delete(c.Request.Context(), uid, id)
    d := h.buildChargesPage(ctx, uid, csrfToken, teslaIDFilter, today, start, end)
    if err != nil {
        log.Printf(...)
        d.Error = i18n.T(ctx, i18n.KeyChargesErrorCouldNotDeleteEntry)
        renderFragmentError(c, http.StatusInternalServerError, pages.ChargePage(d), "charges-list")
        return
    }
    if hadEntry {
        h.recalculateAfterChargeWrite(...)
    }
    renderFragment(c, http.StatusOK, pages.ChargePage(d), "charges-list")
}
```

This reuses the SAME `buildChargesPage` list-building path (and therefore the SAME
`renderFragment`/`renderFragmentError` machinery) every other list render already uses — no new
render function. `fragments.ChargeRowEmpty` and `fragments.ChargeRowError` become dead code
(nothing calls either after this change) and are removed in the same change (tasks.md), verified
unreferenced first.

**Rejected alternative (delete):** keep the primary target at `#charge-row-{id}` and add an OOB
`#charges-list` refresh, mirroring Update exactly. Rejected because after a successful delete the
OOB-refreshed `#charges-list` no longer contains ANY element with that row's id (the entry is
gone) — the primary swap's target has been removed from the DOM by the time htmx tries to apply
it, which is a real (if likely silently-no-op'd) edge case Update's mirrored pattern does not
have to face, since Update's row still exists post-write. Retargeting to `#charges-list` directly
sidesteps the question rather than relying on unverified htmx behavior for a vanished target.

**Rejected alternative (both):** `HX-Retarget`/`HX-Reswap` response headers, letting the SAME
static `hx-target` markup be overridden per-response (success vs. failure) from the server.
Rejected in favor of the OOB pattern for Update specifically because `ChargeCreateSuccessOOB`
already proves the OOB approach works in this exact codebase; introducing a header-based
redirect mechanism nobody has used here yet would trade a proven pattern for an unproven one to
save a harmless redundant re-paint — not a good trade for a change this contained.

### D-Colspan — the deferred fix from tier 2

Tier 2's design.md §D-Colspan explicitly deferred `charge_row_edit.templ`'s
`<td colspan="8">` to this tier ("tier 3... adds a net column-count change tier 3 must carry its
own colspan update for"). The table grows from 8 to 9 columns (Vehicle removed, Status and
Battery Range added: net +1). `colspan="8"` → `colspan="9"`.

---

## Roadmap-decision mapping

| Decision | Where honored |
|---|---|
| **D9** — Status column, both signals | D-Dot (predicate, colour, badge) |
| **D10** — completeness colour computed in the Go view model | D-Dot (`entryComplete` is a pure Go helper called from `chargeEntryVMFromEntry`, never the template) |
| **D11** — battery-range column added ALONGSIDE the delta | tasks.md (`ChargeEntryVM.BatteryRange`, new column in `charges_list.templ`/`charge_row.templ`) |
| **D13** — filter + tiles computed server-side from ONE query, single COP total | D-Range, D-Presets, D-Tiles, D-Empty |
| **D14** — `—` for empties | D-Empty (tiles' `0` vs `—` split), tasks.md (`CostPerKWhLabel`/`BatteryDelta`/`DurationLabel`/`BatteryRange` em-dash fallback) |
| **D-RM33-9** — no-vehicle fallback drops `ListEntriesByAccount`, renders the existing empty state | D-Empty (state 1) |
| **D-RM33-10** — filter wiring mirrors `superchargerMonthsSelector`, browser-local day, "this month" is the full calendar month | D-Range, D-Presets |
| **D-RM33-11** — post-write window preservation, threaded via hidden inputs / URL params mirroring `supercharger_row.templ` | D-Include (create form), D-Refresh (edit row + both write paths) |
| **D-RM33-12** — two-state completeness dot (green/yellow, no red) | D-Dot |

---

## Test Contract

Fixed here, before implementation, per `ai/go-conventions.md` §Testing authoring order. All of
these are `httptest`-style handler tests in `internal/gateway/handlers/charges_test.go`, mirroring
the existing suite's fake-port style (no DB). No `DATABASE_URL`-gated test is added by this
tier — the gateway owns no database. Several EXISTING tests must be REWRITTEN, not merely
extended, because this tier changes `ChargesListFragment`'s query contract and `ChargeRowDelete`'s
target — see the explicit list at the end of this section.

### Group A — `parseChargesRange` / `buildChargesPresets` (offline, pure functions, no gin needed beyond a query-carrying `*gin.Context`)

- **A1.** Both `start`/`end` absent, `today = 2026-08-29` → `start = 2026-08-23`,
  `end = 2026-08-29`, `ok == true` (7-day inclusive default).
- **A2.** `?start=2026-08-01&end=2026-08-31`, any `today` in August 2026 → `ok == true` (a
  valid, non-default window is always accepted, not just the two presets).
- **A3.** `?end=2026-09-15`, `today = 2026-08-29` (an `end` AFTER today) → `ok == true` — the
  explicit no-future-rejection assertion (D-Range point 1). Contrast with `parseHistoryRange`'s
  own test suite, which asserts the OPPOSITE for its own endpoint; this test exists specifically
  to pin the divergence.
- **A4.** `?start=2026-08-01` with no `end` (partial pair) → `ok == false`.
- **A5.** `?start=2026-09-01&end=2026-08-01` (`end` before `start`) → `ok == false`.
- **A6.** A window wider than 400 days → `ok == false`; the SAME window narrowed to exactly 400
  days → `ok == true`.
- **A7.** `buildChargesPresets` with `today = 2026-08-15` (a 31-day month) → the "This month"
  preset's `EndStr == "2026-08-31"` (the LAST day of August, not `"2026-08-15"` — the
  month-to-date rejection, D-RM33-10's literal "full calendar month" requirement) and
  `StartStr == "2026-08-01"`.
- **A8.** `buildChargesPresets` called with `(start, end)` exactly matching the "last 7 days"
  preset's own computed window → that preset's `Active == true`, "this month"'s `Active ==
  false`. Called with a CUSTOM (non-preset) window → both `Active == false`.

### Group B — `entryComplete` / `buildChargeTiles` (offline, pure functions)

- **B1.** An entry with `EndedAt`, `EndBatteryPct`, `EnergyAddedKWh` all non-nil and `Price >
  0` → `entryComplete == true`.
- **B2.** Four variants, each with exactly ONE of the four inputs missing/zero (`EndedAt ==
  nil`; `EndBatteryPct == nil`; `EnergyAddedKWh == nil`; `Price == 0`) → `entryComplete ==
  false` in all four cases — proving the predicate is a strict AND, not "most fields present."
- **B3.** An `IN_PROGRESS` entry with `EndedAt == nil` (the normal, valid shape for that status)
  → `entryComplete == false` — confirming the predicate does NOT special-case `IN_PROGRESS`
  (D9's "makes DONE but missing a price visible" implies evaluating the SAME bar regardless of
  status).
- **B4.** `buildChargeTiles` over an empty `[]charging.Entry{}` → `Sessions == "0"`, `Energy ==
  "0.0 kWh"`, `Cost == "0.00 COP"`, `AvgKWh == "—"` (D13/D14's "0 and —" split, asserted
  per-field).
- **B5.** `buildChargeTiles` over three entries: one with `EnergyAddedKWh = 10.0`, `Price =
  1000`; one with `EnergyAddedKWh = nil`, `Price = 500`; one with `EnergyAddedKWh = 5.0`, `Price
  = 0` → `Sessions == "3"`, `Energy == "15.0 kWh"` (nil-skip), `Cost == "1500.00 COP"` (summed
  regardless of nil energy), `AvgKWh == "7.5 kWh"` (15.0 / 2, the count of non-nil-energy
  entries, NOT 3).

### Group C — the completeness dot / status badge render (offline, rendered-HTML assertions)

- **C1.** A `DONE`, fully-complete entry's row renders `ui.Dot` with a class containing
  `bg-success` (or whatever `dotClass("success")` resolves to) and the status badge text
  matches the DONE label.
- **C2.** A `DONE` entry missing `EndBatteryPct` (a data state the domain permits even though
  the gateway's own form now requires it for a NEW `DONE` save — an entry could have been
  edited by an earlier code path or seeded directly) renders the WARNING dot class, never the
  success one, and never any red/error variant (D-RM33-12 — assert the rendered class is never
  `bg-error`).
- **C3.** An `IN_PROGRESS` entry (the normal shape, `EndedAt`/`EndBatteryPct` both nil) renders
  the warning dot AND the IN_PROGRESS badge — both signals independently correct on the same
  row.

### Group D — window preservation (offline, `httptest`)

- **D1.** `POST /ui/charges/create` with a valid submission AND `start=2026-08-01&end=2026-08-31`
  in the form body (simulating `hx-include`) → the response's OOB `#charges-list` div contains
  rows/tiles built from that window, not the default 7-day window — assert via the rendered
  window-selector's "this month" button carrying the active-variant marker, or via a
  Templ-rendered substring check on the hidden `#charges-window-start`/`#charges-window-end`
  values.
- **D2.** Same as D1 but the `start`/`end` form fields are ABSENT (a client with `hx-include`
  disabled/stripped) → the OOB refresh falls back to the default 7-day window, `ok`/success
  status unaffected (D-Include's "cosmetic only, never a validation gate").
- **D3.** `PUT /ui/charges/row/{id}` with a valid submission and hidden `start`/`end` inputs
  set to a non-default window → the response's OOB `#charges-list` div reflects that window
  (mirrors D1 for the edit-row path).
- **D4.** `DELETE /ui/charges/row/{id}?start=2026-08-01&end=2026-08-31` → the response is a
  full `#charges-list` fragment (not a bare `<tr>`) reflecting the post-delete state within
  THAT window, and the deleted row's id is absent from it.

### Group E — no-vehicle / malformed-window / reader-error empty states (offline, `httptest`)

- **E1.** A signed-in user with ZERO registered vehicles requests `GET /charges` → the response
  contains `ChargesEmptyState()`'s message and contains NEITHER a preset button NOR any
  `ui.StatTile` markup NOR a `<table>` (D-RM33-9 — assert absence, not just presence of the
  message).
- **E2.** `GET /ui/charges/list?start=not-a-date&end=2026-08-31` → HTTP 400, same
  no-chrome assertions as E1 (D-Empty state 1, malformed-window branch).
- **E3.** A valid vehicle + valid window, fake `Reader` returns an error → response contains the
  preset buttons AND four `ui.StatTile`s (all zero/`—`) AND an `ui.Alert` with the error
  message — NOT the same as E1/E2 (D-Empty state 2 is a strictly different render from state 1).
- **E4.** A valid vehicle + valid window, fake `Reader` returns `[]charging.Entry{}` (no error)
  → response contains the preset buttons, tiles showing `0`/`—` (not hidden), and
  `ChargesEmptyState()`'s message in place of table rows (D-Empty state 3).

### Existing tests requiring REWRITE, not extension (enumerated so the test-writing worker does not silently break them)

- `TestChargesListFragment_WithEntries`, `TestChargesListFragment_ReaderError`,
  `TestChargesListFragment_EmptyState` (`charges_test.go:384,421,445`) — must now account for
  the default-window `?start=&end=` resolution and the new tiles/presets in the response.
- `TestChargeRowDelete_ValidInput_RendersEmptyRow` (`charges_test.go:829`) — the response is no
  longer a bare empty `<tr>`; rename/rewrite to assert the full `#charges-list` fragment shape
  (Group D4's assertion).
- `TestChargeRowDelete_ThenListReflectsRemoval` (`charges_test.go:1784`) — likely still valid in
  INTENT but must be re-pointed at the new response shape.
- The comment at `charges_test.go:853` referencing `ChargeRowError`'s `<td>` shape needs updating
  or removal once that template is deleted.

### Owner-verified, not automatable here

- `make i18n-guard` / `TestCatalog_AllKeysHaveBothLanguages` — deterministic signals Claude runs
  itself (`CLAUDE.md` §Builds & local checks), listed here because they are the actual
  enforcement mechanism for every new/changed catalogue key this tier adds.
- Visual confirmation that the completeness dot and status badge use visually distinct colour
  vocabularies (D-Dot's "never collide" intent) — a rendered-HTML class assertion proves the
  CLASSES differ, not that a human finds them visually distinguishable; the owner's own review
  of `make dev` output is the final check for that, same as any other new UI affordance.
