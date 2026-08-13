# Tasks — RM24-gateway-add-i18n-foundation

> Full design rationale for every decision referenced below (D1–D9) lives in `design.md`.
> Group headers state real dependencies so independent groups can be implemented in parallel;
> within a group, do the sub-tasks in order.

## T1. `internal/gateway/i18n` package — no dependencies

New package: the translation catalogue + context-carried language resolution (design.md D1/D2/D5).

- [ ] T1.1 Create `internal/gateway/i18n/i18n.go`: an unexported `ctxKey struct{}` +
      `var langCtxKey ctxKey`; `WithLang(ctx context.Context, lang string) context.Context`
      (`context.WithValue(ctx, langCtxKey, lang)`, no validation — callers normalize before
      calling); `FromContext(ctx context.Context) string` using the **comma-ok** type-assertion
      form (`lang, _ := ctx.Value(langCtxKey).(string)`) — never a bare `.(string)` assertion,
      per templ's documented panic-on-missing-key behavior (design.md D5) — returning
      `account.LanguageEN` only on an exact match, `account.LanguageES` otherwise (covers a
      missing key, a mistyped value, and an explicit `"es"`).
- [ ] T1.2 Add `type Key string` and `func T(ctx context.Context, key Key) string { return
      translate(FromContext(ctx), key) }` to `i18n.go`.
- [ ] T1.3 Create `internal/gateway/i18n/catalog.go`: `type entry struct{ ES, EN string }`, the
      closed `Key` constant vocabulary for this tier's gold-standard surface (nav sidebar labels,
      "Soon" badge, sidebar open/close aria-labels, `NavLogout`'s "Log out", the nav-header
      connect-prompt + four status words, the lang-switcher's aria-label + "Español"/"English"
      list-item labels), the `catalog map[Key]entry` literal with both languages **on the same
      line** per key (design.md D1), `const missingKeyMarker = "!!"`, and
      `func translate(lang string, key Key) string` (comma-ok catalog lookup → `missingKeyMarker
      + string(key)` when the key itself is absent; `e.EN` when `lang == account.LanguageEN`;
      `e.ES` otherwise — covering both `lang == LanguageES` and a language-within-key gap).
      Doc-comment the file per design.md D2's two distinct missing-value behaviors.
- [ ] T1.4 `internal/gateway/i18n/i18n_test.go`: `TestFromContext_DefaultsAndPanicSafety` — no
      value on ctx → `es`; `WithLang(ctx, "en")` → `en`; a ctx carrying a **non-string** value
      under an unrelated key does not panic and still resolves to `es` (proves the comma-ok
      guard). `TestTranslate_KnownKey`, `TestTranslate_UnknownKeyReturnsVisibleMarker`
      (`translate("es", Key("not.a.real.key"))` → starts with `"!!"`).
- [ ] T1.5 `internal/gateway/i18n/catalog_test.go`:
      `TestCatalog_AllKeysHaveBothLanguages` — iterate `catalog`, fail on any entry with an empty
      `ES` or `EN` field (design.md D2 — this is the actual enforcement of the "both languages"
      rule, not the render-time marker). Table-driven, one failure message per bad key so a future
      violation is easy to locate.

## T2. Language resolution middleware + cookie helpers (`internal/gateway/handlers`) — depends on T1

- [ ] T2.1 Create `internal/gateway/handlers/lang.go`: `const langCookieName = "lang"`,
      `const langCookieMaxAge = 365 * 24 * 60 * 60`; `func normalizeLang(v string) string`
      (`account.LanguageEN` on exact match, `account.LanguageES` otherwise — design.md D3);
      `func setLangCookie(c *gin.Context, lang string)` which MUST call
      `c.SetSameSite(http.SameSiteLaxMode)` **before** `c.SetCookie(langCookieName, lang,
      langCookieMaxAge, "/", "", false, true)` — `httpOnly: true`, no JS ever needs to read or
      write this cookie, unlike `browser_tz`. **`SameSite=Lax` is load-bearing, not cosmetic:**
      it is the actual defence behind design.md D8's user-approved decision to omit a CSRF token
      on `POST /ui/lang/switch`, and gin's `SetCookie` has no SameSite parameter — it is only
      applied via the separate `SetSameSite` call, so omitting that line silently removes the
      protection. Add the `net/http` import if absent.
