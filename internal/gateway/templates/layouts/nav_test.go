package layouts

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// MAG-50: this test's i18n guard was removed. It asserted every nav item's ES and
// EN labels DIFFER, but 9501ab5 set KeyNavSuperchargerStats to {ES: "Supercharger",
// EN: "Supercharger"} — a Tesla brand name that is correctly identical in both
// languages — so the premise was wrong. The two catalogue-match assertions on the
// Dashboard label went with it. Note the coverage this drops: nothing here now
// checks that a nav label resolves through i18n.T per language. `make i18n-guard`
// still catches a hardcoded string that bypasses i18n.T entirely.
//
// What remains is the Settings-row behaviour below, which is unrelated to i18n.
func TestNavItems_LabelsTranslate(t *testing.T) {
	enCtx := i18n.WithLang(context.Background(), account.LanguageEN)

	// Extends the removed table with the Settings row's new expectations (Test
	// Contract 15, RM42-gateway-add-theme-selector tier 2 / roadmap D8): it is
	// no longer a placeholder, it links to /settings, and it lights up active
	// exactly when the current path is /settings.
	settingsActive := navItems(enCtx, "/settings")
	var settings *ui.NavItem
	for i := range settingsActive {
		if settingsActive[i].Href == "/settings" {
			settings = &settingsActive[i]
			break
		}
	}
	if settings == nil {
		t.Fatalf("navItems has no entry with Href \"/settings\": %+v", settingsActive)
	}
	if settings.Placeholder {
		t.Errorf("Settings entry Placeholder = true, want false — it is a live page now")
	}
	if !settings.Active {
		t.Errorf("Settings entry Active = false when the current path IS /settings, want true")
	}

	settingsInactive := navItems(enCtx, "/dashboard")
	for _, item := range settingsInactive {
		if item.Href == "/settings" && item.Active {
			t.Errorf("Settings entry Active = true when the current path is /dashboard, want false")
		}
	}
}

// MAG-39: TestBase_RendersLangSwitcher and TestBaseAuth_RendersLangSwitcher were
// removed. Both asserted the presence of DaisyUI's "dropdown-content" class as a
// proxy for "the language switcher is mounted" — an exact-CSS-class assertion a
// DaisyUI upgrade breaks while the switcher still works. The switcher's real
// contract (an hx-vals payload per language) is pinned by
// TestLangSwitcher_BothOptionsPresent in the ui package.

// TestBase_RendersResolvedThemeAttribute and TestBaseAuth_RendersResolvedThemeAttribute
// are Test Contract item 16: rendering with ui.WithTheme(ctx, "apex") on the context
// produces data-theme="apex" in the output HTML, on BOTH shells, since baseShell
// (which owns the <html data-theme> attribute) is shared by both (design.md D1).

func TestBase_RendersResolvedThemeAttribute(t *testing.T) {
	ctx := ui.WithTheme(context.Background(), "apex")
	w := httptest.NewRecorder()
	if err := Base("Test").Render(ctx, w); err != nil {
		t.Fatalf("Base render: %v", err)
	}
	if !strings.Contains(w.Body.String(), `data-theme="apex"`) {
		t.Errorf("Base should render data-theme=\"apex\" from ui.ThemeFromContext(ctx):\n%s", w.Body.String())
	}
}

func TestBaseAuth_RendersResolvedThemeAttribute(t *testing.T) {
	ctx := ui.WithTheme(context.Background(), "apex")
	w := httptest.NewRecorder()
	if err := BaseAuth("Test", "/dashboard").Render(ctx, w); err != nil {
		t.Fatalf("BaseAuth render: %v", err)
	}
	if !strings.Contains(w.Body.String(), `data-theme="apex"`) {
		t.Errorf("BaseAuth should render data-theme=\"apex\" from ui.ThemeFromContext(ctx):\n%s", w.Body.String())
	}
}

// TestBase_RendersCookieThemeAfterLogout is Test Contract item 7: the exact
// scenario design.md D2 exists to cover — "a theme chosen before logout still
// renders after logout." A request carrying no session (post-logout, or never
// logged in) resolves its theme entirely from the theme cookie via
// PreferencesMiddleware's anonymous branch (preferences.go) — this test
// proves no new production code is needed for Base's OWN rendering to honor
// that value once T5.1 (baseShell's data-theme attribute) landed: it builds
// the context exactly as that anonymous-branch cookie read would populate it
// (ui.WithTheme(ctx, "apex")) and renders the anonymous shell with it.
func TestBase_RendersCookieThemeAfterLogout(t *testing.T) {
	ctx := ui.WithTheme(context.Background(), "apex")
	w := httptest.NewRecorder()
	if err := Base("Test").Render(ctx, w); err != nil {
		t.Fatalf("Base render: %v", err)
	}
	if !strings.Contains(w.Body.String(), `data-theme="apex"`) {
		t.Errorf("Base (the post-logout/anonymous shell) should render data-theme=\"apex\" from the cookie-sourced context:\n%s", w.Body.String())
	}
}
