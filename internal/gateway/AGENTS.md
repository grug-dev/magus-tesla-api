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
- `Deps.TelemetryReader telemetry.Reader` — the telemetry read port; injected at
  construction via `gateway.Deps` and `handlers.Deps`. The gateway calls
  `LatestSnapshotsByAccount(ctx, accountID)` once per dashboard render to populate
  vehicle card telemetry. Added by `gateway-read-stored-vehicles` (tier 5).
  NEVER import `internal/telemetry/db` (`telemetrydb`) — all access through this
  interface only.
- `Deps.ManualChargeWriter manualcharge.Writer` — the manual charge write port; injected
  at construction. Called ONLY by the write handlers (ChargeCreate, ChargeRowUpdate,
  ChargeRowDelete) on explicit user-initiated form submissions. See "Exception:
  user-initiated writes" below. NEVER import `internal/manualcharge/db` — all access
  through this interface only.
- `Deps.ManualChargeReader manualcharge.Reader` — the manual charge read port; injected
  at construction. Called by read handlers (ChargePage, ChargesListFragment,
  ChargeRowStatic, ChargeRowEditFragment) and the `buildChargesPage` helper to list
  charge entries. NEVER import `internal/manualcharge/db` — all access through this
  interface only.
- `Deps.SuperchargerReader telemetry.SuperchargerReader` — the telemetry
  Supercharger-sessions read port; injected at construction via
  `gateway.Deps`/`handlers.Deps` (wired from `cmd/web` via
  `telemetry.NewSuperchargerReader(pool)`). Called by `SuperchargerStatsPage` /
  `SuperchargerStatsFragment` (via `superchargerStatsViewFor` /
  `buildSuperchargerStatsView`) — ONE `SuperchargerSessionsByVehicle` read per
  Supercharger Stats render, capped at `superchargerReadLimit` (500 rows). Added by
  `gateway-add-supercharger-stats`. NEVER import `internal/telemetry/db`
  (`telemetrydb`) — all access through this interface only.

## Boundaries

- Calls other modules ONLY through their public interfaces — never a database, never
  another module's internals. If a handler "needs" SQL, the design is wrong: add a
  method to the owning module instead.
- Templates live in `templates/{layouts,pages,fragments}` (Templ); static assets are
  embedded via `go:embed`. Map module DTOs to gateway view models — never leak
  `...Tesla`-suffixed DTOs into templates.
- Handlers stay thin: session/auth check → call an interface → render. Testable logic
  goes in helper funcs driven through interface fakes (see `handlers/`).

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
- **Compose the `ui/` kit** (Card, StatTile, Button, Alert, Badge, Table, PageHeader, NavShell,
  ConfirmDialog, and the form set **Field / Input / Select / Textarea**) — **never inline a DaisyUI component
  class** (`btn`, `input`, `card`, `fieldset`, …) in a page/fragment; that's a bug. If a
  repeated element has no wrapper, **add one to `ui/`** instead of inlining. Theme tokens
  (`text-error`, `bg-base-100`) and Tailwind layout utilities stay inline — the stable layers.
  Pages/fragments pass VM-ready strings in.
- **Semantic tokens only — never hex / raw palette** (`bg-base-100`, `primary`,
  `success`; not `#fff` / `bg-red-500`). The app re-skins from one `<html data-theme>`
  (default `lemonade`; `dark` auto-applies via `prefers-color-scheme`).
- **No client-side JS init** — keeps htmx swaps safe. Prefer CSS-only DaisyUI patterns
  (`<dialog>` modal, `dropdown`, `collapse`, `tabs`) over any JS. There are exactly **two**
  standing exceptions, each with its own recorded decision below: **RD9** (the `browser_tz`
  cookie script in `layouts.BaseAuth`) and **RD10** (`ui.ConfirmDialog`, whose JS lives in
  the shared `static/app.js`). Adding a third needs its own RD entry per RD8.
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
  `telemetry.Reader.LatestSnapshotsByAccount`). Never call `Collector` or
  `Writer` ports from a handler, **except as documented below**.
- No writes, no Tesla API calls, no side effects on user requests. The only
  user-initiated Tesla API call is listing vehicles on first Tesla connect
  (one-time seed), and even that happens through the account module's interface —
  not the gateway calling Tesla directly.
