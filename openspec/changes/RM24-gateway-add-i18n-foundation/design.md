## Context

Roadmap `RM24-i18n-translations` makes the htmx web layer bilingual Spanish (default) /
English. Tier 1 (`RM24-account-add-language-preference`, archived) added `accounts.language` and
two port methods on `account.Service`:

```go
const ( LanguageES = "es"; LanguageEN = "en" )
var ErrUnsupportedLanguage error

LanguageFor(ctx, accountID uuid.UUID) (string, error)   // always exactly "es" or "en";
                                                          // normalizes an unrecognized stored
                                                          // value to "es"; never errors on it
SetLanguage(ctx, accountID uuid.UUID, lang string) error // rejects anything outside {es,en}
                                                          // with ErrUnsupportedLanguage, no write
```

This tier is the **design-bearing** gateway tier (roadmap tiers table). It is not "translate the
app" — it is "decide the vocabulary, resolution mechanism, and switch UX once, correctly, and
prove them on one surface." Tier 3 then repeats the established pattern across every remaining
page with no new design decisions. Every decision below is written so tier 3 can follow it by
example rather than re-deriving it.

Performance profile: **read-heavy** (`ai/architecture.md` §7). Tier 1's `design.md` D2 already
accepted that `LanguageFor` is an unavoidable extra indexed by-primary-key `SELECT` on every
signed-in render, because there is no existing "loaded `Account`" object at the gateway layer to
piggyback on (the gateway's session stores only the account id). This tier is where that read
actually gets wired in — the discipline this design owes the performance profile is calling it
**exactly once per request**, never once per fragment or once per template.

## Goals / Non-Goals

**Goals:**
- A closed, small translation vocabulary an agent looks up by constant name instead of
  re-inventing per page (AI-efficiency: closed vocabularies).
- Exactly one DB read for language per signed-in request; zero for anonymous requests.
- A mechanism for carrying the active language into Templ components that does not force tier 3
  to touch every page's view-model struct and render call site (change-locality).
- A language switch that leaves the user on the same page, in the same state, wherever that is
  achievable without inventing a parallel routing table.
- One fully-translated surface (layout/nav shell) proving every piece works end to end.
- A binding documentation rule so the catalogue cannot silently drift back toward English-only.

**Non-Goals:**
- Translating any page body, form, or handler-produced string beyond the layout/nav shell —
  tier 3's job.
- A third locale, ICU plural rules, or a locale-negotiation header. The vocabulary is fixed at
  exactly `{es, en}` by roadmap decision D4; anything more general is scope creep this ticket
  does not ask for.
- Any change to `account.Service`'s public surface — tier 1 is closed; this tier only consumes
  `LanguageFor`/`SetLanguage`.
- Any database object. This tier touches no schema, no migration, no `sqlc` query.

## Decisions

### D1 — Catalogue format: a single Go `map[Key]entry{ES, EN}` in a new `internal/gateway/i18n` package

```go
package i18n

type Key string

type entry struct{ ES, EN string }

const (
    KeyNavDashboard Key = "nav.dashboard"
    // ...
)

var catalog = map[Key]entry{
    KeyNavDashboard: {ES: "Panel", EN: "Dashboard"},
    // ...
}
```

**Why this shape.** A `Key` constant per string plus a map literal that puts both languages
**on the same line** is the format that makes the AGENTS.md "both ES and EN" rule
mechanically easy to follow and easy to review: a reviewer (human or agent) sees both strings in
one diff hunk, and a missing language is a visibly empty field, not a separate file or a separate
PR to cross-reference. It needs no encoding/decoding step (no JSON/TOML unmarshal error path to
handle at startup), no external file to keep in sync with a `go:embed` directive, and no build
step beyond the Go compiler already in the loop. It costs nothing at runtime beyond a map lookup.

**Rejected: `golang.org/x/text/message` / `go-i18n` / ICU-style catalogues.** These solve
problems this project does not have — plural rules, gender agreement, locale-negotiation
headers, `.po`/ICU-MessageFormat toolchains, codegen steps. For a closed two-locale, ~20-key
vocabulary, adopting a full ICU-message pipeline is the textbook over-abstraction the project's
AI-efficiency rule warns against ("indirection costs tokens to resolve... add a wrapper only
where it buys change-locality on a volatile or repeated surface"): there is no volatility here
the map doesn't already handle, and the pipeline would need its own message-catalogue files,
its own extraction tooling, and its own generated Go code to keep in sync — three new moving
parts this project's Go+Templ, no-Node-runtime stack does not otherwise have.

**Rejected: embedded JSON/TOML via `go:embed`.** Buys editability by a non-engineer, which is
not a project requirement (a single developer + AI agents edit Go source directly everywhere
else in this codebase). Costs a marshal/unmarshal indirection, a runtime "file failed to parse"
failure mode that a Go map literal cannot have (a syntax error there is a compile error, the
earliest and cheapest failure point), and a second file per key to keep in sync with the Go
constant that names it. Every other closed vocabulary in this module (`ui.Icon`'s glyph names,
`ui.Badge`'s Kind strings) is already a Go switch/map, not a data file — this stays consistent
with that existing pattern rather than introducing a second lookup mechanism.

**Rejected: a `LoginStrings`/`NavStrings`-style struct per page.** Multiplies types across the
~13 pages/fragments tier 3 will touch, with no shared lookup surface — grepping "does this key
have both languages" means opening N structs instead of one map. A string reused on two pages
(e.g. "Dashboard" appears in the nav sidebar and could appear in a page title) needs either a
duplicated field in two structs or a bridging helper; the flat map has exactly one entry, reused
anywhere by importing one constant.

**Rejected: two parallel `map[string]string`, one per language.** Same lookup cost as the chosen
shape, but a new key requires editing two separate map literals in two separate places (or two
separate declarations far apart in one file) — a reviewer cannot see both languages in one line,
which is the opposite of what the "both ES and EN" documentation rule needs to be cheap to
enforce.

### D2 — Missing-key behavior: visible marker for an unknown key, `ES` fallback for a missing language within a known key

```go
func translate(lang string, key Key) string {
    e, ok := catalog[key]
    if !ok {
        return "!!" + string(key) // key was never added to the map at all
    }
    if lang == account.LanguageEN {
        return e.EN
    }
    return e.ES // covers lang == LanguageES, and any language-within-key gap
}
```

Two distinct failure modes, two distinct answers:
- **A `Key` that isn't in the map at all** (a typo'd constant, or — this should never compile,
  since `Key` is a defined string type, but a raw string literal cast bypasses that) renders a
  visible `!!key.name` marker. This is a deliberate choice over silently rendering an empty
  string or the raw key with no marker: a blank space in the UI is easy to miss in a screenshot
  or a quick manual QA pass; `!!nav.dashboard` is not. It never panics and never 500s — "an
  invalid value must never break a render" (roadmap decision D4) extends here too, to a
  catalogue gap, not just an unrecognized language code.
- **A key that exists but is missing one language's string** (Go's zero value for `entry.EN`
  or `entry.ES` is `""`) falls back to the `ES` string, since `ES` is the catalogue's base/default
  locale. This is a **render-time backstop**, not the enforcement mechanism — see below.

**The enforcement mechanism is a test, not the fallback.** `TestCatalog_AllKeysHaveBothLanguages`
iterates the `catalog` map and fails if any entry has an empty `ES` or `EN` field. This is the
"deterministic signal over human round-trips" the project's AI-efficiency rule asks for: a
reviewer does not have to manually check every new catalogue entry has both languages — `go
test ./internal/gateway/...` does it, every time, for free. The two language-name entries
(`KeyLangSwitcherSpanish`/`KeyLangSwitcherEnglish`, both literally `"Español"`/`"English"` in
both languages — language names are conventionally not translated) are not a special case for
this test: it only asserts non-empty, never asserts the two strings differ.

### D3 — Per-request resolution: one Gin middleware, registered once, after sessions

```go
r.Use(gin.Logger(), gin.Recovery())
r.Use(sessions.Sessions("magus", store))
r.Use(languageMiddleware(d.Account)) // NEW — must come after sessions (reads currentUID)
```

`languageMiddleware` resolves the language **exactly once per request**:

- Signed in (`currentUID(c)` ok) → `acct.LanguageFor(ctx, uid)`. On any error, fall back to
  `LanguageES` — the same "never break a render" posture tier 1 already committed to for a bad
  stored value; here it also covers a transient DB error.
- Anonymous → read the `lang` cookie; present and recognized → that value; absent, empty, or
  outside `{es, en}` → `LanguageES`.

This is a **middleware**, not a per-handler helper call (the alternative the roadmap text
explicitly floats), because a middleware runs unconditionally for every route — a new page added
in tier 3 or later gets language resolution automatically, with nothing to remember. A helper
that each handler must remember to call is exactly the kind of per-file convention this
project's AI-efficiency principle prefers to eliminate in favor of a structural guarantee.
This mirrors the existing `sessions.Sessions(...)` registration one line above it — same file,
same idiom, same "global middleware handles a cross-cutting concern" shape already established
in `gateway.go`.

**Read cost:** exactly one `LanguageFor` call per signed-in HTTP request (page load or htmx
fragment fetch) — never zero (every authenticated route needs a language to render in) and never
more than one (the middleware runs once per request, stores the result on the request's
`context.Context`, and every handler downstream reads that context rather than calling
`LanguageFor` again). Anonymous requests never call `LanguageFor` at all.

### D4 — Cookie ↔ DB synchronization, both directions

Roadmap decision D2 requires the cookie to "not flip the language at the session boundary."
Two distinct sync points, each justified independently:

1. **Every authenticated render, cookie follows DB.** Inside `languageMiddleware`'s signed-in
   branch: if the incoming `lang` cookie is absent or differs from the just-read DB value, the
   middleware re-sets the cookie to match. Cost: a string compare plus, only on a mismatch, one
   `Set-Cookie` header — no extra DB read (the DB value was already fetched this request for
   rendering). This guarantees that if the user later logs out, the anonymous cookie they land on
   already carries their most recent signed-in choice.
2. **At login, an explicit cookie propagates into the account.** `GoogleCallback` (after
   `UpsertFromOAuth` resolves/creates the account) checks whether the request carried an
   **explicitly present** `lang` cookie (`c.Cookie("lang")` returned no error — not merely "the
   resolved language happened to be `es`," which is also true for an absent cookie) and, if so,
   calls `acct.SetLanguage(ctx, acct.ID, cookieLang)`. This is what "the cookie choice carries
   into the session at login" (roadmap decision D2) means operationally: whichever language the
   visitor picked on the login page (anonymous, cookie-only) becomes their account's language the
   moment they authenticate.

**Trade-off, stated plainly (see also Risks):** because step 2 always propagates a *present*
cookie regardless of whether the account already has an established preference, a returning user
who set `en` on one device and logs in from a second device carrying a stray/default `es` cookie
(e.g. a shared browser) will have their DB preference overwritten to `es` by that login. This is
the literal reading of the roadmap's "carries into the session at login" wording, is simple (no
"is this the account's first-ever login" heuristic needed — `UpsertFromOAuth` does not report
that), and is a low-severity, one-click-reversible edge case for a personal Tesla-monitoring app.
Flagged for the reviewer; not treated as a blocker.

### D5 — Carrying the active language into Templ components: `context.Context`, not a typed `Lang` prop

This is the single highest-leverage decision in this tier — tier 3 repeats it roughly 13 times.

**Chosen: `context.Context`**, following Templ's own documented internationalization pattern
(verified against the current `a-h/templ` docs, `docs/12-integrations/02-internationalization.md`
and `docs/03-syntax-and-usage/15-context.md` — "Common use cases for [context] middleware include
authentication, and theming"). Concretely:

```go
// internal/gateway/i18n/i18n.go
func WithLang(ctx context.Context, lang string) context.Context { ... }
func FromContext(ctx context.Context) string { ... } // defends against a missing/mistyped value
func T(ctx context.Context, key Key) string { return translate(FromContext(ctx), key) }
```

```templ
// any .templ file, anywhere in the component tree
<span>{ i18n.T(ctx, i18n.KeyNavLogout) }</span>
```

Every Templ component has an **implicit `ctx` variable** bound to whatever `context.Context` was
passed to the top-level `Render` call. `render()`/`renderFragment()` (`handlers.go`) already pass
`c.Request.Context()` — so the middleware setting the language on that same request context (D3)
is picked up automatically by `i18n.T(ctx, ...)` calls **anywhere in the tree**, including inside
`ui.*` components nested several levels deep under a page, with zero extra plumbing: templ's
codegen threads the ambient `ctx` through every `@Child(...)` composition for free.

**Rejected: an explicit `Lang string` field threaded through every fragment/page `ViewData`.**
This was the roadmap's other explicitly-named option, and it looks like the "typed Props, not
`context.Value`" pattern this project otherwise mandates for the `ui/` kit (`ai/htmx-conventions.md`
— "a wrong field fails the build"). It is rejected here for a reason specific to this value, not
a departure from the typed-Props principle in general:

- The claimed benefit of a typed Prop — a wrong/forgotten field **fails the build** — does not
  actually apply to a plain `string` field. Go has no "required field" enforcement in a struct
  literal; a VM built without setting `Lang` silently gets the zero value `""`, which is not a
  compile error and is easy to mistake for "the fallback is working as intended" during review.
  The safety property that justifies typed Props everywhere else in this module simply is not
  available for an ambient, always-present, cross-cutting value like this one.
- It would touch **every** VM struct and every render call site tier 3 visits (~13 pages/
  fragments) purely to plumb a value that has nothing to do with any of those structs' actual
  domain content — `DashboardData`, `NavHeaderVM`, `ChargesData` etc. would each carry a
  presentation concern unrelated to their data, and every `dataFor`-style helper would need a
  `lang string` parameter threaded through purely to pass it along. That is the opposite of
  change-locality: one cross-cutting concern multiplying into N unrelated files.
- `context.Context` is precisely the tool templ's own maintainers built and documented for this
  exact class of value (their examples are literally locale and theme). Using it here is
  reaching for the closed, small vocabulary the library already provides instead of re-inventing
  a parallel mechanism — the AI-efficiency principle applied to library usage, not just to this
  project's own code.

**Caveat carried into the implementation (from the templ docs themselves):** "accessing a
non-existent key or performing an invalid type assertion on the context value will trigger a
runtime panic." A naive `ctx.Value(langKey).(string)` would panic in, e.g., a unit test that
renders a component with `context.Background()` and no middleware. `i18n.FromContext` therefore
uses the two-result (`comma-ok`) type-assertion form and treats a missing/mistyped value exactly
like an unrecognized language: falls back to `LanguageES`. This is not optional defensiveness —
it is required correctness given the documented panic behavior.

**Where a genuine typed Prop is still correct:** `ui.LangSwitcher` itself still takes explicit
typed `Props` (`Current string`, `CSRFToken`-equivalent as needed) for data that is not the
ambient language but the switcher's own structured state (which language is "current" for the
`aria-selected` marker, etc.) — the distinction is "ambient cross-cutting concern" (context) vs.
"this component's own structured input" (typed Props), the same distinction the project already
draws between theme (`<html data-theme>`, ambient) and `NavHeaderVM.CSRFToken` (an explicit,
component-specific field).

