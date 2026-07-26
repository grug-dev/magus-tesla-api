# Design — gateway-add-stitch-design-handoff

Module: `internal/gateway/`. This design turns the binding grill outcomes
(`progress.json` decisions **D1–D12**) into a concrete gateway-runtime plan:
the **Login re-skin** and the **Menu/navigation redesign**, plus the
`home.templ` debug cleanup. The Stitch MCP wiring and the
`ai/htmx-conventions.md` Stitch→`ui/`-kit translation section are
**already done** by the leader (Phase A, events 2026-07-26T10:13:57Z and
10:22:00Z) and are NOT re-done here.

This is an **artifacts-only** step (Step 3 of the dev-harness-pipeline): it
authors `design.md`, `specs/`, and `tasks.md`. No source is changed in this
step.

Design-level decisions in this file are numbered **DD1..DD11**. They are
SEPARATE from the grill outcomes `progress.json` D1–D12; each DD
cross-references the grill outcome it implements.

---

## 1. Overview

Two Stitch screens (read directly via the `stitch` MCP) are translated into
gateway templates composed from the `ui/` kit:

1. **Login** — Stitch screen `593dc9d2dea647808643aa29935151c3`
   ("Login - Tesla Core"). A centered glass-panel card ("TESLA CORE" /
   "Precision Fleet Monitoring") over a dark Tesla background. Per **D6**
   the redesign keeps the branding header + the "Continue with Google"
   button (wired to the EXISTING `/auth/google/login`) and drops the
   email/password inputs, the "Sign In" primary button, and the "OR"
   divider. Per **D12** the external `googleusercontent.com` background
   image and ALL inline `<script>` (parallax + focus-scale) are dropped.

2. **Menu / navigation** — the left `<nav>` of Stitch screen
   `db4901e044c54869972239f92534cedc` ("Dashboard - Tesla Core (Apex
   Theme)"). It carries a vehicle header ("Model S Plaid" + green dot +
   "Connected • 94%"), four nav items (Dashboard / Manual Records /
   Supercharger Stats / Settings with Material Symbols glyphs), and a
   "Wake Vehicle" button. Per **D7** the header reads the latest snapshot
   via the EXISTING `telemetry.Reader.LatestSnapshotsByAccount`; per **D8**
   the "Wake Vehicle" button is DROPPED; per **D9**/​**D10** the four nav
   items map to two live pages + two placeholders.

3. **`home.templ` debug cleanup** — per **D11** remove the "Visits this
   session" line and the "Check database" htmx health-fragment demo, plus
   the now-dead handler/route/fragment/tests.

**No auth model, DB object, module, or read path is created.** The login
flow is a visual re-skin of the existing Google login; the nav header
reuses the existing telemetry + account read ports.

### Stitch design system context (for reference, not committed)

Stitch project `1959646399633310328` ("Tesla Insights Dashboard", DESKTOP,
dark "Kinetic Precision" theme): primary `#E82127`, Inter headline font,
Geist label font, roundness 8. Per **D4** the `DESIGN.md → DaisyUI theme`
mapping is OUT OF SCOPE (deferred). This change renders the translated
markup in the app's CURRENT default theme (`lemonade` / `dark` via
`prefers-color-scheme`) using semantic tokens only — NEVER hex from the
Stitch export. The dark "feel" is preserved by the gradient background
(DD1) and the existing dark auto-theme, not by importing Stitch's palette.

---

## 2. Login redesign

### 2.1 What is kept vs dropped (DD9 — implements grill D6 + D12)

| Stitch element | Kept / Dropped | Why |
|---|---|---|
| Branding header "TESLA CORE" + "Precision Fleet Monitoring" | **Kept** (as static text) | Visual identity; pure markup, no behavior |
| Glass-panel card container | **Kept**, rebuilt as `ui.Card` with semantic tokens | Around the Google button |
| "Continue with Google" button + Google SVG | **Kept**, wired to `/auth/google/login` via `ui.Button` `Href` | The actual login affordance; reuses existing route |
| Footer (Privacy / Terms / Support) | **Kept as static, null-href links** | Cosmetic; no pages exist. May be dropped in apply if a reviewer prefers — design leaves them in |
| Email input | **DROPPED** (D6) | No password auth in this app |
| Password input + "Forgot?" link | **DROPPED** (D6) | Same |
| "Sign In" primary button | **DROPPED** (D6) | Same |
| "OR" divider | **DROPPED** (D6) | One button only |
| External `googleusercontent.com` background image | **DROPPED** (DD1/D12) | No external CDN / no hotlinked asset |
| Inline `<script>` (parallax + focus-scale) | **DROPPED** (D12) | Zero client-side JS convention |

