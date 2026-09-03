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
  `Deps.AnalyticsReader.BatteryLevelByDay(ctx, uid, teslaID, start, end)` instead, with
  **no lookback** (the port returns exactly `[start, end]`, since `vehicle_metrics.
  metric_date` is already the effective day it needs). The four `LatestSnapshotsByAccount`
  call sites (dashboard, vehicle cards, nav header, charges battery suggestion) were
  already repointed onto `Deps.AnalyticsReader.LatestMetricsByAccount` below by the
  earlier `RM38-gateway-read-dashboard-from-metrics`. Do **not** reintroduce a
  `Deps.TelemetryReader` field or an import of that module's package — `make
  boundary-guard` enforces this repo-wide (fails on a non-test file, warns on a
  `_test.go` file; escape hatch `// boundary:allow: <reason>`, which this module
  carries **zero** of). Rule and migration status: `ai/architecture.md` §"Exception:
  the gateway may not depend on `telemetry` at all".
- `Deps.ChargingWriter charging.Writer` — the manual charge write port; injected
  at construction. Called ONLY by the write handlers (ChargeCreate, ChargeRowUpdate,
  ChargeRowDelete) on explicit user-initiated form submissions. See "Exception:
  user-initiated writes" below. NEVER import `internal/charging/db` — all access
  through this interface only.
- `Deps.ChargingReader charging.Reader` — the manual charge read port; injected
  at construction. Called by read handlers (ChargePage, ChargesListFragment,
  ChargeRowStatic, ChargeRowEditFragment) and the `buildChargesPage` helper to list
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
  Since `RM38-gateway-read-dashboard-from-metrics`, also **`LatestMetricsByAccount`** —
  the account's latest per-vehicle status, replacing the equivalent
  `telemetry.Reader.LatestSnapshotsByAccount` calls. Four callers: `dashboardFor` (single-
  vehicle bento, via `mapDashboardSnapshot`), `vehiclesFor`/`mapVehicles` (the unrouted
  `/ui/vehicles` card list — see `mapVehicles`'s own doc comment for why it is kept
  working despite having no route), `navHeaderFor` (status dot/battery — a nil
  `CapturedAt` forces `NavStatusAsleep`, never `NavStatusConnected`), and
  `buildChargesPage`'s battery-suggestion lookup (`charges.go`). Eight of
  `analytics.VehicleStatus`'s fields are pointers (`InsideTempC`, `OutsideTempC`,
  `CarVersion`, `ChargeLimitSocPct`, `ChargingState`, `CapturedAt`, `Locked`,
  `SentryMode`) — nil means "not yet computed since the migration," never a fabricated
  zero value; see `openspec/changes/RM38-gateway-read-dashboard-from-metrics/design.md`
  D2/D3/D8 for the exact per-field nil-handling table.