- [ ] T2.2 In `lang.go`, add `func languageMiddleware(acct account.Service) gin.HandlerFunc`
      (design.md D3/D4): resolves `lang` once — signed-in branch calls `acct.LanguageFor(ctx,
      uid)` (fallback to `account.LanguageES` on error) and, if the incoming cookie is absent or
      differs from the resolved value, calls `setLangCookie` to sync it; anonymous branch reads
      the `lang` cookie via `normalizeLang` (no DB call). Ends by setting
      `c.Request = c.Request.WithContext(i18n.WithLang(c.Request.Context(), lang))` then
      `c.Next()`.
- [ ] T2.3 In `internal/gateway/gateway.go`, register the middleware immediately after the
      sessions middleware: `r.Use(sessions.Sessions("magus", store))` then
      `r.Use(languageMiddleware(d.Account))` (reuses the existing `Deps.Account` — no new `Deps`
      field). Comment why the ordering matters (needs `currentUID`, which reads the session).
- [ ] T2.4 `internal/gateway/handlers/lang_test.go`: `TestLanguageMiddleware_SignedInReadsAccount`
      (fake `LanguageFor` returns `en` → request context carries `en`, asserted via a tiny
      downstream handler reading `i18n.FromContext(c.Request.Context())`);
      `TestLanguageMiddleware_AnonymousReadsCookie`;
      `TestLanguageMiddleware_AnonymousNoCookieDefaultsToSpanish`;
      `TestLanguageMiddleware_SignedInLanguageForErrorDefaultsToSpanish`;
      `TestLanguageMiddleware_SyncsStaleCookieToDBValue` (signed-in, cookie says `es`, DB says
      `en` → response sets a fresh `lang=en` cookie); `TestNormalizeLang_UnrecognizedFallsBack`.

## T3. `LangSwitch` handler + route + login-time cookie sync — depends on T1, T2

- [ ] T3.1 In `lang.go`, add `func (h *Handler) LangSwitch(c *gin.Context)` (design.md D7/D8): no
      auth guard; validate `c.PostForm("lang")` is exactly `account.LanguageES` or
      `account.LanguageEN` (else `c.String(http.StatusBadRequest, "unsupported language")` and
      return, no cookie set, no write); `setLangCookie(c, lang)` unconditionally; if
      `currentUID(c)` ok, call `h.acct.SetLanguage(c.Request.Context(), uid, lang)` (on error,
      `c.String(http.StatusInternalServerError, ...)` and return — do not still send
      `HX-Location`, so the client does not reload into a state the write never reached).
- [ ] T3.2 Same function: derive the redirect target from `c.GetHeader("HX-Current-URL")`
      (`net/url.Parse`, use `.Path` + `"?"+.RawQuery` when non-empty), falling back to
      `c.Request.Referer()` then `"/"` when the header is absent or fails to parse. Build the
      `HX-Location` header value as JSON (`encoding/json.Marshal` of an anonymous/typed struct
      with `Path`/`Push` fields tagged `path`/`push`, `Push: "false"` — design.md D7 explains
      why a hand-built string is riskier than `json.Marshal` here, since a query string can carry
      characters that need proper JSON escaping). `c.Header("HX-Location", string(body))`,
      `c.Status(http.StatusOK)` (no body) — **never** `c.Redirect` (a 3xx; htmx does not process
      response headers on 3xx per design.md D7).
- [ ] T3.3 In `gateway.go`, add `r.POST("/ui/lang/switch", h.LangSwitch)`.
- [ ] T3.4 In `GoogleCallback` (`handlers.go`), after `UpsertFromOAuth` succeeds and before
      redirecting to `/dashboard`: if `c.Cookie(langCookieName)` returns a value with **no
      error** (an explicitly-present cookie, not the absent-cookie case), call
      `h.acct.SetLanguage(ctx, acct.ID, normalizeLang(cookieVal))` (design.md D4 — best-effort;
      log and continue on error, do not fail the login over a language-sync write).
