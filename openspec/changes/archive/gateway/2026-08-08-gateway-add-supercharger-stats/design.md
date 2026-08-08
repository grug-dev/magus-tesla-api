# Design: gateway-add-supercharger-stats

## Context

Backlog item #4. The nav sidebar has carried a "Supercharger Stats" placeholder
(`Href="#"`, "Soon" badge, icon `analytics`) since `gateway-add-stitch-design-handoff`
(D9). `internal/telemetry` already stores Tesla-billed Supercharger sessions
(`supercharger_sessions`, Source B) and already exposes a read-only
`SuperchargerReader` port — `SuperchargerSessionsByVehicle(ctx, accountID, teslaID, limit)`
and `SuperchargerSessionsByAccount(ctx, accountID, limit)` — landed by a prior telemetry
change and unmodified here. This change turns the placeholder into a live route + page,
mirroring the `charges` gold-standard slice (view model → page/fragments → Gin handler →
routes) composed from the `ui/` kit.

All seven binding decisions below (D1–D7) were made **with the user** in the leader↔user
grill-me pass on 2026-08-08 and are not open for reinterpretation by this change or by
implementation. Each carries its rationale and the alternatives that were considered and
rejected.

## Goals / Non-Goals

**Goals**
- One page (`GET /supercharger-stats`) + one fragment (`GET /ui/supercharger-stats`) that
  show the session-selected vehicle's Supercharger charging stats over a chosen month window.
- KPI tiles, a kWh/month trend chart, and a full sessions table — all derived from **one**
  bounded read per render.
- Multi-currency-safe cost aggregation that never silently sums incompatible currencies.
- A live nav entry replacing the "Soon" placeholder.
- Record the explicit "no unattributed-session disclosure" limitation as a tested spec
  scenario, not a silently-dropped edge case.

**Non-Goals**
- Any `internal/telemetry` change — the `SuperchargerReader` port, its query, and the
  `supercharger_sessions` table are consumed as-is.
- Any new database object — table, column, index, constraint, view, or migration. See
  "No database change" below.
