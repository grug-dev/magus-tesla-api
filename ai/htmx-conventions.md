# htmx + Templ Conventions — magus-tesla-api

How to write the web layer. **The UI Gateway (`internal/gateway/`) is the only place HTML
lives** (see [`architecture.md`](./architecture.md)). Any assistant/human writing markup
must follow this file. The gateway↔module wiring is in
[`htmx-go-integration.md`](./htmx-go-integration.md).

> **Status:** conventions are decided; the web layer is **not built yet**. Follow these
> when you create it.

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
└── templates/    # .templ files only
    ├── layouts/  # full-page shells (html, head, base structure)
    ├── pages/    # full pages composed from fragments (initial, non-htmx loads)
    └── fragments/# the units htmx swaps in (cards, rows, panels)
```

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
