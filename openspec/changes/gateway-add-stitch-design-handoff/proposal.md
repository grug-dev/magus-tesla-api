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

Primary module: **`internal/gateway/`** (the only module that produces HTML), plus one
repo-root config file and the shared UI convention doc.

### (a) Wire the Stitch MCP server into Claude Code

Add a **project-scoped** `.mcp.json` at the repo root so the Stitch MCP server is available
to anyone working this repo in Claude Code:

```json
{
  "mcpServers": {
    "stitch": {
      "command": "npx",
      "args": ["-y", "@google/stitch-mcp@latest"],
      "env": { "STITCH_API_TOKEN": "${STITCH_API_TOKEN}" }
    }
  }
}
```

- **Secret handling (non-negotiable).** The `STITCH_API_TOKEN` (from stitch.withgoogle.com
  → Settings → API Tokens → Generate New Token) is a credential. It is referenced via
  `${STITCH_API_TOKEN}` env expansion and sourced from the untracked `.env` / shell —
  **never** the literal token inside the committed `.mcp.json`. This mirrors the project's
  "files that must never be committed" rule (`CLAUDE.md` §Security). Add `STITCH_API_TOKEN`
  to `.env.example` (documented, empty) so contributors know it is required.
- Runs as a **local stdio** server via `npx` (nothing to host). Requires restarting Claude
  Code after adding; verify with "what tools do you have access to?" in a fresh session.
- Alternative scope (open question below): keep it user-level in
  `~/.claude/mcp_servers.json` instead of a committed project `.mcp.json`.

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

### (c) Apply it — build one screen (deferred to design/build)

Translate the specific screen the user designed in Stitch into a gateway slice (view model
→ page/fragments → Gin handler → routes). **Which screen is an open question** (below) —
this proposal establishes the mechanism; the concrete screen, its data source, and its read
paths are pinned down at design time once the user names it and shares the design.

### (d) DESIGN.md → DaisyUI theme mapping — out of scope (future)

Mapping Stitch's `DESIGN.md` (colors/typography/spacing) into a custom DaisyUI theme so the
whole app re-skins from one `data-theme` is **not** part of this change. If wanted, it
becomes its own change and should be recorded in `openspec/roadmaps/backlog.md`.

## Breaking

No. Adding a project `.mcp.json`, an `.env.example` entry, and doc sections is additive; the
eventual screen is a new gateway slice. No existing route, handler, Go interface, or Templ
component changes shape.

## Modules affected

- `internal/gateway/` — primary: the new screen slice + the translation-convention pointer
  in its `AGENTS.md`.
- Repo-root `.mcp.json` (new), `.env.example`, and `ai/htmx-conventions.md` — cross-cutting
  dev-workflow/config, leader-owned, outside any module sandbox.
- No other `internal/` module. (The chosen screen may *consume* an existing module's port
  read-only; which one is TBD with the screen.)

## No Database Changes

This change introduces **no** database object — no table, column, index, constraint, or
migration, and no sqlc change. The database design gate is **not** triggered. (If the chosen
screen needs a read a module does not yet expose, that read becomes its own module-scoped
change — not this gateway UI slice.)

## Read Paths Affected

TBD with the chosen screen. If it renders existing module data (battery / charge / vehicle
state, etc.), it reuses that module's existing indexed read via its public port — no new
query, no hot path. Any new read path will be named in design.md per the proposal rule
before build. The MCP handoff itself is a **dev-time** activity (the assistant reads the
design during development); it is not a runtime read path.

## Capabilities

### Added / Modified Capabilities

- **Dev-workflow + docs (immediate).** The `.mcp.json` Stitch entry and the
  `ai/htmx-conventions.md` translation section — enablement/convention, not runtime
  behavior, so no GIVEN/WHEN/THEN behavioral spec is required for them.
- **`gateway` (deferred — this change's runtime deliverable).** One new screen rendered
  from the translated Stitch design. Its behavioral spec is written at design time once the
  screen is chosen.

### Consumed Capabilities (no change to their specs)

- TBD — whichever module port the chosen screen reads from (read-only). No port/spec change
  anticipated.

## Open questions — resolve on resume (before design.md)

Saved as a proposal only, per request; these are deliberately left for the resume pass:

1. **Which screen** did you design in Stitch? Does it redesign an existing page
   (`dashboard` / `charges` / …) or add a new one, and what data does it show?
2. **MCP scope** — committed project `.mcp.json` (shared; token via `${STITCH_API_TOKEN}`)
   as proposed, or keep it user-level in `~/.claude/mcp_servers.json`?
3. **grill-me pass** — `openspec/config.yaml` requires the grill-me skill on proposals for
   binding decisions. A focused grill on the chosen screen's data model / read path is still
   pending and must run before design.md.
4. **DESIGN.md → DaisyUI theme** — include now or defer to the backlog (default: defer)?
