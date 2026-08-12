# htmx + Templ Conventions — magus-tesla-api

How to write the web layer. **The UI Gateway (`internal/gateway/`) is the only place HTML
lives** (see [`architecture.md`](./architecture.md)). Any assistant/human writing markup
must follow this file. The gateway↔module wiring is in
[`htmx-go-integration.md`](./htmx-go-integration.md).

> **Status:** the web layer is **live** — the Node-less Tailwind + DaisyUI foundation, the
> themed drawer shell, and the typed `templates/ui/` kit are in the repo (scaffolded once by
> `kkpa-goth-scaffold-ui init` on 2026-07-24; default theme `lemonade`). Follow these
> conventions for every page you add or change.

---

## Template engine: Templ (`github.com/a-h/templ`)

We use **Templ**, not `html/template`. Templ components are written in `.templ` files and
**compiled to type-safe Go** — a fragment that references a field which no longer exists
**won't compile**, giving the presentation layer the same compile-time safety our Go
interfaces give the domain.

- Components are Go functions that return `templ.Component`, e.g. `templ BatteryCard(s battery.State) { ... }`.
- After editing any `.templ` file, regenerate the `*_templ.go` with **`make templ`**
  (the pinned `go tool templ generate ./...` — see the `Makefile`). **Never** run a bare
  `go run .../templ generate` inside the module: it pulls the templ CLI's transitive deps
  into `go.mod` as accidental `// indirect` requires.
- Codegen runner: this project's `CLAUDE.md` authorizes Claude to run `make templ`
  (and other codegen) directly; in a project without that override, an assistant edits
  `.templ` files and asks the user to run `make templ` instead of running it.
- The templ CLI is pinned as a `go tool` directive at the same version as the
  `github.com/a-h/templ` runtime — verify setup via Context7 (`/a-h/templ`) rather than
  memory, since tooling evolves.

---

## Where files live

```
internal/gateway/
├── handlers/     # Go: receive /ui requests, call module interfaces, render components
├── static/       # embedded assets: htmx.min.js, app.js (htmx error-fragment listener), DaisyUI .mjs bundles, input.css, generated app.css
├── tools/        # git-ignored Node-less Tailwind CLI binary (re-fetch: make ui-toolchain)
└── templates/    # .templ files only
    ├── layouts/  # full-page shells (html, head, themed DaisyUI drawer shell)
    ├── pages/    # full pages composed from fragments (initial, non-htmx loads)
    ├── fragments/# the units htmx swaps in (cards, rows, panels)
    └── ui/       # owned, typed DaisyUI wrapper components (Card, StatTile, Button, …)
```

---

## Styling: Node-less Tailwind + DaisyUI (zero JS)

Styling is a **closed vocabulary**, so any assistant/agent produces on-theme markup by
lookup, not invention. Three layers:

- **Templ** — the typed component boundary (`templates/ui/`, package `ui`). Each wrapper
  takes a `Props` struct so a wrong field fails the build.
- **DaisyUI** — component look + theme (`card`, `btn`, `input`, `select`, `fieldset`, `stat`,
  `table`, `alert`, `badge`, `menu`, `drawer`) and **semantic theme tokens** (`bg-base-100`,
  `text-base-content`, `primary`/`secondary`/`accent`, `info`/`success`/`warning`/`error`).
  It ships **zero JavaScript**, which is why htmx fragment swaps stay styled with nothing to
  re-initialize.
- **Tailwind** — layout/spacing utilities only (`grid`, `flex`, `gap-4`, breakpoints).

**The `ui/` kit is an anti-corruption adapter around DaisyUI.** DaisyUI is an external library
that ships **breaking changes** across majors (v4→v5 removed `form-control`/`label-text`, made
`input-bordered` a no-op). Routing every DaisyUI **component class** through a `ui.*` wrapper
makes a version bump a **one-file edit per component** instead of an app-wide sweep. Split the
surface by churn: **component classes are volatile → own them behind a wrapper**; **semantic
theme tokens and Tailwind layout utilities are stable → use them inline.** (An adapter bounds
an upgrade's blast radius; it doesn't make it free — re-verify the wrapper internals and
smoke-test on a major bump.)

Rules:
- **Never write hex colors or raw palette utilities** (`bg-red-500`). Use theme tokens so the
  whole app re-skins from one `<html data-theme>`.
- **Compose the `ui/` kit; never inline a DaisyUI component class in a page/fragment.** A raw
  `<button class="btn">`, `<input class="input">`, or `<div class="card">` in `pages/` or
  `fragments/` is a bug — use `ui.Button`/`ui.Input`/`ui.Card`. If a repeated element has no
  wrapper yet, **add one to `ui/`** rather than inlining the class. Form controls compose
  `ui.Field` + `ui.Input`/`ui.Select`/`ui.Textarea` (the DaisyUI `fieldset`/`input`/`select`/
  `textarea` classes live only there). Theme tokens (`text-error`, `bg-base-100`) and Tailwind
  layout utilities (`grid`, `sm:col-span-2`) stay inline — those are the stable layers. Pass
  VM-ready strings into components (`ui/` components hold no domain imports and no business
  logic).
