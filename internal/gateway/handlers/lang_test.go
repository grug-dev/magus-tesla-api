package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// --- LanguageMiddleware's own tests were ported to PreferencesMiddleware ---
//
// LanguageMiddleware itself was removed from lang.go and replaced by
// PreferencesMiddleware (preferences.go), which resolves language AND theme
// from the SAME account.Service.PreferencesFor call (design.md D1,
// RM42-gateway-add-theme-selector tier 2). Every TestLanguageMiddleware_*
// case that lived here was renamed onto PreferencesMiddleware in
// preferences_test.go, with the same assertions — language behavior is
// unchanged. findCookie (below) stays here: it is also used by the
// LangSwitch tests in this file, and preferences_test.go reuses it directly
// (same package, no import needed).

func TestNormalizeLang_UnrecognizedFallsBack(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"en", account.LanguageEN},
		{"es", account.LanguageES},
		{"fr", account.LanguageES},
		{"", account.LanguageES},
		{"EN", account.LanguageES}, // case-sensitive: not a recognized exact match
	}
	for _, tc := range cases {
		if got := normalizeLang(tc.in); got != tc.want {
			t.Errorf("normalizeLang(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// findCookie returns the first cookie named name set on the recorder's
// response, or nil.
func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// --- LangSwitch tests (T3.5) ---

// langSwitchEngine builds a minimal Gin engine with session middleware, a
// /_session route that seeds uid (mirroring sessionCookie's contract), and
// the POST /ui/lang/switch route.
func langSwitchEngine(h *Handler, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	store := cookie.NewStore([]byte("test-secret"))
	r.Use(sessions.Sessions("test", store))
	r.GET("/_session", func(c *gin.Context) {
		sess := sessions.Default(c)
		sess.Set("uid", uid.String())
		_ = sess.Save()
		c.String(http.StatusOK, "ok")
	})
	r.POST("/ui/lang/switch", h.LangSwitch)
	return r
}

func TestLangSwitch_AnonymousSetsCookieOnly(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uuid.Nil)

	w := postLangSwitch(eng, "en", nil, "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	lc := findCookie(w, langCookieName)
	if lc == nil || lc.Value != "en" {
		t.Fatalf("want lang cookie set to en, got %+v", lc)
	}
	if len(acct.setLanguageCalls) != 0 {
		t.Fatalf("anonymous switch must NOT call SetLanguage, got %d calls", len(acct.setLanguageCalls))
	}
}

func TestLangSwitch_SignedInPersistsAndSyncsCookie(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uid)
	sessCookie := signInCookie(eng, uid)

	w := postLangSwitch(eng, "en", []*http.Cookie{sessCookie}, "")

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	if len(acct.setLanguageCalls) != 1 {
		t.Fatalf("want exactly 1 SetLanguage call, got %d", len(acct.setLanguageCalls))
	}
	if acct.setLanguageCalls[0].ID != uid || acct.setLanguageCalls[0].Lang != "en" {
		t.Fatalf("SetLanguage called with %+v, want {%s en}", acct.setLanguageCalls[0], uid)
	}
	lc := findCookie(w, langCookieName)
	if lc == nil || lc.Value != "en" {
		t.Fatalf("want lang cookie set to en, got %+v", lc)
	}
}

func TestLangSwitch_RejectsUnsupportedLanguage(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uuid.Nil)

	w := postLangSwitch(eng, "fr", nil, "")

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for unsupported language, got %d", w.Code)
	}
	if findCookie(w, langCookieName) != nil {
		t.Fatalf("want no lang cookie set on a rejected language")
	}
	if len(acct.setLanguageCalls) != 0 {
		t.Fatalf("want no SetLanguage call on a rejected language, got %d", len(acct.setLanguageCalls))
	}
}

func TestLangSwitch_HXLocationPreservesQueryString(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uuid.Nil)

	form := url.Values{"lang": {"en"}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/lang/switch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Current-URL", "http://x/dashboard/history?start=2026-08-01&end=2026-08-07")
	eng.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	loc := decodeHXLocation(t, w)
	wantPath := "/dashboard/history?start=2026-08-01&end=2026-08-07"
	if loc.Path != wantPath {
		t.Errorf("HX-Location path = %q, want %q", loc.Path, wantPath)
	}
	if loc.Push != "false" {
		t.Errorf("HX-Location push = %q, want %q (no duplicate history entry)", loc.Push, "false")
	}
}

