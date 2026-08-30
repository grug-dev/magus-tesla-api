package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// --- rejectIfInactive (design.md D24, RM34-gateway-block-inactive-login) ---
//
// rejectIfInactive is exercised directly, with a hand-built gin.Context, rather
// than through a full HTTP round trip via GoogleCallback: h.google is a
// concrete *googleauth.Client whose Exchange method calls Google's real OAuth
// endpoints, so there is no seam to fake the exchange from within this module
// (the same constraint documented on syncLoginLanguageCookie's own tests,
// lang_test.go). This is the Test Contract table from design.md.

func newRejectIfInactiveContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/auth/google/callback", nil)
	return c, w
}

func TestRejectIfInactive_ActiveAccountPassesThrough(t *testing.T) {
	c, w := newRejectIfInactiveContext(t)

	got := rejectIfInactive(c, account.Account{Status: account.StatusActive})

	if got {
		t.Fatalf("rejectIfInactive(Active) = true, want false")
	}
	if w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Fatalf("rejectIfInactive(Active) must render nothing, got status=%d body=%q", w.Code, w.Body.String())
	}
}

func TestRejectIfInactive_InactiveAccountIsBlocked(t *testing.T) {
	c, w := newRejectIfInactiveContext(t)

	got := rejectIfInactive(c, account.Account{Status: account.StatusInactive})

	if !got {
		t.Fatalf("rejectIfInactive(Inactive) = false, want true")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("rejectIfInactive(Inactive) status = %d, want 403", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Cuenta desactivada") {
		t.Errorf("blocked page body missing the account_blocked.title text:\n%s", w.Body.String())
	}
}

// TestRejectIfInactive_UnrecognizedStatusIsBlocked pins the "anything not
// exactly StatusActive blocks" behavior: the check is a single == comparison,
// not an allow-list of known-bad values, so an unrecognized/zero-value status
// blocks exactly like Inactive.
func TestRejectIfInactive_UnrecognizedStatusIsBlocked(t *testing.T) {
	c, w := newRejectIfInactiveContext(t)

	got := rejectIfInactive(c, account.Account{Status: ""})

	if !got {
		t.Fatalf("rejectIfInactive(\"\") = false, want true")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("rejectIfInactive(\"\") status = %d, want 403", w.Code)
	}
}
