Source: MAG-43 — https://linear.app/magus-monitor/issue/MAG-43/theme-selector-settings-page
Roadmap: openspec/roadmaps/RM42-settings-theme-selector.md
Tier: 2 of 2 (gateway; depends on tier 1 `RM42-account-add-settings-table`, archived)

## Why

Tier 1 gave the `account` module a real home for per-user preferences
(`account.settings`, one row per account) and a `PreferencesFor(ctx, accountID) (Settings,
error)` port that returns language AND theme in a single query. This tier is where MAG-43's
actual ask lands: a real `/settings` page, replacing the placeholder nav item, with a working
theme selector on it — `apex`, `graphite`, and `halloween`, applied instantly with no page
reload (roadmap D6), and the platform's `<html data-theme>` attribute reading a per-user
preference instead of the hardcoded `"graphite"` it carries today.

The MAG-8 language slice (`RM24-gateway-add-i18n-foundation`) is the file-for-file pattern to
mirror, per the roadmap's own proposal prompt: `handlers/lang.go`, `ui.LangSwitcher`, and the
`LanguageMiddleware` context plumbing. This tier does exactly that, with one structural
difference the roadmap's "Out of scope" section makes binding: `account.settings` returns
BOTH language and theme in one row, and this tier's whole point is to consume that pairing
correctly — the existing per-request middleware is the one and only place either preference is
read from the database, so adding theme to it must not turn one query into two. Full rationale
for every decision below lives in `design.md`, split into D1–D8.

## What Changes