### D6 — Navbar switcher: new `ui.LangSwitcher`, mounted once in `layouts.Base`

No existing `ui/` wrapper owns a DaisyUI `dropdown`. A new one is required (the roadmap says so
explicitly: "add a wrapper to `ui/` if none fits"). It is CSS-only — DaisyUI's `dropdown` is a
`:focus`-driven CSS pattern (`<div class="dropdown dropdown-end"><div tabindex="0" role="button"
class="btn ...">...</div><ul tabindex="-1" class="dropdown-content menu ...">...</ul></div>`,
verified against current DaisyUI docs), so it needs **no client-side JS** — it is already on the
`ai/htmx-conventions.md` list of sanctioned zero-JS patterns (`dropdown`, `<dialog>`, `collapse`,
`tabs`). No new RD8/RD9/RD10-style exception is needed.

```go
// templates/ui/lang_switcher.templ
type LangSwitcherProps struct {
    Current string // must be account.LanguageES or account.LanguageEN
}
```

No `Options []...` parameter: the supported set is frozen at exactly `{es, en}` by roadmap
decision D4 and is not expected to grow without a code change to `account` (tier 1) regardless —
parameterizing a two-item, closed, never-growing list would be the over-abstraction the AI-
efficiency rule warns against. The two options are hardcoded inside the component (globe icon +
`ES`/`EN` code shown for `Current`; a dropdown listing `Español`/`English`, each an
`hx-post="/ui/lang/switch"` button carrying `hx-vals='{"lang":"es"}'`/`{"lang":"en"}'` and
`hx-swap="none"` — a non-submitting action control, so `hx-*` on the `<button>` itself is correct
per `ai/htmx-conventions.md`'s carve-out, not the `<form>`-with-`Type:"submit"` rule, which is for
HTML5-validated data entry).

