# Gateway Sub-Agent

Agent-Name: `gateway`

Per-module instructions for `internal/gateway/` — merged with the global rules
(`CLAUDE.md`, `ai/*.md`) by any assistant working here (see `ai/agentic-workflow.md`).

## Doc-Pack (module)

Extends the project base Doc-Pack (`CLAUDE.md` → "Pipeline config") — never replaces it.
A dispatched worker/reviewer reads: base pack + this list + this file, before any write.

- `ai/htmx-conventions.md` — Templ engine rules; binding for any markup in this module
- `ai/htmx-go-integration.md` — wiring htmx fragments to the Go handlers

## Responsibility

The web layer — the ONLY module allowed to produce HTML. Gin router with cookie
sessions, Google login flow, Tesla connect flow, dashboard pages and htmx fragments.
It renders what other modules expose; it owns no business data.

## Public interface

- `NewEngine(d Deps) (*gin.Engine, error)` (`gateway.go`) — builds the router; `cmd/web`
  calls this and nothing deeper.
- `Deps` struct — every collaborator arrives as a public Go interface (account, tesla,
  googleauth). New dependencies extend `Deps`; never construct another module's
  internals here.
- **The gateway does not depend on the telemetry module at all** — no `Deps` field, no
  `telemetry.*` type, no import of that module's package, anywhere under
  `internal/gateway/`. This was completed by `RM40-gateway-drop-telemetry-dependency`:
  the history fragment's battery chart (`history.go`, `/ui/dashboard/history`) was the
  module's last caller of that module's `Reader.SnapshotsByVehicleBetween` — it now reads
  `Deps.AnalyticsReader.BatteryLevelByDay(ctx, teslaID, start, end)` instead, with
  **no lookback** (the port returns exactly `[start, end]`, since `vehicle_metrics.
  metric_date` is already the effective day it needs). The four telemetry latest-state
  call sites (dashboard, vehicle cards, nav header, charges battery suggestion) were
  already repointed onto `Deps.AnalyticsReader.LatestMetricsForVehicles` below by the
  earlier `RM38-gateway-read-dashboard-from-metrics`. Do **not** reintroduce a
  `Deps.TelemetryReader` field or an import of that module's package — `make
  boundary-guard` enforces this repo-wide (fails on a non-test file, warns on a
  `_test.go` file; escape hatch `// boundary:allow: <reason>`, which this module
  carries **zero** of). Rule and migration status: `ai/architecture.md` §"Exception:
  the gateway may not depend on `telemetry` at all".
- `Deps.ChargingWriter charging.Writer` — the manual charge write port; injected
  at construction. Called ONLY by the write handlers (ExternalChargeCreate, ExternalChargeRowUpdate,
  ExternalChargeRowDelete) on explicit user-initiated form submissions. See "Exception:
  user-initiated writes" below. NEVER import `internal/charging/db` — all access
  through this interface only.
- `Deps.ChargingReader charging.Reader` — the manual charge read port; injected
  at construction. Called by read handlers (ExternalChargesPage, ExternalChargesListFragment,
  ExternalChargeRowStatic, ExternalChargeRowEditFragment) and the `buildExternalChargesPage` helper to list
  charge entries. NEVER import `internal/charging/db` — all access through this
  interface only.
- `Deps.SuperchargerReader charging.SessionReader` — the charging module's
  Supercharger-session read port; injected at construction via
  `gateway.Deps`/`handlers.Deps` (wired from `cmd/web` via
  `charging.NewSessionReader(pool)`). Called by `SuperchargerStatsPage` /
  `SuperchargerStatsFragment` (via `superchargerStatsViewFor` /
  `buildSuperchargerStatsView`) — ONE `ListSessionsByVehicleBetween` read per
  Supercharger Stats render, bounded by the requested `?start=&end=` window
  (not a row limit). Added by `gateway-add-supercharger-stats`; the port swap
  from `telemetry.SuperchargerReader` to `charging.SessionReader` and the
  `?months=N` → `?start=&end=` migration are
  `RM30-gateway-read-supercharger-stats-from-charging`. NEVER import
  `internal/charging/db` (`chargingdb`) for this path — all access through
  this interface only.
- `Deps.SuperchargerVerifier charging.SessionVerifier` — the charging
  module's Supercharger-session verification write port; injected at
  construction via `gateway.Deps`/`handlers.Deps` (wired from `cmd/web` via
  `charging.NewSessionVerifier(pool)`). Called ONLY by
  `SuperchargerRowUpdate` (`PATCH /ui/supercharger-stats/row/:id`), on an
  explicit user-initiated row save, to call `VerifySession` — the sole
  write this port permits (start/end battery percentage correction). See
  "Exception: Supercharger session battery verification" below. NEVER
  import `internal/charging/db` (`chargingdb`) for this path — all access
  through this interface only. Added by
  `RM31-gateway-add-session-battery-edit`.
- `Deps.AnalyticsMonthlyReader analytics.MonthlyReader` — the analytics module's
  PER-MONTH read port over `vehicle_monthly_metrics`, wired from `cmd/web` via
  `analytics.NewMonthlyReader(pool)`. One method, `MonthlyMetricsBetween`, called once
  per `/vehicle-stats` (or `/ui/vehicle-stats`) render. **Separate from
  `Deps.AnalyticsReader` on purpose** — the two read different tables at different
  grains (per-day `vehicle_metrics` versus the monthly rollup), so a page takes only
  the one it needs and a test fakes only that one; folding the method into
  `analytics.Reader` would have forced every existing fake of it, in two modules, to
  grow a method neither caller uses. **Its result is sparse BY MONTH**: the nightly
  poller writes only the current and previous month and nothing backfilled the rest,
  so a whole-year window legitimately returns fewer than twelve rows. Roll up whatever
  comes back; never index by month number or assume a count. Added by MAG-87.

- `Deps.AnalyticsReader analytics.Reader` — the analytics module's read
  port; injected at construction via `gateway.Deps`/`handlers.Deps` (wired
  from `cmd/web` via `analytics.NewReader(...)`). Called by
  `buildHistoryView`, once per `/ui/dashboard/history` fragment render.
  Methods used here: `ConsumedByDay`, populating the "Battery consumed" chart
  panel, and — since `RM29-analytics-add-vehicle-metrics` —
  **`OdometerDeltaByDay`**. `buildOdometerChart` no longer derives per-day
  distance from raw snapshots itself; that calculation moved into the module
  that owns it (roadmap D5), and the gateway reads the precomputed result.
  `internal/analytics` now DOES own a database (`analyticsdb`,
  `vehicle_metrics`) — this bullet used to note that it did not — so the
  standard rule applies here in full: NEVER import `internal/analytics/db`,
  all access through this interface only. Added by
  `RM28-gateway-add-consumed-graph` (tier 4), renamed from `battery` by
  `RM29-analytics-rename-from-battery`.
  Since `RM38-gateway-read-dashboard-from-metrics`, also **`LatestMetricsForVehicles`** —
  the latest status of each vehicle in the set you pass it, replacing the equivalent
  telemetry latest-state calls. It takes `[]vehicleref.Ref`, not an account id: the port
  no longer filters by account, so the caller must pass only vehicles it has already
  proven the account owns. Four callers: `dashboardFor` (single-
  vehicle bento, via `mapDashboardSnapshot`), `vehiclesFor`/`mapVehicles` (the unrouted
  `/ui/vehicles` card list — see `mapVehicles`'s own doc comment for why it is kept
  working despite having no route), `navHeaderFor` (status dot/battery — a nil
  `CapturedAt` forces `NavStatusAsleep`, never `NavStatusConnected`), and
  `buildExternalChargesPage`'s battery-suggestion lookup (`external_charges.go`). Nine of
  `analytics.VehicleStatus`'s fields are pointers (`InsideTempC`, `OutsideTempC`,
  `CarVersion`, `ChargeLimitSocPct`, `ChargingState`, `CapturedAt`, `Locked`,
  `SentryMode`, `MaxRangeChargeCounter`) — nil means "not yet computed since the
  migration" (or, for `SentryMode` and `MaxRangeChargeCounter`, possibly "not reported
  this capture"), never a fabricated zero value; `dashCountOrDash` renders a nil counter
  as `"—"` and a reported `0` as `"0"`, the same omit-never-fabricate rule
  `dashTempOrDash` follows; see `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md`
  D2/D3/D8 for the exact per-field nil-handling table.
- `Deps.AnalyticsRecalculator analytics.Recalculator` — the analytics module's
  **write** port, injected the same way (wired from `cmd/web` via
  `analytics.NewRecalculator(...)`). Called by `ExternalChargeCreate` after a manual
  charge entry is written, so the affected days' `vehicle_metrics` rows are
  recomputed immediately instead of waiting for the nightly pass
  (`RM29-analytics-add-vehicle-metrics`, design D5). Same rule: the interface,
  never `analyticsdb`.

## Boundaries

- Calls other modules ONLY through their public interfaces — never a database, never
  another module's internals. If a handler "needs" SQL, the design is wrong: add a
  method to the owning module instead.
- Templates live in `templates/{layouts,pages,fragments}` (Templ); static assets are
  embedded via `go:embed`. Map module DTOs to gateway view models — never leak
  `...Tesla`-suffixed DTOs into templates.
- Handlers stay thin: session/auth check → call an interface → render. Testable logic
  goes in helper funcs driven through interface fakes (see `handlers/`).

## Allowed / forbidden imports

**May import** (in addition to the domain-module ports listed under "Public interface"
above):

