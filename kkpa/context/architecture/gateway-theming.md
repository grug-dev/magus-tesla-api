# Gateway theming — palettes, fonts and the CSS file layout

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `theme`, `palette`, `data-theme`, `theme file`, `_shared.css`, `apex`,
  `graphite`, `halloween`, `self-hosted fonts`, `Inter`, `JetBrains Mono`, `RD11`,
  `font-mono`, `battery scale`, `battery band`, `status colours`, `theme-guard`
- **Internal name:** `internal/gateway/static/themes/` — one shared file plus one file per
  palette, all compiled into `static/app.css` by `make css`.

## Component map

| Layer | File / symbol | Role |
|---|---|---|
| entry | `internal/gateway/static/input.css` | Registers the DaisyUI builtins, then `@import`s every theme file in order. The Tailwind input. |
| shared | `internal/gateway/static/themes/_shared.css` | Everything every theme inherits. Imported **first**. |
| palette | `internal/gateway/static/themes/apex.css` | The apex palette. One `@plugin` block. Carries `default: true`. |
| palette | `internal/gateway/static/themes/graphite.css` | The graphite palette. One `@plugin` block. No `default`. |
| correction | `internal/gateway/static/themes/halloween.css` | **Not a palette.** Four status-token overrides for the DaisyUI *builtin* `halloween`. |
| spec | `internal/gateway/static/themes/design.md` | The Stitch design spec. Pins the Apex typography this guide implements. |
| fonts | `internal/gateway/static/fonts/` | Four woff2 binaries. Picked up by `gateway.go`'s existing `//go:embed static`. |
| vocabulary | `internal/gateway/templates/ui/theme.go` | `ui.Themes` — the closed theme vocabulary. `ui.IsSupportedTheme`. |
| resolution | `internal/gateway/templates/layouts/base.templ` | `baseShell` writes `<html data-theme={ ui.ThemeFromContext(ctx) }>`. The only place the attribute is set. |
| mono wrappers | `internal/gateway/templates/ui/` — `input.templ`, `select.templ`, `textarea.templ`, `badge.templ`, `stat_tile.templ` | The kit wrappers that carry `font-mono`. |
| bands | `internal/gateway/templates/ui/ui.go` | `ui.BatteryBandClass` — the single battery-band definition. |
| adapter | `internal/gateway/templates/pages/dashboard.go` | `pages.dashBatteryColorClass` — a thin adapter over `ui.BatteryBandClass`. |
| guard | `Makefile` §`theme-guard` | Fails when the three theme lists disagree. Wired into `make check`. |
| storage | `account.settings.theme` | The signed-in user's saved choice. See `entities/account-settings/guide.md`. |
| docs | `internal/gateway/AGENTS.md` | Keeps the no-CDN rule only. Points here for the rest. |

## The file layout

`_shared.css` owns everything a palette must **not** re-declare:

- the four `@font-face` blocks
- the `[data-theme]` token rule — `--default-font-family`, `--default-mono-font-family`,
  and `--color-battery-low` / `-mid` / `-warn` / `-full`
- the `.text-battery-*` utilities
- the `.divider` reset (DaisyUI puts divider styles in `@layer daisyui.l1.l2.l3`)

A palette file contributes **only** `--color-*` / radius / size tokens. Anything shared
belongs in `_shared.css`, so adding a palette never duplicates the fonts or the battery
colours. Exactly one theme may carry `default: true` — apex holds it.

`halloween` is the odd one. It is a DaisyUI builtin, registered in `input.css`'s
`@plugin "./daisyui.mjs" { themes: halloween; }` block. Its file is therefore a plain
UNLAYERED `[data-theme="halloween"]` rule, **not** a `@plugin` block — a `@plugin` block
would fight the builtin's own registration (MAG-49). Its `@import` must come after the
builtin is registered.

## Self-hosted fonts (RD11)

**Inter** (400/700/800) and **JetBrains Mono** (500) are self-hosted as four woff2 files
under `static/fonts/` — about 94 KB total, OFL-licensed, taken from the Fontsource
`font-files` repo. Latin subset only. Added alongside the Stitch `design.md` port
(2026-08-19) so the Apex typography spec is live, not just documented.

DaisyUI v5's body rule reads `var(--default-font-family, <system stack>)`, so setting that
token in `_shared.css` cascades to every component with **no per-template edit** for the
body font.

The mono font reaches the page through Tailwind's `font-mono` utility, which resolves
`var(--font-mono)` to JetBrains Mono via the same override. Where it is applied today:

- the `ui/` wrappers `ui.Input`, `ui.Select`, `ui.Textarea`, `ui.Badge`, and
  `ui.StatTile`'s `stat-value` — the roles `design.md` calls "technical labels / values /
  status labels"
- inline in `fragments/nav_header.templ`, on the battery readout

Note the second bullet: `font-mono` is **not** confined to `ui/`. `make ui-guard` blocks
raw DaisyUI *component* classes in pages and fragments and explicitly allows Tailwind
utilities inline, so nothing stops a fragment from using it. Prefer the `ui/` wrapper when
one fits, but do not describe this as a guarded boundary — it is not one.

## Colour vocabularies that never change per theme

Two groups of tokens are **state** vocabularies, not decoration. A palette changes what
"action" looks like; it must not change what "critically low" or "failed" looks like.

**The battery scale** — red (0–10%), orange (11–20%), yellow (21–40%), green (41%+).
Defined once in `_shared.css`, read once through `ui.BatteryBandClass`. The corollary binds
every new theme: **keep the primary out of the red/orange/yellow/green band**, or one
colour will mean two things. Apex violates this for its *primary* (red collides with
battery-low); `graphite.css` exists as the accessible alternative and documents the
measured contrast per token.

