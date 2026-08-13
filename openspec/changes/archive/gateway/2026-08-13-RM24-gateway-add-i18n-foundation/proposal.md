Source: MAG-8 — https://linear.app/magus-monitor/issue/MAG-8/i18n-translations-htmx
Roadmap: openspec/roadmaps/RM24-i18n-translations.md
Tier: 2 of 3 (gateway; depends on tier 1 `RM24-account-add-language-preference`, archived;
tier 3 `RM24-gateway-translate-all-pages` depends on this tier)

## Why

MAG-8 makes the htmx web layer bilingual Spanish (default) / English. Tier 1 gave the `gateway`
module a place to read and persist a signed-in user's language (`account.Service.LanguageFor` /
`SetLanguage`, always exactly `"es"` or `"en"`). This tier is the **design-bearing** gateway
tier: it decides the translation catalogue's shape, how a request's resolved language reaches
every Templ component without breaking the gateway boundary or the logic-free-template
invariant, and how the navbar switch works over htmx without disrupting the page the user is on
— then proves the whole mechanism on exactly one surface (the layout/nav shell) as the gold
standard tier 3 mirrors mechanically across the rest of the app. It does **not** translate the
whole app.

Full rationale for every decision below lives in `design.md`, split into D1–D9.

## What Changes

- **New package `internal/gateway/i18n`** — the translation catalogue: a closed `Key` constant
  vocabulary mapped to `{ES, EN}` string pairs in a single Go source map, plus `WithLang`/
  `FromContext`/`T(ctx, key)` for context-carried language resolution. See `design.md` D1/D2 for
  the format decision (and the four alternatives rejected: `golang.org/x/text/message`, embedded
  JSON/TOML, per-page structs, two parallel `map[string]string`) and the missing-key behavior
  (per-key: a visible `!!key` marker; per-language-within-a-key: fall back to `ES`).
- **Per-request language resolution middleware** (`internal/gateway/handlers`, registered in
  `gateway.go` immediately after the sessions middleware) — resolves the active language exactly
  once per request: `account.Service.LanguageFor` for a signed-in session, a `lang` cookie for an
  anonymous one, falling back to `"es"` for an absent or unrecognized value either way. The
  resolved value is stored via `i18n.WithLang` on the request's `context.Context` (not gin's
  key/value store), so every `render`/`renderFragment` call — which already passes
  `c.Request.Context()` into the Templ component tree — carries it to every nested component
  automatically. See `design.md` D3/D5.
- **Cookie ↔ DB synchronization** — an authenticated render whose DB value differs from the
  incoming cookie refreshes the cookie (so a later logout carries the current choice forward);
  `GoogleCallback` propagates an explicitly-present `lang` cookie into the new/resolved account
  via `SetLanguage` so a language picked before login carries into the session. See `design.md`
  D4.
- **`ui.LangSwitcher`** — a new `templates/ui/` wrapper (globe icon, current language code,
  CSS-only DaisyUI `dropdown`/`dropdown-end`, zero client JS) mounted **once** in `layouts.Base`
  (mirrors the RD10 `ui.ConfirmDialog` precedent), so Home, Login, and every `BaseAuth` page
  inherit it automatically with no per-page markup. A new `"globe"` case is added to the existing
  closed `ui.Icon` vocabulary. See `design.md` D6.
- **`POST /ui/lang/switch`** — the switch handler. No auth guard (works for anonymous and
  signed-in callers), sets/refreshes the `lang` cookie, calls `account.Service.SetLanguage` when
  signed in, and responds with an `HX-Location` header targeting the caller's current path+query
  (from the `HX-Current-URL` htmx request header) with `push:"false"` — an hx-boost-style AJAX
  re-fetch of the same page in the new language, no hard reload, no parallel page-dispatch table.
  Deliberately carries **no CSRF check** — see `design.md` D8 for the justification and its
  trade-off, and the new `internal/gateway/AGENTS.md` write-exception amendment this requires.
