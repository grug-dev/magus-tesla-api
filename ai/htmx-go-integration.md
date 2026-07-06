# htmx ↔ Go API Integration — magus-tesla-api

> **Status: TBD — stub.** There is no web layer or browser-facing Go API in the repo
> yet. Populate this file when the htmx frontend and its serving endpoints land.
> Referenced from `CLAUDE.md`.

This is a **design/integration reference**, not a code-style file. It describes *how the
pieces interact* — the contract between htmx fragments in the browser and the Go/Gin
endpoints that serve them. Sibling to [`go-conventions.md`](./go-conventions.md) and
[`htmx-conventions.md`](./htmx-conventions.md).

---

## Intended scope (what belongs here once the web layer exists)

- **Partial-template contract** — which Go endpoints return HTML fragments vs full pages, and how the server decides (e.g. `HX-Request` header handling).
- **`hx-*` request patterns** — the standard shape of htmx requests the backend expects (triggers, targets, swap strategies) and what each endpoint assumes.
- **Endpoint → fragment mapping** — the convention linking a Go route (likely under a new `cmd/api` / `internal/web` package) to the template fragment it renders.
- **Response format** — content type, status codes, and htmx response headers (`HX-Redirect`, `HX-Trigger`, `HX-Retarget`, etc.) the backend is allowed to emit.
- **Error handling across the boundary** — how backend errors (including the `vehicle.ErrUnauthorized` / 401 flow from `go-conventions.md`) surface as htmx-friendly responses.
- **Where the web layer lives** — package placement, keeping the modular-monolith rule (one concern per package) intact for the HTTP/rendering concern.

---

_No integration exists yet. Do not treat this file as active guidance until it is populated._
