## Why

Tier 1 gave us an `account` module that can provision users and hold Tesla tokens, but there is
no **web surface** — nothing a person can open in a browser. The platform's goal is per-user
htmx dashboards, and every user-facing tier ahead (Google login, Tesla connect, vehicle
dashboard) needs a running web gateway to hang routes on. This change stands up that gateway
**skeleton**: the single presentation layer (`ai/architecture.md` §2) where all HTML lives —
routing, a Templ base layout, cookie sessions, embedded static assets, and graceful shutdown —
proven end-to-end but with **no authentication yet**.

## What Changes

- Add **`cmd/web`** — the web server entrypoint (thin: wires config, pool, services, router,
  shutdown).
- Add **`internal/gateway/`** — the UI gateway: `handlers/` (Gin) + `templates/` (Templ
  `layouts/`, `pages/`, `fragments/`) + embedded `static/`.
- **Router: Gin.** It graduates from the throwaway smoke-test server to the *chosen* gateway
  router. Templ components render to `gin.Context` via a small render helper.
- **Cookie-based sessions** via `gin-contrib/sessions` — signed + AES-encrypted with a
  `SESSION_SECRET`, stateless, so sessions survive server restarts.
- **Vendored, embedded (`go:embed`), pinned htmx** served at `/static`.
- **Foundation routes that prove the whole stack once:**
  - `GET /` — landing page from the Templ base layout, with a small htmx-driven region.
  - `GET /ui/health` — an htmx **fragment** reflecting database connectivity.
  - `GET /healthz` — liveness/readiness (200 + `pool.Ping`).
  - `GET /static/*` — embedded assets.
- **Config:** `PORT` and `SESSION_SECRET` added to `internal/config`.
- **No** authentication, Tesla calls, or dashboards yet (Tiers 3–5).

## Capabilities

### New Capabilities
- `gateway`: the web presentation layer — serving HTML pages and htmx fragments over HTTP with
  cookie-based sessions and embedded static assets. The single place HTML lives and the host for
  all future `/ui` routes.

### Modified Capabilities
- None (first `gateway` spec).

## Impact

- **New:** `cmd/web`, `internal/gateway/` (handlers + Templ templates + embedded static).
- **New deps:** `github.com/a-h/templ`, `github.com/gin-contrib/sessions` (Gin already present).
  **New codegen:** `templ generate` — an explicit dev step, like `sqlc generate`.
- **Config:** `PORT`, `SESSION_SECRET` (+ `.env.example`, `docs/deployment.md`).
- **Architecture:** promotes **Gin to the chosen gateway router**, superseding the "Gin server is
  a throwaway smoke test" note in `ai/architecture.md` (updated as part of this change). The
  existing `internal/server` (OAuth callback used by `cmd/setup`) is untouched.
- **Non-breaking** — adds a new web surface; no existing package behavior changes.
