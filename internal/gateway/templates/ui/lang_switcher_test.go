package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// TestLangSwitcher_RendersGlobeAndCurrentCode asserts the switcher renders the
// globe glyph and the current language's uppercase code (design.md D6).
func TestLangSwitcher_RendersGlobeAndCurrentCode(t *testing.T) {
	var buf bytes.Buffer
	if err := templ.Handler(LangSwitcher(LangSwitcherProps{Current: account.LanguageEN})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render LangSwitcher: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "<svg") {
		t.Errorf("LangSwitcher should render an inline <svg> globe icon:\n%s", body)
	}
	if !strings.Contains(body, "EN") {
		t.Errorf("LangSwitcher should show the current language code %q:\n%s", "EN", body)
	}
}

// TestLangSwitcher_NoClientSideJS asserts the CSS-only-dropdown invariant from
// design.md D6 — no <script> tag anywhere in the rendered switcher.
func TestLangSwitcher_NoClientSideJS(t *testing.T) {
	var buf bytes.Buffer
	if err := templ.Handler(LangSwitcher(LangSwitcherProps{Current: account.LanguageES})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render LangSwitcher: %v", err)
	}
	if strings.Contains(buf.String(), "<script") {
		t.Errorf("LangSwitcher must render no client-side JS (CSS-only dropdown):\n%s", buf.String())
	}
}

// TestLangSwitcher_BothOptionsPresent asserts both supported languages' switch
// requests are present, one hx-vals payload per language (design.md D6).
func TestLangSwitcher_BothOptionsPresent(t *testing.T) {
	var buf bytes.Buffer
	if err := templ.Handler(LangSwitcher(LangSwitcherProps{Current: account.LanguageES})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render LangSwitcher: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, `lang&#34;:&#34;es`) {
		t.Errorf("LangSwitcher should carry an hx-vals payload for es:\n%s", body)
	}
	if !strings.Contains(body, `lang&#34;:&#34;en`) {
		t.Errorf("LangSwitcher should carry an hx-vals payload for en:\n%s", body)
	}
}
