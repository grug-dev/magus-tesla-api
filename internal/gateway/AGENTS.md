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

## Boundaries

- Calls other modules ONLY through their public interfaces — never a database, never
  another module's internals. If a handler "needs" SQL, the design is wrong: add a
  method to the owning module instead.
- Templates live in `templates/{layouts,pages,fragments}` (Templ); static assets are
  embedded via `go:embed`. Map module DTOs to gateway view models — never leak
  `...Tesla`-suffixed DTOs into templates.
- Handlers stay thin: session/auth check → call an interface → render. Testable logic
  goes in helper funcs driven through interface fakes (see `handlers/`).

## Read-only at request time

The gateway is **read-only on every user-facing request**. This is both a tenancy
safety rule and a read-optimization principle (see [`ai/architecture.md`](../../ai/architecture.md)
§7): the hot path (user → DB read → HTML) stays cheap and predictable.

- Handlers only call **`Reader` ports** (e.g. `account.RegisteredVehicles`,
  `telemetry.Reader.LatestSnapshotsByAccount`). Never call `Collector` or
  `Writer` ports from a handler.
- No writes, no Tesla API calls, no side effects on user requests. The only
  user-initiated Tesla API call is listing vehicles on first Tesla connect
  (one-time seed), and even that happens through the account module's interface —
  not the gateway calling Tesla directly.
- All writes (telemetry collection, summary computation, token rotation) happen in
  the nightly batch (`telemetry.Collector`) or inside `account.Service` methods
  called from non-gateway paths — never from a gateway handler.

## Testing

- `httptest` against `NewEngine` with fakes for the `Deps` interfaces — the existing
  suite covers auth guards, CSRF state, empty/error states, and fragment rendering.
  New handlers follow that pattern.

## How to add or modify a page

Use this recipe whenever a task asks you to add, modify, or extend an HTML page or
region in the gateway. It tells you which files to touch and in what order. For the
*why* behind each rule, see [`ai/htmx-conventions.md`](../../ai/htmx-conventions.md) and
[`ai/htmx-go-integration.md`](../../ai/htmx-go-integration.md).

### Decide first: does the markup need data the handler doesn't already have?

#### No — pure markup, or data already on the view model

1. Edit `internal/gateway/templates/pages/<name>.templ` (page shell) and/or
   `internal/gateway/templates/fragments/<region>.templ` (swap region).
2. Run `templ generate` (regenerates `*_templ.go`).
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
8. **Template** — `internal/gateway/templates/pages/<name>.templ`:

   ```templ
   @layouts.Base("<title>") {
       <main>
           <h1>...</h1>
           <button hx-get="/ui/<region>" hx-target="#<region>" hx-swap="outerHTML">
               Refresh
           </button>
           @templ.Fragment("<region>") {
               @fragments.ViewRegion(d)
           }
       </main>
   }
   ```
9. **Regenerate** — `templ generate` (always after any `.templ` edit).
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
| Pages (`templates/pages`) | The only place HTML skeletons live. Uses `layouts.Base` + `@templ.Fragment` blocks + `hx-*` button attributes. |
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
| `internal/<module>/db/queries.sql` | `sqlc generate` | `db/*.go` (generated) |
| `*.templ` | `templ generate` | `*_templ.go` (generated) |
| `*.go` | `go build` / `go test` | nothing else |

Never hand-edit generated files (`db/*.go`, `*_templ.go`).