- Disclosing sessions whose VIN doesn't match a currently-registered vehicle (`TeslaID IS
  NULL`) — out of scope by design (D2), not a future-work item.
- A "top charging sites" breakdown — deferred; see "Future work" below.
- Pagination of the sessions table — the window + read cap already bound its size (D7).

## Decisions

### D1 — Scope: per-selected-vehicle

The page covers the vehicle currently selected in the session, resolved via the existing
`h.resolveSelectedVehicle(ctx, c, uid)` (the same mechanism the dashboard, history charts,
and manual-charge log already use). The read port call is
`SuperchargerSessionsByVehicle(ctx, accountID, teslaID, limit)`.

**Rationale:** consistency with the dashboard and history slices — every other per-vehicle
page in the gateway resolves the vehicle context the same way, so an agent implementing this
page looks up one already-established pattern instead of inventing a new one. Per-car totals
are also the meaningful unit for "how much did *this* car cost me at Superchargers."

**Rejected:**
- *Account-wide (all vehicles mixed).* Mixes cars with very different charging profiles into
  one number; the vehicle-scoped switcher becomes decorative on this page while every other
  page respects it — a tenancy-consistency smell as well as a UX one.
- *Account-wide + VIN filter chips.* Two interacting controls (month preset + VIN chips) on
  one page is a materially larger slice for a first cut, and the account-wide read is a
  second/larger read on a page whose profile is read-heavy by design.

### D2 — Unattributed sessions are silently out of scope (specified limitation)

`SuperchargerSession.TeslaID` is `*int64` and is `NULL` when the session's VIN does not match
a currently-registered vehicle. Such sessions can **never** match a `teslaID` filter passed to
`SuperchargerSessionsByVehicle`, so they are **not shown or counted** anywhere on this page.
This is a deliberate, specified limitation — not an oversight — and MUST appear in
`specs/gateway/spec.md` as an explicit GIVEN/WHEN/THEN scenario so a future change can't
silently regress or "fix" it without a conscious decision.

**Rationale:** the alternative — a second, account-wide read via
`SuperchargerSessionsByAccount` to find and disclose orphaned sessions — doubles the number of
reads on a read-heavy page for a case that is **empty for every single-car account** (the
overwhelmingly common case for this project's user base today). Doubling a hot-path read to
serve an edge case that doesn't fire for the primary user is the wrong trade on a
performance-sensitive proposal.

**Rejected:** a second account-wide read to disclose unattributed sessions. Explicitly
rejected by the user in the grill — see "Future work" for how this could be revisited.

### D3 — Page composition: three sections

1. A `ui.StatTile` KPI row (D7's four tiles).
2. A zero-JS SVG bar chart of **kWh per month**, following the existing
   `historyBarChart`/`HistoryBar` pattern in
   `internal/gateway/templates/fragments/history.templ`: the handler pre-computes each bar's
   `HeightPct` (int, 0–100) and `<title>` tooltip string; the template only lays out
   `<rect>`/`<title>` elements from the view model — no arithmetic, no unit handling (mirrors
   `internal/gateway/AGENTS.md` §"Charts are hand-rolled SVG").
3. A `ui.Table` of the sessions in the window (D7: unpaginated).

**Rationale:** tiles alone can't show a trend; a table alone can't summarize. Three sections
gives an at-a-glance summary (tiles), a shape-over-time signal (chart), and the drill-down
detail (table) — the same three-tier composition the dashboard already uses (bento tiles +
history charts + — no table there, but the manual-charge page supplies the table precedent).

**Rejected:**
- *Table-only.* No trend visibility; a user with a year of sessions has no way to see "did my
  monthly Supercharger spend go up."
- *A fourth "top sites" section.* `SiteLocationName` + `CountryCode` are on the domain type and
  would support a "most-visited sites" breakdown, but that's a distinct aggregation
  (group-by-site rather than group-by-month) that materially grows this slice's first cut.
  Deferred — recorded under "Future work" for the backlog, not built here.

### D4 — Cost aggregation groups by currency, never sums across currencies

Build `map[string]float64` (currency → total cost) from sessions where **both**
`TotalCost != nil` **and** `Currency != nil`. Render one line per currency in the cost tile
(e.g. "40.12 USD" on one line, "150,000.00 COP" on the next, when both are present). Sessions
with a nil cost or nil currency are **excluded from the cost aggregation only** — they still
count toward the Sessions tile and the Energy tile (their `EnergyKWh`, if non-nil, is still
summed).

**Rationale:** `TotalCost` and `Currency` are per-session and nullable on the domain type
(`telemetry.SuperchargerSession`); `CountryCode` on the same type proves cross-border
Supercharging is a real scenario for this platform (a user charging in more than one country
has sessions in more than one currency by construction — Tesla bills in local currency).
Summing "40 USD + 150,000 COP" as if they were the same unit would silently produce a
meaningless number — this project's own unit-of-measure discipline (`CLAUDE.md`'s
`_km`/`_kwh`/… suffix rule) exists precisely to prevent unit-blind arithmetic, and
`ai/go-conventions.md` explicitly exempts monetary amounts from the suffix convention **on the
condition** that they are paired with a `currency` column instead — i.e. currency-blindness is
never an acceptable simplification for money in this codebase.

**Rejected:**
- *Majority-currency-with-footnote.* Still presents a number as "the" total while silently
  discarding some sessions' cost — a knowingly-partial headline figure is worse than an
  honest multi-line breakdown.
- *No cost tile at all.* Defeats a core purpose of the page — "what did I spend at
  Superchargers" is exactly the question a Supercharger-stats page exists to answer.

### D5 — Time window: month presets `{3, 6, 12}`, default 6

A closed, small vocabulary mirroring `historyDayPresets` / `defaultHistoryDays` /
`clampHistoryDays` exactly:

```go
var superchargerMonthPresets = []int{3, 6, 12}
const defaultSuperchargerMonths = 6

