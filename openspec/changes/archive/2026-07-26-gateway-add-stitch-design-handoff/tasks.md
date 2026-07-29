# Tasks — gateway-add-stitch-design-handoff

Module: `internal/gateway/`. Artifacts step (Step 3); these tasks are
executed in the APPLY stage. Each task is atomic, testable, and tagged
with `parallel_ok` where it touches disjoint files from the others.
`depends_on` lists tasks that MUST complete first.

Acceptance for every task uses the existing `httptest` against
`gateway.NewEngine` with fakes for the `Deps` interfaces (the pattern in
`internal/gateway/handlers/handlers_test.go` /
`internal/gateway/gateway_test.go`). After any `.templ` edit, the apply
worker runs `make templ` + `make css` (or `make generate`) and commits
`static/app.css` in the same change. No `make sqlc` — no DB change
(design.md §4). `CLAUDE.md` authorizes the assistant to run
`go build`/`go vet`/`go test` and `make generate`/`make css` for this
project.

---

## T1. Strip `home.templ` debug bits + dead health fragment

`parallel_ok`: YES (touches `home.templ`, `handlers.go` Home/HealthFragment/health,
`gateway.go` `/ui/health` route, `fragments/health.templ`, and the two
tests — disjoint from T2/T3/T5/T6).
`depends_on`: none.

### Work

1. `internal/gateway/templates/pages/home.templ`: remove `HomeView.VisitCount`
   and `HomeView.Health` fields; remove the "Visits this session" `<p>`,
   the "Check database" `<button hx-get="/ui/health">`, and the
   `@templ.Fragment("health") { @fragments.HealthCard(vm.Health) }` block.
   Keep `HomeView.SignedIn`, `HomeView.Email`, and the signed-in /
   anonymous content. Drop the `strconv` and `fragments` imports if now
   unused.
2. `internal/gateway/handlers/handlers.go`: in `Home`, drop the
   `visits` session read/increment/save and the `Health` field on the
   `HomeView` literal. Delete the `HealthFragment` handler and the
   `health()` helper.
3. `internal/gateway/gateway.go`: remove `r.GET("/ui/health", h.HealthFragment)`.
4. Delete `internal/gateway/templates/fragments/health.templ` (the
   `Health` struct + `HealthCard` component are now unused).
5. `internal/gateway/gateway_test.go`: remove `TestHealthFragment_*` and
   `TestSession_VisitCounterRoundTrips` (their subjects no longer exist).
6. `make templ` + `make css`; commit `static/app.css`.

### Acceptance

- `go build ./...` and `go vet ./internal/gateway/...` pass.
- `go test ./internal/gateway/...` passes (no test references the removed
  visit counter or health fragment).
- `GET /` no longer contains "Visits this session" or "Check database" or
  an `id="health"` region (httptest assertion on the body).
- `GET /ui/health` returns 404 (route not registered).
- The signed-in state (email, "View your vehicles", "Connect your Tesla",
  "Log out") and the anonymous "Sign in with Google" link still render
  (httptest with a session / without).

---

## T2. Redesign `login.templ` (visual re-skin, markup only)

`parallel_ok`: YES (touches only `templates/pages/login.templ`; the
handler/route are unchanged — disjoint from T1/T3/T4/T5/T6).
`depends_on`: none.

### Work

1. Rewrite `internal/gateway/templates/pages/login.templ` per design.md §2:
   - `@layouts.Base("Sign in — Magus")` shell (unchanged).
   - A login-page-local background gradient wrapper `<div>` (Tailwind
     layout utilities, semantic tokens — NEVER hex) positioned behind the
     card. Do NOT modify `layouts.Base` (the gradient is login-local).
   - A centered `<main>` with the branding block ("TESLA CORE" +
     "Precision Fleet Monitoring" tagline) as static text.
   - `@ui.Card(ui.CardProps{}) { ... }` containing a single
     `@ui.Button(ui.ButtonProps{Variant: "primary", Href: "/auth/google/login", Class: "w-full"})`
     whose children is the inline Google "G" SVG + the "Continue with
     Google" label. The Google SVG's brand fills (`#4285F4` etc.) are the
     ONE permitted hardcoded-color exception, with a comment naming it.
   - An optional footer `<footer>` with three null-href `<a>` (Privacy /
     Terms / Support) — static.
   - DROPPED (per D6/D12): email input, password input, "Sign In" button,
     "OR" divider, external `googleusercontent.com` background image,
     ALL inline `<script>`.
2. The handler `LoginPage` and route `GET /login` are unchanged
   (`pages.Login()` takes no view model — pure static markup + one link).
3. `make templ` + `make css`; commit `static/app.css`.

### Acceptance

- `go build ./...` / `go vet` pass.
- `go test ./internal/gateway/...` passes; the existing login-page test
  (or a new one) asserts `GET /login` returns 200 and the body contains
  "Continue with Google" and links to `/auth/google/login`.
