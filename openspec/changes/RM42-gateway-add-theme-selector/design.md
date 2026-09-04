# Design — RM42-gateway-add-theme-selector

Tier 2 of `RM42-settings-theme-selector` (MAG-43). Implements roadmap decisions D1, D6–D11.
D2–D5 and D9 (the `account.settings` schema) belong to tier 1, already shipped and archived —
this tier consumes `account.Service.PreferencesFor`/`ThemeFor`/`SetTheme` through the interface
only and does not revisit their shape.

**No database object is touched by this tier.** The `database` design gate (`CLAUDE.md`
"Design-Gates") therefore does not apply here — it was already exercised at tier 1's design
gate for the `account.settings` table this tier consumes.

---

## D1 — The middleware/context decision

**Decision: rename `LanguageMiddleware` → `PreferencesMiddleware`, and have it resolve BOTH
language and theme from the SAME `account.Service.PreferencesFor` call.**

Today `LanguageMiddleware` (`internal/gateway/handlers/lang.go`) is the only place any gateway
request reads a signed-in user's preference from the database — once per request, via
`acct.LanguageFor(ctx, uid)`. Tier 1 built `PreferencesFor(ctx, accountID) (Settings, error)`
specifically so a caller needing both values pays for one query, not two — its own doc comment
says so directly. The only correct way to honor that is for THIS middleware, the request's
single preference-resolution point, to call `PreferencesFor` once and populate two context
slots from the one result:

```go
func PreferencesMiddleware(acct account.Service) gin.HandlerFunc {
    return func(c *gin.Context) {
        var lang, theme string

        if uid, ok := currentUID(c); ok {
            resolved, err := acct.PreferencesFor(c.Request.Context(), uid)
            if err != nil {
                lang, theme = account.LanguageES, ui.DefaultTheme
            } else {
                lang, theme = resolved.Language, resolved.Theme
            }
            if cookieVal, cerr := c.Cookie(langCookieName); cerr != nil || cookieVal != lang {
                setLangCookie(c, lang)
            }
            if cookieVal, cerr := c.Cookie(themeCookieName); cerr != nil || cookieVal != theme {
                setThemeCookie(c, theme)
            }
        } else {
            lang = normalizeLang(cookieOrDefault(c, langCookieName))
            theme = normalizeTheme(cookieOrDefault(c, themeCookieName))
        }

        ctx := i18n.WithLang(c.Request.Context(), lang)
        ctx = ui.WithTheme(ctx, theme)
        c.Request = c.Request.WithContext(ctx)
        c.Next()
    }
}
```

(`cookieOrDefault` is a two-line helper factoring the existing anonymous-branch shape used
twice above; not a new concept — `LanguageMiddleware`'s anonymous branch already reads exactly
this way for `lang`.)

**Context plumbing: a sibling key, not an extension of `i18n`.** `i18n.WithLang`/
`i18n.FromContext` stay untouched — theme is not a translated string (roadmap D11: theme names
render as-is, never through `i18n.T`), so folding it into the *translation* catalogue's context
carrier would blur that package's stated single job ("closed translation vocabulary and
per-request language carrier," `i18n.go`'s own package doc). Instead, `ui.WithTheme`/
`ui.ThemeFromContext` are a new, structurally IDENTICAL pair — unexported `ctxKey struct{}`,
comma-ok type assertion, default-on-anything-else — colocated in `internal/gateway/templates/ui/
theme.go` next to `ui.Themes` itself (D10 pins the vocabulary's package as `ui`; the context
carrier that reads it belongs in the same file for the same reason `i18n.go` keeps its
vocabulary and its carrier together). `layouts.baseShell` already imports `ui` for
`ui.ConfirmDialog`/`ui.Icon`/`ui.LangSwitcher`, so reading `ui.ThemeFromContext(ctx)` from
`base.templ` adds no new import edge.