The "Continue with Google" button is the SAME link the current `login.templ`
already provides to `/auth/google/login`; the redesign only changes the
visual presentation. The handler `LoginPage` and the route `GET /login`
are unchanged (D6: no new auth module, no new route, no DB change).

### 2.2 Background handling (DD1 — implements grill D12)

**Decision: a pure CSS radial gradient.** No embedded binary asset.

```
A dark, slightly-red-tinted layered gradient, e.g.:
  background: radial-gradient(... ) on <body> via a utility class composed in the layout
```

Rendered with **semantic tokens** (`bg-base-100`, `bg-base-300`, `primary`
tint) and Tailwind layout utilities only — NEVER a Stitch hex literal.

**Justification (recommendation stated in D12, adopted here):**

- **Zero asset cost** — no binary added to the embedded `static/` FS (the
  project ships `app.css` as the committed vendored artifact; an embedded
  photo would inflate the binary and need a license-safe source).
- **No external CDN / no hotlinked asset** — satisfies D12's
  no-external-CDN rule. Stitch's `googleusercontent.com` URL is ephemeral
  and would break.
- **Re-skin safe** — using semantic tokens means the Login re-skins with
  one `<html data-theme>` change, like every other page. A baked photo
  would not.
- **Keeps the dark "Kinetic Precision" feel** — the app's `dark` theme
  auto-applies via `prefers-color-scheme`; a dark gradient on `bg-base-100`
  reads as the same moody tone as Stitch's `#0A0A0A` background.

**Rejected alternative — embedded local asset:** would require sourcing a
license-clear Tesla photo, adding it to `internal/gateway/static/`, and
would not re-skin. The binary weight + non-reskinnable cost outweighs the
visual fidelity for a re-skin of a single page. Recorded as a future option
in `openspec/roadmaps/backlog.md` only if the user wants a photographic
background later.

### 2.3 Markup composition from the `ui/` kit

`login.templ` is rewritten to compose ONLY `ui/` wrappers + semantic
tokens + Tailwind layout utilities (no raw DaisyUI component class, no
hex):

- `layouts.Base("Sign in — Magus")` (the unauth shell — unchanged).
- A centered `<main>` (flex grid utilities) containing:
  - a branding block (`<h1>` "TESLA CORE" + tagline `<p>`) — static text,
    inline typography utilities only.
  - `ui.Card(ui.CardProps{Title: ""}) { ... }` holding the
    "Continue with Google" `ui.Button`:
    `ui.Button(ui.ButtonProps{Variant: "primary", Href: "/auth/google/login", Class: "w-full", Attrs: ...})`
    with the inline Google SVG as its children.
  - an optional footer `<footer>` with three null-href `<a>` (Privacy /
    Terms / Support) — static, no route needed.
- The background gradient lives in `layouts.Base` as an extra body class OR
  a wrapper `<div>` — applied ONLY to the login page via a param? **No**:
  `Base` is shared with `home`, so the gradient is added as a
  login-page-local wrapper `<div>` inside `login.templ`, NOT inside `Base`
  (change-locality: keeps `home`/`Base` untouched). Tailwind layout
  utilities (`fixed inset-0 -z-10 ...`) position it behind the card.

The Google "G" SVG is inlined verbatim in `login.templ` (it is a
single-use decorative SVG, not a repeated component — per the
anti-over-abstraction rule, do NOT wrap it in `ui.Icon`). The SVG path
fills stay their brand colors (`#4285F4` etc.) — these are the ONE place
hardcoded hex is acceptable (a third-party brand logo's official colors,
not the app palette); flag this exception explicitly in a comment.

### 2.4 Wiring

- Route: `GET /login` (EXISTS, unchanged) → `Handler.LoginPage` →
  `pages.Login()` (zero-arg; the redesigned page needs no view model — it
  is pure static markup + one link).
- The link target `/auth/google/login` (EXISTS) starts the existing
  Google OAuth flow. **No new route, no new handler, no new dependency.**

