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
  and the form set **Field / Input / Select / Textarea**) — **never inline a DaisyUI component
  class** (`btn`, `input`, `card`, `fieldset`, …) in a page/fragment; that's a bug. If a
  repeated element has no wrapper, **add one to `ui/`** instead of inlining. Theme tokens
  (`text-error`, `bg-base-100`) and Tailwind layout utilities stay inline — the stable layers.
  Pages/fragments pass VM-ready strings in.
- **Semantic tokens only — never hex / raw palette** (`bg-base-100`, `primary`,
  `success`; not `#fff` / `bg-red-500`). The app re-skins from one `<html data-theme>`
  (default `lemonade`; `dark` auto-applies via `prefers-color-scheme`).
- **No client-side JS init** — keeps htmx swaps safe. Prefer CSS-only DaisyUI patterns
  (`<dialog>` modal, `dropdown`, `collapse`, `tabs`) over any JS.
- **Codegen:** after `.templ` edits or new classes, run `make templ` **and** `make css`
  (`make generate` runs both). `static/app.css` is a committed vendored artifact (like
  `htmx.min.js`); the Tailwind binary in `tools/` is git-ignored (`make ui-toolchain`).
- **New pages go through `kkpa-goth-scaffold-ui scaffold <concept> [module]`**, which
  mirrors the `charges` gold-standard slice.

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