- **The layout/nav shell fully translated** as the gold-standard surface: sidebar nav item
  labels, the "Soon" placeholder badge, the sidebar open/close aria-labels, `ui.NavLogout`'s "Log
  out", and the nav-header's connect-prompt + status words. The nav-header's `StatusLabel` field
  is **removed** from `NavHeaderVM` (was a handler-computed English string) in favor of a
  template-side `i18n.T(ctx, statusLabelKey(vm.Status))` lookup keyed off the existing `Status`
  enum — see `design.md` D9. `LastSeenLabel`'s relative-time phrasing ("2 days ago") and
  `BatteryPct`'s formatting are explicitly **out of scope** here (pluralization is a bigger,
  separate concern; deferred to tier 3).
- **Documentation rule** — `internal/gateway/AGENTS.md` gains a binding "i18n" section requiring
  both `es` and `en` catalogue entries for every new user-facing label added to any page, worded
  as binding for a dispatched worker (not advisory), per MAG-8 and the project's
  docs-track-change rule. `internal/gateway` has no `README.md` to update.
- **Root `README.md`** — the "Project Structure" tree gains a one-line entry for the new
  `internal/gateway/i18n/` package (docs-track-change rule: a new package is a structural change).
- **CI guard** — `make templ && make css` and committing the regenerated `internal/gateway/static/app.css`
  (the `dropdown`/`menu` classes the new switcher and its globe icon pull in are not necessarily
  new to the bundle, but the guard runs regardless per module convention).

**Not breaking.** No existing route's method or URL changes. `NavHeaderVM.StatusLabel` is removed,
which is a breaking change to that Go struct's field set — but `NavHeaderVM` is gateway-internal
(never exported outside `internal/gateway/templates/fragments`), so no external consumer is
affected; the only caller (`navHeaderFor`) is updated in the same change. `account.Service` is
consumed, not modified (tier 1 is closed). No database object is touched by this tier.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `gateway` capability only)

### Modified Capabilities

- `gateway`: new requirements for the translation catalogue + per-request resolution, the navbar
  language selector, and the switch endpoint; modified requirements for the authenticated
  navigation shell, the navigation vehicle header, navigation items, and Google Sign-In (the
  login-time cookie→DB sync).

## Impact

- `internal/gateway/i18n` — new package (catalogue + context helpers).
- `internal/gateway/handlers` — new resolution middleware + `LangSwitch` handler + cookie helpers;
  `navHeaderFor` loses its hardcoded `StatusLabel` strings; `navItems` gains a `ctx` parameter.
- `internal/gateway/templates/ui` — new `lang_switcher.templ`; `icon.templ` gains a `"globe"`
  case.
- `internal/gateway/templates/layouts` — `base.templ` mounts `ui.LangSwitcher` once; `nav.go`'s
  `navItems` reads labels via `i18n.T`.
- `internal/gateway/templates/fragments` — `nav_header.templ` reads its connect-prompt and status
  words via `i18n.T`; `nav_header.templ`'s Go-side `NavHeaderVM` loses `StatusLabel`.
- `internal/gateway/gateway.go` — registers the language middleware and the new route.
- `internal/gateway/AGENTS.md` — new "i18n" binding section + a new write-exception subsection
  (mirroring D4/`manualcharge`'s shape) documenting the `SetLanguage` call from `LangSwitch`.
- `README.md` — "Project Structure" tree gains the new `i18n/` line.
- No other module is touched. `internal/account` is consumed read/write through `account.Service`
  only (`LanguageFor`, `SetLanguage`) — no new method is requested from it.

**Read path affected:** `account.Service.LanguageFor` runs once per request for every
**signed-in** page render and htmx fragment (one indexed by-primary-key `SELECT`, already
accepted and justified at tier 1's design gate — see tier 1's `design.md` D2). Anonymous
requests add **zero** DB round trips (cookie-only). No new query is added to the account module by
this tier.
