## Why

The project owner created a UI design in **Google Stitch** (Google Labs' AI UI design
tool) and wants a repeatable way to bring Stitch designs into this app. Two facts shape
the approach — both verified 2026-07-25:

- **Stitch is free** in its Labs phase, and its **MCP server is free too** (no separate
  fee; it draws from the ~350-generations/month Standard quota). The Stitch MCP server
  plugs into Claude Code and lets the assistant read a Stitch design directly (its
  structure + the per-project `DESIGN.md` design system) instead of relying on pasted
  screenshots. Paid plans are expected ~Q4 2026, so "free" is the current state, not a
  permanent guarantee.
- **Stitch output is NOT drop-in code for this repo.** Stitch exports raw Tailwind/HTML
  (or JSX/Vue/Angular/Flutter/SwiftUI) with hardcoded palette colors and inline utility
  classes — no DaisyUI, no Templ, no `ui/` kit. This project's web layer
  (`ai/htmx-conventions.md`) is a deliberately **closed vocabulary**: every page is a
  Templ component composed from the typed `internal/gateway/templates/ui/` kit, styled
  only with DaisyUI **semantic theme tokens** (never hex), zero client-side JS, and
  htmx-swappable fragments. Pasting a Stitch export would violate every one of those
  rules and break the app's one-`data-theme` re-skinnability.

So Stitch is a **design source we translate**, never code we merge. This change makes that
translation a first-class, documented workflow, wires the free MCP handoff, and then
applies the workflow to build one screen.

Decided in the 2026-07-25 interview (leader ↔ user):

- Goal: **wire the Stitch MCP into Claude Code, then translate one screen the user
  designed into a Templ + `ui/` kit slice**, mirroring the `charges` gold-standard.
- Re-theming the whole app from Stitch's `DESIGN.md` is noted as a future option, **not**
  this change's target.

Raw user request (2026-07-25):

> "please create a proposal for the above questions and directions to connect stitch with
> this project. Save the proposal, not more artifacts. I'll be resuming that proposal
> later."

Per that request this change ships **the proposal only** — design.md, specs, and tasks.md
are intentionally deferred to a later resume pass.

## What Changes

Primary module: **`internal/gateway/`** (the only module that produces HTML), plus the
shared UI convention doc (`ai/htmx-conventions.md`). The Stitch MCP connection itself is
global user config — NOT a committed project file (per D5; see §a).

### (a) Wire the Stitch MCP server into Claude Code

The Stitch MCP connection is **GLOBAL user config** (per resolved decision D5), NOT a
committed project file. The user's global opencode/Claude Code MCP config points at
the Stitch **REMOTE** server at `https://stitch.googleapis.com/mcp` with an
`X-Goog-Api-Key` header env-indirected via `{env:STITCH_API_TOKEN}`. Stitch is a general
design tool, not this project's infrastructure, so this repo carries only the
translation-**convention** doc (`ai/htmx-conventions.md` §"Stitch designs → `ui/` kit
translation") — no `.mcp.json`, no `STITCH_API_TOKEN` in `.env.example`, no Stitch config
or secret in any tracked file. The literal API key stays OUT of all tracked files
(env-indirected); the user exports `STITCH_API_TOKEN` in the shell profile launching the
editor and restarts the editor for the MCP connection to build. (D5 supersedes the
earlier option-a `.mcp.json` plan recorded in the original draft of this proposal.)

### (b) Document the Stitch → `ui/` kit translation convention

Add a section to `ai/htmx-conventions.md` (the UI source of truth) and a pointer in
`internal/gateway/AGENTS.md`, codifying the handoff so every agent/human does it the same
way — a closed, lookup-able procedure rather than an improvised one each time:

1. Stitch is a **visual/design source only**; its exported code is reference, never
   committed.
2. Read the Stitch design (via the MCP server, or a screenshot) → identify layout regions,
   components, and states.
3. Rebuild as a **Templ + `ui/` kit slice**: map Stitch elements to existing `ui.*`
   wrappers (`ui.Card`, `ui.StatTile`, `ui.Button`, `ui.Table`, `ui.Field` + `ui.Input`,
   …); if a repeated element has no wrapper yet, **add one to `ui/`** — never inline a raw
   DaisyUI component class in a page/fragment.
4. Replace Stitch's hardcoded colors with **semantic theme tokens** (`bg-base-100`,
   `primary`, `text-error`); keep only Tailwind **layout** utilities inline (`grid`,
   `gap-4`, breakpoints).
5. Structure swappable regions as **htmx fragments**; **no client-side JS** — prefer a
   CSS-only DaisyUI pattern (`dropdown`, `<dialog>`, `collapse`, `tabs`).
6. Mirror the `charges` gold-standard slice; `kkpa-goth-scaffold-ui scaffold <concept>` is
   the scaffolding entry point.

### (c) Apply it — Login re-skin + Menu/navigation redesign (resolved by D1)

Translate the Stitch Login screen (`593dc9d2dea647808643aa29935151c3`) and the
Menu/navigation (the sidebar of `db4901e044c54869972239f92534cedc`) into gateway
templates composed from the `ui/` kit. The Login is a visual re-skin of the EXISTING
Google login (D6); the navigation reuses the EXISTING `account.RegisteredVehicles` +
`telemetry.Reader.LatestSnapshotsByAccount` read ports (D7). Details in `design.md`;
behavioral specs in `specs/gateway/spec.md`; atomic apply tasks in `tasks.md`.

### (d) DESIGN.md → DaisyUI theme mapping — out of scope (future)

Mapping Stitch's `DESIGN.md` (colors/typography/spacing) into a custom DaisyUI theme so the
whole app re-skins from one `data-theme` is **not** part of this change. If wanted, it
becomes its own change and should be recorded in `openspec/roadmaps/backlog.md`.