- `GET /login` body does NOT contain `type="email"`, `type="password"`,
  a standalone "Sign In" button, or "OR" as a divider, and no `<script>`
  tag, and no `googleusercontent.com` URL.
- No hardcoded hex appears in `login.templ` except the commented Google
  SVG brand fills.
- `pages.Login()` is zero-arg (no view model) — the handler makes no
  domain-module port call to render the login page.

---

## T3. Evolve `ui.NavShell` + add `ui.Icon` + nav-item model

`parallel_ok`: YES (touches `ui/nav_shell.templ`, new `ui/icon.templ`,
`layouts/nav.go`, `layouts/base.templ` for the `#nav-header` placeholder
shell — disjoint from T1/T2/T5/T6; T4 depends on this).
`depends_on`: none.

### Work

1. `internal/gateway/templates/ui/nav_shell.templ`: extend
   `NavItem` with `Icon string` (glyph name for `ui.Icon`) and
   `Placeholder bool`. Render each item as
   `<li><a ...><span class="…icon">…</span><span>Label</span>[Soon
   badge]</a></li>`, with `menu-active` when `Active`. When
   `Placeholder == true`, set `Href="#"` and append a "Soon"
   `ui.Badge` (`Variant: "warning"` or `ghost`). Keep the `menu`
   component class owned inside `NavShell` (no inline raw DaisyUI in
   pages).
2. New `internal/gateway/templates/ui/icon.templ`: `ui.Icon(p
   IconProps{Name string, Class string})` backed by a closed
   `map[string]` (or `switch`) of inline SVG path data. Initial glyph
   set: `dashboard`, `ev_station`, `analytics`, `settings`, `menu`
   (hamburger), and `battery` (nav header). Each glyph is a small inline
   `<svg>` with `currentColor` fill (so semantic tokens drive its color).
3. `internal/gateway/templates/layouts/nav.go`: change `navItems()` to
   `navItems(active string) []ui.NavItem` and return the four entries
   (Dashboard `/dashboard`, Manual Records `/charges`, Supercharger
   Stats placeholder, Settings placeholder) with `Active` set by
   comparing each `Href` to `active`. Pass glyph names.
4. `internal/gateway/templates/layouts/base.templ`: thread a `path`
   parameter into `BaseAuth(title, path string)` so `navItems(path)` can
   light the active item, and render the
   `<div id="nav-header" hx-get="/ui/nav-header" hx-trigger="load"
   hx-swap="outerHTML">…placeholder…</div>` slot ABOVE the
   `ui.NavShell` list inside the drawer side. Update all existing
   `BaseAuth` call sites (`pages/dashboard.templ`, `pages/charges.templ`)
   to pass their path. (If threading the path proves wide, a templ
   helper `@layouts.BaseAuthFor(title, path)` is acceptable — keep it
   in `base.templ`.)
5. `make templ` + `make css`; commit `static/app.css`.

### Acceptance

- `go build ./...` / `go vet` pass; existing dashboard / charges page
  tests still pass (they now pass a path to `BaseAuth`).
- `ui.NavShell` renders an icon `<svg>` per item and a "Soon" badge only
  on placeholder items (httptest / template unit assertion).
- No icon is loaded from an external CDN; no Material Symbols stylesheet
  link is added anywhere.
- No raw DaisyUI `menu`/`badge` class is inlined in a page or fragment —
  only inside `ui.NavShell` / `ui.Badge`.

---

## T4. Nav-header fragment handler + view model + freshness logic

`parallel_ok`: NO (depends on T3; touches `handlers.go`, `gateway.go`).
`depends_on`: T3.

### Work

1. `internal/gateway/handlers/handlers.go`: add a `navHeaderFor(ctx,
   uid) NavHeaderVM` gin-free helper (unit-testable with
   `account.Service` + `telemetry.Reader` fakes) that:
   - calls `h.acct.RegisteredVehicles(ctx, uid)`; if empty → return a
     `NeedsConnect` VM; on error → return a degraded `Unavailable` VM;
   - picks the primary vehicle = first entry (DD4);
   - calls `h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)`; merges
     by `TeslaID`; on error → degrade (name only, "unavailable");
   - computes the connected state with a pure `connectedAt(capturedAt,
     now)` function against the new
     `const connectedFreshnessWindow = 48 * time.Hour` (DD3), distinct
     from the existing `stalenessThreshold` (36h);
   - pre-computes the relative "Last seen" label and the battery % string;
     the VM carries ONLY presentation-ready strings + a `StatusKind`
     enum (`"connected" | "asleep" | "awaiting" | "unavailable"`) for the
     template's `badge-success`/`badge-warning`/`badge-ghost` choice.
