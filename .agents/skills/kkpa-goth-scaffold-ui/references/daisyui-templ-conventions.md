# DaisyUI + Templ conventions (Node-less GOTH foundation)

Source of truth for the `init` mode. Everything here is **gateway-only** and **Node-free**:
a single native `tailwindcss` binary plus two prebuilt DaisyUI `.mjs` bundles the binary
loads itself. No `npm`, no `package.json`, no `node_modules`.

> Verify current commands/versions against Context7 `/websites/daisyui` (install/standalone,
> install/rails "without Node.js") and `/a-h/templ` before running — tooling evolves.

---

## 1. Download the toolchain (no Node)

Two file kinds. **One-liner** (fetches both the Tailwind binary and the DaisyUI bundles):

```bash
cd internal/gateway && curl -sL daisyui.com/fast | bash
```

Or fetch each explicitly:

```bash
# a) Native Tailwind CLI binary — pick your OS (example: macOS arm64). NOT committed.
curl -sLo internal/gateway/tools/tailwindcss \
  https://github.com/tailwindlabs/tailwindcss/releases/latest/download/tailwindcss-macos-arm64
chmod +x internal/gateway/tools/tailwindcss

# b) DaisyUI ESM bundles — loaded by the binary's own runtime. COMMITTED (tiny, pinned).
curl -sLo internal/gateway/static/daisyui.mjs \
  https://github.com/saadeghi/daisyui/releases/latest/download/daisyui.mjs
curl -sLo internal/gateway/static/daisyui-theme.mjs \
  https://github.com/saadeghi/daisyui/releases/latest/download/daisyui-theme.mjs
```

Other Tailwind targets: `tailwindcss-linux-x64`, `tailwindcss-linux-arm64`,
`tailwindcss-linux-x64-musl`, `tailwindcss-macos-x64`, `tailwindcss-windows-x64.exe`.

**Commit vs ignore:**
- `internal/gateway/tools/tailwindcss` → **git-ignore** (platform-specific binary). Add to `.gitignore`.
- `internal/gateway/static/daisyui.mjs`, `daisyui-theme.mjs`, `input.css`, `app.css` → **commit** (`app.css` is a vendored generated artifact, exactly like the already-committed `htmx.min.js`, so `go build ./...` stays self-contained via the existing `//go:embed static`).

---

## 2. `internal/gateway/static/input.css`

Use the **local `.mjs` plugin form**. A bare `@plugin "daisyui"` resolves to the npm package
— do NOT use it (that would drag in Node tooling).

```css
@import "tailwindcss";

/* Scan the gateway's Templ sources for class names. */
@source "../templates/**/*.templ";

/* Don't scan the DaisyUI bundles themselves as content. */
@source not "./daisyui{,*}.mjs";

/* Node-less DaisyUI: point at the downloaded bundles, not the npm name. */
@plugin "./daisyui.mjs" {
  themes: corporate --default, dark --prefersdark;
}
@plugin "./daisyui-theme.mjs";
```

- Pick ONE DaisyUI light theme as `--default` (e.g. `corporate`, `business`, `winter`) plus
  `dark --prefersdark`. Confirm the choice with the user if unspecified.
- Class names live only in `.templ` source (copied verbatim into `*_templ.go`), so scanning
  `templates/**/*.templ` is sufficient.

---

## 3. Makefile — the `css` codegen target

Add beside the existing `templ` target (`Makefile:250`) and fold into `generate`:

```make
css: ## Regenerate app.css from Templ sources via the Node-less Tailwind binary + DaisyUI
	internal/gateway/tools/tailwindcss \
	  -i internal/gateway/static/input.css \
	  -o internal/gateway/static/app.css --minify

generate: sqlc templ css ## Run all code generators (sqlc + templ + css)
```

Running `make css` = one binary invocation, no Node in the command. `make generate` /
`make up` already chain codegen; `app.css` being committed means a plain `go build ./...`
needs no pre-step.

---

## 4. Layout: themed drawer shell (`layouts/base.templ`)

`<html data-theme>` drives the theme; `/static/app.css` carries all styling; the pinned
`/static/htmx.min.js` stays. `BaseAuth` becomes the responsive DaisyUI drawer (sidebar on
`lg`, hamburger → slide-out on mobile). **Pure CSS — no JS, so htmx swaps are safe.**