One boundary check: `internal/gateway/templates/ui/ui.go`'s package doc states "Presentation
only: no domain imports, no business logic." `ui.Themes`/`ui.DefaultTheme` stay plain string
literals (`"apex"`, `"graphite"`, `"halloween"`) — never `account.ThemeApex` etc. — for exactly
that reason, mirroring how `account.go`'s own doc comment already describes the two copies as
deliberately independent ("this module validates against its own copy of the same closed set").
`ui/theme.go` therefore imports nothing from `internal/account`.

**Rejected alternative: a second `ThemeMiddleware` calling `acct.ThemeFor(...)`.** This was the
default temptation and is explicitly the thing this tier must NOT do. It would add a SECOND
DB read to every signed-in request — the exact regression the roadmap's "Out of scope" section
rules out by name ("this roadmap must not regress the current cost... it stays at ONE query per
request, one query both values"). It would also duplicate the cookie-sync branching structure
`LanguageMiddleware` already has, doubling the surface a future bug can hide in for no benefit.

**Anonymous branch: still zero DB calls.** The anonymous branch reads two cookies and performs
no database access — unchanged from today's `lang`-only behavior, just widened to a second
cookie of the same shape. Both `normalizeLang` and the new `normalizeTheme` are pure, in-memory
functions.

**Downstream consumers never re-call `PreferencesFor`.** `pages.SettingsPage`'s handler reads
the current theme via `ui.ThemeFromContext(c.Request.Context())` — the same value the middleware
already resolved for this request — never a second `acct.PreferencesFor`/`acct.ThemeFor` call.
This is asserted directly in the Test Contract (item 9) via a call-counting fake, not left as an
unverified claim.

---

## D2 — The cookie design

`theme` mirrors `lang` attribute-for-attribute:

| Attribute | `lang` (existing) | `theme` (new) |
|---|---|---|
| Name | `lang` | `theme` |
| Max-Age | `365 * 24 * 60 * 60` (1 year) | same |
| Path | `/` | same |
| SameSite | `Lax` (set via `c.SetSameSite` BEFORE `c.SetCookie` — gin's `SetCookie` has no SameSite parameter) | same |
| HttpOnly | `true` | same |
| Secure | `false` (matches the project's local-http default; `true` behind HTTPS in production, unchanged from `lang`) | same |

`setThemeCookie(c *gin.Context, theme string)` is a direct structural copy of `setLangCookie`,
in the new `preferences.go` (see D1). `HttpOnly=true` is correct here for the same reason it is
correct for `lang`: no client-side JS ever needs to *read* this cookie — the RD15 listener (D6
below) reads and writes `document.documentElement.dataset.theme`, a DOM attribute, never
`document.cookie`. The cookie exists purely so the SERVER can resolve the right theme on the
next request; the JS-side instant apply is a separate, parallel mechanism that doesn't touch it.

**Is the anonymous read path meaningful, given the switcher only renders on the authenticated
`/settings` page?** Yes, for the same reason the `lang` cookie's anonymous path matters even
though `LangSwitcher` (mounted on `Base`) is the only place that writes it before login: the
value this cookie carries is READ on every `Base`-shell page too, not only `BaseAuth` ones. A
user who set their theme on `/settings`, then logs out, still lands on the `Home`/`Login` pages
with their chosen palette — the cookie is what carries the preference across that boundary,
exactly as `lang` already does. The asymmetry is real but narrow: `theme`'s WRITE path is
currently reachable only from an authenticated page (no anonymous UI control exists to change
it), while `lang`'s write path is reachable from both. Both cookies' READ path is exercised on
every page regardless of auth state. `ThemeSwitch` itself stays structurally permissive (no auth
guard — see D3) so this asymmetry is a UI-surface decision, not a handler-level restriction; a
future anonymous theme control (e.g. on the login page) would need no handler change.

---

## D3 — The CSRF posture

**Proposed: mirror the language switch's no-CSRF exception — `theme` cookie's `SameSite=Lax` is
the sole defence, no `csrf_token` check on `POST /ui/theme/switch`.**

The risk profile is structurally identical to `LangSwitch`'s, on every axis
`internal/gateway/AGENTS.md`'s "Exception: language switch" section uses to justify it:

1. **No auth guard, no redirect-to-login** — same reasoning: this endpoint must work for
   anonymous callers too (D2 above), so a redirect-to-login would defeat that path entirely.
2. **No tenant-ownership check** — `SetTheme(ctx, uid, theme)` always targets the CALLER'S OWN
   session `uid`. There is no user-submitted resource identifier (unlike D4's vehicle
   `(TeslaID, VIN)` pair) for a forged request to redirect at a different account.
3. **No CSRF token** — a forged cross-site request against this endpoint can only ever change
   the caller's own display theme: no data mutation beyond a cosmetic preference, nothing to
   exfiltrate, reversible in one click on `/settings`. This is the identical trade-off
   `AGENTS.md` already accepted for language, for the identical reason.
4. **Scope stays narrow** — only `SetTheme` is permitted under this exception; every other
   handler in this tier's slice not covered by an existing D4/D-lang exception is Reader-only
   (in fact `ThemeSwitch` is the only write this tier adds at all).

**Why this is a PROPOSAL, not yet a decision this tier is entitled to make unilaterally.**
`internal/gateway/AGENTS.md`'s own language-switch section is explicit: *"This trade-off was put
to the user and explicitly approved on 2026-08-13, conditional on `SameSite=Lax`. It is not a
worker's unilateral call."* That sentence describes exactly the situation here — the reasoning
transfers cleanly, but the sign-off does not. This design.md records the recommendation and its
full justification so the decision is ready to confirm, but implementation of `ThemeSwitch`
MUST NOT be treated as final until the user has explicitly approved the no-CSRF posture for
theme specifically (tasks.md T4 gates on this explicitly). If the user instead wants the
charging/D4 CSRF pattern applied here, the change is small — one `checkCSRFKey(c,
csrfThemeKey)` call plus issuing that session key from `SettingsPage` — but it also means
`SettingsPage` must mint a token even though it renders no other write-capable form, which is
the exact cost `AGENTS.md`'s language section weighs against requiring CSRF for a
mounted-everywhere control. That cost argument is weaker here (the theme switcher, unlike the
language one, mounts on exactly ONE page), so if CSRF is required after review, minting the
token only on `/settings` is cheap and does not generalize into every other Reader-only page
gaining a CSRF token the way the language exception worried about.

---

## D4 — `ui.ThemeSwitcher` + `ui.Themes`

```go
// internal/gateway/templates/ui/theme.go
package ui

// Themes is the closed, presentation-facing theme vocabulary (roadmap RM42 D10) — the single
// source the dropdown iterates AND make theme-guard reconciles against account's own copy and
// static/input.css's two registration shapes. Order is display order in the dropdown.
var Themes = []string{"apex", "graphite", "halloween"}

// DefaultTheme is returned by ThemeFromContext/IsSupportedTheme's normalization path for any
// value outside Themes — mirrors account.ThemeGraphite's value without importing internal/account
// (this package takes no domain imports; see ui.go's package doc).
const DefaultTheme = "graphite"

// IsSupportedTheme reports whether v is one of the three vocabulary codes.
func IsSupportedTheme(v string) bool {
    for _, t := range Themes {
        if v == t {
            return true
        }
    }
    return false
}
```

`ThemeSwitcherProps` — modeled directly on `LangSwitcherProps`, with one deliberate divergence:

```go
type ThemeSwitcherProps struct {
    Current string // one of ui.Themes; caller normalizes before passing in
}

templ ThemeSwitcher(p ThemeSwitcherProps) {
    <div class="dropdown">
        <div tabindex="0" role="button" class="btn btn-ghost btn-sm" aria-label={ i18n.T(ctx, i18n.KeyThemeSwitcherAria) }>
            { i18n.T(ctx, i18n.KeyThemeSwitcherLabel) }: { titleCase(p.Current) }
        </div>
        <ul tabindex="-1" class="dropdown-content menu bg-base-100 rounded-box z-1 w-40 p-2 shadow-sm">
            for _, t := range Themes {
                <li>
                    <button type="button" hx-post="/ui/theme/switch" hx-swap="none" hx-vals={ themeVals(t) }>
                        { titleCase(t) }
                    </button>
                </li>
            }
        </ul>
    </div>
}
```

(`titleCase`/`themeVals` are small unexported helpers in the same file — `titleCase` upper-cases
the first rune only, since roadmap D11 requires theme names render exactly as `Apex`/
`Graphite`/`Halloween`, not `strings.ToUpper` as `LangSwitcher` does for two-letter codes;
`themeVals(t)` returns the literal `{"theme":"apex"}`-shaped JSON string per option.)

**Why `Options`-less like `LangSwitcherProps`, but internally range over `Themes` where
`LangSwitcher` hardcodes two `<li>`s.** `LangSwitcherProps`'s own doc comment states its
omission of an `Options` field explicitly: the language set is FROZEN at exactly two by roadmap
decision D4 (RM24), and parameterizing a never-growing two-item list would be the
over-abstraction the project's AI-efficiency rule warns against — so `LangSwitcher`'s markup
hand-writes both `<li>`s. Theme is a different case on the SAME axis: the set is closed today at
three, but roadmap D10 spells out a standing four-step recipe for adding a fourth (or fifth) —
"adding a theme later is therefore four steps," step 3 being "add `<name>` to `ui.Themes`." A
component that hardcoded three `<li>`s would silently violate that recipe: a theme added to
`Themes` (steps 1–3 done) would not appear in the dropdown until someone remembered to
hand-edit `theme_switcher.templ` too — an undocumented fifth step contradicting D10's explicit
"four steps." Ranging over `Themes` inside the component is what keeps the recipe honestly at
four steps. `ThemeSwitcherProps` still has no `Options` FIELD (the caller passes only `Current`)
— the difference from `LangSwitcher` is entirely inside the component body, not in its Props
shape, so the two components stay structurally comparable at the call site.

---

## D5 — `make theme-guard`

Mirrors `boundary-guard`'s shape (a `@`-prefixed shell recipe under one Makefile target,
grep-based, one trailing escape-hatch comment convention) and wires into `check`'s prerequisite
list next to it: `check: build vet ui-guard i18n-guard money-guard tz-guard migration-guard
boundary-guard theme-guard test`.

**The three sources it reconciles**, each extracted with its own grep/sed shape because each is
a different file format — this is exactly why D10 rejected parsing `input.css` at runtime, and
it is also why the guard needs three extraction passes rather than one:

1. **`ui.Themes`** (`internal/gateway/templates/ui/theme.go`) — the Go slice literal. Extracted
   by isolating the `Themes = []string{...}` line and pulling every quoted identifier out of it:
   `grep -A1 'Themes = \[\]string{' internal/gateway/templates/ui/theme.go | grep -oE '"[a-z]+"' | tr -d '"' | sort -u`.
2. **`account`'s `Theme*` constants** (`internal/account/account.go`) — the domain module's own
   independent copy (tier 1). Extracted from the `const (...)` block declaring
   `ThemeApex`/`ThemeGraphite`/`ThemeHalloween`:
   `grep -E '^\s*Theme[A-Z][A-Za-z]*\s*=\s*"[a-z]+"' internal/account/account.go | grep -oE '"[a-z]+"' | tr -d '"' | sort -u`.
3. **`static/input.css`'s two registration shapes** — the `@plugin` block form (`halloween`,
   which has no theme file of its own) and the `@import` form (`apex`, `graphite`; excludes
   `_shared.css`, which is not a theme):
   ```sh
   plugin_themes=$(grep -oE 'themes:\s*[a-z, ]+;' internal/gateway/static/input.css \
       | grep -oE '[a-z]+' | grep -v themes)
   import_themes=$(grep -oE '@import "\./themes/[a-z]+\.css";' internal/gateway/static/input.css \
       | grep -oE '/[a-z]+\.css' | sed -E 's#/([a-z]+)\.css#\1#' | grep -v shared)
   css_themes=$(printf '%s\n%s\n' "$plugin_themes" "$import_themes" | sort -u)
   ```

The recipe computes all three sorted-unique lists, diffs `ui_themes` against `account_themes`
and against `css_themes` (`diff <(...) <(...)`), and fails with both lists printed on any
mismatch — mirroring `boundary-guard`'s "print the offending lines, explain the fix, name the
escape hatch, exit 1" message shape.

**Escape hatch:** a trailing `// theme:allow: <reason>` comment on the same line as a
Go-side entry that is deliberately excluded from one comparison (e.g. a theme intentionally
staged in `ui.Themes` before its CSS file lands) — same placement rule as `boundary-guard`'s
`// boundary:allow:` (both are `.go`/one-line-comment-capable contexts, so no above-the-line
placement wrinkle like `i18n-guard`'s `.templ` pass needs). `input.css` has no comment syntax
usable mid-line for this purpose either, so the hatch is only ever placed on the Go-side lines
being compared — consistent with `theme-guard` being a Go-vocabulary guard that happens to also
read CSS as one of its three inputs, not a CSS-authoring guard in its own right.

**Why not derive `ui.Themes` from `input.css` at build/run time instead of guarding two
independent lists.** This is roadmap D10's own rejection, restated for completeness: `halloween`
lives in the `@plugin { themes: ... }` block while `apex`/`graphite` live in `@import` lines —
two shapes a parser would need to special-case, staying fragile to any future CSS reshuffle, and
moving a drift error from build time (a failed `make check`) to run time (a silently wrong
dropdown). Three independent, guarded copies is the version whose failure mode is a clear,
localized `make check` error naming exactly which list disagrees.

---

## D6 — The instant-apply JS (RD15)

**What:** one delegated `click` listener on `document.body`, matching
`button[hx-post="/ui/theme/switch"]` (the same delegation-on-body shape RD9–RD14 all use, so a
freshly-swapped or freshly-rendered dropdown option needs no re-binding — though in practice the
switcher's own markup never gets swapped, since D6 explicitly avoids any htmx swap for this
control; the delegated shape is kept anyway for consistency with every other listener in this
file, not because this one specifically needs swap-survival).

```js
// RD15 — clicking a theme option applies it to the DOM immediately, before the
// background hx-post (hx-swap="none") that persists it has resolved. D6 explicitly
// rejects HX-Location/any reload here: a theme is pure CSS, so there is nothing to
// re-render, unlike the language switch's server-rendered text.
document.body.addEventListener("click", function (evt) {
  var btn = evt.target.closest('button[hx-post="/ui/theme/switch"]');
  if (!btn) return;
  var vals;
  try {
    vals = JSON.parse(btn.getAttribute("hx-vals") || "{}");
  } catch (e) {
    return;
  }
  var theme = vals.theme;
  if (!theme) return;
  document.documentElement.dataset.themePrevious = document.documentElement.dataset.theme;
  document.documentElement.dataset.theme = theme;
});

// If the background persist request failed, revert to the value the DOM held before
// the optimistic apply above — see design.md D6 for why reverting is correct here.
document.body.addEventListener("htmx:afterRequest", function (evt) {
  var elt = evt.detail.elt;
  if (!elt || !elt.matches || !elt.matches('button[hx-post="/ui/theme/switch"]')) return;
  if (evt.detail.successful) {
    delete document.documentElement.dataset.themePrevious;
    return;
  }
  var prev = document.documentElement.dataset.themePrevious;
  if (prev) document.documentElement.dataset.theme = prev;
  delete document.documentElement.dataset.themePrevious;
});
```

Reading `theme` out of the button's own `hx-vals` JSON (rather than a duplicate
`data-theme="apex"` attribute) means the value the server receives and the value the DOM applies
come from ONE literal per option, authored once in `theme_switcher.templ` — no second place to
keep in sync.

**On a failed POST, the design REVERTS the optimistic DOM change, and does not leave it in
place.** The alternative — leaving the clicked theme applied even though it never persisted —
was rejected: the server is still the source of truth for what renders on the NEXT full page
load (via `PreferencesMiddleware` → `data-theme` on `base.templ`), so a user who saw `apex`
applied instantly but whose write failed would see it silently flip back to their old theme on
their very next navigation, with no explanation. Reverting immediately, in the same interaction,
is the same philosophy `LangSwitch`'s handler already applies for its own failure case ("The
write failed: do NOT still send `HX-Location`, so the client does not reload into a state the
persisted write never actually reached") — this tier's JS-side revert is that same rule's
client-side mirror, made necessary here specifically because D6's optimistic apply happens
BEFORE the server confirms anything, which `LangSwitch`'s server-driven `HX-Location` never did.

**Why not a CSS-only pattern.** Same category of rejection as RD10/RD12: this is a genuine value
write (`document.documentElement.dataset.theme = theme`) that must happen synchronously on
click, strictly before — not depending on — any network round trip. No DaisyUI `dropdown`/
`<dialog>`/`collapse` primitive can express "write this DOM attribute the instant this element
is activated."

**Why it does not erode the `ui/` boundary.** The listener reads `hx-vals` JSON and writes one
`data.theme` dataset property — no DaisyUI class string, no markup, no styling decision made in
JavaScript. Identical shape to RD13/RD14's own boundary argument.

**Graceful degradation.** Without JS (or in a browser where it errors), the `click` handler
never fires: the `hx-post` still goes through via `htmx.min.js` alone (unaffected — it is a
standard form-less button POST), `SetTheme` still persists the choice, and the user sees their
new theme on the NEXT full page load once `PreferencesMiddleware` resolves it server-side. The
only thing lost is the "instant" half of D6 — degraded to "next navigation," never broken.

---

## D7 — Halloween manual check (roadmap decision D7)

Not a design decision to make — a task to perform and record. Roadmap D7: "Tier 2 carries an
explicit task to check the dashboard charts and tiles under `halloween` and report anything that
looks broken — a report, not a redesign." `tasks.md` T7 captures this as a standalone,
non-automatable task (visual inspection with the dev server running, `data-theme="halloween"`
forced via the new `/settings` page); its findings are recorded in this change's final worker
report and, if anything is broken, filed as a follow-up rather than fixed inline here unless
trivial (a one-line class fix). This section exists so a future reader of this design.md finds
the task's rationale without re-opening the roadmap.

---

## D8 — Docs to update in this same change

Per `CLAUDE.md`'s "docs track structural change" and "workflow & architectural decisions are
documented with their steps" rules:

- **`internal/gateway/AGENTS.md`**
  - New **RD15** section (D6 above), inserted after the existing RD14 section, in the same
    What/Why/Boundary/Graceful-degradation shape as RD9–RD14.
  - The two "exactly **FIVE** sanctioned exceptions" mentions (near line 200 and line 803)
    become "exactly **SIX**," both updated in the same change — a stale count here is exactly
    the kind of doc drift `CLAUDE.md`'s docs rule exists to prevent.
  - A new subsection near the existing "Theme file layout & switching" material, documenting
    roadmap D10's four-step "add a theme" recipe verbatim (new CSS file → `@import` line →
    `ui.Themes` entry → `make css`), since D10 requires it recorded in AGENTS.md specifically,
    not only in README.
  - Document the `PreferencesMiddleware` rename, the new `theme` cookie, and (once the D3
    sign-off lands) the finalized CSRF posture — mirroring the existing "Exception: language
    switch" subsection's shape, added as a new "Exception: theme switch" subsection referencing
    it rather than duplicating its prose.
  - `Deps`/"Public interface" section needs NO new entry — this tier adds no new module
    dependency; `account.Service` is already listed.