- [ ] T3.5 `internal/gateway/handlers/lang_test.go` additions: `TestLangSwitch_AnonymousSetsCookieOnly`
      (asserts `Set-Cookie` present, fake `SetLanguage` NOT called);
      `TestLangSwitch_SignedInPersistsAndSyncsCookie` (fake `SetLanguage` called with the right
      uid+lang, cookie set); `TestLangSwitch_RejectsUnsupportedLanguage` (`lang=fr` → 400, no
      cookie, no write); `TestLangSwitch_HXLocationPreservesQueryString` (request carrying
      `HX-Current-URL: http://x/dashboard/history?start=2026-08-01&end=2026-08-07` → the
      `HX-Location` header's `path` includes the same query string, `push` is `"false"`);
      `TestLangSwitch_FallsBackToRefererThenRoot` (no `HX-Current-URL`);
      `TestLangSwitch_CookieIsSameSiteLax` — asserts the emitted `Set-Cookie` header contains
      `SameSite=Lax`. **This test is mandatory and may not be dropped or weakened:** design.md
      D8's user-approved omission of a CSRF token on this endpoint rests entirely on that
      attribute, and gin only emits it via a separate `SetSameSite` call that is easy to lose in
      a refactor. If this test is failing, the CSRF decision is void — fix the cookie, never the
      test.
      `TestGoogleCallback_PreLoginCookiePropagatesToAccount` in `handlers_test.go` (request
      carrying `lang=en` cookie through the callback → fake `SetLanguage` called with `en`);
      `TestGoogleCallback_NoCookieDoesNotCallSetLanguage`.
      Widen the shared `fakeAccount` (`handlers_test.go`) with a settable `language string` field
      and a `setLanguageCalls []struct{ID uuid.UUID; Lang string}` capture slice so these tests
      (and T2.4's) can assert against it; keep the existing zero-value behavior (`LanguageFor`
      returns `account.LanguageES`) for every pre-existing test that doesn't set it.

## T4. `ui.LangSwitcher` + globe icon (`internal/gateway/templates/ui`) — depends on T1

- [ ] T4.1 Add a `"globe"` case to `iconMarkup` in `internal/gateway/templates/ui/icon.templ`
      (inline SVG globe glyph, `fill="currentColor"`, matching the existing cases' shape) and
      extend `IconProps`'s doc-comment vocabulary list.
- [ ] T4.2 Create `internal/gateway/templates/ui/lang_switcher.templ`: `type LangSwitcherProps
      struct { Current string }` (design.md D6 — no `Options`, the set is hardcoded/closed).
      `templ LangSwitcher(p LangSwitcherProps)` renders `<div class="dropdown dropdown-end">`
      wrapping a `<div tabindex="0" role="button" class="btn btn-ghost btn-sm">` trigger (globe
      icon + `strings.ToUpper(p.Current)`, `aria-label={ i18n.T(ctx, i18n.KeyLangSwitcherAria) }`)
      and a `<ul tabindex="-1" class="dropdown-content menu bg-base-100 rounded-box z-1 w-40
      p-2 shadow-sm">` listing two `<li>` buttons, each `hx-post="/ui/lang/switch" hx-swap="none"`
      with `hx-vals` carrying the fixed `{"lang":"es"}` / `{"lang":"en"}` literal, labeled via
      `i18n.T(ctx, i18n.KeyLangSwitcherSpanish)` / `i18n.T(ctx, i18n.KeyLangSwitcherEnglish)`.
      Semantic tokens only, matching the module's existing dropdown-free precedent's styling
      conventions (no hex).
- [ ] T4.3 `internal/gateway/templates/ui/lang_switcher_test.go` (mirrors `nav_shell_test.go`'s
      shape): `TestLangSwitcher_RendersGlobeAndCurrentCode` (render with
      `LangSwitcherProps{Current: account.LanguageEN}`, assert `<svg` present and `"EN"`
      appears); `TestLangSwitcher_NoClientSideJS` (asserts no `<script` in the rendered output —
      the CSS-only-dropdown invariant from design.md D6); `TestLangSwitcher_BothOptionsPresent`
      (asserts both `hx-vals` payloads appear, one per supported language).

## T5. Mount the switcher + translate the sidebar nav — depends on T1, T4

- [ ] T5.1 In `internal/gateway/templates/layouts/base.templ`, mount `@ui.LangSwitcher(ui.LangSwitcherProps{Current:
      i18n.FromContext(ctx)})` once inside `templ Base`, alongside the existing `@ui.ConfirmDialog()`
      mount (design.md D6 — same "mounted once, inherited by BaseAuth" shape as RD10). Positioned
      as a fixed top-right element (`class="fixed top-3 right-3 z-40"` or equivalent) so it
      renders above page content without requiring a navbar shell change.
- [ ] T5.2 In `internal/gateway/templates/layouts/nav.go`, change `func navItems(active string)
      []ui.NavItem` to `func navItems(ctx context.Context, active string) []ui.NavItem` and
      replace each hardcoded `Label` with `i18n.T(ctx, i18n.KeyNav...)` (Dashboard, Manual
      Records, Supercharger Stats, Settings). Update the one call site in `base.templ`
      (`@ui.NavShell(navItems(path), ...)` → `@ui.NavShell(navItems(ctx, path), ...)` — `ctx` is
      already in scope inside the `templ BaseAuth` block; design.md D9 notes this as the
      "plain Go helper called from a templ block takes ctx explicitly" illustration).
- [ ] T5.3 In `internal/gateway/templates/ui/nav_shell.templ`, replace the hardcoded `"Soon"` in
      `@Badge(BadgeProps{Kind: "warning", Text: "Soon"})` with `i18n.T(ctx, i18n.KeyNavSoonBadge)`
      (ctx is implicit inside the `templ NavShell` block). In `base.templ`'s hamburger `<label>`s,
      translate the `aria-label="open sidebar"` / `"close sidebar"` via
      `i18n.T(ctx, i18n.KeyNavOpenSidebar)` / `i18n.T(ctx, i18n.KeyNavCloseSidebar)`.
- [ ] T5.4 In `internal/gateway/templates/ui/nav_logout.templ`, replace the hardcoded `Log out`
      text with `{ i18n.T(ctx, i18n.KeyNavLogout) }`.
- [ ] T5.5 `internal/gateway/templates/ui/nav_shell_test.go` additions (or a new
      `layouts/nav_test.go`): `TestNavItems_LabelsTranslate` — call `navItems(i18n.WithLang(ctx,
      account.LanguageEN), "/dashboard")` vs. `...LanguageES...` and assert the returned labels
      differ and match the catalogue. `TestBaseAuth_RendersLangSwitcher` /
      `TestBase_RendersLangSwitcher` (both shells render the switcher — the "every page" claim in
      the spec delta, proven directly rather than assumed from the mount point alone).

## T6. Translate the nav-header — depends on T1

- [ ] T6.1 In `internal/gateway/templates/fragments/nav_header.templ`: remove `StatusLabel
      string` from `NavHeaderVM` (design.md D9). Add `func statusLabelKeyFor(s
      NavHeaderStatusKind) i18n.Key` next to the existing `navBadgeKind` (same file, same
      "presentation mapping from the closed enum" shape), mapping each of the four
      `NavHeaderStatusKind` values to its `i18n.Key`. Replace the template's `{ vm.StatusLabel }`
      with `{ i18n.T(ctx, statusLabelKeyFor(vm.Status)) }`.
- [ ] T6.2 Same file: replace the hardcoded `"No Tesla connected."` and `"Connect your Tesla"`
      strings with `i18n.T(ctx, i18n.KeyNavHeaderNoTesla)` / `i18n.T(ctx,
      i18n.KeyNavHeaderConnectLink)`; add `aria-label={ i18n.T(ctx,
      i18n.KeyNavHeaderSwitchVehicleAria) }` to the vehicle-switcher `<select>` (currently has no
      `aria-label`, cheap addition while the block is already touched).
- [ ] T6.3 In `internal/gateway/handlers/handlers.go`'s `navHeaderFor`, delete every
      `StatusLabel: "..."` assignment (four call sites) — the field no longer exists on
      `NavHeaderVM` after T6.1, so this is required for the package to compile, not optional
      cleanup.
- [ ] T6.4 `internal/gateway/handlers/handlers_test.go` / existing nav-header tests: update any
      assertion that reads `vm.StatusLabel` or asserts on the literal English status word in
      rendered output to instead assert on `vm.Status` (the enum) and, where the test renders
      HTML, on the translated string for the resolved language used in that test.
      `internal/gateway/templates/fragments/nav_header_test.go` (new, if one doesn't already
      exist) or an addition to an existing fragments test file:
      `TestNavHeader_StatusLabelTranslatesPerLanguage` — render `NavHeaderVM{Status:
      NavStatusConnected}` under both `i18n.WithLang(ctx, LanguageES)` and `...LanguageEN` and
      assert the two renders differ and each matches the catalogue's `KeyNavHeaderStatusConnected`
      entry.

## T7. Docs — depends on T1, T2, T3 (references concrete symbols added above)

- [ ] T7.1 Add a new `## i18n — every new user-facing label needs BOTH es and en` section to
      `internal/gateway/AGENTS.md`, worded as binding: any `.templ` change that adds or edits
      user-facing text MUST add/update a catalogue key in `internal/gateway/i18n/catalog.go` with
      both `ES` and `EN` non-empty — `TestCatalog_AllKeysHaveBothLanguages` (T1.5) enforces this
      at `go test` time, but the rule applies to every new key regardless of whether a test
      happens to catch an omission. Point at this change's gold-standard surface (nav shell) as
      the pattern to mirror: `i18n.T(ctx, key)` inside a `.templ` file, `i18n.T(ctx, key)` passed
      an explicit `ctx` argument from a plain Go helper called from one. State plainly that a
      hardcoded English (or Spanish-only) string added to any page from this point forward is
      incomplete work, mirroring the tone of `CLAUDE.md`'s "docs track structural change" rule.
- [ ] T7.2 Add a new subsection under "Read-only at request time" → "Exception: user-initiated
      writes" in `internal/gateway/AGENTS.md`: "### Exception: language switch (D-lang amendment
      — RM24-gateway-add-i18n-foundation)", documenting (mirroring the existing D4/manualcharge
      amendment's shape): `LangSwitch` may call `account.Service.SetLanguage`; no auth guard (works
      anonymous); no tenant-ownership check (self-scoped to the caller's own session uid); **no
      CSRF check**, stated explicitly with the design.md D8 rationale (blast radius + cost of
      requiring every page to mint a token) so a future reader does not mistake the omission for
      an oversight; scope stays narrow to `SetLanguage` only.
- [ ] T7.3 Update root `README.md`'s "Project Structure" tree: add a
      `│   │   └── i18n/            #   translation catalogue + per-request language resolution (es default, en)`
      line under the existing `gateway/` block (alongside `handlers/`, `templates/`, `static/`,
      `tools/`).

## T8. Codegen + full verification — depends on T1–T7

- [ ] T8.1 `make templ` (regenerates `*_templ.go` for every `.templ` file touched: `base.templ`,
      `nav_shell.templ`, `nav_logout.templ`, `nav_header.templ`, the new `lang_switcher.templ`).
- [ ] T8.2 `make css`; `git diff --stat internal/gateway/static/app.css` to confirm it changed (or
      document that it didn't, if no new Tailwind/DaisyUI class was actually introduced beyond
      ones already in the compiled bundle) — module CI guard
      (`internal/gateway/AGENTS.md` "Gotcha — stale CSS").
- [ ] T8.3 `go build ./...` and `go vet ./...` clean repo-wide.
- [ ] T8.4 `go test ./...` green, including every test added in T1–T6.
- [ ] T8.5 Boundary check: `internal/gateway/i18n` imports only `internal/account` (for the
      `LanguageES`/`LanguageEN` constants) and the Go standard library — no `templ` import (it is
      a plain Go package, not a `templates/` subpackage), no other domain module.
- [ ] T8.6 `openspec validate RM24-gateway-add-i18n-foundation --strict` passes and every
      `tasks.md` checkbox above is checked, matching `progress.json`.
