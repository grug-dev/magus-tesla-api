# Tasks — RM42-gateway-add-theme-selector

> Full design rationale for every decision referenced below (D1–D8) lives in `design.md`.
> Test Contract item numbers (1–21) are also in `design.md`, at its end.
> Group headers state real dependencies so independent groups can be implemented in parallel;
> within a group, do the sub-tasks in order. No task in this tier touches a database migration
> or a `_test.go` requiring `DATABASE_URL` — every test here is a pure/offline Go test or an
> `httptest` against fakes, so there is no "integration tests last" split this time; the
> ordering below is purely by file dependency.
>
> **`POST /ui/theme/switch` requires auth + CSRF (design.md D3, user-settled).** There is no
> anonymous variant of this endpoint anywhere below — do not add one, and do not port
> `lang.go`'s no-CSRF/cookie-first shape onto it. Read `internal/gateway/handlers/
> supercharger.go`'s `csrfSuperchargerKey`/`SuperchargerStatsPage`/`checkCSRFKey` shape before
> starting T4/T6 and mirror it exactly.

## T1. `ui.Themes` closed vocabulary + context carrier + `ui.ThemeSwitcher` — no dependencies

New files only, in the existing `internal/gateway/templates/ui` package (design.md D1/D4).

- [x] T1.1 Create `internal/gateway/templates/ui/theme.go`: `var Themes = []string{"apex",
      "graphite", "halloween"}`; `const DefaultTheme = "graphite"`; `func IsSupportedTheme(v
      string) bool` (loop over `Themes`); an unexported `ctxKey struct{}` + `var themeCtxKey
      ctxKey`; `func WithTheme(ctx context.Context, theme string) context.Context`; `func
      ThemeFromContext(ctx context.Context) string` (comma-ok assertion, `IsSupportedTheme`
      check, default to `DefaultTheme` on anything else — mirrors `i18n.FromContext`'s shape
      exactly, design.md D1). Package imports NOTHING from `internal/account` (`ui.go`'s
      package doc: "no domain imports" — Test Contract 1/2).
- [x] T1.2 Create `internal/gateway/templates/ui/theme_switcher.templ`: `ThemeSwitcherProps{
      Current, CSRFToken string}` (design.md D3 — `CSRFToken` is minted by `SettingsPage`, T6,
      and has nothing to do with T1 itself beyond carrying it through); `templ ThemeSwitcher(p
      ThemeSwitcherProps)` — CSS-only DaisyUI `dropdown` (mirrors `lang_switcher.templ`'s markup
      shape), ranging over `Themes` for its `<li>` options (design.md D4 — NOT hardcoded
      per-option markup, unlike `LangSwitcher`), each option `hx-post="/ui/theme/switch"
      hx-swap="none" hx-vals={themeVals(t, p.CSRFToken)}`; an unexported `titleCase(s string)
      string` helper (first-rune upper-case only — theme names are proper nouns per roadmap D11,
      never `strings.ToUpper`) and `themeVals(t, csrfToken string) string` building the literal
      `{"theme":"<t>","csrf_token":"<csrfToken>"}` JSON (design.md D3 — the token rides in
      `hx-vals`, not an `X-CSRF-Token` header, since this is a POST and Go parses its body
      normally; the header trick is reserved for the DELETE-body case it was built for). Trigger
      text uses `i18n.T(ctx, i18n.KeyThemeSwitcherLabel)` + the title-cased `Current` value;
      `aria-label={ i18n.T(ctx, i18n.KeyThemeSwitcherAria) }`.
- [x] T1.3 `internal/gateway/templates/ui/theme_test.go`: `TestNormalizeTheme`/
      `TestIsSupportedTheme` (Test Contract 1, 2) and `TestThemeFromContext_DefaultsAndPanicSafety`
      (mirrors `TestFromContext_DefaultsAndPanicSafety` in `i18n_test.go` — missing key, wrong
      type, unsupported value all default to `graphite`; no panic).
- [x] T1.4 `internal/gateway/templates/ui/theme_switcher_test.go`: `TestThemeSwitcher_ListsAllThemesInOrder`
      (Test Contract 18 — exactly `len(Themes)` options, in `Themes` order, each `hx-vals`
      carrying both its own theme code AND the passed-in `CSRFToken`) and
      `TestThemeSwitcher_TriggerShowsCurrentTitleCased`.

## T2. i18n catalogue — no dependencies

- [x] T2.1 In `internal/gateway/i18n/catalog.go`, add four `Key` constants + `catalog` entries
      (ES/EN on the same line each, per the file's existing convention): `KeyThemeSwitcherLabel`
      (`"Tema"` / `"Theme"`), `KeyThemeSwitcherAria` (`"Cambiar tema"` / `"Change theme"`),
      `KeyThemeSwitchErrorUnsupportedTheme` (mirrors `KeyLangSwitchErrorUnsupportedLanguage`'s
      wording, e.g. `"tema no soportado"` / `"unsupported theme"`),
      `KeyThemeSwitchErrorCouldNotSaveTheme` (mirrors `KeyLangSwitchErrorCouldNotSaveLanguage`).
      Theme NAMES (`Apex`/`Graphite`/`Halloween`) are explicitly NOT added here — roadmap D11 —
      they render as literal Go string constants from `ui.Themes`/`titleCase`, never through
      `i18n.T`.
- [x] T2.2 No new test needed (Test Contract 17) — the existing
      `TestCatalog_AllKeysHaveBothLanguages` in `catalog_test.go` covers every key added above
      automatically; confirm it still passes by inspection (both languages non-empty on each new
      line).

## T3. `PreferencesMiddleware` + theme cookie helpers — depends on T1

New file `internal/gateway/handlers/preferences.go` (design.md D1/D2). `LanguageMiddleware` is
REMOVED from `lang.go`; everything else in `lang.go` (`LangSwitch`, `syncLoginLanguageCookie`,
`hxLocation`, `pathAndQuery`, `normalizeLang`, `setLangCookie`) is untouched.

- [x] T3.1 In `preferences.go`: `const themeCookieName = "theme"`,
      `const themeCookieMaxAge = 365 * 24 * 60 * 60` (identical to `langCookieMaxAge` —
      design.md D2); `func normalizeTheme(v string) string` (`ui.IsSupportedTheme(v)` → `v`,
      else `ui.DefaultTheme` — Test Contract 1); `func setThemeCookie(c *gin.Context, theme
      string)` — structural copy of `setLangCookie`: `c.SetSameSite(http.SameSiteLaxMode)`
      BEFORE `c.SetCookie(themeCookieName, theme, themeCookieMaxAge, "/", "", false, true)`.
- [x] T3.2 In `preferences.go`: `func cookieOrDefault(c *gin.Context, name string) string`
      (returns the cookie value or `""` on error — a two-line helper factoring the anonymous
      branch's repeated shape, design.md D1) and `func PreferencesMiddleware(acct
      account.Service) gin.HandlerFunc` per design.md D1's pseudocode exactly: signed-in branch
      calls `acct.PreferencesFor(ctx, uid)` ONCE, defaults to `{account.LanguageES,
      ui.DefaultTheme}` on error, syncs BOTH cookies independently against the resolved values;
      anonymous branch reads both cookies via `normalizeLang`/`normalizeTheme`, zero DB calls.
      Ends with `ctx := i18n.WithLang(...); ctx = ui.WithTheme(ctx, theme); c.Request =
      c.Request.WithContext(ctx)`.
- [x] T3.3 Delete `LanguageMiddleware` from `lang.go` (moved above, renamed).
- [x] T3.4 `internal/gateway/handlers/preferences_test.go`: port every existing
      `TestLanguageMiddleware_*` case from `lang_test.go` onto `PreferencesMiddleware` (rename,
      same assertions — language behavior is unchanged), PLUS the new theme-specific cases:
      `TestPreferencesMiddleware_SignedInResolvesBothFromOneCall` (Test Contract 3 — fake
      `PreferencesFor` returns `{en, apex}`, asserts context carries both AND `PreferencesFor`
      call count == 1), `TestPreferencesMiddleware_PreferencesForErrorDefaultsBoth` (Test
      Contract 4), `TestPreferencesMiddleware_SyncsStaleThemeCookieToDBValue` (Test Contract 5),
      `TestPreferencesMiddleware_AnonymousMakesNoAccountCall` (Test Contract 6 — fake asserts
      zero `PreferencesFor`/`LanguageFor`/`ThemeFor` calls).
      Widen the shared `fakeAccount` (`handlers_test.go`) with a `preferencesForCalls int`
      counter and a settable `theme string` field, alongside the existing `language` field.

## T4. `ThemeSwitch` handler + `csrfThemeKey` — depends on T1, T2, T3

Same file, `preferences.go` (design.md D3, SETTLED — user declined the no-CSRF proposal;
this endpoint mirrors the Supercharger/D8 CSRF write-exception, NOT `LangSwitch`). Read
`internal/gateway/handlers/supercharger.go`'s `csrfSuperchargerKey`/`checkCSRFKey` shape before
writing this task — mirror it, do not invent a variant.

- [x] T4.1 In `preferences.go`: `const csrfThemeKey = "csrf_theme"` (a session key, distinct
      from `csrfManualChargeKey`/`csrfVehicleSelectKey`/`csrfSuperchargerKey`).
- [x] T4.2 `func (h *Handler) ThemeSwitch(c *gin.Context)`, in this exact order (mirrors the
      Supercharger amendment's own point order):
      1. **Auth guard first** — `uid, ok := currentUID(c)`; if not ok, `c.Redirect(http.StatusFound,
         "/login")` and return (Test Contract 8 — this IS the endpoint's entire "anonymous
         caller" behavior; there is no anonymous write path to test separately).
      2. **CSRF check** — `if !h.checkCSRFKey(c, csrfThemeKey) { return }` (the existing generic
         checker already writes the 403 body on failure; Test Contract 10).
      3. **Validate the submitted value** — `theme := c.PostForm("theme")`; if
         `!ui.IsSupportedTheme(theme)`, `c.String(http.StatusBadRequest,
         i18n.T(c.Request.Context(), i18n.KeyThemeSwitchErrorUnsupportedTheme))` and return,
         NOTHING persisted, cookie untouched (Test Contract 11).
      4. **Persist** — `if err := h.acct.SetTheme(c.Request.Context(), uid, theme); err != nil`:
         `c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(),
         i18n.KeyThemeSwitchErrorCouldNotSaveTheme))` and return WITHOUT touching the cookie
         (Test Contract 12 — deliberately the OPPOSITE of `LangSwitch`'s "always set the cookie
         first" ordering; the cookie must never claim a value the DB write never reached — see
         design.md D2/D3 for why).
      5. **On success**, `setThemeCookie(c, theme)` THEN `c.Status(http.StatusOK)` — no body, no
         `HX-Location` header (design.md D6 — a theme change never reloads).
- [x] T4.3 `internal/gateway/handlers/preferences_test.go`:
      `TestThemeSwitch_Unauthenticated_RedirectsToLogin` (Test Contract 8 — fake `SetTheme` and
      `checkCSRFKey` never reached, cookie untouched),
      `TestThemeSwitch_MissingOrWrongCSRFToken_Refused` (Test Contract 10 — `403`, `SetTheme` NOT
      called, cookie unchanged; cover BOTH "no token ever issued for this session" and "wrong
      token submitted"), `TestThemeSwitch_RejectsUnsupportedTheme` (Test Contract 11 — issued
      with a VALID CSRF token, so this isolates value validation from the CSRF check),
      `TestThemeSwitch_SignedInValidCSRF_PersistsThenSyncsCookie` (Test Contract 9 — the happy
      path; assert `SetTheme` called with the exact value AND that the cookie is set to it,
      `200`, no `HX-Location`), `TestThemeSwitch_SetThemeErrorLeavesCookieUnchanged` (Test
      Contract 12 — assert NO `Set-Cookie` for `theme` in the response).

## T5. `base.templ` theme attribute + `nav.go` Settings entry — depends on T1

- [x] T5.1 In `internal/gateway/templates/layouts/base.templ`'s `baseShell`, change
      `data-theme="graphite"` to `data-theme={ ui.ThemeFromContext(ctx) }` (design.md D1's whole
      point). Update the surrounding doc comment (it currently explains the hardcoded value) to
      describe the new per-request source.
- [x] T5.2 In `internal/gateway/templates/layouts/nav.go`'s `navItems`, change the Settings
      entry from `{Label: i18n.T(ctx, i18n.KeyNavSettings), Icon: "settings", Placeholder:
      true}` to `{Label: i18n.T(ctx, i18n.KeyNavSettings), Href: "/settings", Active: active ==
      "/settings", Icon: "settings"}` (roadmap D8).
- [x] T5.3 `internal/gateway/templates/layouts/base_test.go` (or wherever `Base`/`BaseAuth`
      tests live today): `TestBase_RendersResolvedThemeAttribute` and
      `TestBaseAuth_RendersResolvedThemeAttribute` (Test Contract 16 — both shells, since
      `baseShell` is shared), PLUS `TestBase_RendersCookieThemeAfterLogout` (Test Contract 7 —
      render `layouts.Base` with `ui.WithTheme(ctx, "apex")` on the context exactly as a
      `PreferencesMiddleware` anonymous-branch cookie read would populate it, and assert
      `data-theme="apex"` — this is the scenario `specs/gateway/spec.md`'s "A theme chosen
      before logout still renders after logout" describes; no new production code, just proving
      `Base`'s existing rendering already honors it once T5.1 lands).
- [x] T5.4 `internal/gateway/templates/layouts/nav_test.go`: extend the existing nav-items test
      table with the Settings row's new `Placeholder: false`/`Href`/`Active` expectations (Test
      Contract 15) rather than adding a new test function.
- [x] T5.5 Run `make templ` (both `.templ` files changed).

## T6. `/settings` page — depends on T1, T3, T4

Depends on T4 (not just T1/T3) because `SettingsPage` mints the SAME `csrfThemeKey` session
token `ThemeSwitch` validates — the two must agree on the key, so `SettingsPage` lives in the
same file, `preferences.go`, as `ThemeSwitch` and its constant (mirrors `SuperchargerStatsPage`
and `csrfSuperchargerKey` living together in `supercharger.go` — do not split them across
files or reintroduce a `settings.go`).

- [x] T6.1 Create `internal/gateway/templates/pages/settings.templ`: `templ
      SettingsPage(theme, csrfToken string)` — `layouts.BaseAuth("Settings — Magus", "/settings")`
      + `ui.PageHeader({Title: i18n.T(ctx, i18n.KeyNavSettings)})` + `ui.Card({})` wrapping
      `ui.ThemeSwitcher(ui.ThemeSwitcherProps{Current: theme, CSRFToken: csrfToken})`. No
      `@templ.Fragment` swap region — design.md's "no htmx fragment counterpart" call (the
      switcher's own POST is self-contained, D6).
- [x] T6.2 In `preferences.go`: `func (h *Handler) SettingsPage(c *gin.Context)` — auth guard
      first (redirect `/login` on failure, Test Contract 13, no token minted); then mint the
      CSRF token EXACTLY as `SuperchargerStatsPage` does — `csrfToken, err := generateCSRFToken()`
      (the EXISTING helper in `charges.go`, same package, no new helper), on error render a
      500, else `sess := sessions.Default(c); sess.Set(csrfThemeKey, csrfToken); _ =
      sess.Save()`; then `render(c, http.StatusOK, pages.SettingsPage(
      ui.ThemeFromContext(c.Request.Context()), csrfToken))` — theme comes from CONTEXT, never a
      second `acct.PreferencesFor`/`ThemeFor` call (Test Contract 14 — this is the line that
      keeps the "ONE query" invariant on this specific page).
- [x] T6.3 `internal/gateway/handlers/preferences_test.go` additions:
      `TestSettingsPage_Unauthenticated_RedirectsToLogin` (Test Contract 13 — no session value
      set for `csrfThemeKey`), `TestSettingsPage_Authenticated_MintsTokenAndRendersCurrentTheme`
      (Test Contract 14 — build the request through the FULL middleware+handler stack via
      `NewEngine` so `PreferencesMiddleware` actually runs first, assert the fake's
      `preferencesForCalls == 1` for the whole request, AND assert the session holds a non-empty
      `csrfThemeKey` value equal to the token passed into `ui.ThemeSwitcherProps.CSRFToken`).
- [x] T6.4 Run `make templ`.

## T7. Wire routes + rename the middleware registration — depends on T3, T4, T6

- [x] T7.1a (rename clause only) In `internal/gateway/gateway.go`: changed
      `r.Use(handlers.LanguageMiddleware(d.Account))` to
      `r.Use(handlers.PreferencesMiddleware(d.Account))`. Done in wave 2 (T3), per the leader's
      explicit instruction, so the build would not stay red for three waves over one line while
      `LanguageMiddleware` no longer existed after T3.3 deleted it. `h.SettingsPage`/
      `h.ThemeSwitch` do not exist yet — their handlers land in T4/T6 — so the route clause below
      is NOT done and stays for whichever wave completes T4/T6.
- [ ] T7.1b (route clause, still open) add `r.GET("/settings", h.SettingsPage)` and
      `r.POST("/ui/theme/switch", h.ThemeSwitch)` to `internal/gateway/gateway.go`.
- [ ] T7.2 If any existing `gateway_test.go` route-table test enumerates registered routes by
      name, add the two new ones.

## T8. Instant-apply JS (RD15) — depends on T1

- [x] T8.1 Append the two delegated listeners from design.md D6 to
      `internal/gateway/static/app.js`: the `click` listener on
      `button[hx-post="/ui/theme/switch"]` (optimistic apply, stashes
      `document.documentElement.dataset.themePrevious`), and the `htmx:afterRequest` listener
      that reverts on `!evt.detail.successful` and clears the stashed previous value on success.
      Follow the file's existing comment style (cite RD15, explain why, link design.md D6).
- [x] T8.2 No `go test` coverage exists for this (Test Contract 21 — no JS harness in this
      project, matching RD9–RD14's own precedent). Verify manually per T11 below.

## T9. `make theme-guard` — depends on T1

- [x] T9.1 Add a `theme-guard` target to the `Makefile`, in the grep-based shape design.md D5
      specifies exactly (three extraction passes — `ui.Themes`, `account`'s `Theme*` constants,
      `input.css`'s `@plugin`/`@import` shapes — sorted-unique diff, escape hatch `//
      theme:allow: <reason>`). Add `theme-guard` to `.PHONY` and to `check`'s prerequisite list:
      `check: build vet ui-guard i18n-guard money-guard tz-guard migration-guard boundary-guard
      theme-guard test`.
- [x] T9.2 Run `make theme-guard` directly (allowed per `Test-Execution-Policy` — a Makefile
      guard is not `go test`) and confirm it passes (Test Contract 19). Then temporarily remove
      one entry from `ui.Themes`, re-run to confirm the target fails and names the mismatch
      (Test Contract 20), then restore it. Do NOT leave the repo in the broken state.

## T10. D7 — manual Halloween check — depends on T6 (needs a working `/settings` control to
switch themes through the real UI, though setting the `theme` cookie by hand earlier is also
sufficient if this is done before T6 lands)

- [ ] T10.1 With `make dev` running, select `halloween` on `/settings`, then visually inspect
      the dashboard's charts and tiles (battery bar, history bars, KPI tiles, badges/dots).
      Record findings — anything that looks broken (the theme is an unstyled DaisyUI builtin,
      per roadmap D7) — in this change's final worker report. A report, not a redesign: fix
      inline ONLY if it is a trivial one-line class change; otherwise note it as a follow-up.

## T11. Docs — depends on T1, T3, T4, T6, T8, T9 (documents the finished behavior)

- [ ] T11.1 `internal/gateway/AGENTS.md`:
      - Add the new **RD15** section (design.md D6), in the same What/Why/Boundary/Graceful-
        degradation shape as RD9–RD14, inserted after the existing RD14 section.
      - Fix both "exactly **FIVE** sanctioned exceptions" mentions (near the existing line ~200
        and line ~803) to "exactly **SIX**."
      - Add a short subsection near "Theme file layout & switching" documenting roadmap D10's
        four-step "add a theme" recipe verbatim.
      - Add a new "Exception: theme switch" subsection, SIBLING to (not modifying) "Exception:
        language switch", recording D3's settled outcome verbatim: `ThemeSwitch` requires auth
        + `csrfThemeKey` CSRF, mirroring the Supercharger/D8 amendment, and explicitly does NOT
        take the language exception's shape — state why (the language exception's cost argument
        rests on `LangSwitcher` mounting on every page including anonymous ones; `ThemeSwitcher`
        mounts on exactly one already-authenticated page, so the argument that earned language
        its exception does not apply). Also state the cookie-ordering divergence: the `theme`
        cookie is set only AFTER a successful `SetTheme`, not unconditionally first like `lang`'s
        — so a future agent does not "simplify" this file by copying `lang.go`'s shape onto it.
- [ ] T11.2 Root `README.md`, "Switching the theme" section: rewrite per design.md D8 — how a
      user changes their own theme now (`/settings`), the still-accurate "adding a new palette"
      recipe extended with the `ui.Themes` + `make theme-guard` steps. Verify the "Project
      Structure" tree needs no change (design.md D8 — it lists directories, not files, and no
      new package is introduced).
- [ ] T11.3 Grep `kkpa/context/` for `gateway`/`theme`/`settings` and fix any guide whose
      consumer map, file map, or title this change invalidates (`CLAUDE.md` docs-track-change
      rule). If no KB exists or no guide references these, say so explicitly rather than
      skipping silently.

## Cheap-signal checklist (run yourself; do not defer to the owner)

After each group above: `go build ./...`, `go vet ./...`, `gofmt -l .`. After T9: `make
theme-guard` (see T9.2). After any `.templ` edit (T1, T5, T6): `make templ`. If any new DaisyUI/
Tailwind class was introduced anywhere in T1/T6 (unlikely — the dropdown/menu classes should
already be in the compiled bundle from `LangSwitcher`'s use of them, but confirm): `make css`
and `git diff --exit-code internal/gateway/static/app.css`.

## Suite commands for the owner (never run by the assistant)

```sh
go test ./internal/gateway/...
make test
make check
```