---

## 3. Menu / navigation redesign

### 3.1 Vehicle header read path (DD2 — implements grill D7)

**Decision: the nav header is an htmx fragment at `GET /ui/nav-header`,
swapped into a placeholder rendered by `layouts.BaseAuth`.**

`BaseAuth` renders, at the top of the drawer side, an empty anchor:

```html
<div id="nav-header"
     hx-get="/ui/nav-header"
     hx-trigger="load"
     hx-swap="outerHTML">
  <!-- placeholder shown until the fragment loads -->
</div>
```

A new `Handler.NavHeaderFragment` serves `GET /ui/nav-header`:
auth guard (`currentUID`) → `navHeaderFor(ctx, uid)` →
`renderFragment(..., "nav-header")`. `navHeaderFor` is a gin-free helper
(unit-testable with `account.Service` + `telemetry.Reader` fakes) that:

1. Calls `h.acct.RegisteredVehicles(ctx, uid)` — the SAME existing account
   port the dashboard and the charge form already use (RD4 uses it for
   vehicle auto-select). Returns `[]account.Vehicle` with
   `DisplayName`, `TeslaID`, etc.
2. Calls `h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)` — the SAME
   existing `DISTINCT ON` indexed read the dashboard already runs once per
   render. Empty (non-nil) slice when the account has no snapshots.
3. Picks the **primary vehicle** (DD4) and maps to a `NavHeaderVM`
   presentation model.

**Why an htmx fragment and not threading a VM into `BaseAuth`:**

- **Hot-path isolation.** `BaseAuth` runs on EVERY authed page load. The
  telemetry + account reads are O(1-2) indexed queries but they ARE reads
  on every page. Keeping them in a separate fragment handler means the
  page shell renders from identity-only session data immediately, and the
  nav-header read happens as a second swap — the page paint is not blocked
  by the telemetry read, and a refresh of the nav header does NOT re-render
  the whole page.
- **DRY / single source.** One `navHeaderFor` helper, one route, one
  template. Every authed page gets the header for free without each page
  handler threading a VM into `BaseAuth` (which would couple every page VM
  to the layout and duplicate the read).
- **Resilient.** If the read errors, the fragment handler renders a
  degraded `NavHeaderVM` (no vehicle / "unavailable") — the page itself
  already rendered successfully.
- **Aligns with the read-only-exception rule** — `navHeaderFor` calls
  ONLY `Reader` ports (`account.RegisteredVehicles`,
  `telemetry.Reader.LatestSnapshotsByAccount`), never a `Writer`/`Collector`.

**Rejected alternative — `BaseAuth` takes an optional VM:** every page
handler would have to call `navHeaderFor` and pass it through, duplicating
the read across handlers and coupling each page's VM to the layout. Worse
change-locality than one fragment handler.

The fragment approach adds one extra HTTP round trip per authed page load.
For a read-heavy local app where the dashboard already runs the same read
per render, this is an acceptable trade for the DRY + hot-path-isolation
gains. (If a later profile shows it matters, the fragment can be cached on
a short TTL or upgraded to a server-rendered include — out of scope here.)

### 3.2 Primary vehicle selection (DD4 — implements grill D7 multi-vehicle)

The Stitch header shows ONE vehicle. For an account with multiple
vehicles, the **primary vehicle** = the first entry from
`account.RegisteredVehicles(ctx, uid)`. This mirrors the existing RD4
auto-select rule's spirit (a single chosen vehicle) without introducing an
interactive **vehicle selector** — Stitch's top-bar "Vehicle Selector"
button is **out of scope** (no live Tesla call, no DB) and is recorded in
`openspec/roadmaps/backlog.md` as future work.