2. Add `Handler.NavHeaderFragment(c)`:
   - auth guard (`currentUID` → redirect `/login` if absent);
   - `navHeaderFor(c.Request.Context(), uid)`;
   - `renderFragment(c, 200, fragments.NavHeader(vm), "nav-header")`.
3. `internal/gateway/gateway.go`: register `r.GET("/ui/nav-header",
   h.NavHeaderFragment)`.
4. New `internal/gateway/templates/fragments/nav_header.templ`: the
   `NavHeaderVM` struct + `NavHeader(vm)` component, root
   `<div id="nav-header">`. Composes `ui.Badge` for the status dot +
   semantic tokens only; vehicle name as `<span>`; "Connect your Tesla"
   link when `NeedsConnect`. NO business logic in the template.
5. Tests: add `fakeAccount` + `fakeTelemetryReader` (mirror the existing
   `handlers_test.go` fakes) and cover: Connected (snapshot < 48h),
   Asleep (snapshot > 48h, relative label present), Awaiting (no
   snapshot), NeedsConnect (no vehicles), Unavailable (reader error),
   anonymous → redirect `/login`. Test the pure `connectedAt` at the
   exact boundary deterministically (pure function of (capturedAt, now),
   mirroring `isStale`).
6. `make templ` + `make css`; commit `static/app.css`.

### Acceptance

- `go build ./...` / `go vet` pass; `go test ./internal/gateway/...`
  passes including the new `NavHeaderFragment` cases.
- `GET /ui/nav-header` with a session returns 200 and HTML containing the
  vehicle name + a status dot (`badge-success` for connected,
  `badge-warning` for asleep, `badge-ghost` for awaiting/unavailable) +
  battery % when connected.
- `GET /ui/nav-header` without a session redirects to `/login`.
- `connectedFreshnessWindow` is a named const; no magic `48` inline.
- The handler imports no `internal/telemetry/db` or `internal/account/db`
  package; no `pgtype` in any gateway file.
- The template performs no time arithmetic and no method calls on domain
  types (the VM is presentation-ready strings + a `StatusKind`).

---

## T5. Reconcile `home.templ` `Home` handler after T1 (compile closure)

`parallel_ok`: NO (depends on T1; only ensures the `Home` handler's
`HomeView` literal compiles after the field removals and that no other
page references the dropped health fragment).
`depends_on`: T1.

### Work

1. Verify `handlers.Home` builds the `pages.HomeView` literal with only
   `SignedIn` and `Email` (the fields remaining after T1).
2. Grep `internal/gateway` for any lingering `fragments.Health`,
   `HealthCard`, `VisitCount`, `visits`, `/ui/health` reference and
   remove/adjust. If `fragments/health_templ.go` (generated) lingers after
   deleting `health.templ` + `make templ`, confirm `make templ` removed
   it.
3. `go build ./...` + `go vet ./internal/gateway/...` + `go test
   ./internal/gateway/...`.

### Acceptance

- No reference to `fragments.Health`, `HealthCard`, `VisitCount`, or
  `/ui/health` remains anywhere under `internal/gateway/`.
- `go build` / `go vet` / `go test ./internal/gateway/...` pass.

---

## T6. Backlog entries for Supercharger Stats + Settings (future work)

`parallel_ok`: YES (touches only `openspec/roadmaps/backlog.md`; disjoint
from all gateway-source tasks).
`depends_on`: none.

### Work

1. Append two entries to `openspec/roadmaps/backlog.md` under
   "**Pending to be picked up**" following that file's template
   (`## {{NUMBER}}. {{MODULE-NAME}} — {{TITLE}}` + `### PROPOSAL` +
   `### ORIGIN`):
   - "gateway — Supercharger Stats screen" (the placeholder nav item built
     in this change links to `#`; the real screen is future work).
   - "gateway — Settings page" (same).
   Number them continuing the existing sequence (the next free numbers
   after the current Pending items).
2. In each entry's `### PROPOSAL`, note the trigger ("pick up when the
   user wants the Supercharger Stats / Settings nav entries to become
   live pages") and that the placeholder link + "Soon" badge were added
   by `gateway-add-stitch-design-handoff`. In `### ORIGIN`, cite this
   change + grill D9.

### Acceptance

- `openspec/roadmaps/backlog.md` contains both new entries under
  "Pending to be picked up", each following the file's template
  (PROPOSAL + ORIGIN sections, a clear trigger).
- The two entries' numbers continue the existing sequence without
  collision.

---

## Apply-stage ordering (for the leader)

- **Parallel wave 1:** T1, T2, T3, T6 (all `parallel_ok`, disjoint files).
- **Then:** T4 (depends on T3), T5 (depends on T1).
- **Then:** `make generate` (templ + css), `go test ./internal/gateway/...`,
  and commit `static/app.css` together with the template edits.

No task touches a DB, a `sqlc` query, another module's internals, or
`progress.json` (the leader owns progress.json; the apply worker writes
only its task entries' status fields).