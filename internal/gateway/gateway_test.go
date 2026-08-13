package gateway_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// testEngine builds the gateway with a pool pointing at an unreachable database.
// pgxpool.New is lazy (it doesn't connect up front), so this succeeds; pool.Ping
// then fails — exercising the "unhealthy" path without needing a real database.
func testEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/none?sslmode=disable")
	if err != nil {
		t.Fatalf("pool: %v", err)
	}
	t.Cleanup(pool.Close)
	google := googleauth.NewClient("test-client-id", "test-client-secret", "http://localhost/auth/google/callback")
	eng, err := gateway.NewEngine(gateway.Deps{
		Pool:              pool,
		Account:           account.NewService(pool, "", ""),
		Google:            google,
		Tesla:             tesla.NewClient(),
		SessionSecret:     "test-secret-do-not-use",
		TeslaClientID:     "test-tesla-id",
		TeslaClientSecret: "test-tesla-secret",
		TeslaRedirectURL:  "http://localhost/connect/tesla/callback",
	})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	return eng
}

func TestConnectTesla_AnonymousRedirectedToLogin(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/connect/tesla", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous connect should redirect to /login, got %d -> %q", w.Code, w.Header().Get("Location"))
	}
}

func TestTeslaCallback_AnonymousRedirectedToLogin(t *testing.T) {
	eng := testEngine(t)
	// The callback is auth-guarded: an anonymous request is sent to /login before any
	// state check or token save. (The signed-in state-mismatch path mirrors the tested
	// Google callback in TestGoogleCallback_RejectsMismatchedState.)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/connect/tesla/callback?state=forged&code=x", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous callback should redirect to /login, got %d -> %q", w.Code, w.Header().Get("Location"))
	}
}

// TestLogin_RendersContinueWithGoogle asserts the re-skinned login page offers the
// existing Google OAuth flow as a single "Continue with Google" affordance and drops
// the Stitch email/password/"Sign In"/"OR" elements, the external bg image, and all
// inline <script>.
func TestLogin_RendersContinueWithGoogle(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// Anonymous request through the real NewEngine (handlers.LanguageMiddleware IS
	// wired here, unlike the handlers-package unit test engines): no "lang" cookie
	// set → resolves to the platform default, Spanish (KeyLoginContinueGoogle's ES
	// value) — mirrors tier 2's T6.4 precedent (assert the resolved-language
	// string, not the pre-existing English literal).
	for _, want := range []string{"Continuar con Google", `href="/auth/google/login"`} {
		if !strings.Contains(body, want) {
			t.Errorf("login body missing %q\n%s", want, body)
		}
	}
	for _, gone := range []string{
		`type="email"`, `type="password"`,
		`googleusercontent.com`,
		"<script>",
	} {
		if strings.Contains(body, gone) {
			t.Errorf("login body should NOT contain %q", gone)
		}
	}
}

func TestGoogleLogin_RedirectsToGoogleWithState(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/google/login", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "accounts.google.com") || !strings.Contains(loc, "state=") {
		t.Errorf("redirect should target Google with a state param, got %q", loc)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Error("expected a session cookie storing the CSRF state")
	}
}

func TestGoogleCallback_RejectsMismatchedState(t *testing.T) {
	eng := testEngine(t)
	// No prior /auth/google/login → no stored state, so any callback state is invalid.
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/auth/google/callback?state=forged&code=x", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for mismatched state", w.Code)
	}
}

func TestDashboard_AnonymousRedirectedToLogin(t *testing.T) {
	eng := testEngine(t)
	for _, path := range []string{"/dashboard"} {
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Errorf("%s: anonymous should redirect to /login, got %d -> %q", path, w.Code, w.Header().Get("Location"))
		}
	}
}

// TestNavHeaderFragment_AnonymousRedirectedToLogin asserts the nav-header fragment
// route is auth-guarded (spec: a request without an authenticated session is
// redirected to /login — no header served to anonymous callers).
func TestNavHeaderFragment_AnonymousRedirectedToLogin(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/nav-header", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("anonymous /ui/nav-header should redirect to /login, got %d -> %q", w.Code, w.Header().Get("Location"))
	}
}

func TestHome_AnonymousOffersSignIn(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w.Body.String(), `href="/login"`) {
		t.Errorf("anonymous home should offer a sign-in link:\n%s", w.Body.String())
	}
}

// TestHealthRoute_Removed asserts the dead /ui/health route was unregistered by
// the home-debug cleanup — the router now returns 404, not a fragment.
func TestHealthRoute_Removed(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/health", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /ui/health should be 404 after the health-fragment removal, got %d", w.Code)
	}
}

// TestHome redirects anonymous users to the sign-in page and authenticated users
// to the dashboard — the landing page no longer renders its own body; /dashboard
// IS the default authenticated experience (the old pages.Home view is retired).
func TestHome_AnonymousRedirectsToLogin(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302 (redirect to /login)", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/login" {
		t.Fatalf("Location = %q, want /login", loc)
	}
}

// TestSuperchargerStats_AnonymousRedirectedToLogin covers F.3: the router-level
// check that both registered Supercharger Stats routes (the full page and the
// htmx fragment) are auth-guarded through the real gateway.NewEngine router —
// not just the handler tested directly (handlers/supercharger_test.go already
// covers that). Mirrors TestDashboard_AnonymousRedirectedToLogin.
func TestSuperchargerStats_AnonymousRedirectedToLogin(t *testing.T) {
	eng := testEngine(t)
	for _, path := range []string{"/supercharger-stats", "/ui/supercharger-stats"} {
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Errorf("%s: anonymous should redirect to /login, got %d -> %q", path, w.Code, w.Header().Get("Location"))
		}
	}
}

func TestHealthz_UnhealthyWhenDBUnreachable(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when DB unreachable", w.Code)
	}
}

func TestStaticAsset_ServedFromEmbeddedFS(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/static/htmx.min.js", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for embedded htmx", w.Code)
	}
	if !strings.Contains(w.Body.String(), "htmx") {
		t.Errorf("served asset does not look like htmx")
	}
}
