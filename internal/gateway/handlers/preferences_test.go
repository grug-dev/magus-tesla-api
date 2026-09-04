package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// --- normalizeTheme (Test Contract 1) ---

// TestNormalizeTheme locks in the mapping table Test Contract item 1
// describes for handlers.normalizeTheme specifically — NOT ui.theme_test.go's
// TestNormalizeTheme, which exercises the equivalent ui.IsSupportedTheme +
// ui.DefaultTheme composition ahead of this function's own existence (wave 1
// note). This is the real production function's own coverage.
func TestNormalizeTheme(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"apex", "apex"},
		{"graphite", "graphite"},
		{"halloween", "halloween"},
		{"", ui.DefaultTheme},
		{"cyberpunk", ui.DefaultTheme},
		{"APEX", ui.DefaultTheme}, // case-sensitive, no fuzzy match
	}
	for _, c := range cases {
		if got := normalizeTheme(c.in); got != c.want {
			t.Errorf("normalizeTheme(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- PreferencesMiddleware ---

// prefMiddlewareEngine builds a minimal Gin engine with session middleware,
// PreferencesMiddleware, and a downstream /_prefs route that echoes both
// i18n.FromContext and ui.ThemeFromContext (as "<lang>|<theme>") on the SAME
// request context — the only way to observe what the middleware carried
// through. Renamed/widened from lang_test.go's retired langMiddlewareEngine
// (LanguageMiddleware itself was removed — see lang_test.go's note).
func prefMiddlewareEngine(acct account.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.Use(PreferencesMiddleware(acct))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		if uid := c.Query("uid"); uid != "" {
			sess.Set("uid", uid)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/_prefs", func(c *gin.Context) {
		ctx := c.Request.Context()
		c.String(http.StatusOK, i18n.FromContext(ctx)+"|"+ui.ThemeFromContext(ctx))
	})
	return r
}

// splitPrefs parses the "<lang>|<theme>" body prefMiddlewareEngine's /_prefs
// route returns.
func splitPrefs(t *testing.T, body string) (lang, theme string) {
	t.Helper()
	parts := strings.SplitN(body, "|", 2)
	if len(parts) != 2 {
		t.Fatalf("prefs response %q is not lang|theme shaped", body)
	}
	return parts[0], parts[1]
}

// signedInPrefsRequest performs the two-request dance (seed a session via
// /_session, then hit /_prefs with the resulting session cookie) every
// signed-in PreferencesMiddleware test below needs.
func signedInPrefsRequest(eng *gin.Engine, uid uuid.UUID, extraCookies ...*http.Cookie) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_session?uid="+uid.String(), nil)
	eng.ServeHTTP(w, req)
	sessCookie := findCookie(w, "test")

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/_prefs", nil)
	if sessCookie != nil {
		req2.AddCookie(sessCookie)
	}
	for _, c := range extraCookies {
		req2.AddCookie(c)
	}
	eng.ServeHTTP(w2, req2)
	return w2
}

// --- Ported from lang_test.go's retired TestLanguageMiddleware_* cases ---
// (rename onto PreferencesMiddleware, same assertions — language behavior is
// unchanged; each now also has access to fakeAccount's widened theme field,
// but these particular tests keep their original language-only scope. The
// joint language+theme assertions live in the new tests further below.)

func TestPreferencesMiddleware_SignedInReadsAccount(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{language: account.LanguageEN}
	eng := prefMiddlewareEngine(acct)

	w2 := signedInPrefsRequest(eng, uid)

	lang, _ := splitPrefs(t, w2.Body.String())
	if lang != account.LanguageEN {
		t.Fatalf("signed-in request context language = %q, want %q", lang, account.LanguageEN)
	}
}

func TestPreferencesMiddleware_AnonymousReadsCookie(t *testing.T) {
	eng := prefMiddlewareEngine(&fakeAccount{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_prefs", nil)
	req.AddCookie(&http.Cookie{Name: langCookieName, Value: account.LanguageEN})
	eng.ServeHTTP(w, req)

	lang, _ := splitPrefs(t, w.Body.String())
	if lang != account.LanguageEN {
		t.Fatalf("anonymous request context language = %q, want %q", lang, account.LanguageEN)
	}
}

func TestPreferencesMiddleware_AnonymousNoCookieDefaultsToSpanish(t *testing.T) {
	eng := prefMiddlewareEngine(&fakeAccount{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_prefs", nil)
	eng.ServeHTTP(w, req)

	lang, _ := splitPrefs(t, w.Body.String())
	if lang != account.LanguageES {
		t.Fatalf("anonymous, no cookie: context language = %q, want %q", lang, account.LanguageES)
	}
}

func TestPreferencesMiddleware_SignedInPreferencesForErrorDefaultsToSpanish(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{languageErr: errors.New("db down")}
	eng := prefMiddlewareEngine(acct)

	w2 := signedInPrefsRequest(eng, uid)

	lang, _ := splitPrefs(t, w2.Body.String())
	if lang != account.LanguageES {
		t.Fatalf("PreferencesFor error: context language = %q, want fallback %q", lang, account.LanguageES)
	}
}

func TestPreferencesMiddleware_SyncsStaleLangCookieToDBValue(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{language: account.LanguageEN}
	eng := prefMiddlewareEngine(acct)

	w2 := signedInPrefsRequest(eng, uid, &http.Cookie{Name: langCookieName, Value: account.LanguageES})

	lc := findCookie(w2, langCookieName)
	if lc == nil {
		t.Fatalf("want a fresh lang cookie set when the incoming cookie (es) diverges from the DB value (en)")
	}
	if lc.Value != account.LanguageEN {
		t.Fatalf("resynced lang cookie value = %q, want %q", lc.Value, account.LanguageEN)
	}
}

// --- New theme-specific cases (Test Contract 3, 4, 5, 6) ---

// TestPreferencesMiddleware_SignedInResolvesBothFromOneCall is Test Contract
// item 3: a fake PreferencesFor returning {en, apex} must resolve BOTH
// context values from the SAME call — asserted directly via the call
// counter, not inferred from the absence of a second method on the fake
// (there is no separate LanguageFor/ThemeFor call path in
// PreferencesMiddleware at all).
func TestPreferencesMiddleware_SignedInResolvesBothFromOneCall(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{language: account.LanguageEN, theme: account.ThemeApex}
	eng := prefMiddlewareEngine(acct)

	w2 := signedInPrefsRequest(eng, uid)

	lang, theme := splitPrefs(t, w2.Body.String())
	if lang != account.LanguageEN {
		t.Errorf("context language = %q, want %q", lang, account.LanguageEN)
	}
	if theme != account.ThemeApex {
		t.Errorf("context theme = %q, want %q", theme, account.ThemeApex)
	}
	if acct.preferencesForCalls != 1 {
		t.Fatalf("PreferencesFor called %d times, want exactly 1 (one query, both values)", acct.preferencesForCalls)
	}
}

// TestPreferencesMiddleware_PreferencesForErrorDefaultsBoth is Test Contract
// item 4: a PreferencesFor error must default BOTH values, not panic, and
// let the request proceed.
func TestPreferencesMiddleware_PreferencesForErrorDefaultsBoth(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{languageErr: errors.New("db down")}
	eng := prefMiddlewareEngine(acct)

	w2 := signedInPrefsRequest(eng, uid)

	if w2.Code != http.StatusOK {
		t.Fatalf("want 200 even on a PreferencesFor error, got %d", w2.Code)
	}
	lang, theme := splitPrefs(t, w2.Body.String())
	if lang != account.LanguageES {
		t.Errorf("context language = %q, want fallback %q", lang, account.LanguageES)
	}
	if theme != ui.DefaultTheme {
		t.Errorf("context theme = %q, want fallback %q", theme, ui.DefaultTheme)
	}
}

// TestPreferencesMiddleware_SyncsStaleThemeCookieToDBValue is Test Contract
// item 5's theme half (the lang half is
// TestPreferencesMiddleware_SyncsStaleLangCookieToDBValue above): a stale or
// absent theme cookie is refreshed to the DB value, and a cookie already
// matching triggers no new Set-Cookie.
func TestPreferencesMiddleware_SyncsStaleThemeCookieToDBValue(t *testing.T) {
	t.Run("stale cookie is refreshed", func(t *testing.T) {
		uid := uuid.New()
		acct := &fakeAccount{theme: account.ThemeApex}
		eng := prefMiddlewareEngine(acct)

		w2 := signedInPrefsRequest(eng, uid, &http.Cookie{Name: themeCookieName, Value: account.ThemeGraphite})

		tc := findCookie(w2, themeCookieName)
		if tc == nil {
			t.Fatalf("want a fresh theme cookie set when the incoming cookie (graphite) diverges from the DB value (apex)")
		}
		if tc.Value != account.ThemeApex {
			t.Fatalf("resynced theme cookie value = %q, want %q", tc.Value, account.ThemeApex)
		}
	})

	t.Run("absent cookie is set", func(t *testing.T) {
		uid := uuid.New()
		acct := &fakeAccount{theme: account.ThemeApex}
		eng := prefMiddlewareEngine(acct)

		w2 := signedInPrefsRequest(eng, uid)

		tc := findCookie(w2, themeCookieName)
		if tc == nil || tc.Value != account.ThemeApex {
			t.Fatalf("want a theme cookie set to %q when none was present, got %+v", account.ThemeApex, tc)
		}
	})

	t.Run("matching cookie triggers no Set-Cookie", func(t *testing.T) {
		uid := uuid.New()
		acct := &fakeAccount{theme: account.ThemeApex}
		eng := prefMiddlewareEngine(acct)

		w2 := signedInPrefsRequest(eng, uid, &http.Cookie{Name: themeCookieName, Value: account.ThemeApex})

		if tc := findCookie(w2, themeCookieName); tc != nil {
			t.Fatalf("want no fresh theme Set-Cookie when the incoming cookie already matches the DB value, got %+v", tc)
		}
	})
}

// TestPreferencesMiddleware_AnonymousMakesNoAccountCall is Test Contract item
// 6: an anonymous request must never touch account.Service at all — asserted
// via the call counter, which PreferencesMiddleware's anonymous branch never
// increments (it has no other account.Service call to make in the first
// place).
func TestPreferencesMiddleware_AnonymousMakesNoAccountCall(t *testing.T) {
	acct := &fakeAccount{}
	eng := prefMiddlewareEngine(acct)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_prefs", nil)
	req.AddCookie(&http.Cookie{Name: langCookieName, Value: account.LanguageEN})
	req.AddCookie(&http.Cookie{Name: themeCookieName, Value: account.ThemeApex})
	eng.ServeHTTP(w, req)

	lang, theme := splitPrefs(t, w.Body.String())
	if lang != account.LanguageEN {
		t.Errorf("anonymous context language = %q, want %q", lang, account.LanguageEN)
	}
	if theme != account.ThemeApex {
		t.Errorf("anonymous context theme = %q, want %q", theme, account.ThemeApex)
	}
	if acct.preferencesForCalls != 0 {
		t.Fatalf("anonymous request must make zero PreferencesFor calls, got %d", acct.preferencesForCalls)
	}
}

// TestPreferencesMiddleware_AnonymousInvalidThemeCookieDefaults covers the
// anonymous-branch theme normalization path directly (the mirror image of
// TestNormalizeLang_UnrecognizedFallsBack, lang_test.go): an unsupported
// theme cookie value defaults to ui.DefaultTheme rather than being passed
// through.
func TestPreferencesMiddleware_AnonymousInvalidThemeCookieDefaults(t *testing.T) {
	eng := prefMiddlewareEngine(&fakeAccount{})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_prefs", nil)
	req.AddCookie(&http.Cookie{Name: themeCookieName, Value: "cyberpunk"})
	eng.ServeHTTP(w, req)

	_, theme := splitPrefs(t, w.Body.String())
	if theme != ui.DefaultTheme {
		t.Fatalf("anonymous, unsupported theme cookie: context theme = %q, want %q", theme, ui.DefaultTheme)
	}
}

// --- ThemeSwitch (design.md D3, Test Contract 8-12) ---

// themeSwitchEngine builds a minimal Gin engine with session middleware and
// the POST /ui/theme/switch route (ThemeSwitch) — mirrors
// superchargerRowEngine's shape (supercharger_test.go). issueCSRF is a bool,
// not a string, for the same reason that file gives: a test needs a session
// where csrfThemeKey was NEVER set at all, distinct from an issued-but-empty
// value.
func themeSwitchEngine(h *Handler, uid uuid.UUID, csrfToken string, issueCSRF bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		if uid != uuid.Nil {
			sess.Set("uid", uid.String())
		}
		if issueCSRF {
			sess.Set(csrfThemeKey, csrfToken)
		}
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.POST("/ui/theme/switch", h.ThemeSwitch)
	return r
}

// postThemeSwitch posts theme+csrf_token as a form body to /ui/theme/switch,
// attaching sessCookie (nil for an anonymous request — no /_session call was
// ever made, mirroring TestSuperchargerStatsPage_AnonymousRedirectsToLogin's
// shape of never hitting /_session at all for the anonymous case).
func postThemeSwitch(r *gin.Engine, sessCookie *http.Cookie, theme, csrfToken string) *httptest.ResponseRecorder {
	form := url.Values{"theme": {theme}, "csrf_token": {csrfToken}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/theme/switch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sessCookie != nil {
		req.AddCookie(sessCookie)
	}
	r.ServeHTTP(w, req)
	return w
}

// TestThemeSwitch_Unauthenticated_RedirectsToLogin is Test Contract item 8:
// no session at all -> redirect to /login, SetTheme and the CSRF check are
// never reached (checkCSRFKey is unreachable code here, so we assert its
// downstream effect: zero SetTheme calls and no theme Set-Cookie), and the
// theme cookie is left untouched.
func TestThemeSwitch_Unauthenticated_RedirectsToLogin(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	r := themeSwitchEngine(h, uuid.Nil, "", false)

	w := postThemeSwitch(r, nil, account.ThemeApex, "irrelevant")

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for an unauthenticated request, got %d body=%q", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
	if len(acct.setThemeCalls) != 0 {
		t.Errorf("want zero SetTheme calls for an unauthenticated request, got %d", len(acct.setThemeCalls))
	}
	if tc := findCookie(w, themeCookieName); tc != nil {
		t.Errorf("want no theme Set-Cookie for an unauthenticated request, got %+v", tc)
	}
}

// TestThemeSwitch_MissingOrWrongCSRFToken_Refused is Test Contract item 10:
// covers BOTH "no token was ever issued for this session" and "a wrong
// token was submitted" — both must be refused (403 via checkCSRFKey's
// fail-closed contract), with SetTheme never called and the cookie
// untouched.
func TestThemeSwitch_MissingOrWrongCSRFToken_Refused(t *testing.T) {
	cases := []struct {
		name      string
		issueCSRF bool
		issued    string
		submitted string
	}{
		{name: "no token ever issued for this session", issueCSRF: false, issued: "", submitted: "sometoken"},
		{name: "wrong token submitted", issueCSRF: true, issued: "correcttoken", submitted: "wrongtoken"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			uid := uuid.New()
			acct := &fakeAccount{}
			h := newHandler(acct, &fakeTesla{})
			r := themeSwitchEngine(h, uid, tc.issued, tc.issueCSRF)
			sessCookie := sessionCookie(r, uid, tc.issued)

			w := postThemeSwitch(r, sessCookie, account.ThemeApex, tc.submitted)

			if w.Code != http.StatusForbidden {
				t.Fatalf("want 403, got %d body=%q", w.Code, w.Body.String())
			}
			if len(acct.setThemeCalls) != 0 {
				t.Errorf("want zero SetTheme calls on a CSRF rejection, got %d", len(acct.setThemeCalls))
			}
			if tc2 := findCookie(w, themeCookieName); tc2 != nil {
				t.Errorf("want no theme Set-Cookie on a CSRF rejection, got %+v", tc2)
			}
		})
	}
}

// TestThemeSwitch_RejectsUnsupportedTheme is Test Contract item 11: issued
// WITH a valid CSRF token, so this isolates value validation from the CSRF
// check covered above. An unsupported theme value gets 400, SetTheme is not
// called, and the cookie is untouched.
func TestThemeSwitch_RejectsUnsupportedTheme(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	r := themeSwitchEngine(h, uid, "validtoken", true)
	sessCookie := sessionCookie(r, uid, "validtoken")

	w := postThemeSwitch(r, sessCookie, "cyberpunk", "validtoken")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for an unsupported theme value, got %d body=%q", w.Code, w.Body.String())
	}
	if len(acct.setThemeCalls) != 0 {
		t.Errorf("want zero SetTheme calls for an unsupported theme value, got %d", len(acct.setThemeCalls))
	}
	if tc := findCookie(w, themeCookieName); tc != nil {
		t.Errorf("want no theme Set-Cookie for an unsupported theme value, got %+v", tc)
	}
}

// TestThemeSwitch_SignedInValidCSRF_PersistsThenSyncsCookie is Test Contract
// item 9, the happy path: SetTheme is called with the exact submitted
// value, the theme cookie is set to it, the response is 200, and there is
// no HX-Location header (contrast with LangSwitch, which always sets one —
// design.md D6, a theme change never reloads).
func TestThemeSwitch_SignedInValidCSRF_PersistsThenSyncsCookie(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	r := themeSwitchEngine(h, uid, "validtoken", true)
	sessCookie := sessionCookie(r, uid, "validtoken")

	w := postThemeSwitch(r, sessCookie, account.ThemeApex, "validtoken")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200 on a successful switch, got %d body=%q", w.Code, w.Body.String())
	}
	if len(acct.setThemeCalls) != 1 {
		t.Fatalf("want exactly 1 SetTheme call, got %d", len(acct.setThemeCalls))
	}
	if got := acct.setThemeCalls[0]; got.ID != uid || got.Theme != account.ThemeApex {
		t.Errorf("SetTheme called with (%s, %q), want (%s, %q)", got.ID, got.Theme, uid, account.ThemeApex)
	}
	tc := findCookie(w, themeCookieName)
	if tc == nil || tc.Value != account.ThemeApex {
		t.Fatalf("want a theme Set-Cookie = %q, got %+v", account.ThemeApex, tc)
	}
	if loc := w.Header().Get("HX-Location"); loc != "" {
		t.Errorf("want no HX-Location header on a theme switch, got %q", loc)
	}
}

// TestThemeSwitch_SetThemeErrorLeavesCookieUnchanged is Test Contract item
// 12: SetTheme returning an error responds 500 and — the point this test
// exists to isolate — the theme cookie is NOT set. This is the exact
// opposite of LangSwitch's "cookie first, unconditionally" ordering
// (design.md D3); a future agent must not "simplify" ThemeSwitch to match
// it.
func TestThemeSwitch_SetThemeErrorLeavesCookieUnchanged(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{setThemeErr: errors.New("db write failed")}
	h := newHandler(acct, &fakeTesla{})
	r := themeSwitchEngine(h, uid, "validtoken", true)
	sessCookie := sessionCookie(r, uid, "validtoken")

	w := postThemeSwitch(r, sessCookie, account.ThemeApex, "validtoken")

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 when SetTheme errors, got %d body=%q", w.Code, w.Body.String())
	}
	if tc := findCookie(w, themeCookieName); tc != nil {
		t.Fatalf("want NO theme Set-Cookie when SetTheme errors, got %+v", tc)
	}
}

// --- SettingsPage (design.md D3, Test Contract 13-14) ---

// settingsEngine wires sessions + PreferencesMiddleware + GET /settings
// (SettingsPage) + POST /ui/theme/switch (ThemeSwitch) on ONE engine —
// mirroring gateway.NewEngine's own middleware order (sessions, then
// PreferencesMiddleware immediately after; see gateway.go) without
// importing the gateway package itself, which would cycle back to this one
// (gateway imports handlers). This is the "full middleware+handler stack"
// Test Contract 14 asks for: PreferencesMiddleware genuinely resolves theme
// from acct.PreferencesFor before SettingsPage ever runs, so asserting
// PreferencesFor was called exactly once holds against the real call path,
// not a hand-populated context. The actual GET /settings route is not yet
// registered in gateway.go (T7, a later wave — see AGENTS.md/tasks.md), so
// this local wiring is this wave's only way to exercise SettingsPage
// through the real middleware stack.
func settingsEngine(h *Handler, acct account.Service) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.Use(PreferencesMiddleware(acct))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", c.Query("uid"))
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.GET("/settings", h.SettingsPage)
	r.POST("/ui/theme/switch", h.ThemeSwitch)
	return r
}

// csrfTokenPattern extracts the csrf_token value from ui.ThemeSwitcher's
// rendered hx-vals JSON, as templ's HTML-attribute escaping writes it
// (the same &#34; numeric-entity escaping ui/theme_switcher_test.go's own
// jsonAttrEscape helper relies on). generateCSRFToken always emits
// lowercase hex.
var csrfTokenPattern = regexp.MustCompile(`csrf_token&#34;:&#34;([0-9a-f]+)&#34;`)

// TestSettingsPage_Unauthenticated_RedirectsToLogin is Test Contract item
// 13: an unauthenticated caller is redirected to /login, and NOTHING is
// minted — the auth guard returns before sess.Save() is ever reached, so
// there is no session Set-Cookie at all for this response (and therefore no
// csrf_theme session value either).
func TestSettingsPage_Unauthenticated_RedirectsToLogin(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := settingsEngine(h, acct)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("want 302 for an unauthenticated request, got %d body=%q", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Errorf("want redirect to /login, got %q", loc)
	}
	if sc := findCookie(w, "test"); sc != nil {
		t.Errorf("want no session Set-Cookie for an unauthenticated /settings request (nothing minted), got %+v", sc)
	}
}

// TestSettingsPage_Authenticated_MintsTokenAndRendersCurrentTheme is Test
// Contract item 14: 200, a fresh non-empty csrf_theme session token minted
// via generateCSRFToken(), ui.ThemeSwitcher rendered with Current equal to
// the request's already-resolved theme, and — the invariant this whole tier
// exists to protect — PreferencesFor called EXACTLY ONCE for the whole
// request (asserted against the real PreferencesMiddleware call path, per
// settingsEngine's doc comment above, not a second handler-side call).
func TestSettingsPage_Authenticated_MintsTokenAndRendersCurrentTheme(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{theme: account.ThemeApex}
	h := newHandler(acct, &fakeTesla{})
	eng := settingsEngine(h, acct)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_session?uid="+uid.String(), nil)
	eng.ServeHTTP(w, req)
	sessCookie := findCookie(w, "test")
	if sessCookie == nil {
		t.Fatalf("want a session cookie from /_session")
	}

	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/settings", nil)
	req2.AddCookie(sessCookie)
	eng.ServeHTTP(w2, req2)

	if w2.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%q", w2.Code, w2.Body.String())
	}
	if acct.preferencesForCalls != 1 {
		t.Fatalf("want PreferencesFor called exactly once for the whole request (design.md D1's ONE-query invariant), got %d", acct.preferencesForCalls)
	}

	body := w2.Body.String()
	if !strings.Contains(body, "Apex") {
		t.Errorf("want the rendered page to show the request's already-resolved theme (Apex) as the current selection:\n%s", body)
	}

	m := csrfTokenPattern.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("want a non-empty csrf_token embedded in the rendered ThemeSwitcher options:\n%s", body)
	}
	mintedToken := m[1]

	// Prove the SESSION actually holds mintedToken under csrfThemeKey — not
	// merely that the page shows SOME token — by using it on a follow-up
	// ThemeSwitch POST: checkCSRFKey compares the submitted token against the
	// session's own csrfThemeKey value, so acceptance here is exactly the
	// equality Test Contract 14 asks for.
	//
	// The POST MUST carry the cookie /settings just returned, not the one
	// /_session issued. The store here is cookie.NewStore, so the session
	// lives entirely IN the cookie: SettingsPage's sess.Save() re-issues it
	// with csrf_theme added, and the older /_session cookie still holds only
	// uid. A browser sends the refreshed cookie; replaying the stale one
	// reaches checkCSRFKey with an empty want and is correctly refused 403.
	postCookie := findCookie(w2, "test")
	if postCookie == nil {
		t.Fatalf("want /settings to re-issue the session cookie carrying the minted token")
	}
	w3 := postThemeSwitch(eng, postCookie, account.ThemeGraphite, mintedToken)
	if w3.Code != http.StatusOK {
		t.Fatalf("want the token embedded in SettingsPage's render to be accepted by ThemeSwitch (proving the session holds it under csrfThemeKey), got %d body=%q", w3.Code, w3.Body.String())
	}
}
