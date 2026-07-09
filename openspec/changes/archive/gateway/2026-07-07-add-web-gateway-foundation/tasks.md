# Tasks — add-web-gateway-foundation

> Web skeleton for the `gateway` domain. Builds/codegen are permitted for this project
> (CLAUDE.md → Builds project override), so the assistant runs `templ generate` / `go build`
> etc. directly. No auth or Tesla data (Tiers 3–5).

## 1. Dependencies & Templ setup
- [x] 1.1 Verified via Context7: `/a-h/templ` (`component.Render`, `templ.Handler`+`WithFragments`) and `gin-contrib/sessions` (`cookie.NewStore`, `Sessions`/`Default`)
- [x] 1.2 Added `github.com/a-h/templ` v0.3.1020 + `github.com/gin-contrib/sessions` v1.1.0; installed `templ` CLI v0.3.1020
- [x] 1.3 Vendored pinned `htmx.org@2.0.4` → `internal/gateway/static/htmx.min.js`

## 2. Config
- [x] 2.1 Added `PORT` (default 8080) + `SESSION_SECRET` to `internal/config`; documented in `.env.example` and `docs/deployment.md`

## 3. Templates (Templ)
- [x] 3.1 `templates/layouts/base.templ` — HTML shell + `<head>` loading `/static/htmx.min.js`, `{ children... }` slot
- [x] 3.2 `templates/pages/home.templ` — landing page composing Base + htmx region wrapped in `@templ.Fragment("health")`
- [x] 3.3 `templates/fragments/health.templ` — `Health` view model + `HealthCard` fragment
- [x] 3.4 Ran `templ generate` → `*_templ.go` for layouts/pages/fragments

## 4. Gateway handlers
- [x] 4.1 Render helpers (`render` / `renderFragment` via `templ.WithFragments`) → `*gin.Context`, `text/html`
- [x] 4.2 `Home` (`GET /`) — full page; per-session visit counter demonstrates the cookie session
- [x] 4.3 `HealthFragment` (`GET /ui/health`) — renders only the `health` fragment (`pool.Ping` → status)
- [x] 4.4 `Healthz` (`GET /healthz`) — 200 + `pool.Ping`, 503 on failure

## 5. Router, middleware, static, server
- [x] 5.1 `gateway.NewEngine` — `gin-contrib/sessions` cookie store (sha256-derived enc key), routes, embedded `/static` via `go:embed`
- [x] 5.2 `cmd/web/main.go` — loads config, `pgxpool`, `account.NewService`, builds engine, `http.Server` + signal-based graceful shutdown

## 6. Architecture reconciliation
- [x] 6.1 Updated `ai/architecture.md` §2: Gin is the chosen gateway router; `internal/server` clarified as the separate `cmd/setup` OAuth-callback smoke-test helper

## 7. Verification
- [x] 7.1 `templ generate` + `go build ./...` + `go vet ./...` green (go 1.26.4)
- [x] 7.2 `go test ./...` — 5 gateway tests pass: `/` renders layout+htmx; `/ui/health` returns fragment only (no shell); `/healthz` = 503 when DB down; `/static/htmx.min.js` served from embed; session visit-counter round-trips
- [x] 7.3 `openspec validate add-web-gateway-foundation --strict` passes
- [x] 7.4 Live smoke: `make bins` + ran `bin/web` against the `magus` DB — `/healthz`=200, `/` shows "Database: healthy" + visit counter, `/ui/health` fragment-only; graceful shutdown on SIGTERM confirmed
