// preferences.go contains PreferencesMiddleware — the request's single
// preference-resolution point, which resolves BOTH the active language and
// the active UI theme from ONE account.Service.PreferencesFor call per
// signed-in request (design.md D1, RM42-gateway-add-theme-selector tier 2) —
// plus the theme cookie helpers that mirror lang.go's lang-cookie shape
// (design.md D2). It replaces lang.go's former LanguageMiddleware, which
// resolved language alone via the now-doubled-cost LanguageFor call.
//
// csrfThemeKey and ThemeSwitch (design.md D3, roadmap RM42 tier 2 task T4)
// also live in this file — the write handler that persists a theme change,
// CSRF-protected the same way the Supercharger session-verification write
// is (design.md D8 amendment in AGENTS.md), NOT the way lang.go's
// LangSwitch is. SettingsPage (T6, which mints the csrfThemeKey token this
// handler checks) is NOT in this file yet — it lands in a later wave.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// themeCookieName is the READ-ONLY (from the anonymous branch's point of
// view) theme cookie (design.md D2). Unlike langCookieName, there is no
// anonymous WRITE path to this cookie anywhere in the gateway — its sole
// writer is the signed-in-only ThemeSwitch handler (design.md D3/D8, a later
// wave), and PreferencesMiddleware's own signed-in branch, which re-syncs it
// to whatever account.Service.PreferencesFor just resolved. The anonymous
// branch below only ever reads it.
const themeCookieName = "theme"

// themeCookieMaxAge mirrors langCookieMaxAge exactly (design.md D2 — one
// year, in seconds): the preference should outlive a browsing session.
const themeCookieMaxAge = 365 * 24 * 60 * 60

// normalizeTheme maps any input to one of ui.Themes, defaulting to
// ui.DefaultTheme for anything unsupported (empty, mistyped, or a code
// outside the closed vocabulary) — the handlers-package mirror of
// normalizeLang, built on the ui package's own IsSupportedTheme/DefaultTheme
// (Test Contract 1).
func normalizeTheme(v string) string {
	if ui.IsSupportedTheme(v) {
		return v
	}
	return ui.DefaultTheme
}

// setThemeCookie sets/refreshes the theme cookie to theme (which callers
// MUST have already normalized to one of ui.Themes). A direct structural
// copy of setLangCookie (lang.go) — SameSite=Lax is set via c.SetSameSite
// BEFORE c.SetCookie, since gin's SetCookie has no SameSite parameter of its
// own; reordering these two calls silently produces a cookie with no
// SameSite attribute (design.md D2).
//
// HttpOnly=true: no client-side JS ever needs to READ this cookie — the
// RD15 instant-apply listener (static/app.js) reads/writes
// document.documentElement.dataset.theme, a DOM attribute, never
// document.cookie. This cookie exists purely so the SERVER can resolve the
// right theme on the NEXT request.
func setThemeCookie(c *gin.Context, theme string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(themeCookieName, theme, themeCookieMaxAge, "/", "", false, true)
}

// cookieOrDefault returns the named cookie's value, or "" on any error
// (absent cookie, malformed request). Factors the anonymous-branch shape
// PreferencesMiddleware needs twice below (design.md D1) — not a new
// concept, just named: LanguageMiddleware's own anonymous branch already
// read exactly this way for lang, inline.
func cookieOrDefault(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}