func TestLangSwitch_FallsBackToRefererThenRoot(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uuid.Nil)

	t.Run("no HX-Current-URL, falls back to Referer", func(t *testing.T) {
		form := url.Values{"lang": {"en"}}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ui/lang/switch", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Referer", "http://x/external-charges")
		eng.ServeHTTP(w, req)

		loc := decodeHXLocation(t, w)
		if loc.Path != "/external-charges" {
			t.Errorf("HX-Location path = %q, want %q (from Referer)", loc.Path, "/external-charges")
		}
	})

	t.Run("no HX-Current-URL and no Referer, falls back to root", func(t *testing.T) {
		form := url.Values{"lang": {"en"}}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/ui/lang/switch", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		eng.ServeHTTP(w, req)

		loc := decodeHXLocation(t, w)
		if loc.Path != "/" {
			t.Errorf("HX-Location path = %q, want %q (root fallback)", loc.Path, "/")
		}
	})
}

// TestLangSwitch_CookieIsSameSiteLax is MANDATORY and may not be dropped or
// weakened: design.md D8's user-approved omission of a CSRF token on this
// endpoint rests ENTIRELY on the lang cookie carrying SameSite=Lax, and gin
// only emits that attribute via a separate SetSameSite call that is easy to
// lose in a refactor. If this test is failing, the CSRF decision is void —
// fix the cookie in setLangCookie, never this test.
func TestLangSwitch_CookieIsSameSiteLax(t *testing.T) {
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})
	eng := langSwitchEngine(h, uuid.Nil)

	w := postLangSwitch(eng, "en", nil, "")

	setCookie := w.Header().Get("Set-Cookie")
	if !strings.Contains(setCookie, "SameSite=Lax") {
		t.Fatalf("Set-Cookie header = %q, want it to contain %q — the entire CSRF-omission defence (design.md D8) depends on this attribute", setCookie, "SameSite=Lax")
	}
}

// postLangSwitch issues POST /ui/lang/switch with the given lang form value,
// optional cookies (e.g. a signed-in session cookie), and returns the
// recorder.
func postLangSwitch(eng *gin.Engine, lang string, cookies []*http.Cookie, hxCurrentURL string) *httptest.ResponseRecorder {
	form := url.Values{"lang": {lang}}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/ui/lang/switch", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if hxCurrentURL != "" {
		req.Header.Set("HX-Current-URL", hxCurrentURL)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	eng.ServeHTTP(w, req)
	return w
}

// signInCookie calls /_session on eng to obtain a signed-in session cookie
// for uid (mirrors external_charges_test.go's sessionCookie, scoped to this file's
// own langSwitchEngine which uses the "test" session name). eng's /_session
// route is seeded with a fixed uid at construction (langSwitchEngine), so
// this helper just performs the round trip.
func signInCookie(eng *gin.Engine, _ uuid.UUID) *http.Cookie {
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/_session", nil)
	eng.ServeHTTP(w, req)
	return findCookie(w, "test")
}

// decodeHXLocation decodes the HX-Location response header as JSON, failing
// the test if the header is absent or malformed.
func decodeHXLocation(t *testing.T, w *httptest.ResponseRecorder) hxLocation {
	t.Helper()
	raw := w.Header().Get("HX-Location")
	if raw == "" {
		t.Fatalf("response has no HX-Location header (body: %s)", w.Body.String())
	}
	var loc hxLocation
	if err := json.Unmarshal([]byte(raw), &loc); err != nil {
		t.Fatalf("HX-Location %q is not valid JSON: %v", raw, err)
	}
	return loc
}

// --- GoogleCallback pre-login cookie propagation (T3.5) ---
//
// These exercise h.syncLoginLanguageCookie directly — the exact function
// GoogleCallback calls (handlers.go) — rather than driving a full HTTP round
// trip through GoogleCallback itself. h.google is a concrete
// *googleauth.Client (not an interface); its Exchange method calls Google's
// real OAuth token + userinfo endpoints, so there is no seam in this module
// to fake that call. Testing the extracted helper with a hand-built
// gin.Context exercises the identical production logic without a live
// network dependency.

func TestGoogleCallback_PreLoginCookiePropagatesToAccount(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	req.AddCookie(&http.Cookie{Name: langCookieName, Value: "en"})
	c.Request = req

	h.syncLoginLanguageCookie(c, uid)

	if len(acct.setLanguageCalls) != 1 {
		t.Fatalf("want exactly 1 SetLanguage call from a present lang cookie, got %d", len(acct.setLanguageCalls))
	}
	if acct.setLanguageCalls[0].ID != uid || acct.setLanguageCalls[0].Lang != "en" {
		t.Fatalf("SetLanguage called with %+v, want {%s en}", acct.setLanguageCalls[0], uid)
	}
}

func TestGoogleCallback_NoCookieDoesNotCallSetLanguage(t *testing.T) {
	uid := uuid.New()
	acct := &fakeAccount{}
	h := newHandler(acct, &fakeTesla{})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	c.Request = req

	h.syncLoginLanguageCookie(c, uid)

	if len(acct.setLanguageCalls) != 0 {
		t.Fatalf("want no SetLanguage call when the callback carries no lang cookie, got %d", len(acct.setLanguageCalls))
	}
}
