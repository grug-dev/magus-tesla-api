# How DaisyUI theming works in this repo

A short, concrete explainer for AI assistants and humans touching the gateway UI.
Read this before editing `internal/gateway/static/themes/apex.css` or any
`internal/gateway/templates/ui/*.templ` component.

## The one-sentence mental model

Components never say "red." They say `btn-primary`, and `btn-primary` is defined
to read `var(--color-primary)`, which `apex.css` sets to `#e82127`. So changing a
token in `apex.css` re-colors every component that uses it, across every page,
without touching a template.

## The chain, end to end

```
template:     <button class="btn btn-primary">
        ↓  (DaisyUI component class, owned in templates/ui/button.templ)
compiled CSS: .btn-primary { --btn-color: var(--color-primary); --btn-fg: var(--color-primary-content); ... }
        ↓  (CSS variable lookup, resolved on [data-theme="apex"])
apex.css:     --color-primary: #e82127;
        ↓  (final value)
rendered:     red button
```

The same indirection applies to **every** DaisyUI component class, not just
buttons.

## Where each piece lives

| File | Role |
|---|---|
| `internal/gateway/templates/layouts/base.templ` | Sets `<html data-theme="apex">` — the single attribute that picks the active theme. |
| `internal/gateway/static/input.css` | Tailwind v4 entry. `@import`s `themes/apex.css`. To swap the whole theme, change this line + the `data-theme` attribute (names must match). |
| `internal/gateway/static/themes/apex.css` | **Single source of truth for the Apex palette.** A DaisyUI v5 `@plugin` block: a list of `--color-*` / `--radius-*` / `--size-*` / `--border` / `--depth` / `--noise` CSS variables. Every component reads from these. |
| `internal/gateway/static/themes/design.md` | The Stitch "Apex Performance" spec this theme was derived from. Human/agent reference, not wired to anything automatically. |
| `internal/gateway/templates/ui/*.templ` | The `ui/` kit — the only place DaisyUI component classes (`btn`, `card`, `input`, …) may appear. Pages compose `ui.Card(...)`, `ui.Button(...)`, never raw classes. |
| `internal/gateway/static/app.css` | **Generated, committed** artifact. `make css` regenerates it from the above. `//go:embed`-ed into the binary. Never hand-edit. |

## Token → component map

Every DaisyUI component class resolves to one or more `--color-*` tokens from
`apex.css`. Editing the token re-skins the component everywhere:

| DaisyUI class(es) | Reads token (in `apex.css`) | What it re-colors |
|---|---|---|
| `btn-primary`, `badge-primary`, `text-primary`, `bg-primary`, `fill-primary`, `border-primary` | `--color-primary` | primary actions, active nav, leading chart bars |
| `btn-secondary`, `text-secondary`, … | `--color-secondary` | secondary actions, muted accents |
| `btn-accent`, `text-accent`, … | `--color-accent` | tertiary accents |
| `btn-error`, `alert-error`, `text-error`, `badge-error` | `--color-error` | error/danger states |
| `btn-info` / `btn-success` / `btn-warning` + `-content` variants | `--color-info` / `--color-success` / `--color-warning` + their `-content` | info/success/warning states |
| `bg-base-100` | `--color-base-100` | page background, default card bg, nav bg |
| `bg-base-200` | `--color-base-200` | elevated surfaces (drawers, hover) |
| `bg-base-300` | `--color-base-300` | higher surfaces, dividers, `input-bordered` border |
| `text-base-content` | `--color-base-content` | all body text |
| `card`, `input`, `checkbox`, … (radius) | `--radius-selector` / `--radius-field` / `--radius-box` | corner roundness per component class |
| every bordered element | `--border` (width) + `--color-base-300` (color, via DaisyUI defaults) | 1px borders, dividers |

## The binding rules (from `internal/gateway/AGENTS.md`)

1. **Semantic tokens only — never hex / raw palette in templates.** `bg-base-100`,
   `text-primary`, `fill-success`; not `#fff`, not `bg-red-500`. Hex lives in
   `apex.css` (and `design.md`); everything downstream is a token reference.
2. **Never inline a DaisyUI component class** (`btn`, `card`, `input`, `fieldset`,
   …) in a page or fragment. Route it through a `ui.*` wrapper in
   `templates/ui/`. This makes a DaisyUI major-version rename a one-file edit
   per component, not an app-wide sweep.
3. **To restyle one component:** edit its `ui/<name>.templ` wrapper — add/swap
   semantic-token classes (`bg-base-100`, `text-primary`) or Tailwind layout
   utilities (`p-4`, `gap-2`, `w-full`). Pages then inherit the change because
   they call `ui.Card(...)` / `ui.Button(...)`.
4. **If a value has no DaisyUI token** (e.g. a specific `#2A2A2A` card border that
   doesn't match `--color-base-300`): add a custom token to `apex.css`
   (`--color-line-card: #2A2A2A;`) and reference it via
   `bg-[var(--color-line-card)]` / `border-[var(--color-line-card)]` in the
   `ui/` wrapper. This keeps hex out of templates (rule 1 still holds) while
   isolating the new value to the theme file.
5. **Codegen after any class change:** `make templ` (regenerates `*_templ.go`)
   + `make css` (regenerates the committed `app.css`). Skip `make css` and the
   new class ships **unstyled in production** with no build warning.

## What this means for applying a Stitch `design.md`

- **Most spec values map straight onto existing DaisyUI tokens** — no custom
  variables needed. `design.md`'s `primary: #e82127` → `--color-primary`; its
  `surface: #121414` → `--color-base-100`; its `rounded.DEFAULT: 0.25rem` →
  `--radius-field`. A big chunk of any Stitch Apex spec is already set in
  `apex.css` because `apex.css` was originally derived from that same asset.

- **Vocabularies don't map 1:1.** DaisyUI v5 has ~14 color tokens
  (`base-100/200/300`, `primary`, `secondary`, `accent`, `neutral`,
  `info/success/warning/error` + their `-content`). A Material-3-style
  `design.md` has ~50 names (`surface-container-highest`, `primary-fixed-dim`,
  `on-surface-variant`, `outline-variant`, `inverse-surface`, …). Most have no
  DaisyUI home — they become custom tokens referenced via `var(...)`, the same
  pattern as rule 4 above. Add them **on demand** when a real component needs
  one; do not front-load all ~50.

- **Typography is a token, not a template edit.** DaisyUI v5 reads
  `--default-font-family` and `--default-mono-font-family` and applies them
  site-wide. Set them in `apex.css`'s `@plugin` block and every `btn`, `card`,
  `input`, heading inherits the font. Self-host woff2 files under
  `internal/gateway/static/fonts/` with a `@font-face` block — the gateway
  bans external CDNs (`base.templ` comment: "never an external CDN"), so Google
  Fonts `<link>` is out unless an RD8 decision is recorded in
  `internal/gateway/AGENTS.md`.

## Two swap costs (the "is it easy to re-theme?" answer)

- **Swap the whole theme** (Apex → something else): one-line `@import` edit in
  `input.css` + one `data-theme` attribute in `base.templ`. That is the
  explicit re-skin contract documented at the top of `apex.css`.

- **Edit values inside Apex** (e.g. change `#121414` → `#0c0f0f`, or swap Inter
  for Geist): edit the tokens in `apex.css`, run `make css`. All pages pick it
  up because they reference tokens, not literals. No template sweep.

`design.md` itself is not wired to anything automatically — it is a spec. To
make a `design.md` edit "live," re-do the token mapping into `apex.css`'s
`@plugin` block (the vocabulary-translation step above), then `make css`.