- **Do not introduce a component that needs client-side JS init.** If a genuinely interactive
  widget is unavoidable, prefer a CSS-only DaisyUI pattern (`dropdown`, `<dialog>` modal,
  `collapse`, `tabs`) before any JS — that is the whole reason DaisyUI was chosen over templUI.
- After editing `.templ` or adding new classes, run **`make css`** (regenerates `app.css` via
  the Node-less binary) alongside **`make templ`**. `make generate` runs both. `app.css` is a
  **committed** vendored artifact (like `htmx.min.js`), so `go build ./...` needs no pre-step.

### Upgrading Tailwind or DaisyUI

Both are **vendored**: the Tailwind CLI binary (git-ignored, in `tools/`) and the DaisyUI
`.mjs` bundles (committed, in `static/`). Upgrading either is a deliberate, committed step —
and **you must regenerate `app.css` afterward**. The stylesheet is compiled at generation
time, so a newer engine or plugin version has **no effect on the running app** until you
re-run `make css`.

- **Tailwind CLI binary** → `make ui-toolchain` re-downloads the `latest` release for your
  OS/arch. To pin a version, change `latest/download` → `download/vX.Y.Z` in the
  `ui-toolchain` Makefile target. Then `make css`.
- **DaisyUI bundles** → `make ui-bundles` re-downloads `daisyui.mjs` + `daisyui-theme.mjs`
  (`latest`). To pin, change `latest/download` → `download/vX.Y.Z` in the `ui-bundles` target.
  Then `make css`, and **commit the two `.mjs` files together with the regenerated `app.css`**.
- **Always finish with `make css` and commit `app.css`** — skipping it leaves the app on the
  old CSS even though the tooling changed. Check what moved: `git diff --stat internal/gateway/static/`.
- Confirm the upgrade took by reading the banners `make css` prints (`≈ tailwindcss vX.Y.Z`
  and `🌼 daisyUI X.Y.Z`), and smoke-test a page — a major DaisyUI bump can rename classes.

---

## Component & fragment rules

- **Fragment-first.** Design each swappable piece of UI as its own small component so htmx
  can target it. A page is a composition of fragments, not a monolith.
- **Mark htmx-swappable regions with Templ fragments.** Wrap a region in
  `@templ.Fragment("name") { ... }` so the same page component can render either the whole
  page (initial load) or just that region (htmx request) — see
  [`htmx-go-integration.md`](./htmx-go-integration.md) for the handler side.
- **Pass domain structs into components, never vendor DTOs.** Components accept clean
  domain models (e.g. `battery.State`), never `...Tesla` adapter DTOs. *Why: keeps Tesla's
  shape out of the UI and preserves the anti-corruption boundary.*
- **No business logic in templates.** `.templ` files do presentation only — formatting,
  conditionals, loops. Any computation happens in the domain module and arrives as data.
- **Prefer explicit, readable markup** over clever nesting (this project is a Go/htmx
  learning codebase — clarity beats brevity).

---

## htmx attribute conventions

- Point htmx requests at the gateway's `/ui` routes (e.g. `hx-get="/ui/battery"`).
- Keep `hx-*` attributes readable and grouped: verb (`hx-get`/`hx-post`), then `hx-target`,
  then `hx-swap`.
- Give swap targets stable `id`s that match the fragment name they hold, so markup and
  handler stay in sync.
- Load htmx from a pinned version (documented when the layout is created), not an unpinned
  CDN latest.
- **A form that submits data puts its `hx-post`/`hx-put`/`hx-patch` on the `<form>` element,
  with a `Type: "submit"` button inside — never `hx-*` on the button with
  `hx-include="closest form"`.** This is correctness, not style. htmx only runs HTML5
  constraint validation when the element issuing the request *is* the form
  (`elt instanceof HTMLFormElement || hx-validate="true"`), and a `type="button"` never
  triggers the browser's own submit-time validation either. Put the verb on the button and
  every `Required: true` in that form goes silently inert — empty required fields sail past
  the browser and land on the server as a 422. (`ui.Button` defaults to `type="button"`, so
  a submit button must pass `Type: "submit"` explicitly.) Non-submitting row actions
  (delete, cancel, load-a-fragment) correctly stay as `hx-*` on the button.
  `fragments.ChargeRowEdit` is the gold standard for an inline edit form.

### Cross-region refresh via `HX-Trigger` (event-driven, not out-of-band)

When one action must refresh a **different** region on the page, have the handler emit an
`HX-Trigger` response header and let the other region subscribe — do **not** couple the
acting handler to the other region's markup.