- `internal/clock` — the platform's default time zone and calendar-day primitives,
  `Zone()`/`CalendarDay()` (`RM35-gateway-adopt-clock`, roadmap D4). Used by
  `browserLocation`/`browserLocationFromHeader`'s no-cookie/malformed-cookie fallback
  (`handlers/tz.go`) and by `history.go`'s `startOfDay`, which now delegates to
  `clock.CalendarDay(t, time.UTC)`, plus four `handlers.go` call sites that previously
  called raw `time.Now()`. The signed-in user's own `browser_tz` cookie still wins
  whenever present — only the fallback default changed, from `time.UTC` to
  `clock.Zone()` (`America/Bogota`) (RM35 D1). `internal/clock` imports nothing
  project-local, so this creates no cycle.
- `internal/logging` — `Note`, the platform-wide `[Type] [Method] message` log-line format
  (`ai/go-conventions.md` § Logging). Every handler log line goes through it
  (`logging.Note("Handler", "<method>", ...)`); `make logging-guard` enforces it.

## UI stack (styling) — Node-less Tailwind + DaisyUI

The gateway is the **only** module with a UI stack; no other module touches Tailwind,
DaisyUI, or Templ (they expose interfaces, the gateway renders them). Foundation laid
by `kkpa-goth-scaffold-ui init` (2026-07-24, one-time — do not re-run); full rules in
[`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) §"Styling".

- **Three-layer vocabulary:** Templ (typed `templates/ui/` kit) → DaisyUI (component look +
  semantic theme tokens, **zero JS**) → Tailwind (layout/spacing utilities only).
- **The `ui/` kit is an anti-corruption adapter around DaisyUI** — an external library that
  ships breaking changes across majors. Routing every DaisyUI **component class** through a
  `ui.*` wrapper makes a version bump a one-file edit per component, not an app-wide sweep.
- **Compose the `ui/` kit** (Card, StatTile, Button, Alert, Badge, Dot, Dropdown, Progress, Table,
  PageHeader, NavShell, ConfirmDialog, and the form set **Field / Input / Select / Textarea**) — **never inline
  a DaisyUI component class** (`btn`, `input`, `card`, `fieldset`, …) in a page/fragment; that's a
  bug. If a repeated element has no wrapper, **add one to `ui/`** instead of inlining. Theme
  tokens (`text-error`, `bg-base-100`) and Tailwind layout utilities stay inline — the stable
  layers. Pages/fragments pass VM-ready strings in.
- **`ui.StatTileProps.Size`** (`templates/ui/stat_tile.templ`) — closed vocabulary of two:
  `""` (every existing call site) and `"lg"`, the headline treatment for the one or two
  numbers a page exists to answer. It exists because §Mobile R5 says a page needing a one-off
  size is a signal the KIT needs a size variant, never an inline override. `"lg"` grows the
  value only from `sm:` upward, so a phone still renders it at the standard size and the R5
  readable floor holds. First used by `/vehicle-stats`, where seven equal tiles answered
  nothing and three tiers of weight answered the question the page was built for.
- **`ui.Dropdown`** (`templates/ui/dropdown.templ`) — a single-choice menu: a trigger button
  showing the current selection, opening rows the caller supplies as `DropdownItem`s. Each row
  takes its own `Attrs`, so it can carry a complete `hx-get` URL. That is the whole reason it
  exists next to `ui.Select`: one option of a native select element holds a **single** value, so
  a control that must emit two coupled query params (`?start=&end=`) from one click cannot be a
  select without a script. Zero JS — the same CSS-only `:focus` DaisyUI dropdown `ui.LangSwitcher`
  already uses. First composed by the Vehicle Stats period control.
- **`ui.FieldProps.Optional`** (`templates/ui/field.templ`) — appends a muted `(optional)` /
  `(opcional)` hint to a field's legend, resolved from `i18n.KeyFormOptional` inside the kit
  (a deliberately generic, non-`charges_` key: it is the kit's own vocabulary, reusable by
  every future form). Use it INSTEAD of a `placeholder` for this signal: browsers ignore
  `placeholder` on `date`/`datetime-local` inputs and `<select>` has none at all, so a
  placeholder-based hint silently skips exactly the controls whose requiredness is least
  obvious. **Only for an UNCONDITIONALLY optional field** — never for a conditionally
  required one (`ended_at` / `end_battery_pct`, whose `required` attribute RD13 toggles
  client-side as the status select changes), because the server-rendered hint would go stale
  the instant the user switches status.
- **`ui.FieldProps.Help`** (`templates/ui/field.templ`) — renders a muted line under the
  control (DaisyUI's `fieldset-label` class) explaining a rule about the field's *value*,
  e.g. that an empty field gets computed instead of left blank. Independent of `Optional`:
  `Optional` marks the field as not required, on the legend; `Help` explains the value, under
  the control. A field may carry either, both, or neither. Worked example: the charge forms'
  `start_battery_pct` field carries both.
- **`ui.Dot`** (`templates/ui/dot.templ`, `DotProps{Variant, Tooltip, Class}`) — a small
  colour-only completeness/status indicator with a native hover tooltip, for a spot where
  `ui.Badge`'s mandatory text would be redundant with an adjacent label. `Variant` is one of
  `"success"|"warning"|"error"|"neutral"` (DaisyUI semantic token, mapped by `dotClass` exactly
  like `badgeClass` maps `Badge`'s `Kind`); `Tooltip` renders as the `title` attribute and is
  omitted when empty. Gold standard: the charges table's Status column
  (`fragments/external_charge_row.templ`), which pairs a `ui.Dot` (`success`/`warning`, completeness) with
  an adjacent `ui.Badge` (`primary`/`ghost`, lifecycle status) — the two colour vocabularies are
  **deliberately disjoint** so the badge's colour never reads as a second completeness signal
  (design.md §D-Dot, `RM33-gateway-add-entries-dashboard`). Reuse this pairing shape for any
  future dot+badge combination; never repurpose `success`/`warning` for a badge that sits next to
  a dot.
- **`ui.Progress`** (`templates/ui/progress.templ`, `ProgressProps{Value, Max, Class, Attrs}`) —
  the meter bar; the kit owns the DaisyUI `progress` class and converts `Value`/`Max` (plain
  ints) to the HTML attributes, so no call site formats them. DaisyUI paints a progress fill
  with **currentColor**, so the bar's colour comes from a `text-*` token passed in `Class`.
  `Attrs` carries an `aria-label` (mirrors `SelectProps`). Added by MAG-44 when the battery
  meter gained its second call site — per the rule above, a repeated element with no wrapper
  gets one added to `ui/` rather than inlined a second time.
- **`ui.BatteryBandClass(pct int, hasValue bool)`** (`templates/ui/ui.go`) — the SINGLE
  definition of the battery colour bands (`0–10` low, `11–20` mid, `21–40` warn, `41+` full;
  no value → `text-primary`). Both surfaces that render a battery level go through it: the
  dashboard card (`pages.dashBatteryColorClass` is now a thin adapter that only parses the
  VM's string percentage and delegates) and the sidebar vehicle block. Do not re-derive the
  bands anywhere else — that is the drift this function exists to prevent. Tokens themselves
  live in `static/themes/_shared.css` §"Battery-level metric colors".
- **`ui.StatTileProps.Trend`** (`templates/ui/stat_tile.templ`) — an optional `"up"` /
  `"down"` string that adds a small trend glyph next to the stat value (`"up"` →
  `trending_up`, green; `"down"` → `trending_down`, red). Empty renders no icon, so old
  call sites are unaffected. Added by `RM50-gateway-add-travel-progress-subsection` D3,
  with the two new glyph names added to `ui.Icon` (`templates/ui/icon.templ`) at the
  same time. Same pattern as `ui.BadgeProps.Kind` / `ui.DotProps.Variant`: a small closed
  string list, not a bool pair.
- **The Vehicle Status card (`vehicle-status`, `pages/dashboard.templ`) is now split
  into named subsections**, not one flat tile row. Left column: the vehicle image plus
  the two lifetime tiles (Odometer, MaxRangeCharges). Right column: three named
  `<section>` blocks, all built — `travel-progress` (Distance travelled, Battery
  used), `tire-pressure` (four wheels — FL, FR, RL, RR — each a PSI reading, an
  up/down trend, and its day-over-day delta), and `interior-exterior` (Interior,
  Exterior). Added by `RM50-gateway-add-travel-progress-subsection` D1;
  `tire-pressure` filled in by `RM50-gateway-add-tire-pressure-subsection` D7
  (the tier-4 placeholder that section used to hold is gone). A future
  subsection follows this same shape: its own `<section id="...">`, its own
  `ui.SectionHeader`, its own `grid-cols-2` tile row.
- **Semantic tokens only — never hex / raw palette** (`bg-base-100`, `primary`,
  `success`; not `#fff` / `bg-red-500`). The app re-skins from one `<html data-theme>`
  (default `lemonade`; `dark` auto-applies via `prefers-color-scheme`).
- **No client-side JS init** — keeps htmx swaps safe. Prefer CSS-only DaisyUI patterns
  (`<dialog>` modal, `dropdown`, `collapse`, `tabs`) over any JS. There are exactly **seven**
  standing exceptions, each with its own recorded decision below: **RD9** (the `browser_tz`
  cookie script in `layouts.BaseAuth`), **RD10** (`ui.ConfirmDialog`, whose JS lives in
  the shared `static/app.js`), **RD12** (date→time-preserving sync on the charge forms), and
  **RD13** (status-driven required toggle on the charge forms) plus **RD14** (location-kind
  driven label toggle) plus **RD15** (the theme-switch instant-apply listener pair) plus
  **RD16** (the iOS install hint's show/dismiss pair) — the last five also live in
  `static/app.js`. Adding an eighth needs its own RD entry per RD8.
- **Confirmations: never write a modal, never call `window.confirm`.** Put `hx-confirm`
  (plus optional `data-confirm-title` / `data-confirm-label` / `data-confirm-variant="danger"`)
  on the triggering control and the shared `ui.ConfirmDialog` — mounted once in
  `layouts.Base`, driven by `app.js` via htmx's `htmx:confirm` event — renders it. Works on
  every page automatically; do NOT mount a second dialog. See
  [`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) §"Confirmation modals".
- **Codegen:** after `.templ` edits or new classes, run `make templ` **and** `make css`
  (`make generate` runs both). `static/app.css` is a committed vendored artifact (like
  `htmx.min.js`); the Tailwind binary in `tools/` is git-ignored (`make ui-toolchain`).
- **New pages go through `kkpa-goth-scaffold-ui scaffold <concept> [module]`**, which
  mirrors the `charges` gold-standard slice.
- **Responsive is not optional** — the mobile/desktop vocabulary (R1–R8) is the very
  next section, §"Mobile & responsive". Read it before writing any layout class.

## Mobile & responsive — every page, every fragment (MAG-46, 2026-09-04)

**Every page and every fragment this module renders MUST be usable on a 375 px-wide
phone.** A layout that only works on a desktop is **incomplete work**, exactly like a
hardcoded English string. There is no "desktop-only" page in this app.

Before MAG-46 this rule did not exist, and it showed: 37 of 40 `.templ` files carried
no responsive class at all, because nothing ever told an agent to add one. The
decisions below close that gap. They are the whole vocabulary — an agent should not
need to invent a responsive strategy per page.

### R1 — Responsiveness is CSS only. Never a device branch in Go.

There is **no `User-Agent` parsing, no `IsMobile` field on a VM, and no device
argument to a handler** — and none may be added. One HTML is rendered for every
device; CSS decides the layout.

**Why, and these are constraints rather than preferences:**

- **htmx swaps.** Most of the UI arrives from `hx-get` fragments. A device branch in a
  handler would have to be repeated in every fragment handler, and a response cached
  for one device would render wrong on the other.
- **Tailwind's `@source` scanner.** `static/input.css` scans `templates/**/*.templ` for
  **literal** class strings. A class assembled in Go (`"grid-cols-" + strconv.Itoa(n)`)
  is never generated and silently does nothing. Responsive classes must be written out
  in full in the `.templ` source.
- **AI-efficiency.** One markup is one place to read and one place to change. A device
  branch doubles every template an agent must hold in context, and doubles the places a
  new field can be forgotten.

The `<meta name="viewport" content="width=device-width, initial-scale=1">` tag in
`layouts/base.templ` is what makes all of this work. Do not remove or change it.

### R2 — Mobile means below `sm` (640 px). One number for the whole module.

| Prefix | Applies from | Meaning here |
|---|---|---|
| *(none)* | 0 px | **phone — this is the base** |
| `sm:` | 640 px | large phone landscape and up — the desktop layout |
| `lg:` | 1024 px | the drawer opens permanently (`lg:drawer-open`, `layouts.BaseAuth`) |

`sm` is the mobile/desktop line for **content**. `lg` stays the line for the **nav
drawer** only — that was already true before MAG-46 and does not change. Do not
introduce a third line, and do not use `md:` for the mobile/desktop decision.

**`md:` as an intermediate step is fine, and sometimes required.** The rule above is
about *which breakpoint decides mobile vs desktop* — it is not a ban on `md:`. A grid
may legitimately go 2 → 3 → 4 columns across `sm` and `md`. Worked example: the charge
and supercharger tiles are `grid-cols-2 md:grid-cols-4`, and that `md:` is **correct,
not an oversight**. Promoting it to `sm:grid-cols-4` would put four tiles in a 640 px
row — 107 px each, while a desktop-size stat value needs about 173 px — so it would
re-create on a tablet exactly the overlap MAG-46 removed from the phone. Measure before
you "normalise" a breakpoint.

### R3 — Write mobile-first. The base class is the phone.

```
class="text-xl sm:text-3xl"        ✅ phone gets text-xl, desktop gets text-3xl
class="grid grid-cols-1 sm:grid-cols-4"   ✅ stacks on a phone, 4 across on desktop
```

```
class="text-3xl max-sm:text-xl"    ❌ do not author new markup this way
```

The `max-*` variants exist in Tailwind v4 and are not forbidden outright, but
mobile-first is the framework's own idiom and the shape every doc, example and model
expects. Mixing both directions in one codebase is the expensive outcome. When you edit
an existing desktop-first element, **rewrite the base class** rather than appending a
`max-sm:` override.

### R4 — Show and hide by width with the `hidden sm:*` / `sm:hidden` pair.

```html
<span class="hidden sm:inline">{ i18n.T(ctx, i18n.KeyDashboardStatusCharging) }</span>
@ui.Dot(ui.DotProps{Variant: "success", Class: "sm:hidden"})
```

Both elements are always in the HTML; CSS shows exactly one. This is the pattern
`layouts.BaseAuth` already uses for the hamburger (`lg:hidden`).

- Pick the `sm:` display utility that matches the element: `sm:inline`, `sm:block`,
  `sm:flex`, `sm:table-cell`. `hidden sm:block` on a `<td>` breaks the table.
- **A hidden string is still a user-facing string.** It goes through `i18n.T` with both
  `es` and `en` populated, exactly like a visible one. Hiding is not an i18n exemption.
- Never hide something by rendering it twice with different content. One element, one
  source of truth.

### R5 — Font size is fixed once, in `templates/ui/`. Never per page.

Text that overlaps or overflows on a phone is almost always a **kit** problem, not a
page problem. DaisyUI's `.stat-value` is `2rem` at every width and never wraps; put
two or three of them in a 375 px row and they collide, on every page that uses them.

**So the fix belongs in the `ui/` component, where it corrects every call site at
once.** `ui-guard` already forces every page through the kit, so this is the existing
convention working as designed — not a new layer.

- Changing a size in `ui/stat_tile.templ` fixes `/dashboard`, `/external-charges`,
  `/supercharger-stats` and `/vehicle-stats` in one edit. Patching four pages is the same bug fixed four
  times, and the fourth page will be born broken.
- If a page genuinely needs a one-off size, that is a signal the kit needs a **size
  variant prop** (mirroring `ui.FieldProps.Optional` / `ui.BadgeProps.Kind`), not an
  inline override in the page.
- Readable floor on mobile: **do not go below `text-xs` (0.75 rem) for any value a
  user must read**, and never below `text-sm` for body copy. If the numbers still do
  not fit at that size, the layout is wrong — see R7 and ask.

**Overlapping text is usually `white-space: nowrap`, not the font size.** This is the
single most useful thing to know when a value spills over its neighbour. DaisyUI sets
`white-space: nowrap` on `.stat-value` and `.stat-title`, so the text physically
cannot wrap: instead of getting taller it runs out of its grid cell and paints on top
of the next one. Shrink the font all you like — it still overlaps, just in smaller
letters. Check for `nowrap` (and for a large `padding-inline`) before you touch a
size. `.stat`'s 1.5 rem inline padding is a rounding error in a desktop column and a
third of the tile on a phone.

**Gold standard: `templates/ui/stat_tile.templ` (MAG-46).** One component, three
pages (`/dashboard`, `/external-charges`, `/supercharger-stats`, `/vehicle-stats`), one fix. It corrects all
three causes below `sm` — smaller value, wrapping allowed, half the padding — and
restores DaisyUI's exact desktop values at `sm` and up, so the desktop rendering does
not move. Mirror its shape for any other kit component that needs a mobile size. Its
doc comment carries the full reasoning; read it before changing a size anywhere else.

Utilities you put on a DaisyUI element **do** win: Tailwind emits its own utilities
after DaisyUI's component classes inside the shared `utilities` layer. You do not need
`!important`, and you must not use it.

### R6 — Viewport breakpoints only. No container queries.

Tailwind v4 ships `@container` and `@max-md:` and they are the technically correct tool
for a component that must react to its parent box. **We deliberately do not use them
here.** The problems in this module are viewport-shaped, and a second responsive
vocabulary is a real cost: every agent and reader must now learn which of the two a
given component uses. One vocabulary, looked up once. Revisit only if a component is
genuinely reused at two very different container widths on the same screen.

### R7 — Tables drop columns with `hidden sm:table-cell` on the cells.

One table markup serves both widths. A column that is desktop-only carries the class on
**both** its `<th>` and its `<td>`.

- **Never render a separate mobile card list next to a desktop table.** That doubles
  the markup, and the next new column gets added to one of the two and forgotten in the
  other — which is precisely the drift the `ui/` kit exists to prevent.
- On a phone, prefer a **short** format over a hidden column where the data still
  matters: a date as `MM-DD`, a status as `ui.Dot` alone, an action as an icon-only
  `ui.Button`. Formatting is the handler's job — the VM ships both strings, the
  template only chooses which to show.
- Icon-only controls on mobile still need an accessible name (`aria-label` / `title`),
  translated.

### R8 — STOP and ask the user in these three cases.

The rules above cover layout mechanics. They do **not** cover product decisions. When a
mobile change hits one of these, the agent describes the options and **waits for the
user** — it does not pick one and report afterwards:

1. **Content would be REMOVED on mobile.** Hiding a column, a filter, a field, or a
   whole section is a product call. State exactly what disappears, and why it is safe
   to lose on a phone.
2. **A component needs a DIFFERENT SHAPE on mobile.** A row becoming a column, a chart
   changing its axis orientation, four tiles becoming two. Show the options with their
   cost, then wait.
3. **A value would be TRUNCATED or would OVERLAP.** Never silently shrink text past the
   R5 floor and never let a number clip. Report the conflict and offer the real choices:
   smaller font, fewer columns, or a shorter format.

Everything else — stacking a grid, adding a `sm:` variant, using the kit's existing
size prop — the agent just does, and reports.

### Known mobile trap: the drawer stacks BELOW the navbar by default

DaisyUI gives `.drawer-side` `z-index: 10`; `layouts.BaseAuth`'s navbar is `sticky
top-0 z-40`. Below `lg` the open drawer is a **fixed overlay starting at top: 0**, so
without an override the navbar paints over the sidebar's first 4 rem and the vehicle
block appears to begin at its progress bar (MAG-46 step 5). The fix is the stacking
order — `drawer-side z-50` — not a `margin-top` on the swallowed content: a margin
clears the symptom, leaves those 4 rem unclickable, and has to be re-tuned every time
the navbar's height changes. It is a no-op at `lg`, where `lg:drawer-open` makes the
sidebar `position: sticky` in its own grid column and the two never overlap.

Generalise the lesson, not the number: when something is invisible on a phone but fine
on a desktop, check whether a `position: fixed` overlay is losing a z-index race
before you move anything.

### Verifying a responsive change

- `make templ && make css` after any `.templ` edit. A new class that Tailwind has not
  regenerated into `static/app.css` does nothing in the browser.
- `make ui-guard` — the responsive utilities (`sm:`, `hidden`, `table-cell`, `grid-cols-*`)
  are all Tailwind layout utilities, so they stay inline and the guard allows them. A
  responsive **DaisyUI component** class (`sm:stats-horizontal`) belongs in the `ui/`
  kit like any other component class.
- There is no automated test for how a page looks — that is deliberate (§"Do not test
  what the page looks like"). Responsive layout is verified by the owner in a browser at
  375 px, never asserted in Go.

## Chrome surfaces & the honest vehicle block (MAG-44, 2026-09-03)

**The authenticated chrome is ONE surface.** The sidebar column wrapper in
`layouts.BaseAuth` owns `bg-base-200` plus `border-r border-base-300`, and the navbar
carries `bg-base-200 border-b border-base-300`. Everything inside the sidebar — the
`#nav-header` vehicle block and `ui.NavShell`'s `<ul class="menu">` — is **transparent**
and must stay that way. Before this change the three regions painted `base-200`,
`base-300` and (by falling through) `base-100`; in the `graphite` theme those are
`#0e1013 / #161a1f / #252b33` with no borders, so the chrome read as a stack of slightly
different greys rather than one panel. If you are tempted to add a background to a region
inside the sidebar, you are re-creating that bug — add a border instead.

**The active nav item is a primary tint, not `menu-active`.** `ui.NavShell`'s
`navActiveClass` constant (`bg-primary/15 text-primary font-semibold border-l-2
border-primary rounded-l-none`) replaced DaisyUI's `menu-active`, which forces
`bg-neutral` — a flat grey that read as a fourth chrome layer instead of a selection. The
icon and label both inherit `currentColor`, so they tint together. `Pronto`/`Soon`
placeholder badges are `ghost` + `badge-sm`, deliberately quiet: a `warning` yellow made
unfinished pages shout louder than the working ones.

**There is NO live connection state anywhere in this module, and none may be added.**
The app renders the latest stored `vehicle_metrics` row; it never observes whether a
vehicle is reachable right now. MAG-44 therefore deleted the entire
`NavHeaderStatusKind` vocabulary (`connected`/`asleep`/`awaiting`/`unavailable`), its 48 h
`connectedFreshnessWindow`, `handlers.connectedAt`, `handlers.relativeLastSeen`, and the
eleven `KeyNavHeaderStatus*` / `KeyNavHeaderLastSeen*` catalogue entries. `navHeaderFor`
now does no `CapturedAt` branching at all: it reports the vehicle name, the stored battery
level, and the stored range, and renders an em dash with no bar when there is no row.

**What replaced it is a data-age label, and it counts CALENDAR days, not elapsed hours.**
`navHeaderFor` receives `browserToday(c)` and `calendarDaysAgo` compares midnights in the
user's own zone: `0` → *hoy/today*, `1` → *ayer/yesterday*, `2`+ → *hace N días/N days ago*.
**Only `0` is a healthy state**: `dataAgeStaleDays` is `1`, so everything except *today*
is coloured `text-error`. The poller runs nightly, so the newest stored reading should
always carry today's date; *yesterday* already means today's poll did not land. Accepted
consequence: between local midnight and the ~03:30 run the label is red every day, which
is honest — there is no reading from today yet. A nil `CapturedAt` renders **nothing** —
an unknown age is never guessed.

The age is read from `captured_at`, **never** `metric_date`. `metric_date` is
`captured_at`'s calendar day *minus one* (`analytics.effectiveDay`) and buckets the day's
*deltas*; the battery/range/odometer on the same row are the raw observations taken at
`captured_at`. Using `metric_date` here would label a reading taken this morning
"yesterday" and trip the stale colour a day early.

The distinction is the reason MAG-44's deleted `relativeLastSeen` could NOT simply be
restored, and it is worth keeping straight: the nightly poll runs at 03:30, so last night's
reading is ~22 h old when viewed before midnight. An elapsed-hours helper calls that
"22 hours ago"; the user calls it "yesterday". Do not reintroduce a duration-based label
here, and do not let `dataAgeStaleDays` grow back into a connectivity claim — it drives
**emphasis only**. It says the data is old, never that the vehicle is unreachable.

## Identifiable cards & sections

Every card, section, and standalone region a page or fragment renders MUST carry a
stable, page-unique `id` on its root element — even when nothing targets it today.
The `ui.Card` kit exposes this via `CardProps.ID`; free-form `<section>`/`<div>`
containers set `id` on the root tag directly.

**Why:** a page accumulates cards and regions over time; an opaque stack of
`<div class="card">` with no identifiers is expensive for an AI agent (or human) to
reason about — there is no stable hook to target from tests, htmx swaps, CSS,
devtools, or future instrumentation. An `id` is a one-attribute, zero-runtime way to
make every component addressable by name, so the *next* change touches one element
by id instead of re-deriving which `.card:nth-child(2)` is the battery. It is the
same "closed vocabulary over ad-hoc" principle as the `ui/` kit: name things once.

- **kebab-case, page-unique, semantic** — `battery-info`, `vehicle-status`,
  `vital-stats`, `connect-cta`, `dashboard-history`. Not `card-1` / `div3`.
- **Passed through the kit, not inlined on raw markup** — `ui.Card` gets `ID:` in
  `CardProps`; the page never writes `<div class="card" id="...">` by hand, because
  the `ui.Card` wrapper owns that root element. A bare `<section>`/`<div>` the page
  emits directly sets `id` on its own root tag.
- **Empty ID renders no attribute** — `ui.Card`'s `id` is conditional, so existing
  call sites that don't pass `ID` stay clean (no `id=""` clutter). New cards/sections
  in a page MUST pass one.
- **IDs are not swap targets by themselves** — a swap target additionally needs the
  `hx-*` / `@templ.Fragment` wiring (see "Two render entry points"). The `id` is for
  *identification* first; htmx targeting is a separate, opt-in concern on top.

Gold standard: `templates/pages/dashboard.templ` — `connect-cta`, `vehicle-status`,
`vital-stats`, `battery-info`, `dashboard-history` all carry stable ids.

**Every card in this module now carries one** — MAG-46 backfilled the thirteen that
did not (`history-odometer`, `history-battery`, `history-consumed`,
`external-charges-create-card`, `external-charges-empty`, `external-charges-summary`, `external-charges-entries`,
`supercharger-empty`, `supercharger-summary`, `supercharger-kwh-per-month`,
`supercharger-sessions`, `login-actions`, `account-blocked-message`). The rule had
been written but never enforced, so eleven of the seventeen cards were anonymous. If
you add a card without an `id`, you are re-opening that gap.

## Every section is titled and described

A section that shows content to the user MUST carry **all three**: a stable `id`
(above), a **Title**, and a **Desc** — one short sentence saying what the section
shows, in the user's own words.

**Why the description and not just the title.** A title names the section; it does not
say what the numbers in it mean. "Sessions" does not tell a user whether they are
looking at every charge ever or the last 30 days, and it does not tell the *next
agent* either. The description is the cheapest possible place to put that, it is read
by both audiences, and it costs one line. Sections that had a title but no description
were exactly the ones this project kept having to re-explain.

### Where the style lives — one definition, never copied

`sectionTitleClass` and `sectionDescClass` in `templates/ui/ui.go` are the **single**
definition of how a section title and description look. Three components read them
and nothing else may:

| Component | Use it for |
|---|---|
| `ui.Card` (`CardProps.Title` / `.Desc`) | the normal case — a section inside a card |
| `ui.SectionHeader` (`SectionHeaderProps`) | a bare `<section>`/`<div>` the page emits directly |
| `ui.PageHeader` (`PageHeaderProps.Subtitle`) | the **page's** own one-line description |

**Never hand-write a heading.** `<h2 class="text-lg font-semibold">` in a page or
fragment is a bug, the same class of bug as inlining a DaisyUI component class. Before
MAG-46 the module had *three* competing title treatments — `card-title` in `ui.Card`,
`text-2xl font-semibold` in `ui.PageHeader`, and one hand-written `<h2>` in
`fragments/external_charges_list.templ` that bypassed the kit — and nothing kept them in step.
That `<h2>` is gone; its text now goes in through `CardProps.Title`. To restyle every
title in the app, change one constant.

The page title stays visually larger than a section title. That hierarchy is
deliberate: page → section → content. A page subtitle and a section description are
rendered the *same*, so a reader learns one shape and applies it everywhere.

### The narrow exceptions

Two shapes of card pass neither Title nor Desc, and these are the whole list:

- **A bare container for a single control the page header already explained** —
  `login-actions` (one sign-in button under a page whose heading says the rest) and
  `account-blocked-message`.
- **An empty state whose entire body is one explanatory sentence** — `external-charges-empty`
  and `supercharger-empty`. Here a description would only restate the body directly
  above it, and a title would name a section that exists solely to say "there is
  nothing here yet".

Every other card in the module carries all three (Title, Desc, `id`) as of MAG-46.

These are the *only* exemptions, and an agent taking one must say in the change why the
section needs no explanation. "I could not think of a description" is not the
exception; it usually means the section's purpose is unclear, which is a design
problem the description would have exposed. Inventing filler text to satisfy the rule
is worse than both — if the sentence adds nothing, say so and take the exception.

### i18n applies, obviously

Title and Desc are user-facing strings. They resolve through `i18n.T(ctx, key)`
against `i18n/catalog.go` with **both** `es` and `en` non-empty, exactly like every
other label (§i18n below). A description added in English only is incomplete work.


## i18n — every new user-facing label needs BOTH es and en

**This is binding, not advisory.** Any `.templ` change that adds or edits user-facing text
MUST add or update a catalogue key in `internal/gateway/i18n/catalog.go` with **both `ES` and
`EN` non-empty**. `TestCatalog_AllKeysHaveBothLanguages` (`internal/gateway/i18n/catalog_test.go`)
enforces this at `go test` time — but the rule applies to every new key regardless of whether a
test happens to catch an omission before you commit. A hardcoded English (or Spanish-only)
string added to any page from this point forward is **incomplete work**, exactly as an
undocumented structural change is incomplete per `CLAUDE.md`'s "docs track structural change"
rule.

- **Added by `RM24-gateway-add-i18n-foundation`** (tier 2 of `RM24-i18n-translations`, ticket
  MAG-8). The gold-standard surface to mirror is the layout/nav shell: `templates/layouts/base.templ`,
  `templates/layouts/nav.go`, `templates/ui/nav_shell.templ`, `templates/ui/nav_logout.templ`,
  `templates/fragments/nav_header.templ`, and `templates/ui/lang_switcher.templ`.
- **The lookup surface is `i18n.T(ctx, key)`, always.** Inside a `.templ` file, `ctx` is the
  implicit context.Context templ threads through every component composition — call
  `i18n.T(ctx, i18n.KeyXxx)` directly. Inside a plain Go helper called *from* a `.templ` block
  (e.g. `layouts.navItems`), `ctx` is an explicit first parameter the `.templ` call site passes
  through — see `nav.go`'s `navItems(ctx, active)` for the pattern.
- **Add the key to `internal/gateway/i18n/catalog.go`, not a new file.** One `Key` constant +
  one `catalog` map entry with `ES`/`EN` **on the same line** (design.md D1) — this is the
  closed vocabulary; do not invent a second catalogue or a per-page strings struct.
- **A key with no catalogue entry renders a visible `!!key.name` marker, never a blank string**
  (design.md D2) — this is a deliberate render-time backstop, not the enforcement mechanism. The
  enforcement mechanism is the completeness test above; do not rely on the marker to catch a
  missing translation in review.
- Language resolution (which language `ctx` carries) is already wired for every request by
  `handlers.PreferencesMiddleware` — a new page needs no per-handler language plumbing, only
  `i18n.T` calls in its markup. The same middleware also puts the theme on `ctx` in that one
  call (RM42 tier 2), so a page needs no theme plumbing either.

**`make i18n-guard` is the mechanical companion to `TestCatalog_AllKeysHaveBothLanguages`**
(added by `RM24-gateway-translate-all-pages`, tier 3 of `RM24-i18n-translations`, MAG-8). Where
the catalogue test only enforces "every *known* key has both languages," `i18n-guard` is the
check that MAG-8 actually asked for: it fails the build if a hardcoded user-facing string
bypasses `i18n.T(ctx, ...)` in the first place. It is now part of `make check` (`build vet
ui-guard i18n-guard test`), so a page added without translating it fails locally, not only in
review.

- **Two grep passes, mirroring `ui-guard`'s shape exactly** — pass 1 scans
  `templates/{pages,fragments,ui}/*.templ` for a bare text node not already wrapped in
  `i18n.T(...)`; pass 2 scans `handlers/*.go` (excluding `_test.go`) for a hardcoded string
  landing on a known message sink (`Notice:`/`Error:` struct fields, the `errs[...] =`
  validation-map pattern, `vm.`/`d.` field assignment, or a bare-text `c.String(http.Status[45]xx,
  ...)` body — including each of those wrapped in `fmt.Sprintf`/`fmt.Errorf`). It is a heuristic,
  same rigor bar as `ui-guard`, not a parser.
- **The `// i18n:allow: <reason>` marker is the sole exemption mechanism — no separate allowlist
  file — but its PLACEMENT differs by file type, and this trips people up:**
  - In a **`.go` file**, the marker is a trailing `//` comment on the **same line** as the
    literal (e.g. `Healthz`'s `"unhealthy: %v"` line).
  - In a **`.templ` file**, the marker goes on the line **immediately above** the flagged line —
    templ has no comment syntax valid inside markup, and HTML comments are forbidden
    project-wide (`CLAUDE.md` → "HTML templates"), so there is nowhere on the flagged line
    itself to put a Go `//` comment. `i18n-guard`'s pass 1 checks both the flagged line and the
    line above it for the marker, so both placements work where each is syntactically valid; a
    same-line marker only works in a `.templ` file when that particular line is itself plain Go
    code (e.g. a struct field's doc comment), never inside an HTML tag.
  - Only mark a **genuine** non-translatable literal (attribute values, CSS classes, htmx
    attributes, format verbs, units, brand nouns, or a Go doc comment/ops-only response) — never
    a real piece of app copy the guard correctly caught. If the guard flags real copy, add a
    catalogue key instead; weakening the marker's use to silence a true positive defeats the
    check MAG-8 asked for.

## Read-only at request time

The gateway is **read-only on every user-facing request** by default. This is both a
tenancy safety rule and a read-optimization principle (see
[`ai/architecture.md`](../../ai/architecture.md) §7): the hot path (user → DB read →
HTML) stays cheap and predictable.

- Handlers only call **`Reader` ports** (e.g. `account.RegisteredVehicles`,
  `charging.SessionReader.ListSessionsByVehicleBetween`). Never call `Collector` or
  `Writer` ports from a handler, **except through the apertures below**. The telemetry
  module is off-limits entirely — see "Public interface" above.
- No writes, no Tesla API calls, no side effects on user requests. The only
  user-initiated Tesla API call is listing vehicles on first Tesla connect
  (one-time seed), and even that happens through the account module's interface —
  not the gateway calling Tesla directly.
- All writes (telemetry collection, summary computation, token rotation) happen in
  the nightly batch (`telemetry.Collector`) or inside `account.Service` methods
  called from non-gateway paths — never from a gateway handler.

### The write apertures — a closed list

| Handler | Port it may call | Guards, in order |
|---|---|---|
| `ExternalChargeCreate` / `ExternalChargeRowUpdate` / `ExternalChargeRowDelete` | `charging.Writer` | auth → vehicle ownership → CSRF `csrf_externalcharge` |
| `SuperchargerRowUpdate` | `charging.SessionVerifier` | auth → CSRF `csrf_supercharger` → vehicle ownership (`authorizeVehicle`) |
| `ThemeSwitch` | `account.Service.SetTheme` | auth → CSRF `csrf_theme` |
| `LangSwitch` | `account.Service.SetLanguage` | **none** — it must serve anonymous callers |
| `syncLoginLanguageCookie` | `account.Service.SetLanguage` | none — best-effort, inside `GoogleCallback` |
| `VehicleSelect` | none — writes the session only | auth → CSRF `csrf_vehicle_select` → vehicle ownership |
| `rejectIfInactive` | none — refuses a session | must run **before** `sess.Set("uid", …)`; that order is the security property |

**That table is the whole aperture list. Every other handler is Reader-only.** Adding a row
is never a worker's call — each one was put to the user and approved. Stop and ask.

**Do not copy one row's guard set onto another.** Two of them diverge on purpose: the
language switch has no CSRF, and the theme cookie is written only *after* its database write
succeeds. Each divergence is a settled, user-approved decision whose reasoning does not
transfer, and each has been "simplified" by a well-meaning agent before. Read
`kkpa/context/architecture/gateway-reader-writer-ports.md` — the guards, the reasons, and the
per-aperture mechanism — before you touch any of them.

The Supercharger write also checks CSRF before vehicle ownership — the reverse of the manual
charge path's auth → ownership → CSRF order. That order is a consequence of when each check
has what it needs: `authorizeVehicle` needs the vehicle the session already selected, which
the handler reads only after the request body passes validation, well after the CSRF check.

One line is repeated here rather than left to the guide, because dropping it is silent and
the only thing that catches it is a test: **`setLangCookie` MUST call
`c.SetSameSite(http.SameSiteLaxMode)` before `c.SetCookie(...)`.** gin's `SetCookie` has no
SameSite parameter, and that attribute is the language switch's entire CSRF defence.
`TestLangSwitch_CookieIsSameSiteLax` pins it — if it fails, fix the cookie, never the test.

## Vehicle-scoped reads — always send the selected TeslaID

The gateway is multi-tenant **and** multi-vehicle: the user picks the active vehicle with
the sidebar switcher (nav-header `<select>` → `POST /ui/vehicle/select`, persisted in the
session by `setCurrentVehicle`). **Every handler that fetches or filters PER-VEHICLE data
MUST scope that read to the SELECTED vehicle**, identified by its **`TeslaID`** (`int64` —
Tesla's numeric vehicle `id`, `account.Vehicle.TeslaID`). This is a tenancy-correctness rule:
a read that ignores the selection silently shows a *different* car's data.

1. **Resolve once, pass the TeslaID down.** Call `h.resolveSelectedVehicle(ctx, c, uid)` (it
   auto-selects the first OWNER when the session has none) and hand its `.TeslaID` to the
   module port — e.g. filter `charging.Reader.ListEntriesByVehicle(ctx, uid, teslaID, …)`,
   pick the status for that TeslaID out of `analytics.Reader.LatestMetricsForVehicles`, or
   pass it to a `tesla` adapter per-vehicle call. **Never** default a per-vehicle read to
   `registered[0]` or to "all vehicles" when a selection exists.
2. **Identity is the numeric `TeslaID`, not the VIN and not the list index.** The VIN travels
   only as a tenant-ownership check alongside it (the switcher submits `{TeslaID}:{VIN}`; the
   write handlers validate the pair belongs to the account).
3. **Every per-vehicle page/fragment must refresh on switch.** Wrap its per-vehicle content in
   a swappable region that subscribes to the `vehicle-changed` event
   (`hx-trigger="vehicle-changed from:body"`, re-fetching its `/ui/…` fragment); `VehicleSelect`
   emits `HX-Trigger: vehicle-changed`. See
   [`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) §"Cross-region refresh via
   `HX-Trigger`". Gold standards: the dashboard `#dashboard-content` and the external-charges
   `#external-charges-content` regions (each re-fetches `GET /ui/dashboard` / `GET /ui/external-charges`).

## Vehicle ownership — proved once, at the gateway

The gateway is the only layer that checks whether a requested vehicle belongs to the
signed-in user. A domain module never re-checks this on its own — it trusts that the
gateway already proved ownership before calling it.

- **`authorizeVehicle`** (`handlers.go`, next to `resolveSelectedVehicle`) turns an
  account id and a requested `TeslaID` into an `internal/vehicleref.Ref` — a value a
  handler can only get by calling this helper. It reuses the same
  `account.RegisteredVehicles` call `resolveSelectedVehicle` already makes; it adds no
  new account lookup.
- **A miss and a lookup failure look the same to the caller.** Both return one
  unexported error, rendered as HTTP **404**, never 403 — a 403 would confirm the
  vehicle id is real, just not the caller's.
- **`SuperchargerRowUpdate` is its first caller** (`supercharger.go`), wired in once
  `charging.SessionVerifier.VerifySession` required a `vehicleref.Ref` instead of a bare
  `TeslaID`. Every read still filters by account id the same way it did before —
  `authorizeVehicle` is wired into a handler only when that handler's own module port is
  changed to require a `Ref` instead of a bare account id — read
  `internal/vehicleref/AGENTS.md` before adding a new call site.
- **`ownedVehicles`** (`handlers.go`, next to `authorizeVehicle`) answers a different
  question: not "does the caller own THIS one vehicle" but "which vehicles does the
  caller own at all." It calls `account.RegisteredVehicles` once and returns both the
  plain `[]account.Vehicle` list and the same set as `[]vehicleref.Ref`, so a caller that
  needs a fleet-wide `[]int64` (via `vehicleref.TeslaIDs`) and a caller that needs the
  vehicle structs (for a display label) share one read. `ok` is `false` on a lookup error
  or an empty list — an empty list is never "no filter." It is the seam
  `external_charges.go`'s `fetchEntryVM` and `fetchEntryTeslaIDAndChargedOn` use instead
  of the old free helper `teslaIDsOf`, which read `RegisteredVehicles` directly and has
  been deleted.

## HTTP date-filter convention

**Every date-filtered gateway HTTP endpoint takes `?start=YYYY-MM-DD&end=YYYY-MM-DD`** — both
whole calendar days, UTC-midnight-bounded, **`end` inclusive** — never a `?days=N` count.

- **Rationale.** A `days` count couples the API to the caller's notion of "today" and makes the
  read an open-ended `captured_at >= since` scan; absolute `start`/`end` date params decouple the
  window from the caller, bound the read on **both** ends (protecting the read-heavy hot path —
  the platform's Performance-Profile), and let the handler render a **fixed `[start..end]`
  calendar-day axis** so two charts consuming the same window share identical day labels by
  construction (the root cause of the MAG-7 odometer/battery day-1 axis offset was a count-based
  read where the two chart builders consumed different slice offsets of the returned snapshots).
- **Contract** (reference implementations: `GET /ui/dashboard/history`,
  `RM8-gateway-history-date-range` / Linear MAG-7; and `GET /ui/supercharger-stats`,
  `RM30-gateway-read-supercharger-stats-from-charging`):
  - Parse via a single `parseHistoryRange`-style helper (`internal/gateway/handlers/history.go`)
    returning `(start, end time.Time, ok bool)`.
  - Default when both `start` and `end` are absent: endpoint-specific (dashboard history default
    6-day window → `today-6 .. today`).
  - Reject with HTTP **400** on any of: malformed non-ISO date, only one of `start`/`end`
    present, `end.Before(start)`, a **future `end`**, or a window wider than the
    **endpoint's own cap** (hard cap against unbounded range scans). The cap is **per-endpoint,
    set by that table's row density**, not one flat number: history's source table
    (`vehicle_snapshots`) is dense — many rows per day — so its cap is **90 days**
    (`historyRangeMaxDays`); the Supercharger Stats endpoint's source table (`charging.supercharger_sessions`)
    is sparse — a handful of rows per month — so its cap is **400 days**
    (`superchargerRangeMaxDays`, `RM30-gateway-read-supercharger-stats-from-charging`). A new
    endpoint sizes its own cap the same way: measure the source table's row density, don't copy
    either existing number by default.
  - The "future" frame is **also per-endpoint**: history compares against the browser-local
    today (`browserToday(c)`, the `browser_tz` cookie — RD9 below) and rejects `end` after
    *browser yesterday*; Supercharger Stats uses plain `startOfDay(time.Now().UTC())` and
    accepts `end == UTC today`, rejecting only strictly after it. An endpoint inherits the
    timezone machinery only if its data is browser-local-day-sensitive; don't copy it by
    default.
  - On 400, render the empty-state placeholder (`dashHistoryEmpty`), **do not** call the read
    port, and return no preset selector — a malformed request gets no chrome.
  - The caller may fetch a bounded extra lookback (e.g. the dashboard's 1-day pre-window for the
    first odometer delta) by passing `start-1day` to the owning module's bounded read port — the
    lookback is a **gateway concern**, never a parameter on the owning module's port method.
- **A month-grain endpoint still uses `?start=&end=` — `GET /ui/vehicle-stats` is the worked
  example.** Its control offers whole calendar months and whole calendar years, never a day, so
  its parser adds one rule its siblings do not have: **both bounds must be month-aligned**
  (`start` a 1st, `end` a month's last day) or it is a 400. The page reads analytics'
  per-month rollup, so a window starting mid-month cannot be answered by the underlying rows —
  it would silently return whole months and misreport what was asked. Its cap is **366 days**
  (`vehicleStatsRangeMaxDays`), one leap year, because a whole year is the widest window the
  control can express. Its "future" frame is the CURRENT MONTH'S END, not today, so the current
  month stays selectable all month long. A second lower bound applies: `start` before the
  account's `analysis_start_date` month is a 400.
  The control itself is a `ui.Dropdown` whose every row carries a complete `hx-get` URL, NOT a
  native select — one option of a select holds a single value, so emitting two coupled params
  from one click would need a script. **Reach for that shape whenever a single control must set
  both bounds at once**; it is what let this page keep the one vocabulary below.
- **Every future date-filtered gateway endpoint follows the same contract** — a closed
  vocabulary of one: `?start=&end=`. A new endpoint that needs date filtering reuses the
  `parseHistoryRange` pattern and a bounded read port on the owning module (`parseSuperchargerRange`,
  `internal/gateway/handlers/supercharger.go`, is a second worked example of the same pattern
  with its own default/cap constants — see the contract bullet above).
- See also the one-line pointer in
  [`ai/go-conventions.md`](../../ai/go-conventions.md) §"Read optimization".

## Testing

- `httptest` against `NewEngine` with fakes for the `Deps` interfaces — the existing
  suite covers auth guards, CSRF state, empty/error states, and fragment rendering.
  New handlers follow that pattern.

### Do not test what the page looks like (MAG-39)

This module renders HTML, so nearly every test here reads markup. That is fine. The
rule is not "never touch the rendered body" — it is:

> **Assert what the markup DOES. Never assert how it LOOKS.**

The UI changes often and is checked by hand. A test that pins appearance fails on every
redesign while the app still works, so it costs a fix on each change and buys nothing.

**Do NOT write these — they are layout, verified by eye:**

| Banned | Example of the mistake |
|---|---|
| Relative **order** of two elements | comparing `strings.Index(body, a) < strings.Index(body, b)` |
| **Which container/section** an element renders inside | "odometer must be inside the optional section" |
| Presence of **decoration** — an icon, a heading, a badge glyph | "renders an `<svg>` globe", "shows the Soon badge" |
| **Order of a list** of options | "themes render in `Themes` order" |
| Exact **CSS / Tailwind / DaisyUI class strings** | `bg-success`, `dropdown-content`, `class="mt-2"` (use `make ui-guard` instead) |
| Exact **copy text** | column headings, button labels, title casing |

**DO write these — they are behaviour, and a redesign must not break them:**

| Required | Why it survives a redesign |
|---|---|
| `required` / `disabled` / `checked` / `value` attributes | the validation and data-binding contract |
| `name="..."` present at all | the handler reads that name; a rename silently stops saves |
| `hx-*` attributes, `HX-Trigger`, `HX-Retarget`, `HX-Location` | htmx wiring is a real integration |
| Status codes, redirects, cookies, CSRF tokens | security; never weaken these |
| i18n — a key resolves, and ES ≠ EN | `TestCatalog_AllKeysHaveBothLanguages` is mandatory |
| Pure Go functions — parsers, view-model builders, formatters | no markup involved; test them directly |

**When a rule feels worth pinning but is layout, prefer one of these instead:** extract
the decision into a pure function and test that (see `dashLockedBadge` /
`dashSentryBadge` in `templates/pages/dashboard_test.go`), or assert the *save-path*
consequence in the handler rather than the rendered shape.

**The one layout-shaped exception is an invisible failure.** Assert appearance only when
getting it wrong produces **no visible symptom** — the closed-`<details>` trap above is
the model: the submit silently does nothing and only a console line says why. "It looks
wrong" is not that; you will see it.

## Charts / data-viz — hand-rolled SVG, no chart library (RD7)

All charts in this module are **hand-rolled responsive SVG** generated by Templ.
This decision was made for the RM5 dashboard history charts (2026-07-31) and is a
standing module convention.

**Approach:** Templ emits `<svg viewBox="0 0 N 100" preserveAspectRatio="none"
class="w-full h-24">` with one `<rect>` per bar at a pre-computed `HeightPct` and a
child `<title>` per bar for the native hover tooltip. `viewBox` + `width:100%` scales
the chart to any container width — purely CSS-responsive, **zero JavaScript**.

**Rejected alternative:** vanilla-JS chart library (uPlot / Chart.js). Reason:
unjustified weight for simple bars in a Node-less, server-rendered stack. Adds a
client dependency to keep current and an asset-pinning concern. The native SVG
approach has no JS runtime, no resize listener, and no CDN/vendored asset — it
is "free" in terms of complexity. Revisit ONLY if a future chart genuinely needs
axes, zoom, or interactivity beyond hover — and record that reversal here (see
convention below).

**Key invariants (mirror these on every chart you add):**
- Handler pre-computes ALL heights (as int %) and tooltip strings; the template
  does **no** arithmetic, no unit handling, no formatting, and no time calls — the
  handler has already done all of it (RM7: `telemetry.Snapshot` fields arrive
  pre-converted in kilometres; the read-time companion conversion methods were
  removed in tier 2 of `RM7-store-display-units`).
- Bar fills use DaisyUI semantic fill tokens (`fill-primary`, `fill-secondary`, …),
  never hardcoded hex — re-skins from one `data-theme`.
- Empty state (too few data points) falls back to the existing `dashHistoryEmpty()`
  component (in `templates/fragments/history.templ`).

**Standing convention (RD8):** Any decision to ADD, REPLACE, or DROP a client-side
library, or to CHANGE a rendering/architecture approach for the gateway's UI (charts,
interactive widgets, animations, drag-and-drop, etc.) MUST be recorded in the SAME
change — never in a commit message alone. **Where it is recorded depends on how far the
decision reaches, and this split is mandatory:**

- **Module-wide** (it binds a worker sent to *any* page): the rule, its rationale AND the
  rejected alternative all go here, in this file.
- **One page, one route, or one control**: only the *rule* gets an entry here — what is
  permitted, plus any divergence a later worker could "fix" by mistake. The rationale, the
  rejected alternative and the implementation detail go in that page's guide under
  `kkpa/context/`, linked from the entry. Precedent: MAG-39 moved the `/external-charges`
  form detail out this way; the D4, D8 and login-block entries above follow it.

**Why the split is a rule and not a preference:** this file is re-read **in full** on every
gateway dispatch, so one page's detail parked here is a tax on every other page's work. A
long entry about a single route is the signal that it belongs in the KB.

## Client-side JS — the zero-JS rule and its seven exceptions (RD9-RD16)

The gateway's DaisyUI foundation is **zero-JS** (`ai/htmx-conventions.md` §"Styling").
There are exactly **seven** sanctioned exceptions. All but RD9 live in `static/app.js`, and
they delegate on `document.body` — with two deliberate exceptions: RD10 binds three listeners
on the dialog itself once it is open, and RD16's show step is not a listener at all.

| RD | What it does |
|---|---|
| **RD9** | Inline script in `layouts.BaseAuth` only. Sets the `browser_tz` cookie. |
| **RD10** | Intercepts `htmx:confirm` and drives `ui.ConfirmDialog`. |
| **RD12/13/14** | The three `/external-charges` form listeners. |
| **RD15** | Applies a theme to the DOM at once, closes the dropdown, reverts if the save fails. |
| **RD16** | Unhides `ui.InstallHint` on iOS only, and remembers a dismissal in `localStorage`. |

**Five rules bind you even when you are not working on these**, so they stay here:

- **Adding any client-side JS needs its own RD entry first**, per RD8 above — the rule,
  the reason, and the option you rejected. None of the six grandfathers in a seventh.
- **Never mount a second `ui.ConfirmDialog`.** `app.js` resolves it by `id`, so a
  duplicate makes the wrong one open.
- **The dialog stays in the layout, outside every swappable region.** An htmx swap must
  never be able to replace a dialog while it is open.
- **Never delete the inline script in `BaseAuth`.** Without the `browser_tz` cookie the
  dashboard's date math falls back to `clock.Zone()`, not the user's own day.
- **Never render an install prompt for Android, and never sniff iOS in Go.** Android Chrome
  raises its own prompt from `/site.webmanifest`; a second one of ours would be a duplicate.
  And the three facts that gate `ui.InstallHint` — iOS, not already running standalone, not
  already dismissed — are client state no request header carries, so the decision cannot
  move server-side even if the User-Agent were trusted.

**To add a confirmation to any page: put `hx-confirm` on the control. Nothing else.**

Everything else — what each listener does in detail, why it exists, the option each one
rejected, and how each degrades without JS — lives in the KB:

**Read `kkpa/context/architecture/gateway-client-side-js.md` before changing any of them.**
RD12/13/14 page detail is in `kkpa/context/input-port/charging/external-charges.md`.

## SEO & social-share metadata (MAG-seo, 2026-09-07)

Two rules bind every page author, so they stay here:

- **No page writes its own `<meta>` tag.** Every search-engine and link-preview tag comes
  from one component — `layouts.seoHead` in `templates/layouts/base.templ`, called by
  `baseShell`. A page that needs different metadata gets a new catalog key, never a tag.
- **The shell you pick decides indexing.** `layouts.Base` is public and indexable.
  `layouts.BaseAuth` is per-user and emits `robots: noindex, nofollow` and nothing else.

Everything else lives in the KB, with its rationale and its rejected options: the
absolute-URL rule and why `Host` is never used, `BASE_URL`, the bilingual strings, the
share image, `robots.txt` / `sitemap.xml` / `site.webmanifest`, the favicons, and the
JSON-LD rules.

**Read `kkpa/context/architecture/seo-metadata.md` before changing any of them.**

## Theming & self-hosted fonts (RD11) — detail lives in the KB

The gateway ships **two palettes** (`apex`, `graphite`), one corrected DaisyUI builtin
(`halloween`), and **self-hosted** Inter + JetBrains Mono. All of it lives under
`internal/gateway/static/themes/` and `static/fonts/`.

One rule stays here, because it binds you while you are working on something else:

- **Never add an external CDN link.** No `<link>` to Google Fonts, no `<script src>` to a
  CDN, for any asset. Every asset — `htmx.min.js`, `app.css`, the fonts — is committed and
  `//go:embed`-ed. Adding one is a new RD entry per RD8, not a convenience.

Everything else lives in the KB, with its rationale and its rejected options: the
`_shared.css` / palette-file split, what a theme file may declare, the DaisyUI v5 font-token
quirk, the battery and status colour vocabularies that never change per theme, the four
steps to add a theme, per-request `data-theme` resolution, and `make theme-guard`.

**Read `kkpa/context/architecture/gateway-theming.md` before changing a theme, a colour
token, or a font.**

---

## How to add or modify a page

Use this recipe whenever a task asks you to add, modify, or extend an HTML page or
region in the gateway. It tells you which files to touch and in what order. For the
*why* behind each rule, see [`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) and
[`ai/htmx-go-integration.md`](../../ai/htmx-go-integration.md).

### Decide first: does the markup need data the handler doesn't already have?

#### No — pure markup, or data already on the view model

1. Edit `internal/gateway/templates/pages/<name>.templ` (page shell) and/or
   `internal/gateway/templates/fragments/<region>.templ` (swap region).
2. Run `make templ` (pinned `go tool templ generate`; regenerates `*_templ.go`).
3. Done. No DB, no domain module, no `sqlc`.

#### Yes — the page must show something fetched or stored

Work backwards from the template to the database. Identify which domain module owns
the data (e.g. `account`, `charging`, `battery`, `drives`). If none fits, create a new
`internal/<module>/` — never put SQL or business logic in the gateway.

1. **Persist** — `internal/<module>/db/queries.sql` (edit) + `sqlc generate`
   (regenerates `db/*.go`).
2. **Domain** — `internal/<module>/<module>.go` (add DTO to the public `Service`
   interface) + `internal/<module>/service.go` (implement the method using the new
   sqlc query).
3. **Wire** (only if the module is new to the gateway) — `internal/gateway/gateway.go`
   (`Deps`) + `internal/gateway/handlers/handlers.go` (`Deps`, `Handler` struct,
   `New()`).
4. **View** — `internal/gateway/templates/fragments/<region>.templ`: a `ViewData`
   struct (presentation model; no `...Tesla` suffix leak) and a `ViewRegion(d
   ViewData)` component whose root element `<div id="<region>">` matches the
   `templ.Fragment("<region>")` id — required invariant for htmx swaps.
5. **Map** — `internal/gateway/handlers/handlers.go`: a `dataFor(ctx, uid)` helper
   decoupled from gin/session (unit-testable with `Service` fakes) and an optional
   `mapX()` mapper from domain DTO to the fragment view model.
6. **Render** — `internal/gateway/handlers/handlers.go`: `Page(c)` (auth guard →
   `dataFor` → `render`) and `RegionFragment(c)` (auth guard → `dataFor` →
   `renderFragment` with the fragment id).
7. **Route** — `internal/gateway/gateway.go`: `r.GET("/<name>", h.Page)` and
   `r.GET("/ui/<region>", h.RegionFragment)`.
8. **Template** — `internal/gateway/templates/pages/<name>.templ`. Use the authenticated
   drawer shell `layouts.BaseAuth` and compose the `ui/` kit (never raw markup/class soup);
   the swap button is `ui.Button` with its `hx-*` in `Attrs`:

   ```templ
   @layouts.BaseAuth("<title> — Magus") {
       @ui.PageHeader(ui.PageHeaderProps{Title: "<title>"})
       @ui.Card(ui.CardProps{}) {
           @ui.Button(ui.ButtonProps{Variant: "ghost", Class: "btn-sm", Attrs: templ.Attributes{
               "hx-get": "/ui/<region>", "hx-target": "#<region>", "hx-swap": "outerHTML",
           }}) {
               Refresh
           }
           @templ.Fragment("<region>") {
               @fragments.ViewRegion(d)
           }
       }
   }
   ```
9. **Regenerate** — `make templ` (pinned `go tool templ generate`; always after any `.templ` edit).
10. **Tests** — `internal/gateway/handlers/handlers_test.go` and/or
    `gateway_test.go`: fake the new `Service` method; `httptest` both `/<name>` and
    `/ui/<region>`.

### Layer-by-layer contract (what each layer enforces)

| Layer | Rule |
|---|---|
| Persist (`<module>/db`) | sqlc-generated; only `queries.sql` changes by hand. |
| Domain (`<module>`) | Every DB query is wrapped in a public `Service` method. DTOs carry no presentation fields; no vendor suffix leaks (e.g. `...Tesla`). |
| Handler (`gateway/handlers`) | Thin: `currentUID(c)` → `dataFor(ctx, uid)` → `render`. `dataFor` is gin-free so it's testable with `Service` fakes. NEVER imports a DB or any module's internals. |
| Fragments (`templates/fragments`) | `ViewData`/`ViewModel` structs live next to the markup. Root `<div id="X">` matches `templ.Fragment("X")` — required invariant for htmx swaps. |
| Pages (`templates/pages`) | The only place HTML skeletons live. Wrap in `layouts.BaseAuth` (authed) / `layouts.Base` (public), compose the `ui/` kit (`ui.PageHeader`/`ui.Card`/`ui.Button`, never raw class soup), and mark swap regions with `@templ.Fragment` blocks. |
| Router (`gateway.go`) | Full list of endpoints. One `GET` per page + one `GET` per swap region. |

### Two render entry points — reuse both

```go
func render(c *gin.Context, status int, comp templ.Component)
func renderFragment(c *gin.Context, status int, comp templ.Component, fragment string)
```

The SAME `pages.Name(d)` tree serves both `/<name>` and `/ui/<region>`. `renderFragment`
runs the whole template but emits only the `@templ.Fragment("<region>")` subtree — no
duplicate partial template. See
[`ai/htmx-go-integration.md`](../../ai/htmx-go-integration.md).

### Non-2xx error fragments — use the `*Error` variants

```go
func renderError(c *gin.Context, status int, comp templ.Component)
func renderFragmentError(c *gin.Context, status int, comp templ.Component, fragmentNames ...string)
```

Rendering a validation form or error row at 4xx/5xx with plain `render` makes it
**invisible**: htmx never swaps a 4xx/5xx body, so the response arrives and nothing happens
on screen. The `*Error` variants set `HX-Error-Fragment: true`, which the `htmx:beforeSwap`
listener in `static/app.js` honours. Rule of thumb: **if the non-2xx body is a component,
it goes through `renderError`; if it's bare text (`c.String`), it doesn't.** Full rationale
in [`ai/htmx-go-integration.md`](../../ai/htmx-go-integration.md) §Response conventions.

Companion markup rule: a submitting form carries `hx-post`/`hx-put` on the `<form>` with a
`Type: "submit"` button, never `hx-*` on the button — otherwise htmx skips HTML5 validation
entirely and every `Required` prop is inert. See
[`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) §htmx attribute conventions.

### Two response traps a server-side test cannot catch

Both were found on the charges page, but neither is page-specific. Both pass every Go test
and fail only in a real browser.

- **Never pair a top-level `<tr>` with a non-table `hx-swap-oob` sibling in one response.**
  htmx 2.0.4 parses a response inside a `<template>` (`makeFragment`); a leading `<tr>` start
  tag switches the HTML parser into table insertion mode, and the non-table sibling that
  follows is foster-parented off the fragment's top level — the only place htmx looks for
  `hx-swap-oob`. **A server-side test sees the OOB element in the response body and passes;
  only the browser drops it.** Found on `ExternalChargeRowUpdate`, applies to any handler
  returning a row plus an OOB sibling. `ExternalChargeCreateSuccessOOB` is unaffected because
  both of its elements are `<div>`s.
- **No `<details>`/`<summary>` collapse around a required control, on any form.** A browser
  cannot report an HTML5 validation message on a control inside a closed `<details>` — Chrome
  logs *"An invalid form control ... is not focusable"* and the submit silently does nothing:
  no message, no request. Pinned by `TestExternalChargeForms_NoDetailsCollapse`
  (`handlers/external_charges_test.go`).

### Auth guard pattern — same on every page

```go
func (h *Handler) Page(c *gin.Context) {
    uid, ok := currentUID(c)              // encrypted cookie session key "uid"
    if !ok {
        c.Redirect(http.StatusFound, "/login")
        return
    }
    render(c, http.StatusOK, pages.Name(h.dataFor(c.Request.Context(), uid)))
}
```

Every page must opt in. There is no middleware-based auth today — copy this guard.

### When the data doesn't belong to an existing module

If the data is conceptually a new subsystem (charging, drives, battery…):

- The owner is a **new** `internal/<module>/`, NOT the gateway.
- It exposes a `Service` interface + DTOs (same shape as `account.Service`).
- Gateway consumes it ONLY through that interface — add it to `Deps` + `Handler`
  (step 3 above).
- The recipe above is unchanged from step 4 onwards.

If the data fits an existing module, add the new method/DTO to that module's `Service`
and skip step 3 (the module is already in `Deps`).

**Hard rule:** the gateway never owns business data. If a handler "needs SQL", the
design is wrong — add a method to the owning module instead.

### Regeneration cheatsheet

| File changed | Run | Produces |
|---|---|---|
| `internal/<module>/db/queries.sql` | `make sqlc` | `db/*.go` (generated) |
| `*.templ` (structure/markup) | `make templ` (pinned `go tool templ generate`) | `*_templ.go` (generated) |
| new/changed DaisyUI or Tailwind **class** in a `.templ` | `make css` (auto-fetches the Tailwind binary if missing) | `static/app.css` (committed) |
| `*.go` | `go build` / `go test` | nothing else |

Never hand-edit generated files (`db/*.go`, `*_templ.go`, `static/app.css`). `make generate`
runs **sqlc + templ + css** together — prefer it after a template change so nothing is missed.

### Hot-reload dev loop (`make dev`)

`make dev` is the UI hot-reload path — it starts three watchers in parallel and runs the
web server with `MAGUS_DEV=1`, which flips the `/static` handler from the `//go:embed` FS to
on-disk `internal/gateway/static` (see `gateway.go`). CSS edits surface on the **next
browser refresh** with NO Go rebuild, because `tailwindcss --watch` writes a fresh `app.css`
to disk and the running server serves that disk copy. `*_templ.go` changes (from saving a
`.templ`) still require a Go rebuild; `air` does that in ~1s and restarts the server. Cookie
sessions survive, so you do not re-login. `make dev` does NOT run migrations — apply them
once with `make migrate-up` before. `make up` (full regenerate + build + run) stays the
correct path for non-UI Go logic changes.

> **Gotcha — stale CSS silently ships unstyled markup.** `static/app.css` is a committed,
> `//go:embed`-ed artifact: only classes present in it at build time are styled. If you add a
> class to a `.templ` but skip `make css`, that class ships **unstyled in production** — the
> build still succeeds, so nothing warns you. Always run `make css` (or `make generate`) and
> **commit `app.css` in the same change** as the template edit. CI guard:
> `make css && git diff --exit-code internal/gateway/static/app.css`.
