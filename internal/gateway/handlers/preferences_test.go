package handlers

import (
	"errors"
	"net/http"
	"net/http/httptest"
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