**The four status colours (MAG-49)** — `error` is red, `warning` amber, `success` green, in
EVERY theme. Re-hueing them per palette would make a failure read as decoration.
**`info` is the sole exception**: it warns of nothing, so it carries no convention to
protect, and it is where a theme shows its identity — apex `#7c8cff` (its accent,
lightened), graphite `#22d3ee` (its accent), halloween `#c084fc` (its secondary; its accent
is green and would collide with success). Every status colour is measured in BOTH alert
styles and must clear WCAG AA (4.5:1); the previously inherited DaisyUI default `#2563eb`
failed at 3.94:1.

## How maintenance works

### Adding a theme — four steps (RM42 D10)

1. New `internal/gateway/static/themes/<name>.css` — one `@plugin` block, mirror
   `graphite.css`. (A *builtin* being corrected rather than a new palette takes the
   `halloween.css` shape instead: a plain unlayered `[data-theme="<name>"]` rule.)
2. One `@import "./themes/<name>.css";` line in `internal/gateway/static/input.css`.
3. Add `"<name>"` to `ui.Themes` (`internal/gateway/templates/ui/theme.go`).
4. `make css`.

### Switching

Since `RM42-gateway-add-theme-selector`, `data-theme` is resolved **per request** from
`ui.ThemeFromContext(ctx)` in `baseShell`, not a literal in source. A signed-in user
changes their own theme on `/settings`; an anonymous visitor or a just-logged-out user gets
whatever the `theme` cookie last recorded. There is no source-edit step for switching
between themes that already exist. Full steps: root `README.md` §"Switching the theme".

The instant DOM swap that runs before the save returns is RD15 — see
`architecture/gateway-client-side-js.md`.

### `make theme-guard`

Wired into `make check`. It fails if `ui.Themes`, `internal/account`'s own `Theme*`
constants, and `input.css`'s registered themes ever disagree.

Reconciling three independent copies was chosen over parsing `input.css` at build or run
time because `halloween` lives in the `@plugin { themes: ... }` block while
`apex` / `graphite` live in `@import` lines — two shapes a parser would need to
special-case. Three guarded copies keep the failure mode a clear, localized `make check`
error instead of a silently wrong dropdown at runtime (design.md D5,
`RM42-gateway-add-theme-selector`). Escape hatch: a trailing `// theme:allow: <reason>`
comment on the same line as a Go-side entry deliberately excluded from a comparison.

## Conventions & gotchas

- **Never add an external CDN link.** This rule outlives typography and stays in
  `AGENTS.md` for that reason. Every asset — `htmx.min.js`, `app.css`, the fonts — is
  committed and `//go:embed`-ed.
- **DaisyUI v5 drops font tokens set inside a `@plugin` block.** Values for
  `--default-font-family` / `--default-mono-font-family` written there are silently
  replaced with `sans-serif` / `monospace`. A plain unlayered CSS rule in `_shared.css`
  wins over DaisyUI's `@layer base` output, so the tokens actually resolve. This is a
  documented DaisyUI v5 quirk, not a Tailwind v4 bug.
- **The font selector is attribute-only `[data-theme]`, never `[data-theme="apex"]`.** The
  fonts are shared by every palette, so keying them to one theme name would silently drop
  them the moment the theme changes. Same specificity (0,1,0), still unlayered.
- **Two different defaults, both correct.** `apex.css` carries the CSS `default: true`;
  `account.settings.theme` defaults to `graphite` for a new account. The CSS default only
  decides what an unset `data-theme` renders as.
- **`input.css`'s header comment is stale.** It still describes switching as editing one
  `data-theme` attribute in `base.templ` followed by `make templ && make css`. That was
  true before RM42. The per-request resolution above is what the code does.
- **This is not an opening for arbitrary self-hosted fonts.** Like RD9/RD10, RD11 is a
  narrow, sanctioned decision — two families, four weights, pinned to the Apex `design.md`.
  A third family or more weights needs its own RD entry per RD8, with its own rationale and
  rejected alternative. Ship other subsets (cyrillic, etc.) only when a real page needs
  them; do not front-load them. Fonts live under `static/fonts/` so the existing
  `//go:embed static` picks them up with no embed directive change — never put a font
  anywhere else.

## Rejected alternatives

- **Google Fonts `<link>`.** Adds a runtime CDN dependency to a stack that explicitly bans
  CDNs, plus a privacy surface (a third-party font fetch per page view) and a single point
  of failure for the page's typography. Self-hosting keeps the deploy self-contained and
  adds ~94 KB of binary, committed and cacheable forever — the `@font-face` URLs are
  content-addressed by filename.
- **System stack only, no web fonts.** Cheaper, but the Apex `design.md` spec pins Inter +
  JetBrains Mono as part of the brand ("technical precision", "engineered aesthetic"). The
  system fallbacks (San Francisco / Segoe UI / Roboto) are visually close to Inter but are
  not Inter, and there is no system equivalent of JetBrains Mono's character for the
  "technical label" role. The cost is acceptable for a dashboard app: loaded once, with
  `font-display: swap` so text paints immediately in the fallback and reflows minimally.
- **Parsing `input.css` to derive the theme list.** See `make theme-guard` above.

## Related KB

- `architecture/gateway-client-side-js.md` — RD15, the instant theme swap in `app.js`.
- `entities/account-settings/guide.md` — `account.settings.theme`, the stored preference.