**Mount point: once, in `layouts.Base`.** This is a direct application of the existing `RD10`
precedent (`ui.ConfirmDialog`, "mounted once in `layouts.Base` so it is on every page... `BaseAuth`
inherits it automatically"). `layouts.Base` is the shell both `Home`/`Login` (anonymous) and,
indirectly through `BaseAuth`, every authenticated page render through. One call site covers
100% of pages — including the login page (roadmap decision D2 requires the selector there) and
any future page not yet written, with nothing to remember when adding a page. The alternative
(mounting it inside `BaseAuth`'s own `<nav class="navbar">` bar for authenticated pages, plus a
second, separate mount for the navbar-less `Login`/`Home` shell) would need two call sites and,
for `Base`, inventing a navbar strip that does not otherwise exist there — more files touched for
a purely cosmetic difference (a fixed-position top-right element vs. an inline navbar item).
Flagged as an open, non-blocking cosmetic question below.

**New `ui.Icon` case: `"globe"`.** `Icon`'s vocabulary is explicitly designed to be extended by
"adding a glyph... in one place" (its own doc comment) — this is exactly that sanctioned,
low-cost extension, not a new wrapper.

### D7 — htmx switch mechanism: `HX-Location` targeting the current path, not an out-of-band swap or a hard redirect

`POST /ui/lang/switch` (`hx-swap="none"` on the triggering button — its own response body is
irrelevant; the real work happens via a response header):

```
HX-Location: {"path": "<current path + query, unchanged>", "push": "false"}
```

verified against current `bigskysoftware/htmx` docs: `HX-Location` "act[s] like following a
`hx-boost` link" — an AJAX `GET` to `path`, with the fetched full-page HTML's `<body>` swapped in
by default (the same mechanism a boosted `<a>` uses), no full browser reload, no visible flash.
`push: "false"` prevents a duplicate history entry for a URL the user is already on (documented
`push` param: `'false'` or a path string overrides/suppresses the pushed entry).

**Path derivation:** the `HX-Current-URL` request header, which htmx sends automatically on every
request, parsed via `net/url.Parse` and reduced to `Path` (+ `RawQuery` when present — this
matters concretely for the dashboard history page's `?start=&end=` selection, see below).
Fallback chain when that header is absent (a non-htmx caller): `Referer`, then `/`.

**Rejected: an out-of-band (`hx-swap-oob`) swap of just the switcher control.** Updates the
button's own label but leaves every *other* string on the page in the old language — directly
contradicts the point of switching (the user expects the page they're looking at to change, not
just the control they clicked).

**Rejected: `HX-Redirect` (hard reload) or a plain `c.Redirect` (3xx).** `HX-Redirect` forces a
full `window.location` reload — a visible white-flash navigation — for no benefit over
`HX-Location`'s smoother AJAX-driven boost, which produces the identical end state (the same page,
fully re-rendered server-side in the new language). A plain 3xx redirect is actually **broken**
for this use case: htmx's own docs state "Response headers are not processed on 3xx response
codes" — `HX-Location` would be silently ignored, so a bare `c.Redirect` cannot be combined with
this mechanism at all; the handler must return a 2xx (200, empty body) carrying the header.

**Rejected: a parallel "render the page for this arbitrary path" dispatch table inside the
switch handler.** Re-fetching via `HX-Location` reuses the **real** route table (`gateway.go`'s
existing `r.GET(...)` entries) with zero new registration surface. Building a second path→handler
map inside the switch handler would mean every future page addition needs registering in *two*
places instead of one — directly against change-locality, and a maintenance trap an agent adding
a page later could easily miss.

**In-page state after a switch, stated honestly:**
- **URL-encoded selections survive** — e.g. the dashboard history page's `?start=&end=` date-range
  selection is part of the `HX-Current-URL` path+query carried into `HX-Location`, so the
  boost-fetch re-requests the identical range, just re-rendered in the new language.
- **Session-held selections survive** — the sidebar vehicle switcher's selection lives in the gin
  session (`setCurrentVehicle`), not in DOM state, so it is unaffected by any full-body swap.
- **Unsaved, not-yet-submitted form input does NOT survive** — e.g. someone mid-typing in the
  manual-charge create form loses that input on a language switch, identical to what a manual
  page refresh would do. This is accepted as the same cost a real navigation always has; the
  alternative (a true in-place per-string swap with no page-level HTML refetch) would require
  either the abandoned OOB-per-control approach or threading `Lang` through every VM (D5's
  rejected alternative) purely to avoid an edge case (switching language mid-form-fill) that is
  itself rare.

### D8 — Gateway boundary amendment: `LangSwitch` may call `account.Service.SetLanguage`

`internal/gateway/AGENTS.md`'s "Read-only at request time" section permits exactly one existing
write exception (`manualcharge.Writer`, the D4/RM3 amendment). Calling `account.Service.SetLanguage`
from `LangSwitch` needs its **own** documented exception — it is a Writer-port call from a
handler, and the existing amendment's constraints (auth-guard-first, tenant-ownership check,
CSRF) do not transplant cleanly:

- **No auth guard / no redirect-to-login.** Every other write handler starts with `currentUID(c)`
  and redirects anonymous callers to `/login`. `LangSwitch` must work for anonymous callers too
  (the whole point of the `lang` cookie path) — it branches instead: always set the cookie; call
  `SetLanguage` only when a session UID is present.
- **No tenant-ownership check.** `SetLanguage(ctx, uid, lang)` always targets the caller's own
  session `uid` — there is no user-submitted resource identifier (unlike the vehicle
  `(TeslaID, VIN)` pair D4 validates) for a forged request to redirect at a different account.
- **No CSRF check — a deliberate divergence from D4, not an oversight.** Justification:
  1. **Blast radius.** A forged switch request can only ever change the *caller's own* display
     language. There is no data mutation, no financial record, nothing to exfiltrate or destroy —
     worst case is a cosmetic annoyance, reversible in one click by the victim themselves.
  2. **Cost of requiring it.** `ui.LangSwitcher` is mounted on **every** page (D6) — including
     ones that today issue no CSRF token at all (`Home`, `Dashboard`, `SuperchargerStats`; only
     `ChargePage`/`NavHeaderFragment` currently mint one). Requiring CSRF here would mean every
     page handler in the gateway starts generating and storing a session CSRF token purely to
     support this one control — a broad, invasive change across most of the module for a
     protection whose value (per point 1) is low. This directly conflicts with the roadmap's
     explicit tier-2 scoping instruction ("resist translating/touching more" — extended here to
     "resist widening more than this one endpoint needs").
  3. **`SameSite=Lax` carries the actual defence — and is MANDATORY, not incidental.** The `lang`
     cookie MUST be set `SameSite=Lax` (see the cookie-attribute bullet below), which makes modern
     browsers refuse to attach it to a cross-site `POST` in the first place. That is what closes
     the forged-request path here; points 1 and 2 explain why a token on top of it is not worth
     its cost, but they are not the whole argument and must not be quoted without this one.
     Dropping `SameSite=Lax` would invalidate this decision and re-open the CSRF question.

  **This trade-off was put to the user and explicitly approved on 2026-08-13, conditional on the
  `SameSite=Lax` requirement in point 3.** It is not the worker's unilateral call. It remains
  flagged for the reviewer as a deliberate, contestable divergence from D4 rather than something
  hidden in the diff — but reversing it now requires the user, not just a review finding.