// PreferencesMiddleware resolves the active render language AND the active
// UI theme EXACTLY ONCE per signed-in request (design.md D1 — the whole
// point of tier 1's account.Service.PreferencesFor: a caller needing both
// values pays for one query, not two) and stores both on the request's
// context.Context — language via i18n.WithLang, theme via ui.WithTheme — so
// every handler and every nested Templ component downstream reads them via
// i18n.FromContext/i18n.T and ui.ThemeFromContext with zero additional
// plumbing. Registered in gateway.go immediately AFTER the sessions
// middleware (renamed from handlers.LanguageMiddleware there in this same
// wave), because the signed-in branch below needs currentUID, which reads
// the session — ordering matters, unchanged from before.
//
//   - Signed in: acct.PreferencesFor(ctx, uid) ONCE. Any error falls back to
//     {account.LanguageES, ui.DefaultTheme} (never breaks the render over a
//     transient DB error). Each cookie (lang, theme) is independently
//     re-synced to its resolved value if the incoming cookie is absent or
//     stale — at no extra DB-read cost, since both values came from the one
//     PreferencesFor call already made for this request.
//   - Anonymous: reads both cookies via normalizeLang/normalizeTheme. Zero DB
//     calls — unchanged from LanguageMiddleware's own anonymous behavior,
//     just widened to a second cookie of the same shape. This is the ONLY
//     place the theme cookie is ever read for an anonymous request, and it is
//     never written here.
//
// See design.md D1 for the pseudocode this implements verbatim, and the
// rejected alternative (a second ThemeMiddleware calling acct.ThemeFor
// separately) this function deliberately avoids — that would double the
// per-signed-in-request DB read count the roadmap explicitly rules out.
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

// csrfThemeKey is the session key for the theme-switch CSRF token
// (design.md D3) — a new key, distinct from csrfManualChargeKey,
// csrfVehicleSelectKey, and csrfSuperchargerKey. Minted by SettingsPage
// (T6, a later wave) via the existing generateCSRFToken() (charges.go);
// checked here by ThemeSwitch via the existing checkCSRFKey (charges.go).
const csrfThemeKey = "csrf_theme"

// ThemeSwitch handles POST /ui/theme/switch — persists a theme change for
// the calling user. Requires an authenticated session and a valid
// csrfThemeKey CSRF token: this endpoint mirrors the Supercharger/D8
// write-exception pattern (SuperchargerRowUpdate in supercharger.go), NOT
// lang.go's LangSwitch shape (design.md D3, SETTLED — the user explicitly
// declined the no-CSRF option analyzed and initially proposed in an earlier
// draft of design.md). There is no anonymous path to this endpoint: the
// only control capable of submitting to it, ui.ThemeSwitcher, is rendered
// exclusively on the authenticated /settings page.
//
// Order (Test Contract 8-12, design.md D3 point 4 — mirrors the
// Supercharger amendment's own point order exactly):
//  1. Auth guard — no session, no token could ever have been minted, so
//     checking CSRF before auth would just compare against an empty session
//     value. An unauthenticated caller is redirected to /login; this IS the
//     endpoint's entire "anonymous caller" behavior (Test Contract 8).
//  2. CSRF check — checkCSRFKey already writes the 403 body on failure.
//  3. Value validation against the closed ui.Themes vocabulary — an
//     unsupported value is rejected with nothing persisted and the cookie
//     untouched (Test Contract 11).
//  4. Persist via account.Service.SetTheme. A write error responds 500 and
//     returns WITHOUT touching the cookie (Test Contract 12).
//  5. Only on a successful persist is the theme cookie refreshed, then a
//     bare 200 with no body and no HX-Location header — a theme change
//     never reloads or re-navigates the page (design.md D6; RD15 in
//     static/app.js applies it client-side instantly instead).
//
// The cookie-write ordering (step 5, AFTER the persist) is the one place
// this handler deliberately does NOT mirror lang.go's LangSwitch, which
// sets its cookie FIRST, unconditionally. That is correct there because the
// lang cookie is that endpoint's only persistence for an anonymous caller.
// Since design.md D2, the theme cookie has no anonymous writer at all — it
// is purely a mirror of what the account row actually holds — so setting it
// before (or regardless of) a successful SetTheme would let it claim a
// value the database write never reached. Do not "fix" this ordering to
// match lang.go; see AGENTS.md's "Exception: theme switch" (D8) for the
// same warning recorded where a future agent is more likely to read it
// first.
func (h *Handler) ThemeSwitch(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRFKey(c, csrfThemeKey) {
		return
	}

	theme := c.PostForm("theme")
	if !ui.IsSupportedTheme(theme) {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyThemeSwitchErrorUnsupportedTheme))
		return
	}

	if err := h.acct.SetTheme(c.Request.Context(), uid, theme); err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyThemeSwitchErrorCouldNotSaveTheme))
		return
	}

	setThemeCookie(c, theme)
	c.Status(http.StatusOK)
}