func clampSuperchargerMonths(raw string) int {
    // same shape as clampHistoryDays: parse, reject non-preset → default
}
```

The `SuperchargerReader` port takes a `limit` (row count), **not** a time range — so the
window filter (`ChargeStartDateTime >= since`) is applied **in Go, in the handler**, after the
read, mirroring history's "window math stays in the caller" precedent
(`buildHistoryView`/`startOfDay`).

**Rationale:** months, not days — Supercharger sessions are far sparser than the nightly
telemetry snapshots the history charts window (most drivers Supercharge a handful of times a
month, not daily), so a day-granularity preset would produce mostly-empty short windows.
Reusing the exact `historyDayPresets` shape (a package-level slice + a named default constant
+ a `clampX` helper) means an agent implementing this recognizes the pattern instantly instead
of re-deriving validation logic — the AI-efficiency "closed vocabulary, one gold-standard
example to mirror" principle.

**Rejected:**
- *No selector at all (fixed window).* Removes a control the equivalent history page already
  gives the user; inconsistent UX between two very similar pages.
- *Adding an "All" sentinel (e.g. `0` meaning unbounded).* A closed preset vocabulary with a
  sentinel value is a bigger, leakier vocabulary (every consumer of `superchargerMonthPresets`
  now has to special-case 0) and doubles the aggregate-computation test matrix (bounded window
  vs. unbounded) for a case the 500-row read cap (D6) already approximates reasonably.

### D6 — Read cap: `const superchargerReadLimit = 500`

One named location, mirroring history's `LIMIT 400` safety cap
(`SnapshotsByVehicleSince`'s port-level cap). The port orders results
`charge_start_date_time DESC`, so the 500 most recent sessions are retained; the D5 window
filter then trims that set down to the selected month range in Go.

**Rationale:** an unbounded read (`limit=0`, which the port's own doc comment states maps to
`math.MaxInt32` — see `telemetry.SuperchargerReader`'s doc comment) on a hot, read-heavy page
directly contradicts the project's read-heavy performance profile precedent. 500 sessions is
generous headroom for any real single-vehicle Supercharger history (even frequent Supercharging
a few times a week for years stays well under 500 rows) while keeping the read bounded and the
in-memory aggregation cheap.

**Rejected:** passing `limit=0` (unbounded). Directly contradicts the "read performance is
mandatory" project-wide profile for no benefit — the D5 month window will discard almost all
of an unbounded read's extra rows anyway.

### D7 — Tiles: Sessions · Energy · Cost · Avg kWh per session

- **Sessions** = `len(filtered)` — count of sessions in the selected window (after D2's
  natural exclusion and D5's window filter, before any cost-specific exclusion).
- **Energy** = `Σ EnergyKWh` over sessions in the window where `EnergyKWh != nil` (nil-skip,
  not nil-as-zero, so a batch of unknown-energy sessions doesn't silently deflate the total —
  though in practice it's summed the same either way since skipping a nil contributes 0; the
  distinction matters for the average below).
- **Cost** = per D4 (map by currency, one line per currency, nil cost/currency excluded).
- **Avg kWh per session** = `energy / count(EnergyKWh != nil)`, guarded against
  divide-by-zero (renders "—" or an equivalent empty display when the count is 0).

The table renders **every** session in the window — no pagination, no "show more." The
window (D5) and the read cap (D6) already bound the row count, and the tiles, chart, and table
all derive from the **one** filtered slice built once per render, so their counts can never
diverge (a user can never see "12 sessions" in a tile and count 11 rows in the table).

**Rationale:** these four tiles answer the natural questions a Supercharger-stats page exists
for — how often, how much energy, how much money, how efficient per stop — without inheriting
D4's currency-split problem into a second tile (see rejected alternatives). Deriving every
section from one shared filtered slice, computed once, is both simpler code (one loop, several
accumulators) and a correctness guarantee (single source of truth per render) — an
AI-efficiency win: an agent modifying one tile's formula can't accidentally desync it from the
table because there is only one slice being read.

**Rejected:**
- *Avg cost per kWh.* Inherits the D4 currency split into a second multi-line tile — doubles
  the currency-aware-rendering surface for a single stat page's marginal value.
- *Avg duration.* `ChargeStartDateTime`/`ChargeStopDateTime` support it, but it wasn't part of
  what the user asked to see first; can be added later without touching the other three tiles
  (change-locality is preserved).
- *A 25-row table cap.* Would introduce a second, different count on the same screen (tiles
  say N sessions, table shows min(N, 25) rows) — directly contradicts the "single filtered
  slice, one source of truth" design above.

## Inherited precedent (not re-litigated here)

These behaviors follow existing gateway conventions and are restated in `specs/gateway/spec.md`
as scenarios, but the underlying pattern is not a new decision of this change:

- **Anonymous caller** → `c.Redirect(http.StatusFound, "/login")`, no data served (mirrors
  `DashboardHistoryFragment`).
- **No registered/selected vehicle** (`resolveSelectedVehicle` returns `false`) → render the
  page in its empty state, not an error (mirrors `dashboardFor`'s `NeedsConnect` shape, adapted
  to this page's simpler "nothing to show yet" empty state — no Tesla connect prompt is implied
  here since Supercharger data is a nightly-batch concern, not a live-connect concern).
- **Reader error** → `log.Printf` + degrade to empty tiles/chart/table. Never a 500 (mirrors
  `buildHistoryView`'s degrade-on-error shape).
- **Zero sessions in the window** → empty state (mirrors the history charts' empty-state
  fallback for sparse data).
- **All arithmetic, formatting, unit and currency rendering happens in the handler**; the Templ
  template does no arithmetic, no domain-method calls, no conditionals beyond simple presence
  checks (`ai/htmx-conventions.md` §"No business logic in templates").
- **Semantic theme tokens only** — never raw hex, never ad-hoc Tailwind colors
  (`ai/htmx-conventions.md` §"Styling").

## No database change

**This change adds, alters, or removes no database object.** It consumes the existing
`telemetry.SuperchargerReader` port (`SuperchargerSessionsByVehicle` /
`SuperchargerSessionsByAccount`) over the existing `supercharger_sessions` table, both shipped
by a prior `internal/telemetry` change and unmodified here. No new query, no new column, no new
index, no migration file. The `database` design gate
(`CLAUDE.md` → "Pipeline config" → `Design-Gates`) does not trigger for this change; this
section exists to make that explicit rather than silently omitting a schema discussion.

## View model (handler-computed, template-dumb)

Mirrors `HistoryView`/`HistoryChart`/`HistoryBar`'s shape (logic-free, pre-computed strings):

```go
// fragments package
type SuperchargerStatsView struct {
    Months      int      // selected/validated window (always one of Presets)
    Presets     []int    // superchargerMonthPresets, passed through for the selector
    Tiles       SuperchargerTiles
    Chart       HistoryChart          // reused type: Bars []HistoryBar, Empty bool
    Sessions    []SuperchargerRowVM
    Empty       bool     // true when zero sessions in the window (drives the page-level empty state)
}