## Breaking

No. The translation-convention doc section is additive; the Stitch MCP connection is
global user config (not a committed file, per D5). The runtime deliverable is a Login
re-skin (visual only — keeps the existing `/auth/google/login` link) plus a navigation
redesign that reuses existing read ports — no existing route, handler, Go interface, or
Templ component is removed; the only handler/route removed is the dead `/ui/health`
debug demo (per D11). No DB change (see "No Database Changes").

## Modules affected

- `internal/gateway/` — primary: the Login re-skin, the Menu/navigation redesign (nav
  header + nav items), and the `home.templ` debug cleanup. Consumes the existing
  `account.RegisteredVehicles` and `telemetry.Reader.LatestSnapshotsByAccount` read
  ports (read-only); no new module, no DB object.
- `ai/htmx-conventions.md` — the Stitch→`ui/`-kit translation-convention section
  (leader-owned Phase A, already done); the Stitch MCP connection is global user config
  (D5), outside any module sandbox and NOT committed to this repo.
- `openspec/roadmaps/backlog.md` — two future-work entries (Supercharger Stats screen;
  Settings page) appended per D9.
- No other `internal/` module.

## No Database Changes

This change introduces **no** database object — no table, column, index, constraint, or
migration, and no sqlc change. The database design gate is **not** triggered. (If the chosen
screen needs a read a module does not yet expose, that read becomes its own module-scoped
change — not this gateway UI slice.)

## Read Paths Affected

The runtime deliverable reuses two EXISTING indexed reads (no new query, no new index):
`account.RegisteredVehicles(ctx, accountID)` (vehicle name) and
`telemetry.Reader.LatestSnapshotsByAccount(ctx, accountID)` (battery level + `CapturedAt`),
the latter the same single `DISTINCT ON` range scan the dashboard already runs once per
render. The nav-header fragment runs these once per authed page load; they are isolated to
a single `GET /ui/nav-header` htmx fragment so the page shell renders identity-only (hot
path stays cheap). The Login page needs NO read path (pure static markup + one link). The
`home.templ` debug cleanup removes a dead fragment handler/route. Full hot-path analysis is
in `design.md` §5 "Read Paths Affected". The MCP handoff itself is a **dev-time** activity
(the assistant reads the design during development); it is not a runtime read path.

## Capabilities

### Added / Modified Capabilities

- **Dev-workflow + docs (Phase A — already done by the leader).** The global Stitch MCP
  connection (D5) and the `ai/htmx-conventions.md` translation section —
  enablement/convention, not runtime behavior, so no GIVEN/WHEN/THEN behavioral spec is
  required for them.
- **`gateway` (this change's runtime deliverable).** The Login re-skin and the
  Menu/navigation redesign. Behavioral specs are in `specs/gateway/spec.md` (delta).

### Consumed Capabilities (no change to their specs)

- TBD — whichever module port the chosen screen reads from (read-only). No port/spec change
  anticipated.

## Resolved decisions (D1–D12 — see progress.json `decisions[]`)

The open questions in the original draft of this proposal were resolved in the
leader↔user grill-me pass (2026-07-26) and recorded as binding decisions D1–D12 in
`progress.json` `decisions[]`. The design.md authoring cross-references these by ID.

- **D1** (user) — The Stitch screen to translate is the **Login** screen plus the
  **Menu/navigation** (redesign-or-new pinned at grill time).
- **D2** (user, **superseded by D5**) — the original "committed project `.mcp.json`"
  plan for the Stitch MCP connection.
- **D3** (user) — The grill-me pass ran AFTER the Stitch MCP was connected, against
  the actual designs.
- **D4** (user) — `DESIGN.md → DaisyUI theme` mapping is **deferred** (not part of
  this change; backlog when wanted).
- **D5** (user, supersedes D2) — The Stitch MCP connection is **GLOBAL user config**
  (remote server `https://stitch.googleapis.com/mcp`, `X-Goog-Api-Key` env-indirected
  via `{env:STITCH_API_TOKEN}`), NOT project-scoped. This repo keeps only the
  translation-convention doc, no Stitch config/secret. The `.mcp.json` paragraph above
  was reconciled to this.
- **D6** (user) — Login is a VISUAL re-skin of the EXISTING Google login; keep
  "Continue with Google" (→ `/auth/google/login`), drop Stitch's email/password/"Sign
  In"/"OR". No new auth module, no DB change.
- **D7** (user) — Nav header (vehicle name + battery % + status dot) reads the LATEST
  snapshot via the EXISTING `telemetry.Reader.LatestSnapshotsByAccount` + the account
  vehicle-name port; "Connected" is freshness-derived from `CapturedAt`. No new read
  path, no new module, no DB object.
- **D8** (user) — Stitch nav's "Wake Vehicle" button is DROPPED (conflicts with the
  gateway's Data Access Model).
- **D9** (user) — "Supercharger Stats" + "Settings" render as placeholder/"soon" links;
  both appended to `openspec/roadmaps/backlog.md` as future work.
- **D10** (leader) — "Dashboard" → `/dashboard` and "Manual Records" → `/charges` map
  to the EXISTING pages.
- **D11** (user) — `home.templ` debug artifacts ("Visits this session" + the
  "Check database" htmx health-fragment demo) are REMOVED; the rest of the landing is
  untouched.
- **D12** (user) — Stitch's inline `<script>` (parallax + focus-scale) and ALL inline
  client JS are DROPPED (zero-JS); the external `googleusercontent.com` background
  image is replaced by a CSS gradient or embedded local asset (design.md decides —
  gradient).