- **`PreferencesMiddleware`** replaces `LanguageMiddleware` (renamed, same registration point
  in `gateway.go`, immediately after the sessions middleware). It resolves BOTH language and
  theme exactly once per request — `acct.PreferencesFor(ctx, uid)` for a signed-in caller (one
  query, unchanged from tier 1's contract), the `lang` and new `theme` cookies for an anonymous
  caller (zero DB calls, unchanged). Both values land on the request `context.Context`: language
  via the existing `i18n.WithLang`/`i18n.FromContext`, theme via a new, symmetric
  `ui.WithTheme`/`ui.ThemeFromContext` pair colocated with `ui.Themes` (design.md D1).
  **No handler or page calls `acct.PreferencesFor` a second time** — every consumer downstream
  of the middleware reads theme from context, which is what keeps the read-heavy performance
  profile's "ONE query per request, one query both values" invariant (roadmap "Out of scope")
  intact even though the settings page now exists.
- **`ui.Themes` + `ui.ThemeSwitcher`** — the closed presentation vocabulary
  (`{"apex","graphite","halloween"}`, `internal/gateway/templates/ui/theme.go`) and a new
  Templ dropdown component mirroring `ui.LangSwitcher`'s CSS-only DaisyUI `dropdown` shape,
  with an `Options`-less `Current string` prop that DOES range over `ui.Themes` internally
  (unlike `LangSwitcherProps`, which is deliberately frozen at two — see design.md D4 for why
  the two components diverge on this one point).
- **`GET /settings`** — a real page (`pages.SettingsPage`), replacing the placeholder nav
  entry. Auth-guarded like every other page; renders `ui.ThemeSwitcher` seeded from the
  request context's already-resolved theme (no extra read). No htmx fragment counterpart —
  the switcher's own POST is self-contained and does not swap the page.
- **`POST /ui/theme/switch`** — the switch handler (`handlers.ThemeSwitch`, new file
  `handlers/preferences.go`, alongside the renamed middleware). Mirrors `LangSwitch`'s
  anonymous/signed-in branching (no auth guard, no tenant-ownership check — `SetTheme` always
  targets the caller's own session uid) but returns a bare `200`/`4xx`/`5xx` with **no
  `HX-Location`** — D6 is explicit that a theme change never reloads or re-renders the page.
  CSRF posture: proposed to mirror the language switch's user-approved no-CSRF exception
  (`SameSite=Lax` on the `theme` cookie as the sole defence), for the same reason MAG-8's
  language exception was approved — a forged request can only change the caller's own cosmetic
  preference, is reversible in one click, and exfiltrates nothing. **This is a proposal, not a
  decision**: `internal/gateway/AGENTS.md`'s own language-switch section states in as many
  words that its CSRF omission "was explicitly approved... not a worker's unilateral call," so
  this tier's design.md flags the same sign-off as still outstanding for theme and must not be
  treated as settled until the user confirms it (design.md D3).
- **`theme` cookie** — name, 1-year max-age, `SameSite=Lax`, `HttpOnly=true`: identical
  attributes to `lang`. Its anonymous branch is exercised even though the switcher itself only
  renders on the authenticated `/settings` page: the cookie is what keeps a chosen theme
  applied on `Base`-shell pages (home, login) and after logout, exactly as `lang`'s anonymous
  cookie does today (design.md D2).
- **`data-theme` on `templates/layouts/base.templ`** reads `ui.ThemeFromContext(ctx)` instead
  of the hardcoded literal `"graphite"` — the one-line change every other piece of this tier
  exists to make possible.
- **Instant client-side apply (D6, RD15)** — a new delegated `click` listener in
  `static/app.js` (the sixth sanctioned zero-JS exception; RD9–RD14 are the first five) that
  sets `document.documentElement.dataset.theme` the instant a dropdown option is clicked,
  before the `hx-post` request the same click also triggers has resolved. On a failed POST
  (network error or non-2xx), the listener reverts `data-theme` to the value it held before the
  click — see design.md D6 for why reverting, not leaving the optimistic value in place, is the
  correct failure behavior here.
- **`make theme-guard`** — a new grep-based Makefile target, wired into `check` alongside
  `boundary-guard`/`i18n-guard`, reconciling THREE independent sources of the theme vocabulary:
  `ui.Themes`, `account`'s `Theme*` constants, and `static/input.css`'s two registration shapes
  (the `@plugin { themes: halloween }` block and the `@import "./themes/<name>.css"` lines).
  Escape hatch: `// theme:allow: <reason>`, mirroring `boundary-guard`'s convention exactly.
- **Nav item** — `nav.go`'s Settings entry drops `Placeholder: true`, gains
  `Href: "/settings"` and `Active: active == "/settings"` (roadmap D8).
- **i18n catalogue** — two new keys (the switcher's own label + its aria-label; theme NAMES
  are proper nouns and are never translated, roadmap D11) plus two error-response keys
  mirroring `KeyLangSwitchError*`.
- **Docs** — `internal/gateway/AGENTS.md` gains: the RD15 entry (and the "exactly FIVE" →
  "exactly SIX" count fix at both existing mentions), the four-step "add a theme" recipe
  (roadmap D10), and updated middleware/CSRF documentation. Root `README.md`'s "Switching the
  theme" section is rewritten — the mechanism it documents today (hand-edit `base.templ` line
  17) no longer exists once this tier ships; it becomes "change your preference on `/settings`"
  plus the still-accurate "adding a new palette" recipe, now mentioning `ui.Themes` and
  `make theme-guard` as the two additional steps a new palette requires beyond the CSS file.
- **D7 manual check** — tier 2 carries an explicit, non-automated task: view the dashboard's
  charts and tiles under `halloween` and report anything that looks broken. A report, not a
  redesign; findings are recorded in this change's final artifacts, not fixed here unless
  trivial.

**Not breaking.** `account.Service` is consumed, not modified (tier 1 is closed and archived).
`LanguageMiddleware`'s rename to `PreferencesMiddleware` is an unexported-surface change with a
single call site (`gateway.go`) and no external consumer. No database object is touched by this
tier — the `account.settings` schema and its `theme` column already exist and are already
readable/writable through the port tier 1 shipped.

**Affected modules:** `internal/gateway` (implements this tier). `internal/account` is affected
only as an already-implemented dependency (tier 1, consumed read/write through `account.Service`
only — no new method requested).

## Capabilities

### New Capabilities

(none — this proposal extends the existing `gateway` capability only)

### Modified Capabilities

- `gateway`: new requirements for the theme presentation vocabulary + switcher component, the
  per-request theme resolution (folded into the existing language-resolution requirement's
  single-query contract), the `/settings` page, the switch endpoint, and the instant-apply
  client-side behavior. Modified requirements for the base document shell (`data-theme` is no
  longer a hardcoded literal) and the navigation items list (Settings is no longer a
  placeholder).

## Impact

- `internal/gateway/templates/ui` — new `theme.go` (`Themes`, `DefaultTheme`,
  `IsSupportedTheme`, `WithTheme`, `ThemeFromContext`) and new `theme_switcher.templ`
  (`ui.ThemeSwitcher`).
- `internal/gateway/handlers` — `lang.go`'s `LanguageMiddleware` is removed; a new
  `preferences.go` holds the renamed `PreferencesMiddleware`, the theme cookie helpers, and
  `ThemeSwitch`. `lang.go` keeps everything else (`LangSwitch`, `syncLoginLanguageCookie`,
  `hxLocation`, `pathAndQuery`, `normalizeLang`, `setLangCookie`) unchanged.
- `internal/gateway/templates/layouts` — `base.templ`'s `data-theme` attribute becomes
  context-driven; `nav.go`'s Settings entry becomes a live link.
- `internal/gateway/templates/pages` — new `settings.templ` (`pages.SettingsPage`).
- `internal/gateway/gateway.go` — the middleware registration is renamed; two new routes
  (`GET /settings`, `POST /ui/theme/switch`).
- `internal/gateway/static/app.js` — one new delegated listener (RD15).
- `internal/gateway/i18n/catalog.go` — four new keys.
- `Makefile` — new `theme-guard` target, added to `check`'s prerequisite list.
- `internal/gateway/AGENTS.md`, root `README.md` — updated per the docs-track-change rule
  (see "What Changes" above for the exact sections).
- No other module is touched. `internal/account` is consumed read/write through
  `account.Service` only (`PreferencesFor`, `ThemeFor`, `SetTheme`, `SetLanguage`) — no new
  method is requested from it.

**Read path affected:** `account.Service.PreferencesFor` runs once per request for every
**signed-in** page render and htmx fragment — this REPLACES the existing once-per-request
`LanguageFor` call one-for-one (same query shape tier 1 already justified at its own design
gate), not an addition to it. Anonymous requests still cost zero DB round trips. No new query
is added to the account module by this tier, and no existing gateway request goes from one
query to two.
