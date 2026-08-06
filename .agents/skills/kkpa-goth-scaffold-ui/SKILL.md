---
name: kkpa-goth-scaffold-ui
description: >-
  Scaffolds AI-efficient GOTH-stack UI for the magus-tesla-api gateway (Go + Templ +
  htmx + Node-less Tailwind + DaisyUI). Use when the user says "scaffold a UI page/slice",
  "add a dashboard page", "build the UI for <concept>", "set up the DaisyUI/Tailwind
  foundation", "add a themed page", "create a gateway page for <module>", or runs
  /kkpa-goth-scaffold-ui. Two build modes — `init` lays down the one-time Tailwind+DaisyUI
  foundation, themed drawer shell, and the templates/ui/ component kit; `scaffold <concept>
  [module]` generates one full vertical slice (view model → page/fragments → Gin handler →
  routes) mirroring the `charges` gold-standard. Enforces the gateway boundary rules,
  semantic theme tokens (never hex), and km-companion values. NOT for domain-module logic,
  non-gateway code, or JSON /api routes.
---

# kkpa-goth-scaffold-ui — GOTH-stack UI scaffolder

Generates UI for **`internal/gateway/`** only (the single place HTML lives — see
`ai/architecture.md`). Built on the confirmed stack: **Templ + htmx + Node-less standalone
Tailwind + DaisyUI**, responsive **drawer** navigation, and a small **owned** kit of typed
Templ wrapper components in `internal/gateway/templates/ui/`.

**AI-efficiency principle this skill encodes:** every visual decision is a lookup into a
small closed vocabulary, and every reusable unit is a *typed* component composed rather than
re-authored. Three layers: **Templ** (typed boundary) → **DaisyUI** (component + theme
vocabulary, zero JS) → **Tailwind** (responsive + spacing vocabulary).

Read the three reference files before acting — they are the source of truth for markup,
setup, and the slice recipe:

- `references/daisyui-templ-conventions.md` — Node-less setup, theme tokens, drawer shell, wrapper pattern, htmx-swap safety.
- `references/charges-slice-pattern.md` — the gold-standard vertical-slice recipe + exact reference file paths.
- `references/component-kit.md` — the `templates/ui/` kit inventory + each component's `Props` contract.

---

## Mode dispatch (evaluate top-down; first match wins)

### 0. Help mode — intercept BEFORE any workflow

If the args are exactly `help`, `--help`, or `-h` (case-insensitive, no other term):
**do not run any workflow.** Instead, **re-read this `SKILL.md`, `README.md`, and the three
`references/*.md` at runtime** and synthesize help from what you read (never a hardcoded
block):

- one-line what-it-does (from the description),
- how to invoke it (real trigger phrases + `/kkpa-goth-scaffold-ui <args>`),
- 3–5 concrete example invocations grounded in the current modes,
- a brief "what happens when you run it" for each mode (init / scaffold),
- note that `init` is one-time and `scaffold` is repeatable.

Help mode is **side-effect-free**: read docs, print help, end the turn. Write nothing.

### 1. `init` mode — one-time foundation

Trigger: args are `init`, OR any scaffold request when the foundation is **absent** (detect
by checking that `internal/gateway/static/input.css` and `internal/gateway/templates/ui/`
both exist). If absent, run `init` first, then continue to the requested scaffold.

Follow `references/daisyui-templ-conventions.md` exactly. Summary of steps:

1. **Download the Node-less toolchain** (see the reference for the exact per-OS commands or
   the `curl -sL daisyui.com/fast | bash` one-liner):
   - the native `tailwindcss` binary → `internal/gateway/tools/tailwindcss` (chmod +x) — **git-ignore it**;
   - `daisyui.mjs` + `daisyui-theme.mjs` → `internal/gateway/static/` — **commit these** (tiny, version-pinned).
2. **Write `internal/gateway/static/input.css`** using the local `@plugin "./daisyui.mjs"`
   form (the Node-less form — a bare `@plugin "daisyui"` needs the npm package, which we do
   NOT use). Pick a default DaisyUI light theme + `dark --prefersdark`.
3. **Add the `css:` Makefile target** (runs the local binary: `-i input.css -o app.css
   --minify`) and extend `generate: sqlc templ` → `generate: sqlc templ css`.
4. **Rewrite `internal/gateway/templates/layouts/base.templ`**: `<html data-theme="…">`,
   link `/static/app.css` in `<head>` (keep the pinned `/static/htmx.min.js`), and rebuild
   `BaseAuth` as the DaisyUI `drawer`+`navbar`+`menu` responsive shell.
5. **Seed `internal/gateway/templates/ui/`** with the typed wrapper components in
   `references/component-kit.md` (Card, StatTile, Button, Alert, Badge, Table, PageHeader,
   Drawer/NavShell). Semantic theme tokens only — **never hex**.
6. **Run codegen**: `make css` then `make templ` (both Codex-authorized in this repo).
7. **Update docs in the SAME change** (required by `AGENTS.md` "Docs track structural
   change"): `ai/htmx-conventions.md`, root `README.md` "Project Structure" tree,
   `internal/gateway/AGENTS.md`. Also add the git-ignore entry for the Tailwind binary.
8. **Design-gate:** none here (no DB). Stop and confirm the chosen theme with the user
   before writing `input.css` if they have not specified one.

### 2. `scaffold <concept> [module]` mode — one vertical slice

Trigger: args name a concept (e.g. `scaffold battery`, `scaffold battery telemetry`, or a
natural request like "build the battery panel"). Foundation must exist (else `init` first).

Follow `references/charges-slice-pattern.md` exactly, imitating the `charges` slice. Steps:

1. **Resolve the domain module + its ports** with CodeGraph (`codegraph_context` on the
   concept). The gateway calls module **interfaces only** — never a DB, never a module's
   internals. If the concept has no owning module, stop and ask.
2. **View model** — `internal/gateway/templates/fragments/<concept>_vm.go`: a logic-free
   struct of pre-computed display strings. No domain types, no `pgtype`, no arithmetic in
   templates. Miles→km via the domain's existing `…Km()`/`…Kmh()` methods.
3. **Templates** — `templates/pages/<concept>.templ` composing `ui/` components inside
   `@templ.Fragment("<region>")` regions; `templates/fragments/<concept>_*.templ` for
   swappable sub-parts.
4. **Handler** — `handlers/<concept>.go`: auth-guard via `currentUID`, a gin-free
   `build<Concept>Page` helper (unit-testable), `render` (full page) + `renderFragment`
   (htmx swap). **CSRF on every write path** — reuse `generateCSRFToken`/`checkCSRF` from
   `charges.go`. Graceful-degradation notices, never a raw error/stack to the page.
5. **Routes** — register `GET /<concept>` + the `/ui/<concept>/…` fragment/action routes in
   `internal/gateway/gateway.go`.
6. **Codegen** — `make templ` (and `make css` if new classes were introduced).
7. **Tests** — add handler tests mirroring `charges_test.go` / `handlers_test.go` (fake
   ports; gin-free helper unit-tested). Do NOT test `tesla-exploration` (`raw.go`) — see
   `AGENTS.md`.
8. **Docs** — update the README structure tree if a new public surface appears.

**Boundary guards enforced on every slice:** HTML only in the gateway; clean domain structs
in (never vendor `…Tesla` DTOs); no business logic in templates; `/ui` routes for htmx;
semantic theme tokens (never hex); km companion values for any distance/speed field.

---

## After any write mode

Run `make check` (`build` + `vet` + `test`) and report the result honestly. If codegen or
build fails, keep the task in progress and surface the actual output — do not claim success.
