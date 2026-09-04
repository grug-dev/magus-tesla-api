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

// TestNavItems_LabelsTranslate asserts navItems resolves its labels through the
// translation catalogue per the ctx-carried language (design.md D5/D9): the two
// languages must differ and each must match the catalogue.
func TestNavItems_LabelsTranslate(t *testing.T) {
	enCtx := i18n.WithLang(context.Background(), account.LanguageEN)
	esCtx := i18n.WithLang(context.Background(), account.LanguageES)

	en := navItems(enCtx, "/dashboard")
	es := navItems(esCtx, "/dashboard")

	if len(en) != len(es) {
		t.Fatalf("navItems length mismatch: en=%d es=%d", len(en), len(es))
	}
	for i := range en {
		if en[i].Label == es[i].Label {
			t.Errorf("item %d: expected en/es labels to differ, both are %q", i, en[i].Label)
		}
	}

	if en[0].Label != i18n.T(enCtx, i18n.KeyNavDashboard) {
		t.Errorf("en dashboard label = %q, want catalogue value %q", en[0].Label, i18n.T(enCtx, i18n.KeyNavDashboard))
	}
	if es[0].Label != i18n.T(esCtx, i18n.KeyNavDashboard) {
		t.Errorf("es dashboard label = %q, want catalogue value %q", es[0].Label, i18n.T(esCtx, i18n.KeyNavDashboard))
	}

	// Extends the table above with the Settings row's new expectations (Test
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

// TestBase_RendersLangSwitcher asserts the anonymous shell (Home/Login) mounts
// the language selector — the spec delta's "visible on every page including
// login" claim, proven directly (design.md D6).
func TestBase_RendersLangSwitcher(t *testing.T) {
	w := httptest.NewRecorder()
	if err := Base("Test").Render(context.Background(), w); err != nil {
		t.Fatalf("Base render: %v", err)
	}
	if !strings.Contains(w.Body.String(), "dropdown-content") {
		t.Errorf("Base should mount ui.LangSwitcher (no dropdown-content found):\n%s", w.Body.String())
	}
}

// TestBaseAuth_RendersLangSwitcher asserts every authenticated page shell
// inherits the language selector via Base (design.md D6 — "mounted once,
// inherited by BaseAuth", the same shape as RD10's ui.ConfirmDialog).
func TestBaseAuth_RendersLangSwitcher(t *testing.T) {
	w := httptest.NewRecorder()
	if err := BaseAuth("Test", "/dashboard").Render(context.Background(), w); err != nil {
		t.Fatalf("BaseAuth render: %v", err)
	}
	if !strings.Contains(w.Body.String(), "dropdown-content") {
		t.Errorf("BaseAuth should inherit ui.LangSwitcher from Base (no dropdown-content found):\n%s", w.Body.String())
	}
}

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
