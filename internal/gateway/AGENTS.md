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

## Boundaries

- Calls other modules ONLY through their public interfaces — never a database, never
  another module's internals. If a handler "needs" SQL, the design is wrong: add a
  method to the owning module instead.
- Templates live in `templates/{layouts,pages,fragments}` (Templ); static assets are
  embedded via `go:embed`. Map module DTOs to gateway view models — never leak
  `...Tesla`-suffixed DTOs into templates.
- Handlers stay thin: session/auth check → call an interface → render. Testable logic
  goes in helper funcs driven through interface fakes (see `handlers/`).

## Testing

- `httptest` against `NewEngine` with fakes for the `Deps` interfaces — the existing
  suite covers auth guards, CSRF state, empty/error states, and fragment rendering.
  New handlers follow that pattern.