When the account has NO registered vehicles, the header renders an
"awaiting connect" state ("Connect your Tesla" link to `/connect/tesla`,
mirroring the dashboard's `NeedsConnect` state) with no status dot.

### 3.3 Freshness-derived "Connected" status (DD3 + DD10 — implement grill D7)

A new package-level constant in `handlers`:

```go
// connectedFreshnessWindow is how recent a snapshot's CapturedAt must be
// for the vehicle to show as "Connected" in the nav header. It tolerates
// ONE missed nightly poll (24h cycle + 24h buffer). Distinct from
// stalenessThreshold (36h) which gates the dashboard CARD stale marker —
// a stricter signal about a single reading's freshness, not connectivity.
const connectedFreshnessWindow = 48 * time.Hour
```

**Status logic** (pure function of `(capturedAt, now)`, deterministic for
tests — mirrors the existing `isStale` pattern):

| Snapshot state | Dot color (semantic token) | Label |
|---|---|---|
| No snapshot for primary vehicle | neutral (`badge-ghost` / `text-base-300`) | "Awaiting first snapshot" |
| `CapturedAt` within `connectedFreshnessWindow` | success green (`badge-success`) | "Connected • {batteryPct}%" |
| `CapturedAt` older than window | warning/neutral (`badge-warning`) | "Asleep • Last seen {relative}" |

`{relative}` is pre-computed by the handler (e.g. "2 days ago") — the
template does NO time math (gateway spec invariant: "Templates contain no
business logic"). Battery % comes from `Snapshot.BatteryLevel`.

**Why 48h and not the existing 36h:** the nav-header "Connected" signal
answers "is this vehicle still being monitored?" — a single missed nightly
poll (24h cycle) should NOT flip it to "Asleep"; 48h tolerates one missed
poll, flips only after two. The 36h `stalenessThreshold` is stricter
because the dashboard card stale marker warns about ONE reading's
freshness. Keeping two constants documents that they answer different
questions; both are named (no magic numbers).

**Status dot as semantic tokens only (DD10):** the colored dot is a
`ui.Badge` (or a small inline `<span class="badge badge-success">`) —
NEVER a Stitch hex (`#E82127`). "Connected" green = `badge-success`;
"Asleep" = `badge-warning`. The `ui/` kit owns the `badge` component class
(add a `ui.BadgeProps.Variant` mapping if the existing `badge.templ` does
not already cover `success`/`warning`/`ghost` — re-use, do not inline).

### 3.4 Nav items mapping (DD6/DD7 — implement grill D8/D9/D10)

`layouts.navItems()` is updated to four entries; `ui.NavItem` gains a
`Placeholder bool` field and an `Icon string` (glyph name) field (DD5).

| Stitch label | Href | State | Icon glyph |
|---|---|---|---|
| Dashboard | `/dashboard` | Live (active on `/dashboard`) | `dashboard` |
| Manual Records | `/charges` | Live (active on `/charges`) — relabels the existing "Charge log" entry (DD7) | `ev_station` |
| Supercharger Stats | `#` | Placeholder (D9) — "Soon" badge | `analytics` |
| Settings | `#` | Placeholder (D9) — "Soon" badge | `settings` |

- **"Wake Vehicle" button is NOT rendered** (DD8 / grill D8). It conflicts
  with `internal/gateway/AGENTS.md` "Data Access Model" (no user-triggered
  Tesla API calls; only listing vehicles at first connect). Do NOT add it.
- **Placeholder rendering (DD6):** when `NavItem.Placeholder == true`,
  `ui.NavShell` renders `Href="#"` and appends a small "Soon" `ui.Badge`.
  No stub route is built this change. Privacy: `templ.SafeURL("#")` is
  fine for a placeholder anchor.
- **Active highlighting:** `BaseAuth` currently receives only a title.
  Enabling per-item `Active` requires knowing the request path inside the
  layout. **Scope decision:** threading the path into `BaseAuth` is a
  small, self-contained change done as part of the nav evolution —
  `BaseAuth` gains a `path string` param (or reads `c.Request.URL.Path` via
  a passed-in value); `navItems()` becomes `navItems(active string)` and
  sets `Active` by comparing. This is included in the nav tasks (T3/T4).
  Pages calling `BaseAuth` are updated to pass their path (or, simpler,
  `BaseAuth` is called with the path by a helper). Minimizing churn: give
  `BaseAuth` an optional path via a templ parameter and default-empty.

### 3.5 Icons without a client JS/icon-font dependency (DD5 — implements grill D12)

Stitch uses the **Material Symbols Outlined** icon font (loaded from
`fonts.googleapis.com` — an external CDN). That is forbidden by D12
(no external CDN / no hotlinked asset) and by the zero-JS/zero-init rule
(an icon font is a CSS-loaded webfont, not JS, but it is still an external
hotlinked asset).

**Decision: inline SVG icons via a new `ui.Icon` wrapper.**

- A new `internal/gateway/templates/ui/icon.templ` exposes
  `ui.Icon(ui.IconProps{Name string, Class string})`.
- `ui.IconProps.Name` is one of a closed set: `dashboard`, `ev_station`,
  `analytics`, `settings`, `menu` (hamburger), plus any needed for the
  nav header (e.g. `battery`). Each name maps to an inline `<svg>` with
  `<path>` data.
- The glyph map is a `map[string]templ.Component` (or a `switch` inside
  `Icon`) — ONE place to add a glyph. This is a closed vocabulary, not
  ad-hoc SVG sprinkled in pages (AI-efficiency: an agent looks up the name
  instead of re-drawing the SVG).
- Sourcing the path data: use the equivalent Material Symbols geometry
  (Material Symbols is Apache-2.0; the individual glyph path data is
  usable). If path data is uncertain, a simple geometric stand-in (circle
  for the status dot, rectangles for "dashboard" tiles) is acceptable for
  the first cut — the `ui.Icon` abstraction means the SVG can be refined
  in one place later without touching pages.

**Why not DaisyUI's built-in icons:** DaisyUI does NOT ship an icon set; it
relies on an external font (Material Icons / Material Symbols). That
re-introduces the external-CDN problem. Inline SVG inside `ui.Icon` is the
only zero-CDN, zero-JS, re-skin-safe option.

### 3.6 `ui.NavShell` evolution

The existing `NavShell(items []NavItem)` becomes:

```go
type NavItem struct {
    Label       string
    Href        string
    Active      bool
    Icon        string       // DD5: glyph name passed to ui.Icon
    Placeholder bool        // DD6: renders a "Soon" badge + Href="#"
}
```

`NavShell` renders:
- a header slot (the `#nav-header` placeholder, swapped by htmx — DD2),
- each item as `<li><a ...><span class="icon">…</span><span>Label</span>
  [Soon badge]</a></li>`, with `menu-active` when `Active`.

All DaisyUI component classes (`menu`, `menu-active`, `badge`) stay owned
inside `ui.NavShell` / `ui.Badge` — pages/nav.go pass only VM-ready fields.
No hex; semantic tokens only.

---

## 4. No Database Changes

**No database object is created or changed by this change.** There is no
new table, column, index, constraint, view, or migration, and no
`sqlc` change.

**Rationale:**

- The **Login redesign is a visual re-skin** of the existing Google login
  page — no new auth model, no new route, no persistence (grill D6).
- The **nav header reuses two EXISTING read ports**:
  - `telemetry.Reader.LatestSnapshotsByAccount(ctx, accountID)` — the same
    single `DISTINCT ON (tesla_id) ... ORDER BY tesla_id, captured_at DESC`
    query the dashboard already runs once per render. It is already
    indexed by the `(account_id, tesla_id, captured_at)` account-leading
    index (`ai/architecture.md` §7, `ai/go-conventions.md` §Persistence).
    No new query, no new index.
  - `account.RegisteredVehicles(ctx, accountID)` — the same port the
    dashboard and the charge form already use for the vehicle list / the
    RD4 auto-select. No new method on `account.Service`.
- The **freshness window** (`connectedFreshnessWindow`) and the
  "Connected/Asleep" derivation are pure Go logic in the gateway handler,
  computed from `Snapshot.CapturedAt` — no stored field.
- The **home.templ debug cleanup** removes dead handler/route/fragment
  code; it touches no DB.

The database design gate (`openspec/config.yaml` `design:` rules;
`CLAUDE.md` `Design-Gates: database`) is therefore **NOT triggered**.

---

## 5. Read Paths Affected

Per `openspec/config.yaml` `proposal:` rule, this change names the read
paths it affects:

| Path | Module port | Existing? | Hot? | Mitigation |
|---|---|---|---|---|
| Nav header vehicle name | `account.RegisteredVehicles(ctx, uid)` | Yes — same as dashboard + charge form | Yes (every authed page load via the nav-header fragment) | Isolated to one `GET /ui/nav-header` fragment handler (DD2); page shell render stays identity-only; read is the same O(rows) query already run by the dashboard |
| Nav header battery + freshness | `telemetry.Reader.LatestSnapshotsByAccount(ctx, uid)` | Yes — same `DISTINCT ON` indexed read as dashboard | Yes (every authed page load via the nav-header fragment) | Same isolation as above; the read is one indexed range scan, Already the dashboard's per-render cost |
| Login | NONE | — | No | Pure static markup + one link to existing `/auth/google/login` |
| home.templ cleanup | NONE | — | No | Removes a dead fragment handler/route |

**Hot-path consideration:** the nav-header fragment adds one `account`
read + one `telemetry` read per authed page load (the dashboard already
does both per render; other authed pages — `/charges` — did not). The
fragment design (DD2) keeps these reads OFF the critical render path (the
page shell paints immediately; the header swaps in after). Neither read is
new SQL — both are existing indexed queries. If profiling later shows the
extra round trip matters, a short-TTL in-memory cache of the latest
snapshot per account is the mitigation — explicitly **out of scope** for
this change (it would be its own gateway change, recorded in backlog).

The MCP handoff itself is a **dev-time** activity (the assistant reads the
Stitch design during development) — NOT a runtime read path.

---

## 6. Codegen steps the apply stage will need

After the apply worker edits the `.templ` files and any new classes:

1. **`make templ`** — regenerate `*_templ.go` (new `ui/icon.templ`,
   evolved `ui/nav_shell.templ`, new `templates/fragments/nav_header.templ`,
   rewritten `templates/pages/login.templ`, stripped
   `templates/pages/home.templ`).
2. **`make css`** — regenerate `static/app.css` for any new DaisyUI/Tailwind
   classes used by the new components (`badge-success`, `badge-warning`,
   gradient utilities, icon `<svg>` styling). **`static/app.css` MUST be
   committed in the same change** as the template edits (the
   `//go:embed` ships only classes present at build time —
   `internal/gateway/AGENTS.md` "stale CSS silently ships unstyled markup"
   gotcha). CI guard: `make css && git diff --exit-code
   internal/gateway/static/app.css`.
3. **`make generate`** runs both — prefer it so nothing is missed.
4. **No `make sqlc`** — no DB change (§4).
5. **`go test ./internal/gateway/...`** — the apply worker runs the
   `httptest` suite against `NewEngine` with fakes for the `Deps`
   interfaces (existing pattern in `handlers_test.go` /
   `gateway_test.go`). `CLAUDE.md` authorizes the assistant to run go
   build/vet/test directly for this project.

---

## 7. Design decision summary (DD1..DD11)

- **DD1** — Login background is a pure CSS gradient (semantic tokens), not
  an embedded or external image. Implements grill **D12**.
- **DD2** — Nav header is an htmx fragment at `GET /ui/nav-header` swapped
  into a `BaseAuth` placeholder. Implements grill **D7** (hot-path
  isolation + DRY).
- **DD3** — `connectedFreshnessWindow = 48 * time.Hour`, a separate
  constant from the 36h `stalenessThreshold`. Implements grill **D7**.
- **DD4** — Primary vehicle = first `account.RegisteredVehicles` entry;
  vehicle-selector deferred to backlog. Implements grill **D7**.
- **DD5** — Icons rendered as inline SVG via a new `ui.Icon` wrapper
  (closed glyph map); no Material Symbols font / no external CDN. Implements
  grill **D12** + zero-JS.
- **DD6** — Placeholder nav items render `Href="#"` + a "Soon"
  `ui.Badge` via a new `ui.NavItem.Placeholder` field; no stub route.
  Implements grill **D9**.
- **DD7** — Nav label "Manual Records" maps to `/charges`; the existing
  "Charge log" nav entry is relabeled. Implements grill **D10**.
- **DD8** — "Wake Vehicle" button is not rendered. Implements grill **D8**.
- **DD9** — Login keeps branding header + glass card + "Continue with
  Google" (→ `/auth/google/login`) + optional footer; drops email/password/
  "Sign In"/"OR". Implements grill **D6** + **D12**.
- **DD10** — Nav status dot uses semantic `badge-success`/`badge-warning`
  tokens, never hex. Implements grill **D7** + semantic-tokens rule.
- **DD11** — `home.templ` cleanup removes the debug bits AND the now-dead
  `HealthFragment` handler, `/ui/health` route, `fragments/health.templ`,
  and the two tests referencing them. Implements grill **D11**.