- **Cookie attributes (normative — `setLangCookie` MUST set exactly these).** `Name=lang`,
  `Value` ∈ `{es, en}` only, `Path=/`, `SameSite=Lax` (required by the CSRF decision above),
  `Max-Age=31536000` (one year — the preference should outlive a browsing session; this is the
  whole point of persisting it for anonymous visitors), `HttpOnly=true` (no JS reads or writes
  this cookie, unlike `browser_tz` — the server is its only consumer), and `Secure` set to match
  however the module's existing session cookie decides it, so local HTTP development keeps
  working.

  **Implementation note — `SameSite` is not a `SetCookie` parameter.** gin's
  `c.SetCookie(name, value, maxAge, path, domain, secure, httpOnly)` has no SameSite argument;
  it is applied only by calling `c.SetSameSite(http.SameSiteLaxMode)` **before** `SetCookie`.
  Omitting that separate call silently produces a cookie with no SameSite attribute and removes
  the entire defence point 3 relies on. A test MUST assert `SameSite=Lax` on the emitted
  `Set-Cookie` header specifically, so this cannot regress unnoticed.
- **Scope stays narrow.** Only `account.Service.SetLanguage` is permitted under this exception —
  it does not open general write access to the gateway; every other handler stays Reader-only
  except the pre-existing D4 aperture.