- `Deps.AnalyticsRecalculator analytics.Recalculator` — the analytics module's
  **write** port, injected the same way (wired from `cmd/web` via
  `analytics.NewRecalculator(...)`). Called by `ChargeCreate` after a manual
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
- **Compose the `ui/` kit** (Card, StatTile, Button, Alert, Badge, Dot, Table, PageHeader,
  NavShell, ConfirmDialog, and the form set **Field / Input / Select / Textarea**) — **never inline
  a DaisyUI component class** (`btn`, `input`, `card`, `fieldset`, …) in a page/fragment; that's a
  bug. If a repeated element has no wrapper, **add one to `ui/`** instead of inlining. Theme
  tokens (`text-error`, `bg-base-100`) and Tailwind layout utilities stay inline — the stable
  layers. Pages/fragments pass VM-ready strings in.
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
- **`ui.Dot`** (`templates/ui/dot.templ`, `DotProps{Variant, Tooltip, Class}`) — a small
  colour-only completeness/status indicator with a native hover tooltip, for a spot where
  `ui.Badge`'s mandatory text would be redundant with an adjacent label. `Variant` is one of
  `"success"|"warning"|"error"|"neutral"` (DaisyUI semantic token, mapped by `dotClass` exactly
  like `badgeClass` maps `Badge`'s `Kind`); `Tooltip` renders as the `title` attribute and is
  omitted when empty. Gold standard: the charges table's Status column
  (`fragments/charge_row.templ`), which pairs a `ui.Dot` (`success`/`warning`, completeness) with
  an adjacent `ui.Badge` (`primary`/`ghost`, lifecycle status) — the two colour vocabularies are
  **deliberately disjoint** so the badge's colour never reads as a second completeness signal
  (design.md §D-Dot, `RM33-gateway-add-entries-dashboard`). Reuse this pairing shape for any
  future dot+badge combination; never repurpose `success`/`warning` for a badge that sits next to
  a dot.
- **Semantic tokens only — never hex / raw palette** (`bg-base-100`, `primary`,
  `success`; not `#fff` / `bg-red-500`). The app re-skins from one `<html data-theme>`
  (default `lemonade`; `dark` auto-applies via `prefers-color-scheme`).
- **No client-side JS init** — keeps htmx swaps safe. Prefer CSS-only DaisyUI patterns
  (`<dialog>` modal, `dropdown`, `collapse`, `tabs`) over any JS. There are exactly **five**
  standing exceptions, each with its own recorded decision below: **RD9** (the `browser_tz`
  cookie script in `layouts.BaseAuth`), **RD10** (`ui.ConfirmDialog`, whose JS lives in
  the shared `static/app.js`), **RD12** (date→time-preserving sync on the charge forms), and
  **RD13** (status-driven required toggle on the charge forms) plus **RD14** (location-kind
  driven label toggle) — the last three also live in `static/app.js`. Adding a sixth needs
  its own RD entry per RD8.
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
  `handlers.LanguageMiddleware` — a new page needs no per-handler language plumbing, only
  `i18n.T` calls in its markup.

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
  `Writer` ports from a handler, **except as documented below**. The telemetry
  module is off-limits entirely — see "Public interface" above.
- No writes, no Tesla API calls, no side effects on user requests. The only
  user-initiated Tesla API call is listing vehicles on first Tesla connect
  (one-time seed), and even that happens through the account module's interface —
  not the gateway calling Tesla directly.
- All writes (telemetry collection, summary computation, token rotation) happen in
  the nightly batch (`telemetry.Collector`) or inside `account.Service` methods
  called from non-gateway paths — never from a gateway handler.

### Exception: user-initiated writes (D4 amendment — RM3-gateway-add-manual-charge-ui)

The gateway MAY call `charging.Writer` (Create / Update / Delete) on explicit
**user-initiated form POSTs/PUTs/DELETEs** (`ChargeCreate`, `ChargeRowUpdate`,
`ChargeRowDelete`), subject to ALL of the following constraints:

1. **Auth guard first** — `currentUID(c)` must resolve a valid session UID or the
   handler redirects to `/login` and returns. No write proceeds without an
   authenticated user.
2. **Tenant ownership validated before every write** — the handler calls
   `h.acct.RegisteredVehicles(ctx, uid)` and confirms the submitted `(tesla_id, vin)`
   pair belongs to the calling user's account. If the vehicle is not in the user's
   list, the handler returns HTTP 403 (forbidden) without calling Writer. This is the
   referential integrity flow: no cross-module FK exists in the DB, so the gateway
   enforces tenant scoping at the application layer.
3. **CSRF token on every state-changing route** — the handler calls `checkCSRF(c)`,
   which reads `csrf_token` from the form body (or `X-CSRF-Token` header) and
   compares it via `subtle.ConstantTimeCompare` to the session key
   `"csrf_manualcharge"`. Returns HTTP 403 on mismatch; no write proceeds.
4. **Only `charging.Writer` is permitted** — this is the narrow aperture.
   This amendment does NOT open general write access to the gateway; Reader-only
   remains the default for ALL other handlers (dashboard, telemetry fragments,
   health, OAuth, etc.).

**Rationale:** Manual charge entry is a different class of request from dashboard
reads: the user explicitly fills a form and submits it. Denying all writes at the
gateway layer would force an external HTTP API that the browser would then need to
call — an unnecessary layer when the gateway is already the only HTML surface.
The write is intentional (form POST), narrow (one module's Writer port),
CSRF-protected, and tenant-scoped.

### Manual charge form layout (both forms, 2026-08-29)

`ChargeCreateForm` and `ChargeRowEdit` render the SAME ten fields in the SAME
order — `status`, `charged_on`, `energy_added_kwh`, `price`, `started_at`,
`ended_at`, `start_battery_pct`, `end_battery_pct`, `location_kind`,
`location_label` — in the main grid, followed by an always-visible
**"Optional details"** `<section>` (`charging_type`, `odometer_km`, `notes`).
The edit row appends one edit-only extra after the shared ten: the read-only
Vehicle display. `TestChargeForms_FieldOrderIsSharedAndLocationLast`
(`handlers/charges_form_layout_test.go`) checks both templates against ONE
order list, so "the same order" is enforced rather than merely intended.
(`location_label` moved from the optional section into the main grid on
2026-09-01, closing the grid behind `location_kind`.)

- **No `<details>`/`<summary>` collapse on either form — do not reintroduce one.**
  It previously hid `location_kind` (always required) and `ended_at` (required when
  status is DONE) in the edit row, and a browser **cannot report an HTML5 validation
  message on a control inside a closed `<details>`** — Chrome logs *"An invalid form
  control with name='location_kind' is not focusable"* and the submit silently does
  nothing: no message, no request. If a future field must be tucked away, it has to be
  unconditionally optional, and the section stays open.
- **`location_kind` is required and belongs in the main grid** — never in the
  optional section; the optional `location_label` follows it as the grid's final
  field. `TestChargeForms_LocationIsLastInTheMainGrid` pins this.
- **The section heading carries the optional signal for its own three fields**; the
  per-field `ui.FieldProps.Optional` hint marks the optional fields that live in
  the MAIN grid (`energy_added_kwh`, `price`, `started_at`, `location_label`), so
  the two signals never duplicate each other.
- **`location_label` is disabled unless `location_kind` is `OTHER`** (RD14) — the
  server renders the initial `disabled` state and the `static/app.js` listener
  keeps it live on select change; a disabled input is not submitted, so a label
  typed under HOME/WORK is dropped at save (deliberate).

### Manual charge edit: a successful save returns the whole list, retargeted

`ChargeRowUpdate`'s SUCCESS path answers with the **whole `#charges-list`
fragment** plus `HX-Retarget: #charges-list` / `HX-Reswap: outerHTML`, rendered
under `defaultChargesWindow(today)` so the user lands back on the **"last 7
days"** preset. `defaultChargesWindow` returns exactly that preset's
`(today-6, today)` range, so `buildChargesPresets` marks it `Active` by its own
exact-match rule — nothing hardcodes a preset index or label.

- **Never pair a top-level `<tr>` with a non-table `hx-swap-oob` sibling in one
  response.** This path used to return a primary row swap plus an OOB
  `#charges-list` div, and **the list never refreshed in the browser**: htmx
  2.0.4 parses a response inside a `<template>` (`makeFragment`), a leading
  `<tr>` start tag switches the HTML parser into table insertion mode, and the
  non-table sibling that follows is foster-parented off the fragment's top
  level — the only place htmx looks for `hx-swap-oob`. Server-side tests saw the
  OOB div in the response body and passed; only the browser dropped it.
  `ChargeCreateSuccessOOB` is unaffected because both of its elements are
  `<div>`s, which is exactly why create refreshed the list and edit did not.
- **Why `HX-Retarget` rather than changing the form's `hx-target`.** The form's
  `hx-target` stays `#charge-row-{id}`, which is correct for the 4xx/5xx
  branches: they re-render the edit row in place and preserve the user's typed
  values (design.md §D-Values). Only the success path retargets, so one response
  element covers it with no OOB and no mixed content.
- **Only success resets the window.** The error branches still echo the POSTED
  window (`windowFromForm` → the form's hidden `start`/`end` inputs, §D-Include);
  a failed save must not move the user's filter. Test Contract **D3** covers the
  retarget + reset, **D3b** the error-path echo — the pair is the contract.
- **This amends the original §D-Refresh/§D-Include rule** for the update path
  only; `ChargeCreate` and `ChargeRowDelete` still preserve the posted window.
- **Known consequence:** an entry dated outside the last 7 days will not appear
  in the refreshed list after being edited. That is inherent to resetting the
  filter — the record is saved, it is just outside the window now shown.

**Known latent issue, not yet fixed:** `ChargeCreateSuccessOOB` wraps
`ChargesList` (whose own root is `<div id="charges-list">`) in a second
`<div id="charges-list" hx-swap-oob=...>`, so after a create the live DOM holds
two nested elements with that id. It works today — the OOB replaces the outer,
lookups resolve to it — but it is a duplicate-id trap for anything that later
targets `#charges-list`. The fix is to let `ChargesList`'s own root carry the
OOB attribute instead of wrapping it; do that the next time this path is touched.

### Manual charge list: only ONE row is editable at a time

`GET /ui/charges/row/:id/edit` (`ChargeRowEditFragment`) renders the **whole
`#charges-list` region** with that row — and only that row — in edit mode, driven
by `ChargesPageData.EditingID`. The row's Edit button therefore carries
`hx-target="#charges-list"`, not `hx-target="#charge-row-{id}"`.

- **Why the list, not the row.** When the row was its own swap target, each Edit
  click was independent, so a user could open every row at once and end up with N
  competing forms. Making the LIST the swap unit means opening a second editor
  necessarily re-renders the first one closed — the invariant holds on every
  render instead of depending on client-side bookkeeping a stray swap could
  desynchronize. It also needs **no new JS**, so no RD entry: the Delete button in
  the same file already targets `#charges-list` this exact way, and this mirrors
  that existing mechanism rather than inventing a second one.
- **`EditingID` is set by `ChargeRowEditFragment` and by nothing else.** Every
  other render leaves it empty, which is what closes an open editor after a
  successful save (`ChargeRowUpdate`'s OOB `#charges-list` refresh), a delete, a
  filter click or a vehicle switch. Do not set it from `buildChargesPage`.
- **Cancel still swaps the single row** (`ChargeRowStatic` → `#charge-row-{id}`)
  and stays correct precisely because only one row can be open.
- A 404 for an id absent from the rendered window is deliberate: Edit is only
  reachable from a row the user can see, and the presence check costs no extra
  read (it scans the page just built).

### Manual charge form helper copy

Both forms carry one short line under the title, from the catalogue:
`KeyChargesFormCreateHint` (create) and `KeyChargesFormEditHint` (edit row).
**These strings state rules that live in Go**, so a change to either rule is
incomplete until the copy follows:

- the create hint states `charging.resolveEnergy`'s derivation (`service.go`) — an
  omitted energy is estimated from the battery delta × pack capacity, and **only
  when both `StartBatteryPct` and `EndBatteryPct` are present with end > start**,
  so an IN_PROGRESS entry gets no estimate until it is completed — and the
  IN_PROGRESS required set;
- the edit hint states `charging.RequiredFieldsFor(StatusDone)`'s extra fields
  (`ended_at`, `end_battery_pct`).

### Manual charge rule: one IN_PROGRESS entry per (vehicle, charged_on)

A vehicle may have at most **one** manual charge entry with status `IN_PROGRESS`
on any given `charged_on` date. `DONE` entries are unconstrained — any number may
share a date. Enforced in `handlers.inProgressConflictOn` (`handlers/charges.go`)
on **both** write paths: `ChargeCreate` (`POST /ui/charges/create`, excluding
nothing) and `ChargeRowUpdate` (`PUT /ui/charges/row/:id`, excluding the edited
row's own id so an already-in-progress entry never conflicts with itself). A
conflict is reported through the SAME 422 branch as every other validation
failure — `validationErrors["_top"]`, so the user's submitted values survive the
re-render — carrying `i18n.KeyChargesErrorInProgressExists` formatted with the
conflicting date as `YYYY-MM-DD`.

- **No new port.** The check reads
  `charging.Reader.ListEntriesByVehicleBetween(chargedOn, chargedOn)` — the same
  port every list render already uses, with both bounds on the single day in
  question. Do NOT add a status-filtered method to `charging.Reader` for this;
  the day's entry count is small and the read is already bounded. The helper
  nonetheless **re-asserts the calendar day on every returned row** instead of
  trusting the port's window — a write-blocking rule must not depend on a read
  port's filtering being exact, and the two sides carry different time
  components (form-parsed UTC midnight vs. the `DATE` column's round-trip).
- **There is NO database constraint behind this rule** — it is an
  application-level rule, so the check **fails open**: a reader error is logged
  and the write proceeds, matching this module's log-and-continue posture for
  non-essential follow-ups (`recalculateAfterChargeWrite`, the telemetry
  suggestion lookup in `buildChargesPage`). Turning a transient read failure
  into a refusal to save would trade a real data loss for a hypothetical
  duplicate. If this ever needs to be airtight, the fix is a partial unique
  index in the `charging` module, not a fail-closed gateway check.
- **Only `IN_PROGRESS` submissions are checked** — a `DONE` submission returns
  without reading anything.

### Manual charge success notice

`fragments.ChargesPageData.Notice` is the success counterpart of `.Error`: a
non-empty value renders a `ui.Alert{Kind: "success"}` at the top of the
create-form card (the same slot the `_top` validation alert uses). It is set in
exactly ONE place — `ChargeCreate`'s success path, to
`i18n.KeyChargesNoticeEntryCreated` — so it rides in on the response to the write
that earned it via the primary `#charges-create-form` swap and is gone on the
next render of any kind. `buildChargesPage` never sets it; do not set it from a
read path, or the message will persist across refreshes.

### Exception: language switch (D-lang amendment — RM24-gateway-add-i18n-foundation)

The gateway MAY call `account.Service.SetLanguage` from `handlers.LangSwitch`
(`POST /ui/lang/switch`), subject to a **different** set of constraints than the
D4/charging amendment above — it does not transplant cleanly, because this
endpoint must work for anonymous callers too:

1. **No auth guard, no redirect-to-login.** Every other write handler starts with
   `currentUID(c)` and redirects an anonymous caller to `/login`. `LangSwitch`
   branches instead: it always sets the `lang` cookie; it calls `SetLanguage` only
   when a session `uid` is present.
2. **No tenant-ownership check.** `SetLanguage(ctx, uid, lang)` always targets the
   caller's own session `uid` — there is no user-submitted resource identifier (unlike
   the vehicle `(TeslaID, VIN)` pair D4 validates) for a forged request to redirect at
   a different account.
3. **No CSRF check — a deliberate divergence from D4, not an oversight.** A forged
   switch request can only ever change the caller's own display language (no data
   mutation, nothing to exfiltrate, reversible in one click). Requiring CSRF here
   would mean minting a session CSRF token on every page in the module — including
   `Home`/`Dashboard`/`SuperchargerStats`, which mint none today — for a control
   mounted on every page (design.md D6), to protect against a cosmetic annoyance.
   **The actual defence is the `lang` cookie's `SameSite=Lax` attribute**, which
   makes modern browsers refuse to attach it to a cross-site `POST` — this is
   MANDATORY, not incidental; dropping it would void this decision and re-open the
   CSRF question. `setLangCookie` (`internal/gateway/handlers/lang.go`) MUST call
   `c.SetSameSite(http.SameSiteLaxMode)` **before** `c.SetCookie(...)` (gin's
   `SetCookie` has no SameSite parameter). `TestLangSwitch_CookieIsSameSiteLax`
   (`lang_test.go`) is mandatory and may not be dropped or weakened — if it fails,
   fix the cookie, never the test.
   - **This trade-off was put to the user and explicitly approved on 2026-08-13,
     conditional on `SameSite=Lax`.** It is not a worker's unilateral call.
4. **Scope stays narrow.** Only `account.Service.SetLanguage` is permitted under this
   exception. Every other handler stays Reader-only except the pre-existing D4
   aperture above.

### Exception: Supercharger session battery verification (D8 amendment — RM31-gateway-add-session-battery-edit)

The gateway MAY call `charging.SessionVerifier.VerifySession` from
`SuperchargerRowUpdate` (`PATCH /ui/supercharger-stats/row/:id`), subject to
ALL of the following constraints. `SessionVerifier` is a DIFFERENT port from
D4's `charging.Writer` — this amendment does not stretch D4's language to
cover it, it names its own aperture:

1. **Auth guard first** — `currentUID(c)` must resolve a valid session UID or
   the handler redirects to `/login` and returns. No write proceeds without an
   authenticated user.
2. **CSRF token on the write route** — the handler calls
   `checkCSRFKey(c, csrfSuperchargerKey)`, where `csrfSuperchargerKey =
   "csrf_supercharger"` is a NEW session key, distinct from D4's
   `"csrf_manualcharge"`. Issued once by `SuperchargerStatsPage`; read (never
   re-issued) by `SuperchargerStatsFragment` and every row-level handler.
   Returns HTTP 403 on a missing/stale/mismatched token; no write proceeds.
3. **No separate `RegisteredVehicles` ownership check — a deliberate
   divergence from D4, not an oversight.** D4's write path validates the
   submitted `(TeslaID, VIN)` pair against `account.RegisteredVehicles`
   before calling `Writer`. This aperture does NOT perform that check.
   `VerifySession`'s own `WHERE id = @id AND account_id = @account_id` clause
   is the sole tenant boundary for this write: it has no `TeslaID`/vehicle
   predicate at all, so a session belonging to a different vehicle on the
   SAME account remains writable through this route (not a tenancy
   escalation — the account owns both sessions), while a session belonging
   to a DIFFERENT account's data is excluded by the `WHERE` clause itself,
   never reachable regardless of the id supplied.
4. **Only `charging.SessionVerifier.VerifySession` is permitted** — this is
   the narrow aperture. This amendment does NOT open general write access to
   the gateway; Reader-only remains the default for ALL other handlers, and
   D4's own `RegisteredVehicles` ownership check remains required on D4's
   write path — this amendment changes nothing about D4.

**Rationale:** identical in shape to D4's — an explicit user-initiated form
save, CSRF-protected — but the ownership-check divergence (point 3) exists
because `VerifySession` was designed (tier 1,
`RM29-charging-add-session-verification`) with account-scoping as its own
complete tenant boundary, and re-deriving a `TeslaID`-based check the port's
own `WHERE` clause does not use would test a predicate the write itself never
applies.

### Inactive-account login block (RM34-gateway-block-inactive-login, 2026-08-30)

`GoogleCallback` (`handlers.go`) refuses to establish a session for an account whose
`account.Account.Status` is not `account.StatusActive`. Immediately after
`h.acct.UpsertFromOAuth` resolves the account and BEFORE `h.syncLoginLanguageCookie` or
`sess.Set("uid", ...)` runs, it calls `rejectIfInactive(c, acct)`: for anything other than
`StatusActive` it renders `pages.AccountBlocked()` at HTTP 403 via `renderError` and returns
`true`, telling the caller to stop. `rejectIfInactive` is a free function (no `Handler`
receiver), factored out the same way `syncLoginLanguageCookie` is, because `h.google` is a
concrete `*googleauth.Client` with no fake-able seam — the extraction is what makes the check
testable with a hand-built `gin.Context` and a plain `account.Account`, with no live network
call.

**One render site, no route.** `pages.AccountBlocked()` is rendered exactly once, inline inside
`GoogleCallback`'s 403 response. There is **no `GET /account-blocked` route** and no
`exemptFromStatusGate` allowlist — an earlier design considered a per-request middleware
(`AccountActiveGate`) that would have needed both as its own redirect target, but that
middleware was withdrawn before implementation (roadmap `RM34-account-vehicle-status` D26): a
newly-gated signup has never held a session, so the login-time block alone satisfies the
ticket. If a future change needs to revoke an *already-established* session mid-flight, it
needs its own design — do not assume this entry's shape (one inline render, no route)
generalizes to that different problem.

**Why the blocked page always renders in the visitor's pre-login language, not the account's
stored preference.** `LanguageMiddleware` runs before every handler and branches on
`currentUID(c)`. For the `/auth/google/callback` request specifically, no session exists yet
(this is the very request that would create one), so `currentUID` always returns `ok=false`
here — `LanguageMiddleware` takes its anonymous branch (the pre-login `lang` cookie, or Spanish
by default) regardless of the resolved account's status or stored language. `GetAccountLanguage`
(tier 1's `status = 'Active'`-filtered query) is never consulted for this request at all, so
there is no imprecision to reconcile.

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
   pick the status for that TeslaID out of `analytics.Reader.LatestMetricsByAccount`, or
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
   `HX-Trigger`". Gold standards: the dashboard `#dashboard-content` and the manual-records
   `#charges-content` regions (each re-fetches `GET /ui/dashboard` / `GET /ui/charges`).

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
library, or to CHANGE a rendering/architecture approach for the gateway's UI
(charts, interactive widgets, animations, drag-and-drop, etc.) MUST be recorded in
this `AGENTS.md` in the SAME change — never in a commit message alone. The rationale
and the rejected alternative must both be documented. This makes the decision visible
to every future AI agent or human who reads this doc at the start of a session.

## Client-side JS exception: browser_tz cookie script (RD9)

The gateway's declared **zero-JS** DaisyUI foundation (`ai/htmx-conventions.md`
§"Styling" — "Do not introduce a component that needs client-side JS init") has
exactly **FIVE** sanctioned exceptions: this one, **RD10** (the confirmation
modal) below, and **RD12**/**RD13**/**RD14** (the charge-form date-sync,
status-required toggle, and location-label toggle) further below. This entry covers the first: a single inline `<script>` in
`layouts.BaseAuth` that sets the `browser_tz` cookie. Added by
`gateway-browser-tz-cookie` (MAG-7, shipped 2026-08-11; documented here in the
MAG-7 review fix round, 2026-08-12).

**What:** One `<script>` block, inside `templ BaseAuth` only (never the
anonymous `Base` shell), wrapped in `try/catch`. It reads
`Intl.DateTimeFormat().resolvedOptions().timeZone` and sets
`document.cookie = "browser_tz=" + encodeURIComponent(tz) + ";path=/;max-age=31536000;SameSite=Lax"`.
No library, no `fetch`, no event listener — a single synchronous read plus one
cookie write, on every authenticated page load.

**Why:** The server has no other way to learn the browser's IANA timezone —
unlike, say, `Accept-Language`, no HTTP header or cookie carries it
unprompted. Without it, the dashboard's date math (`parseHistoryRange`,
`defaultHistoryHref`, `buildHistoryPresets`) computed "today"/"yesterday" in
UTC, which diverges from the user's local calendar day (the MAG-7 bug: a user
in PST at 10pm local saw the previous UTC day). The **rejected alternative**
was moving date computation/rendering to the client (JS computes and formats
the dates the browser displays): a far larger departure that would move date
math out of Templ/Go entirely, contradicting "no business logic in
templates, no time math in markup" (`ai/htmx-conventions.md`) for every
date-touching page, not just this one. A single cookie write is the
minimal-surface-area way to hand the server the ONE fact it is missing (the
IANA zone name) while keeping all date arithmetic server-side.

**Boundary — this is NOT an opening for general client-side JS.** It is a
narrow, sanctioned exception (one script, one cookie write, one fact), not a
precedent. Any future addition of client-side JS to the gateway needs its own
RD entry here, per RD8, with its own rationale and rejected alternative —
this entry does not grandfather it in.

**Graceful degradation:** the script is wrapped in `try/catch`; on any JS
failure, or in a `<noscript>` browser, the cookie is simply never set and the
server falls back to `clock.Zone()`, the platform default `America/Bogota`
(`browserLocation`'s fallback rule — was `time.UTC` before
`RM35-gateway-adopt-clock`) — no error surfaces to the user and no page
render breaks.

## Client-side JS exception: confirmation modal (RD10)

The **second** sanctioned exception to the zero-JS rule (see **RD12**/**RD13** below for
the third and fourth): the
`htmx:confirm` interception in `static/app.js` that drives `ui.ConfirmDialog`. Added by
`gateway-add-confirm-dialog` (MAG-5, shipped 2026-08-12, PR #24; documented here
2026-08-13).

**What:** One `htmx:confirm` listener in the shared `static/app.js` (~40 lines, no
library, no `fetch`), plus `ui.ConfirmDialog` — a native `<dialog>` mounted **once** in
`layouts.Base` (so `BaseAuth`, which composes `Base`, inherits it on every authenticated
page). htmx fires a **cancelable** `htmx:confirm` event before every request carrying
`hx-confirm`, exposing the element's message as `detail.question` and a
`detail.issueRequest(skip)` callback. The listener calls `preventDefault()`, fills the
dialog from the element's attributes, and calls `issueRequest(true)` on confirm — htmx
then resumes the exact same request. Per-use content is a four-attribute vocabulary on
the triggering control: `hx-confirm` (message), `data-confirm-title`,
`data-confirm-label`, `data-confirm-variant="danger"`.

**Why:** replacing the browser's unstyleable `window.confirm()` is inherently a
JS-interception job — htmx offers the decision **only** as an event. The **rejected
alternative** was a CSS-only DaisyUI pattern (checkbox/anchor `<dialog>` modal), the
approach this doc mandates everywhere else: it cannot work here at all, because a
CSS-only modal has no way to *gate an in-flight htmx request* — the request would fire
before the user answered. The second rejected alternative was a bespoke per-page modal
with its own script, which reintroduces hand-rolled JS on every page that needs a
confirmation. Hooking htmx's own documented event instead means **any element on any
page carrying `hx-confirm` gets the modal automatically — including pages not yet
written** — so the marginal cost of the next confirmation is one attribute, not a
component.

**Why it does not erode the `ui/` boundary:** the dialog pre-renders **both** confirm
buttons (default + danger) and `app.js` only ever toggles the `hidden` property and sets
`textContent`. No DaisyUI `btn-*` class string ever appears in JavaScript — the component
vocabulary stays owned by `templates/ui/`, exactly as the anti-corruption-adapter rule
requires. Native `<dialog>.showModal()` is used so focus-trapping, Esc-to-close, page
inertness, and top-layer stacking are the browser's job, not ours.

**Boundary — this is NOT an opening for general client-side JS.** Like RD9, it is a
narrow, sanctioned exception (one listener, one shared dialog, one decision-gating job),
not a precedent. Any further client-side JS needs its own RD entry per RD8, with its own
rationale and rejected alternative. Two concrete rules follow from the single-instance
design: **do not mount a second `ui.ConfirmDialog`** (`app.js` resolves it by `id`; a
duplicate makes the wrong one open), and it stays in the layout, **outside every
swappable region**, so an htmx swap can never replace an open dialog.

**Graceful degradation:** if the dialog is absent (a page not built on `layouts.Base`) or
the browser has no `<dialog>` support, `app.js` returns early and htmx falls back to its
native `confirm()` — degraded styling, but the guard itself is never lost.

## Self-hosted web fonts — Inter + JetBrains Mono (RD11)

The gateway's no-CDN rule (`base.templ`: "never an external CDN") now extends to
typography: **Inter** (400/700/800) and **JetBrains Mono** (500) are self-hosted as
woff2 binaries under `internal/gateway/static/fonts/`, `//go:embed`-ed via
`gateway.go`'s existing `//go:embed static` (no Go change needed). Added alongside
the Stitch `design.md` port (2026-08-19) so the Apex theme's typography spec is
live, not just documented.

**What:** four woff2 files (~94 KB total, OFL-licensed, fetched from the Fontsource
`font-files` repo), four `@font-face` blocks, and a `[data-theme]` override of
`--default-font-family` / `--default-mono-font-family` — all in
`internal/gateway/static/themes/_shared.css`, the single source of truth for
everything every theme shares (see *Theme file layout* below).
DaisyUI v5's body rule reads `var(--default-font-family, <system stack>)`, so setting
the token cascades to every component with **no per-template edit** for the body font.
The `font-mono` utility (Tailwind's `var(--font-mono)`, which resolves to JetBrains
Mono via the same override) is added only to the `ui/` wrappers `design.md` calls out
as "technical labels / values / status labels": `input`, `select`, `textarea`,
`badge`, and `stat_tile`'s `stat-value` — the anti-corruption-adapter boundary keeps
the class owned in `ui/`, not inlined in pages.

**Why a plain `[data-theme]` rule and not entries in the `@plugin` block:**
DaisyUI v5 owns `--default-font-family` / `--default-mono-font-family` internally —
values set for those keys inside a theme's `@plugin` block are silently dropped and
replaced with `sans-serif` / `monospace`. A plain unlayered CSS rule in `_shared.css`
wins over DaisyUI's `@layer base` output, so the tokens actually resolve to the real
fonts. This is a documented DaisyUI v5 quirk, not a Tailwind v4 bug.

The selector is the **attribute-only** `[data-theme]`, not `[data-theme="apex"]`: the
fonts are shared by every palette, so keying them to one theme name would silently
drop them the moment `base.templ` switches to another. Same specificity (0,1,0), still
unlayered, so the reason above holds unchanged.

**Rejected alternative — Google Fonts `<link>`:** would add a runtime CDN dependency
to a stack that explicitly bans CDNs (`base.templ` comment) and ships every other
asset (`htmx.min.js`, `app.css`) `//go:embed`-ed. A CDN link also introduces a
privacy surface (third-party font fetch per page view) and a single point of failure
for the page's typography. Self-hosting keeps the deploy self-contained, consistent
with the existing asset-pinning convention, and adds ~94 KB of binary (committed,
cacheable forever — the `@font-face` URLs are content-addressed by filename).

**Rejected alternative — system stack only (no web fonts):** cheaper, but the Apex
`design.md` spec pins Inter + JetBrains Mono as part of the brand ("technical
precision," "engineered aesthetic"). The system fallbacks (San Francisco / Segoe UI /
Roboto) are visually close to Inter but are not Inter, and there is no system
equivalent of JetBrains Mono's character for the "technical label" role. The cost
(~94 KB, loaded once with `font-display: swap` so text paints immediately in the
fallback and reflows minimally on swap) is acceptable for a dashboard app.

### Theme file layout & switching (2026-09-01)

`internal/gateway/static/themes/` holds one shared file plus one file per palette:

| File | Owns |
|---|---|
| `_shared.css` | The four `@font-face` blocks, the `[data-theme]` font tokens, the battery-level scale (`--color-battery-*` + the `.text-battery-*` utilities), the `.divider` reset. Imported **first** by `input.css`. |
| `apex.css` | The apex palette only — one `@plugin` block. Carries `default: true`. |
| `graphite.css` | The graphite palette only — one `@plugin` block. No `default`. |

A theme file contributes **only** `--color-*` / radius / size tokens. Anything shared
belongs in `_shared.css`, so adding a palette never duplicates the fonts or the battery
colours. Exactly one theme may carry `default: true`.

**The battery scale is deliberately NOT per-theme.** It is a *state* vocabulary — red
(0–10%), orange (11–20%), yellow (21–40%), green (41%+), consumed by
`pages.dashBatteryColorClass`. A palette changes what "action" looks like; it must not
change what "critically low" looks like. The corollary binds every new theme: **keep the
primary out of the red/orange/yellow/green band**, or one colour will mean two things.
Apex violates this (red primary collides with battery-low, and `error` #ffb4ab reads
calmer than a primary button); `graphite.css` exists as the accessible alternative and
documents the measured contrast per token.

**Switching:** all themes compile into `app.css`, so it is one `data-theme` attribute in
`templates/layouts/base.templ` (line 17) plus `make templ && make css` — never an
`@import` swap. Full steps live in the root `README.md` §"Switching the theme".

**Boundary — this is NOT an opening for arbitrary self-hosted fonts.** Like RD9/RD10,
it is a narrow, sanctioned decision (two families, four weights, pinned to the Apex
`design.md`), not a precedent. Adding a third family or more weights needs its own
RD entry per RD8, with its own rationale and rejected alternative. The Latin subset
only is shipped; add other subsets (cyrillic, etc.) only when a real page needs them —
do not front-load every subset. Fonts live under `static/fonts/` so the existing
`//go:embed static` picks them up with no embed directive change; never put a font
anywhere else.

## Client-side JS exception: date→time-preserving sync (RD12)

The **third** sanctioned exception to the zero-JS rule: a `change` listener on
`input[name="charged_on"]` in `static/app.js` that keeps the manual-charge forms'
`started_at`/`ended_at` time-of-day intact when the user edits the date. Added by
`RM33-gateway-update-charge-form` (tier 2 of `RM33-manual-record-status`, ticket
MAG-18, roadmap decision D12).

**What:** One `change` listener, delegated on `document.body`, matching
`input[name="charged_on"]`. On fire, it looks up `started_at`/`ended_at` within
`evt.target.closest("form")` and, for each that is **non-empty**, rewrites only the
date portion: `input.value = newDate + input.value.slice(10)`. A `datetime-local`
input's value is always `YYYY-MM-DDTHH:MM`, so `.slice(10)` is exactly `"THH:MM"` —
the time half is preserved verbatim. An empty `started_at`/`ended_at` is left empty;
the listener never auto-fills one (roadmap D12's explicit rejection of "clobber to
midnight"). Delegation on `document.body` (the same pattern RD9/RD10 use) means the
create form and any number of simultaneously-open inline edit rows all get the
behavior with no per-row re-binding when htmx swaps a row in.

**Why:** Both charge forms let the user set `charged_on` (the calendar day) alongside
`started_at`/`ended_at` (timestamps for the same day). Without this listener, editing
the date leaves the time fields pointing at the *old* date while displaying only a
time — an easy way to silently record a charge session on the wrong day. The
**rejected alternative** was a CSS-only DaisyUI pattern: rejected because this is a
value *transformation* (splicing one field's substring into another field's value),
which no CSS mechanism (`dropdown`/`<dialog>`/`collapse`) can express — those patterns
toggle presentational state, they cannot rewrite an input's value.

**Boundary — this is NOT an opening for general client-side JS.** Like RD9/RD10, it is
a narrow, sanctioned exception (one listener, one value-splice rule, two named target
fields), not a precedent. Any further client-side JS needs its own RD entry per RD8,
with its own rationale and rejected alternative.

**Graceful degradation:** if `started_at`/`ended_at` are empty, or the changed input
isn't inside a `<form>`, the listener no-ops — no error, no partial write. Without JS
entirely, the fields simply keep whatever the user last typed; the browser still
accepts a mismatched date/time pair (a minor UX regression, not a data-integrity
issue — the server does not derive one field from the other).

## Client-side JS exception: status-driven required toggle (RD13)

The **fourth** sanctioned exception to the zero-JS rule: a `change` + `htmx:load`
listener pair on `select[name="status"]` in `static/app.js` that toggles
`ended_at`/`end_battery_pct`'s `required` attribute live, with no htmx round-trip.
Added by `RM33-gateway-update-charge-form` (tier 2 of `RM33-manual-record-status`,
ticket MAG-18, roadmap decision D-RM33-6).

**What:** A `change` listener, delegated on `document.body`, matching
`select[name="status"]`, plus an `htmx:load` listener that re-applies the same logic
to every `select[name="status"]` present in the loaded/swapped content (covers the
initial full-page load and every htmx-swapped fragment, e.g. a freshly-opened inline
edit row, with no separate `DOMContentLoaded` handler). Both call one helper,
`applyChargeStatusRequiredToggle(select)`, which resolves `select.closest("form")` and
sets `ended_at.required` / `end_battery_pct.required` to `select.value === "DONE"`.
Running on `htmx:load` as well as `change` means a freshly-rendered or
freshly-swapped form is always correct immediately, not just after the user's first
interaction with the dropdown — the literal ask in D-RM33-6 ("must run on page load as
well as on change"). This deliberately duplicates the server-rendered initial
`required` state computed from `charging.RequiredFieldsFor` (design.md §D-Fields,
`RM33-gateway-update-charge-form`): if the two ever disagree, the JS state wins in the
live DOM after it runs, and the disagreement is inert — never something that can only
be fixed by special-casing the template.

**Why:** D-RM33-6 explicitly asks for "no htmx round-trip" — the user must see the
required asterisk change the instant they pick `DONE`, before they've filled in
anything else. The **rejected alternative** was relying solely on the server-rendered
initial `required` state and letting a status change take effect only after a full
submit/re-render round-trip: rejected because that is exactly the round-trip
D-RM33-6 asks to avoid.

**Why it does not erode the `ui/` boundary:** the listener only ever reads
`select.value` and writes a native DOM `.required` boolean — no DaisyUI class string,
no markup, no styling decision is made in JavaScript. The `ui/` kit's ownership of
component classes is untouched.

**Boundary — this is NOT an opening for general client-side JS.** Like RD9/RD10/RD12,
it is a narrow, sanctioned exception (one delegated listener pair, one boolean
toggle, two named target fields), not a precedent. Any further client-side JS needs
its own RD entry per RD8, with its own rationale and rejected alternative.

**Graceful degradation:** if a matched `<select>` has no enclosing `<form>`, or the
form has no `ended_at`/`end_battery_pct` input, the helper no-ops on the missing
piece (`if (endedAt) endedAt.required = isDone`). Without JS entirely, the
server-rendered initial `required` state from §D-Fields still governs at submit time — the
fields simply stop updating live on a status change, falling back to correctness
only on the next full page render rather than instantly.

## Client-side JS exception: location-kind-driven label toggle (RD14)

The **fifth** sanctioned exception to the zero-JS rule: a `change` + `htmx:load`
listener pair on `select[name="location_kind"]` in `static/app.js` that toggles
`input[name="location_label"]`'s `disabled` attribute live, with no htmx
round-trip. Added 2026-09-01, alongside `location_label`'s move from the
optional-details section into the main grid (same date).

**What:** A `change` listener, delegated on `document.body`, matching
`select[name="location_kind"]`, plus an `htmx:load` listener that re-applies the
same logic to every `select[name="location_kind"]` present in the loaded/swapped
content — the exact shape RD13 uses one section up. Both call one helper,
`applyChargeLocationLabelToggle(select)`, which resolves `select.closest("form")`
and sets `location_label.disabled = select.value !== "OTHER"`. The server
renders the same initial state (`Disabled: LocationKind != "OTHER"` on
`ui.InputProps` in both `charge_create_form.templ` and `charge_row_edit.templ`),
pinned by `TestChargeForms_LocationLabelDisabledUnlessOther`
(`handlers/charges_form_layout_test.go`); if the two ever disagree, the JS state
wins in the live DOM, and the disagreement is inert — the same deliberate
duplication RD13 makes. The `change` path additionally **focuses** the input
the moment it is enabled (picking OTHER is the only path that enables it, and
the user's next action is typing into it); the `htmx:load` path deliberately
does **not** focus — a page load or row swap must never steal focus from where
the user already is.

**Why:** the free-text label is only meaningful when the kind is `OTHER`; a
live text field next to HOME/WORK invites noise data. The **rejected
alternative** was the server-only `disabled` attribute with no round-trip: on
the create form the select change never re-renders, so the input could never be
enabled at all. The second rejected alternative was an htmx round-trip on
select change (re-render the form so the server recomputes `disabled`): rejected
for the same reason RD13 rejected it — a full form re-render on every select
change, plus new wiring to preserve the user's typed values.

**Consequence (deliberate):** a `disabled` input is **not submitted**. If a user
types a label and then switches the kind to HOME/WORK, the save silently drops
the label — correct by definition, since the label carries no meaning for those
kinds. Do not "fix" this by hiding the value without disabling; the drop is the
feature.

**Why it does not erode the `ui/` boundary:** the listener only ever reads
`select.value` and writes a native DOM `.disabled` boolean — no DaisyUI class
string, no markup, no styling decision is made in JavaScript.

**Boundary — this is NOT an opening for general client-side JS.** Like
RD9–RD13, it is a narrow, sanctioned exception (one delegated listener pair, one
boolean toggle, one named target field), not a precedent. Any further
client-side JS needs its own RD entry per RD8.

**Graceful degradation:** if a matched `<select>` has no enclosing `<form>`, or
the form has no `location_label` input, the helper no-ops on the missing piece
(`if (label) label.disabled = ...`). Without JS entirely, the server-rendered
initial `disabled` state still governs — the input simply stops toggling live on
a kind change, so a user on the create form must rely on the next full render
(a degradation RD13 shares, not a data-integrity issue: a label typed for
HOME/WORK is dropped at save either way).

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
