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
	for _, path := range []string{"/dashboard", "/ui/vehicles"} {
		w := httptest.NewRecorder()
		eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
			t.Errorf("%s: anonymous should redirect to /login, got %d -> %q", path, w.Code, w.Header().Get("Location"))
		}
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

func TestHome_RendersLayoutAndHtmx(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	for _, want := range []string{"<title>Magus</title>", "/static/htmx.min.js", "Visits this session", `id="health"`} {
		if !strings.Contains(body, want) {
			t.Errorf("home body missing %q", want)
		}
	}
}

func TestHealthFragment_ReturnsFragmentOnly(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ui/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `id="health"`) {
		t.Errorf("fragment missing the health region:\n%s", body)
	}
	if strings.Contains(body, "<html") || strings.Contains(body, "<title>") {
		t.Errorf("fragment must not include the page shell:\n%s", body)
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

func TestSession_VisitCounterRoundTrips(t *testing.T) {
	eng := testEngine(t)

	w1 := httptest.NewRecorder()
	eng.ServeHTTP(w1, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(w1.Body.String(), "Visits this session: 1.") {
		t.Fatalf("first visit should show 1:\n%s", w1.Body.String())
	}
	cookies := w1.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a session cookie to be set")
	}

	// Second visit carrying the session cookie — the counter must advance, proving
	// the signed+encrypted cookie round-trips with no server-side state.
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, ck := range cookies {
		req2.AddCookie(ck)
	}
	w2 := httptest.NewRecorder()
	eng.ServeHTTP(w2, req2)
	if !strings.Contains(w2.Body.String(), "Visits this session: 2.") {
		t.Fatalf("second visit should show 2:\n%s", w2.Body.String())
	}
}