### D9 — NavHeader translation: derive the status label from the existing `Status` enum, don't add a parallel field

`NavHeaderVM` already carries a closed `Status NavHeaderStatusKind` enum (`connected`/`asleep`/
`awaiting`/`unavailable`) alongside a **separate**, handler-computed English `StatusLabel string`
(`navHeaderFor` in `handlers.go` sets both together in every branch — e.g. `Status:
NavStatusConnected, StatusLabel: "Connected"`). Translating `StatusLabel` in place would mean the
Go handler choosing English text that then needs translating — backwards, since `Status` already
names the same information as a typed, closed value.

**This tier removes `StatusLabel` from `NavHeaderVM` and from every `navHeaderFor` branch**,
replacing its one call site (`nav_header.templ`) with a template-side lookup:
`i18n.T(ctx, statusLabelKeyFor(vm.Status))`, where `statusLabelKeyFor` is a small, pure
`NavHeaderStatusKind → i18n.Key` switch living next to the existing `navBadgeKind` helper in
`nav_header.templ` — same file, same "presentation mapping from a closed enum, not business
logic" precedent that function already establishes. This is a simplification, not just a
translation: one source of truth (the enum) instead of two (the enum plus a parallel hand-typed
string), and it is impossible for `Status` and `StatusLabel` to disagree because there is no
longer a second field to disagree.