```templ
templ Base(title string) {
	<!DOCTYPE html>
	<html lang="en" data-theme="corporate">
		<head>
			<meta charset="utf-8"/>
			<meta name="viewport" content="width=device-width, initial-scale=1"/>
			<title>{ title }</title>
			<link rel="stylesheet" href="/static/app.css"/>
			<script src="/static/htmx.min.js" defer></script>
		</head>
		<body class="min-h-screen bg-base-100 text-base-content">
			{ children... }
		</body>
	</html>
}

templ BaseAuth(title string) {
	@Base(title) {
		<div class="drawer lg:drawer-open">
			<input id="nav-drawer" type="checkbox" class="drawer-toggle"/>
			<div class="drawer-content flex flex-col">
				<nav class="navbar bg-base-300 w-full">
					<label for="nav-drawer" aria-label="open sidebar" class="btn btn-square btn-ghost lg:hidden">
						<svg xmlns="http://www.w3.org/2000/svg" class="inline-block h-6 w-6 stroke-current" fill="none" viewBox="0 0 24 24">
							<path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 6h16M4 12h16M4 18h16"></path>
						</svg>
					</label>
					<div class="mx-2 flex-1 px-2 font-semibold">Magus</div>
				</nav>
				<main class="p-4">
					{ children... }
				</main>
			</div>
			<div class="drawer-side">
				<label for="nav-drawer" aria-label="close sidebar" class="drawer-overlay"></label>
				<ul class="menu bg-base-200 min-h-full w-64 p-4">
					<li><a href="/dashboard">Dashboard</a></li>
					<li><a href="/external-charges">External charges</a></li>
				</ul>
			</div>
		</div>
	}
}
```

When the nav grows, extract the `<ul class="menu">` into a `ui.NavShell([]ui.NavItem)`
component (see `component-kit.md`) so pages/agents don't re-author nav markup.

---

## 5. Theme tokens — NEVER hex

Style with DaisyUI **semantic tokens** so every page/agent is automatically on-theme and the
whole app re-skins from one `data-theme`. Do not write hex colors or raw palette utilities
(`bg-red-500`) in components.

| Purpose | Token utilities |
|---|---|
| Surfaces | `bg-base-100` / `bg-base-200` / `bg-base-300` |
| Text on surfaces | `text-base-content` |
| Brand / actions | `primary`, `secondary`, `accent` (e.g. `btn-primary`, `text-primary`, `bg-primary`) |
| Status | `info`, `success`, `warning`, `error` (e.g. `alert-error`, `badge-success`) |
| Neutral | `neutral`, `text-neutral-content` |

Component look = DaisyUI class (`card`, `btn`, `stat`, `table`, `alert`, `badge`, `menu`,
`navbar`, `drawer`). Layout/spacing = Tailwind utilities (`grid grid-cols-1 md:grid-cols-2
lg:grid-cols-3 gap-4`, `flex`, `p-4`). Granular tweaks = append utilities without conflict
(`btn btn-primary rounded-xl px-8`).

---

## 6. Component-wrapper pattern (owned components)

DaisyUI is a vocabulary; the components are **ours** and **typed**. Each lives in
`internal/gateway/templates/ui/` as a `templ` fn taking a `Props` struct, so an agent
composes a typed component instead of re-authoring class soup, and the compiler rejects a
wrong field.

```templ
package ui

type CardProps struct {
	Title string
	Class string // optional extra layout utilities
}

templ Card(p CardProps) {
	<div class={ "card bg-base-100 shadow", p.Class }>
		<div class="card-body">
			if p.Title != "" {
				<h2 class="card-title">{ p.Title }</h2>
			}
			{ children... }
		</div>
	</div>
}
```

Rules for `ui/` components: presentation only (no domain imports, no business logic); accept
primitives/VM-ready strings + a slot (`{ children... }`); optional `Class string` for layout
overrides; tokens only. See `component-kit.md` for the full kit + `Props` contracts.

---

## 7. htmx-swap safety

DaisyUI ships **zero JavaScript** and does not hook DOM updates, so htmx can swap fragments
freely — swapped-in markup stays styled with nothing to re-initialize. This is the whole
reason we chose DaisyUI over templUI (whose JS components need post-swap re-init). **Keep it
that way:** do not introduce a component that needs client-side JS init. If a genuinely
interactive widget is unavoidable later, prefer a CSS-only DaisyUI pattern (`dropdown`,
`modal` via `<dialog>`/checkbox, `collapse`, `tabs`) before any JS.
