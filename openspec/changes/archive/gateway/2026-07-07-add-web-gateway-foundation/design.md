## Context

The gateway is the UI boundary described in `ai/architecture.md` §2 and the htmx contract in
`ai/htmx-conventions.md` / `ai/htmx-go-integration.md`: the only place HTML lives, a translator
that calls domain-module interfaces in-process and streams back HTML fragments. This change
builds the empty shell of that boundary so later tiers add routes/pages without re-deciding the
plumbing. All four foundational choices below were settled by grilling the design.

## Goals / Non-Goals

**Goals**
- A runnable web server (`cmd/web`) serving a Templ-rendered landing page and an htmx fragment.
- Cookie-based sessions wired on all routes (foundation for Tier 3 login).
- Pinned htmx embedded in the binary; self-contained deploy.
- Prove the full path once — Gin → Templ page + fragment → `account`/`pgxpool` wiring → DB ping.

**Non-Goals**
- Any authentication / Google login (Tier 3) or Tesla data (Tiers 4–5).
- A real dashboard or domain fragments beyond the health demo.
- Replacing `internal/server` (the `cmd/setup` OAuth callback) — left as-is.

## Decisions

### Router: Gin (chosen, not throwaway)
Gin (already in `go.mod`) becomes the gateway router. Handlers take `*gin.Context`; Templ
components render through a small helper (`component.Render(c.Request.Context(), c.Writer)` with
`Content-Type: text/html`), plus a fragment variant using `templ.WithFragments(name)`. This
**supersedes** the `ai/architecture.md` note calling the Gin server a throwaway smoke test — that
doc is updated in this change. (The htmx-integration examples use bare `net/http`; we adapt the
same pattern to `gin.Context`.)

### Sessions: gin-contrib/sessions, cookie store
Signed + AES-encrypted cookie via a `SESSION_SECRET`. **Stateless** — no server-side session
storage — so sessions survive restarts and scale across instances. A small session payload
(a `uid` in Tier 3) fits comfortably. Trade-off: no server-side revocation; if we need it later
we swap in a Postgres-backed store (a table + adapter). Rotating `SESSION_SECRET` invalidates all
sessions. *(Documented in README → "Sessions & staying logged in".)*

### Static assets: vendored + embedded (go:embed)
A pinned `htmx.min.js` is committed under `internal/gateway/static/` and embedded with `go:embed`,
served at `/static`. Single self-contained binary, offline-capable, no runtime CDN dependency —
matching the `make bins` / deployment ethos. The exact htmx version is pinned when vendored.

### Scope: a skeleton that proves the stack
Routes: `GET /` (Templ base layout + landing with an htmx region), `GET /ui/health` (the health
**fragment** only), `GET /healthz` (200 + `pool.Ping`), `GET /static/*`. Session middleware is
global. To make sessions observably work in the foundation (not just wired), the landing handler
keeps a tiny per-session **visit counter** — a deliberate demonstration, removable once Tier 3
puts real data in the session.

## Request flow (foundation)

```
browser GET /            -> gateway Home handler -> render pages.Home() (Base layout) -> HTML
browser (htmx) GET /ui/health -> gateway HealthFragment -> pool.Ping -> render fragments.Health(status) -> HTML fragment
ops GET /healthz         -> gateway Healthz -> pool.Ping -> 200 / 503
```

No domain-module data crosses yet beyond the `pgxpool` health check; Tier 5 adds the
`account → tesla → domain` chain per `ai/htmx-go-integration.md`.

## Risks / Trade-offs

- **Gin vs the net/http examples in ai/*.md** — mitigated by a single render helper that isolates
  the Templ↔Gin adaptation; conventions stay valid, only the `http.ResponseWriter` mechanics differ.
- **Cookie size / secrecy** — small payloads only; `SESSION_SECRET` must be a strong secret and
  stay stable (documented).
- **Templ codegen drift** — `*_templ.go` must be regenerated after editing `.templ`; `templ
  generate` is an explicit step (like `sqlc generate`).

## Open Questions

- **CSS/styling approach** (Tailwind via CDN/build vs plain CSS) — deferred; the foundation ships
  minimal/unstyled markup.
- **CSRF protection** for future POST routes (login/connect) — decide in Tier 3 when the first
  form/POST appears.