**Also translated in this tier (all static/enum-driven, no formatting logic):** the connect-prompt
text ("No Tesla connected." / "Connect your Tesla"), the sidebar nav item labels and the "Soon"
placeholder badge (`navItems`, which gains a `ctx context.Context` first parameter — its own call
site, `navItems(path)` inside `templ BaseAuth`, becomes `navItems(ctx, path)`, illustrating that a
plain Go helper called *from* a templ block takes `ctx` as an explicit argument, while a templ
component receives it implicitly — both consume the exact same `i18n.T(ctx, ...)` surface),
`ui.NavLogout`'s "Log out", and the sidebar's open/close aria-labels.

**Explicitly NOT translated in this tier:** `LastSeenLabel` ("2 days ago", `relativeLastSeen` in
`handlers.go`) and `BatteryPct`'s `%d%%` formatting. Relative-time phrasing needs its own small
pluralization decision (`"1 día"` vs `"2 días"` vs `"1 day"` vs `"2 days"`) that is a real,
separate design question — folding it into this tier's gold-standard scope would violate the
roadmap's explicit "resist translating more" instruction. Left as a named, scoped item for tier 3
(see Open Questions).

## Risks / Trade-offs

- **No CSRF on `POST /ui/lang/switch`** (D8). Deliberate, justified by low blast radius vs. the
  cost of requiring every page to mint a CSRF token. Flagged for the reviewer to accept or push
  back on explicitly — not slipped in silently.