- All writes (telemetry collection, summary computation, token rotation) happen in
  the nightly batch (`telemetry.Collector`) or inside `account.Service` methods
  called from non-gateway paths — never from a gateway handler.

### Exception: user-initiated writes (D4 amendment — RM3-gateway-add-manual-charge-ui)

The gateway MAY call `manualcharge.Writer` (Create / Update / Delete) on explicit
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
4. **Only `manualcharge.Writer` is permitted** — this is the narrow aperture.
   This amendment does NOT open general write access to the gateway; Reader-only
   remains the default for ALL other handlers (dashboard, telemetry fragments,
   health, OAuth, etc.).

**Rationale:** Manual charge entry is a different class of request from dashboard
reads: the user explicitly fills a form and submits it. Denying all writes at the
gateway layer would force an external HTTP API that the browser would then need to
call — an unnecessary layer when the gateway is already the only HTML surface.
The write is intentional (form POST), narrow (one module's Writer port),
CSRF-protected, and tenant-scoped.

### Exception: language switch (D-lang amendment — RM24-gateway-add-i18n-foundation)

The gateway MAY call `account.Service.SetLanguage` from `handlers.LangSwitch`
(`POST /ui/lang/switch`), subject to a **different** set of constraints than the
D4/manualcharge amendment above — it does not transplant cleanly, because this
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

## Vehicle-scoped reads — always send the selected TeslaID

The gateway is multi-tenant **and** multi-vehicle: the user picks the active vehicle with
the sidebar switcher (nav-header `<select>` → `POST /ui/vehicle/select`, persisted in the
session by `setCurrentVehicle`). **Every handler that fetches or filters PER-VEHICLE data
MUST scope that read to the SELECTED vehicle**, identified by its **`TeslaID`** (`int64` —
Tesla's numeric vehicle `id`, `account.Vehicle.TeslaID`). This is a tenancy-correctness rule:
a read that ignores the selection silently shows a *different* car's data.

1. **Resolve once, pass the TeslaID down.** Call `h.resolveSelectedVehicle(ctx, c, uid)` (it
   auto-selects the first OWNER when the session has none) and hand its `.TeslaID` to the
   module port — e.g. filter `manualcharge.Reader.ListEntriesByVehicle(ctx, uid, teslaID, …)`,
   pick the snapshot for that TeslaID out of `telemetry.Reader.LatestSnapshotsByAccount`, or
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
- **Contract** (reference implementation: `GET /ui/dashboard/history`,
  `RM8-gateway-history-date-range` / Linear MAG-7):
  - Parse via a single `parseHistoryRange`-style helper (`internal/gateway/handlers/history.go`)
    returning `(start, end time.Time, ok bool)`.
  - Default when both `start` and `end` are absent: endpoint-specific (dashboard history default
    6-day window → `today-6 .. today`).
  - Reject with HTTP **400** on any of: malformed non-ISO date, only one of `start`/`end`
    present, `end.Before(start)`, `end.After(startOfDay(now))`, or a window wider than **90 days**
    (hard cap against unbounded range scans — see `historyRangeMaxDays`).
  - On 400, render the empty-state placeholder (`dashHistoryEmpty`), **do not** call the read
    port, and return no preset selector — a malformed request gets no chrome.
  - The caller may fetch a bounded extra lookback (e.g. the dashboard's 1-day pre-window for the
    first odometer delta) by passing `start-1day` to the owning module's bounded read port — the
    lookback is a **gateway concern**, never a parameter on the owning module's port method.
- **Every future date-filtered gateway endpoint follows the same contract** — a closed
  vocabulary of one: `?start=&end=`. A new endpoint that needs date filtering reuses the
  `parseHistoryRange` pattern and a bounded read port on the owning module.
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
exactly **TWO** sanctioned exceptions: this one and **RD10** (the confirmation
modal) below. This entry covers the first: a single inline `<script>` in
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
server falls back to `time.UTC` (`browserLocation`'s fallback rule) — no
error surfaces to the user and no page render breaks.

## Client-side JS exception: confirmation modal (RD10)

The **second** (and currently last) sanctioned exception to the zero-JS rule: the
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