- **The actor fires an event.** The handler sets `c.Header("HX-Trigger", "<event>")` alongside
  its own fragment render. htmx bubbles that event up to `<body>`.
- **Subscribers listen with `from:body`.** Any region that should react declares
  `hx-trigger="<event> from:body"` + `hx-get="/ui/<region>"` and re-fetches itself. Because
  the event bubbles to `<body>`, the `from:body` modifier is required.
- **Why this over an out-of-band (`hx-swap-oob`) swap:** the actor stays page-agnostic — it
  fires one event and every page opts in independently, so adding a new subscriber touches
  only that region (change-locality), and the actor wastes no work on pages where the region
  isn't present.

Gold standard: the sidebar **vehicle switcher**. `POST /ui/vehicle/select`
(`handlers.VehicleSelect`) persists the selection, renders the `nav-header` fragment, and
sets `HX-Trigger: vehicle-changed`. The dashboard's `#dashboard-content` region subscribes
with `hx-trigger="vehicle-changed from:body"` + `hx-get="/ui/dashboard"` and swaps its
`innerHTML`, so switching the active vehicle refreshes the bento with no full-page reload.
The `#dashboard-content` wrapper sits **outside** the `@templ.Fragment("dashboard")` block so
the `innerHTML` swap keeps the listening element (and its `hx-trigger`) in the DOM.

The manual-records page follows the same shape: `#charges-content` subscribes to
`vehicle-changed` and re-fetches `GET /ui/charges`, which renders the create-form **and** list
fragments together (`renderFragment(…, "charges-create-form", "charges-list")`) so both the
vehicle-scoped entry list and the create form's vehicle default follow the switch. Any new
per-vehicle page must do likewise — see `internal/gateway/AGENTS.md` §"Vehicle-scoped reads".

## Stitch designs → `ui/` kit translation (closed handoff procedure)

Google Stitch (stitch.withgoogle.com) is a **visual / design source only** for this
project. Its exported Tailwind/HTML (or JSX/Vue/…) is **reference, never committed**:
Stitch hardcodes palette colors and inline utility classes, knows nothing of DaisyUI,
Templ, or this project's `internal/gateway/templates/ui/` kit. Pasting a Stitch export
would violate the closed vocabulary above and break one-`data-theme` re-skinning. So every
Stitch design is **translated**, not merged. The same procedure every time, so an agent or
human does not improvise:

1. **Read the design.** Via the `stitch` MCP server (configured once in the user's global
   opencode/Claude Code MCP config — not this repo's project config; Stitch is a general
   design tool, not this project's infrastructure) — preferred, because it exposes the
   design structure and the project's `DESIGN.md` design system; or via a screenshot when
   MCP is unavailable. Identify layout regions, repeated components, and per-state variants
   (empty / loading / error / authenticated).
2. **Map to the `ui/` kit, never inline.** Rebuild the design as a Templ component
   composed from existing `ui.*` wrappers (`ui.Card`, `ui.StatTile`, `ui.Button`,
   `ui.Table`, `ui.Field` + `ui.Input`, …). If a repeated element has no wrapper yet,
   **add one to `internal/gateway/templates/ui/`** — never inline a raw DaisyUI component
   class in a page or fragment. This is the same closed-vocabulary rule as everywhere else
   in this doc.
3. **Replace Stitch colors with semantic theme tokens.** Strip every hardcoded hex/rgb
   from the Stitch export and use DaisyUI semantic tokens (`bg-base-100`, `primary`,
   `text-error`, …). Keep only Tailwind **layout** utilities inline (`grid`, `gap-4`,
   `flex`, breakpoints). The output must re-skin from one `data-theme` with no per-page
   color edits.
4. **Structure swappable regions as htmx fragments.** State changes (login → logged-in
   menu, menu open → closed, …) become htmx-swapped fragments following the attribute
   conventions above — **no client-side JS**. Prefer a CSS-only DaisyUI pattern
   (`dropdown`, `<dialog>`, `collapse`, `tabs`) when a state is purely presentational.
5. **Mirror the `charges` gold-standard slice.** View model → page/fragments → Gin
   handler → routes, the same layering. `kkpa-goth-scaffold-ui scaffold <concept>` is the
   scaffolding entry point — it generates the slice skeleton against the `ui/` kit and the
   themed shell, after which the Stitch translation fills in the composed components.

### DESIGN.md → custom DaisyUI theme (out of scope here)

Stitch's per-project `DESIGN.md` (colors / typography / spacing) can be mapped into a
custom DaisyUI theme so the whole app re-skins from one `data-theme`. That mapping is a
**separate change** — record it in `openspec/roadmaps/backlog.md` when wanted; this
section only covers translating one Stitch screen into a `ui/`-kit slice.