- **Login-time cookie→DB sync can overwrite an established preference from a stray cookie on a
  new device** (D4). Low-severity, one-click-reversible, and the literal reading of the roadmap's
  decision text. Flagged, not treated as a defect.
- **One extra indexed `SELECT` per signed-in request** (D3) — already accepted at tier 1's design
  gate; restated here because this is the tier that actually wires the call in. No new query is
  added; this reuses tier 1's `LanguageFor`.
- **The `!!key` missing-catalogue-key marker could theoretically reach production** if a key is
  referenced without a catalogue entry. Mitigated primarily by `TestCatalog_AllKeysHaveBothLanguages`
  (D2) — that test only covers keys that exist in the catalog, so a genuinely *unregistered* `Key`
  constant used at a call site is instead caught by `go vet`/`go build` failing to reference an
  undefined constant, or by the marker's own visibility in manual QA as a last-resort backstop.
- **A full-body htmx boost swap loses unsaved form input** (D7). Accepted; documented; identical
  to the cost of a manual page refresh, and switching language mid-form-fill is an edge case.
- **`ui.LangSwitcher`'s fixed-position mount overlays rather than integrates into `BaseAuth`'s own
  navbar bar** (D6). Purely cosmetic; flagged as an open, non-blocking follow-up.

## Implementation Plan

See `tasks.md` for the full, dependency-ordered breakdown. Summary:

1. `internal/gateway/i18n` — `Key` type, catalogue map, `WithLang`/`FromContext`/`T`, the
   completeness test.
2. `internal/gateway/handlers` — `languageMiddleware`, `setLangCookie`/`normalizeLang` helpers,
   `LangSwitch` handler; wire the middleware + route in `gateway.go`; propagate the login-time
   cookie sync in `GoogleCallback`.
3. `internal/gateway/templates/ui` — `lang_switcher.templ` (+ generated `_templ.go`); `"globe"`
   case in `icon.templ`.
4. `internal/gateway/templates/layouts` — mount `ui.LangSwitcher` once in `base.templ`; `nav.go`'s
   `navItems` reads via `i18n.T`.
5. `internal/gateway/templates/fragments` — `nav_header.templ` translated (connect prompt, status
   label lookup replacing `StatusLabel`); `ui.NavLogout` translated.
6. `internal/gateway/AGENTS.md` — new "i18n" binding section + the D8 write-exception amendment.
   Root `README.md` — new `i18n/` tree line.
7. `make templ && make css`; commit `static/app.css`.
8. Tests: catalogue completeness, middleware resolution (signed-in/anonymous/fallback), cookie
   sync (both directions), `LangSwitch` (valid/invalid lang, signed-in write, anonymous no-write,
   `HX-Location` header shape), nav-shell rendering in both languages.

## Open Questions

None blocking. One cosmetic, explicitly non-blocking item carried forward from D6: whether
`ui.LangSwitcher` should eventually be inlined into `BaseAuth`'s own navbar bar instead of
floating as a fixed corner element on `layouts.Base`. Left as-is for this tier; revisit only if
it reads as visually awkward once built. Tier 3 should also make an explicit, named decision
about translating `relativeLastSeen`'s pluralized phrasing (`"X days ago"`) when it reaches
`nav_header.templ`'s remaining untranslated fields — flagged above (D9), not decided here.
