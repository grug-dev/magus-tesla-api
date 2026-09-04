package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// langCookieName is the anonymous-visitor language cookie (design.md D3/D8).
const langCookieName = "lang"

// langCookieMaxAge is one year, in seconds — the preference should outlive a
// browsing session (design.md D8's normative cookie-attribute spec); this is
// the whole point of persisting it for anonymous visitors.
const langCookieMaxAge = 365 * 24 * 60 * 60

// normalizeLang maps any input to exactly account.LanguageES or
// account.LanguageEN. An unrecognized value (empty, mistyped, or any
// language outside the closed {es, en} set) always normalizes to the
// platform default, es — design.md D3's "never break a render" posture.
func normalizeLang(v string) string {
	if v == account.LanguageEN {
		return account.LanguageEN
	}
	return account.LanguageES
}

// setLangCookie sets/refreshes the lang cookie to lang (which callers MUST
// have already normalized to exactly "es" or "en"). SameSite=Lax is
// LOAD-BEARING, not cosmetic: it is the actual defence behind design.md D8's
// user-approved decision to omit a CSRF token on POST /ui/lang/switch — it is
// what makes modern browsers refuse to attach this cookie to a cross-site
// POST in the first place. gin's c.SetCookie(...) has NO SameSite parameter;
// it is applied ONLY via this separate c.SetSameSite call, which MUST run
// BEFORE SetCookie. Do not remove, reorder, or "simplify" this — doing so
// silently produces a cookie with no SameSite attribute and voids a decision
// the user personally approved (see TestLangSwitch_CookieIsSameSiteLax,
// lang_test.go, which asserts this specifically and must never be weakened).
//
// HttpOnly=true: no JS ever needs to read or write this cookie (unlike
// browser_tz, RD9) — the server is its only consumer.
func setLangCookie(c *gin.Context, lang string) {
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(langCookieName, lang, langCookieMaxAge, "/", "", false, true)
}

// LangSwitch handles POST /ui/lang/switch (design.md D7/D8). It works for
// BOTH anonymous and signed-in callers with NO auth guard (redirect-to-login
// would defeat the anonymous cookie path entirely), NO tenant-ownership check
// (SetLanguage always targets the caller's own session uid — there is no
// user-submitted resource id for a forged request to redirect at a different
// account), and NO CSRF token. The missing CSRF check is a deliberate,
// user-approved divergence from the charging/D4 write-exception pattern —
// see internal/gateway/AGENTS.md "Exception: language switch" for the full
// rationale. The lang cookie's SameSite=Lax attribute (set exclusively by
// setLangCookie, above) is the actual defence; do not add a CSRF check here
// without first re-reading that rationale AND getting the user's sign-off —
// this trade-off was explicitly approved, not a worker's unilateral call.
func (h *Handler) LangSwitch(c *gin.Context) {
	lang := c.PostForm("lang")
	if lang != account.LanguageES && lang != account.LanguageEN {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyLangSwitchErrorUnsupportedLanguage))
		return
	}

	// Always set the cookie first — this is the anonymous path's entire job,
	// and it must happen regardless of whether a signed-in write follows.
	setLangCookie(c, lang)

	if uid, ok := currentUID(c); ok {
		if err := h.acct.SetLanguage(c.Request.Context(), uid, lang); err != nil {
			// The write failed: do NOT still send HX-Location, so the client does
			// not reload into a state the persisted write never actually reached.
			c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyLangSwitchErrorCouldNotSaveLanguage))
			return
		}
	}

	body, err := json.Marshal(hxLocation{Path: hxRedirectPath(c), Push: "false"})
	if err != nil {
		// hxLocation is two plain strings; json.Marshal cannot realistically fail
		// here, but every error is checked per project convention.
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyLangSwitchErrorCouldNotBuildRedirect))
		return
	}
	c.Header("HX-Location", string(body))
	c.Status(http.StatusOK)
}

// syncLoginLanguageCookie propagates an explicitly-present lang cookie into
// the just-resolved account's language preference (design.md D4.2 — "the
// cookie choice carries into the session at login"). Only an EXPLICITLY
// PRESENT cookie (c.Cookie returns no error) triggers a write — not merely
// "the resolved language happens to be es," which is also true for an absent
// cookie. Best-effort: a failure here logs and continues, never fails the
// login over a language-sync write.
//
// Split out from GoogleCallback (handlers.go) as its own function so it can
// be unit-tested directly (see TestGoogleCallback_PreLoginCookiePropagatesToAccount
// / TestGoogleCallback_NoCookieDoesNotCallSetLanguage, lang_test.go) without
// driving a full HTTP round trip through GoogleCallback: h.google is a
// concrete *googleauth.Client (not an interface) that calls Google's real
// OAuth token + userinfo endpoints inside Exchange, so there is no seam to
// fake the exchange from within this module — exercising this function
// directly with a hand-built gin.Context is the only way to test the
// cookie-propagation logic without a live network call.
func (h *Handler) syncLoginLanguageCookie(c *gin.Context, acctID uuid.UUID) {
	cookieVal, cerr := c.Cookie(langCookieName)
	if cerr != nil {
		return
	}
	if err := h.acct.SetLanguage(c.Request.Context(), acctID, normalizeLang(cookieVal)); err != nil {
		log.Printf("google callback: language sync failed for account %s: %v", acctID, err)
	}
}

// hxLocation is the JSON body of the HX-Location response header (design.md
// D7): htmx treats it "like following a hx-boost link" — an AJAX GET to Path
// with the fetched page's <body> swapped in, no full browser reload. Push:
// "false" prevents a duplicate history entry for a URL the user is already
// on. json.Marshal (rather than a hand-built string) is used because Path can
// carry a query string with characters that need proper JSON escaping.
type hxLocation struct {
	Path string `json:"path"`
	Push string `json:"push"`
}

// hxRedirectPath derives the path (+ query string) LangSwitch should target,
// so the caller's current page re-renders in the new language without
// navigating away from wherever they were (design.md D7). Preferred source:
// the HX-Current-URL header, which htmx sends automatically on every
// request. Fallback chain for a non-htmx caller: Referer, then "/".
func hxRedirectPath(c *gin.Context) string {
	if p := pathAndQuery(c.GetHeader("HX-Current-URL")); p != "" {
		return p
	}
	if p := pathAndQuery(c.Request.Referer()); p != "" {
		return p
	}
	return "/"
}

// pathAndQuery parses raw as a URL and returns its Path (+ "?"+RawQuery when
// present). Returns "" on a parse failure, an empty raw string, or a URL with
// no path component, so callers can chain a fallback with a single check.
func pathAndQuery(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		return ""
	}
	p := u.Path
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}