- **Root `README.md`, "Switching the theme" section** — rewritten. The mechanism it documents
  today (hand-edit `base.templ` line 17's hardcoded `data-theme`, then `make templ && make
  css`) stops existing the moment this tier ships: the attribute is now per-user and set by
  `/settings`, not a literal in source. The section becomes: (1) how a user changes their own
  theme (visit `/settings`), (2) the still-accurate "adding a new palette" recipe, extended with
  the two steps D10 adds beyond the CSS file (`ui.Themes` entry, `make theme-guard` as a new
  verification step alongside the existing `make templ && make css`).
- **Root `README.md`, "Project Structure" tree** — no update needed. This tier adds files
  within already-listed directories (`templates/ui/`, `handlers/`, `templates/pages/`) — the
  tree lists directories, not individual filenames, and no new package/module is introduced.

---

## Test Contract

Authored up front, before implementation, per `ai/go-conventions.md`'s "author their expected
values up front" rule. `tasks.md` cites these item numbers in its acceptance criteria.

1. **`normalizeTheme`** — `"apex"`→`"apex"`, `"graphite"`→`"graphite"`, `"halloween"`→
   `"halloween"`; `""`, `"cyberpunk"`, `"APEX"` (case-sensitive, no fuzzy match) all →
   `ui.DefaultTheme` (`"graphite"`).
2. **`ui.IsSupportedTheme`** — true for all three codes; false for `""`, an unsupported code,
   and a case-mismatched code.
3. **`PreferencesMiddleware`, signed-in, happy path** — fake `PreferencesFor` returns
   `{Language: "en", Theme: "apex"}` → request context resolves `i18n.FromContext(ctx) == "en"`
   AND `ui.ThemeFromContext(ctx) == "apex"` from the SAME call (assert `PreferencesFor` called
   exactly once for the request, not `LanguageFor`/`ThemeFor` separately).
4. **`PreferencesMiddleware`, signed-in, `PreferencesFor` errors** — context resolves
   `es`/`graphite` (both defaults), no panic, request proceeds.
5. **`PreferencesMiddleware`, signed-in, cookie sync** — DB says `theme="apex"`, incoming
   `theme` cookie is absent or says `"graphite"` → response sets a fresh `theme=apex` cookie
   (mirrors existing `TestLanguageMiddleware_SyncsStaleCookieToDBValue`, added as
   `TestPreferencesMiddleware_SyncsStaleThemeCookieToDBValue`); a cookie already matching the
   resolved value triggers no `Set-Cookie`.
6. **`PreferencesMiddleware`, anonymous** — no `PreferencesFor` call at all (fake asserts zero
   calls); `theme` cookie present and valid → context carries that value; absent/invalid →
   `ui.DefaultTheme`.
7. **`ThemeSwitch`, anonymous** — valid `theme` form value → `theme` cookie set, `SetTheme` NOT
   called (fake asserts zero calls), `200` response with **no** `HX-Location` header (contrast
   with `LangSwitch`, which always sets one).
8. **`ThemeSwitch`, signed-in, happy path** — valid `theme` → cookie set AND
   `SetTheme(ctx, uid, theme)` called with the exact submitted value, `200`, no `HX-Location`.
9. **`ThemeSwitch`, unsupported value** — `theme=cyberpunk` → `400`, no cookie set, `SetTheme`
   NOT called.
10. **`ThemeSwitch`, `SetTheme` returns an error** — `500`; the `theme` cookie WAS already set
    (mirrors `LangSwitch`'s "always set the cookie first" ordering) even though the DB write
    failed.
11. **`SettingsPage`, unauthenticated** — redirects to `/login`, no render.
12. **`SettingsPage`, authenticated** — `200`; renders `ui.ThemeSwitcher` with `Current` equal
    to the request context's already-resolved theme; **`PreferencesFor` is called exactly ONCE
    for the whole request** (asserted via the shared middleware call, not a second call from the
    handler) — this is the test that directly proves the "ONE query per request" invariant holds
    for the settings page specifically, not just for pages that don't need theme.
13. **`nav.go`, `navItems`** — the Settings entry has `Placeholder: false`, `Href: "/settings"`,
    and `Active: true` when `active == "/settings"`, `false` otherwise (extends the existing
    nav-items test table with one more row rather than a new test function).
14. **`base.templ` / `BaseAuth`+`Base`** — rendering with `ui.WithTheme(ctx, "apex")` on the
    context produces `data-theme="apex"` in the output HTML (both anonymous `Base` and
    authenticated `BaseAuth` paths, since `baseShell` is shared by both).
15. **i18n catalogue** — the four new keys (`KeyThemeSwitcherLabel`, `KeyThemeSwitcherAria`,
    `KeyThemeSwitchErrorUnsupportedTheme`, `KeyThemeSwitchErrorCouldNotSaveTheme`) are covered
    by the EXISTING `TestCatalog_AllKeysHaveBothLanguages` — no new test needed, only new map
    entries; theme NAME strings (`Apex`/`Graphite`/`Halloween`) are never catalogue keys
    (roadmap D11) and so are explicitly out of that test's scope.
16. **`ui.ThemeSwitcher`** — renders exactly `len(ui.Themes)` `<li>` options in `ui.Themes`
    order, each with `hx-vals` carrying that option's own theme code, and the trigger's visible
    text includes the title-cased `Current` value.
17. **`make theme-guard`, passing case** — run against the repo as this tier leaves it: exits
    `0`. (Exercised by running the target directly per `Test-Execution-Policy` — this is a
    Makefile guard, not a `go test`, so it is one of the cheap signals the assistant runs itself,
    not deferred to the owner.)
18. **`make theme-guard`, failing case** — a deliberately introduced one-item mismatch (e.g.
    `ui.Themes` temporarily missing `"halloween"`) makes the target exit non-zero and name the
    disagreeing list. This is a manual verification step during implementation (temporarily
    break, confirm the failure, restore) — not a checked-in automated test, since the guard
    itself IS the automation.
19. **RD15 client-side behavior** — not covered by `go test` (no JS test harness exists in this
    project for RD9–RD14 either). The exact behavior (optimistic apply on click, revert on
    `htmx:afterRequest` failure) is the AGENTS.md RD15 prose itself, verified manually in a
    browser during implementation; this is a documented, not automated, contract item.

---

## Summary of new/changed files

| File | Change |
|---|---|
| `internal/gateway/templates/ui/theme.go` | new — `Themes`, `DefaultTheme`, `IsSupportedTheme`, `WithTheme`, `ThemeFromContext` |
| `internal/gateway/templates/ui/theme_switcher.templ` | new — `ui.ThemeSwitcher` |
| `internal/gateway/handlers/preferences.go` | new — `PreferencesMiddleware` (moved+renamed from `lang.go`), `themeCookieName`/`themeCookieMaxAge`/`normalizeTheme`/`setThemeCookie`, `ThemeSwitch` |
| `internal/gateway/handlers/lang.go` | `LanguageMiddleware` removed (moved to `preferences.go` as `PreferencesMiddleware`); everything else unchanged |
| `internal/gateway/templates/layouts/base.templ` | `data-theme` reads `ui.ThemeFromContext(ctx)` |
| `internal/gateway/templates/layouts/nav.go` | Settings entry: `Placeholder` dropped, `Href`/`Active` added |
| `internal/gateway/templates/pages/settings.templ` | new — `pages.SettingsPage` |
| `internal/gateway/gateway.go` | middleware registration renamed; `GET /settings`, `POST /ui/theme/switch` routes added |
| `internal/gateway/static/app.js` | RD15 listeners appended |
| `internal/gateway/i18n/catalog.go` | 4 new keys |
| `Makefile` | new `theme-guard` target; `check` prerequisite list gains it |
| `internal/gateway/AGENTS.md` | RD15, FIVE→SIX fixes, D10 recipe, theme-switch exception subsection |
| `README.md` | "Switching the theme" section rewritten |
