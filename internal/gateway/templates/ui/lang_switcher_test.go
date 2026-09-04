package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cristianpena/magus-tesla-api/internal/account"
)

// MAG-39: TestLangSwitcher_RendersGlobeAndCurrentCode was removed. It asserted
// an <svg> glyph and the visible "EN" text — decoration, checked by eye.

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
