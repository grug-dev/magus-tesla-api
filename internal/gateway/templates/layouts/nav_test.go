package layouts

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
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
