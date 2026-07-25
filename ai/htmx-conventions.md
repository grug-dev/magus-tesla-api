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
├── static/       # embedded assets: htmx.min.js, DaisyUI .mjs bundles, input.css, generated app.css
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