type SuperchargerTiles struct {
    Sessions     string   // e.g. "14"
    Energy       string   // e.g. "612.4 kWh"
    CostLines    []string // one per currency, e.g. ["132.40 USD", "58,000.00 COP"]; nil when no priced sessions
    AvgKWh       string   // e.g. "43.7 kWh"; "—" when no session has a known EnergyKWh
}

type SuperchargerRowVM struct {
    DateLabel      string // ChargeStartDateTime formatted
    SiteLabel      string // SiteLocationName
    CountryCode    string
    EnergyLabel    string // "N.NN kWh" or "—" when EnergyKWh is nil
    CostLabel      string // "N.NN <currency>" or "—" when TotalCost/Currency is nil
    BillingType    string
}
```

`Chart` reuses the existing `fragments.HistoryChart`/`fragments.HistoryBar` types verbatim (one
bar per month in the window, `HeightPct` = kWh that month as a percentage of the window's
tallest month, `Tooltip` = `"<month> · <kWh> kWh"`) — no new chart-bar type is introduced,
honoring the closed-vocabulary principle.

## Data flow

```
page load / month-preset click → GET /supercharger-stats or GET /ui/supercharger-stats?months=N
  handler: currentUID → resolveSelectedVehicle(session) → (accountID, teslaID)   [D1]
           clamp months (clampSuperchargerMonths)                                [D5]
           since = startOfMonth(now).AddDate(0, -months+1, 0)  (or equivalent month-floor)
           telemetry.SuperchargerReader.SuperchargerSessionsByVehicle(
               ctx, accountID, teslaID, superchargerReadLimit)   [1 read, D6]
           filter sessions to >= since                                            [D5, in Go]
           build SuperchargerStatsView{ Months, Presets, Tiles, Chart, Sessions, Empty }
             — Tiles: Sessions/Energy/Cost(per-currency)/AvgKWh from the ONE filtered slice [D4, D7]
             — Chart: bucket filtered sessions by month, sum EnergyKWh per bucket, HeightPct
               relative to the tallest bucket
             — Sessions: map every filtered session to a SuperchargerRowVM (unpaginated)   [D7]
  templ:   #supercharger-stats-content → month selector(active=Months)
             + StatTile row (from Tiles)
             + kWh/month chart (reused historyBarChart-style component)
             + ui.Table (from Sessions)
```

## Risks / Trade-offs

- **[Cost tile grows tall with many currencies]** → acceptable for now; a user Supercharging
  across many currencies is an edge case this page still serves honestly (one line per
  currency) rather than lying with a false single total. Revisit only if real usage shows this
  is common.
- **[500-row cap could theoretically clip a very long window for an extremely heavy user]** →
  the D5 month presets top out at 12 months; even daily Supercharging for a year is ~365 rows,
  well under 500. No realistic single-vehicle account hits the cap within any preset window.
- **[Unattributed sessions (D2) are invisible with no on-page hint]** → deliberate; flagged as
  a specified, tested limitation rather than a silent gap. See "Future work."

## Migration Plan

None (no DB, no data change). Rollback = revert the gateway commit; the nav entry reverts to
`Placeholder: true` and the two new routes are removed.

## Future work

**Deferred from D3's rejected fourth section — a "Top charging sites" breakdown**
(`SiteLocationName` grouped, e.g. most-visited Supercharger locations by session count or
energy). Not built in this change; record it in `openspec/roadmaps/backlog.md` as a follow-up
item under the `gateway` module when picked up (the leader records the backlog entry — not
this artifact set).
