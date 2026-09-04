package ui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestThemeSwitcher_ListsAllThemesInOrder asserts (Test Contract 18) that the switcher
// renders exactly len(Themes) options, in Themes order, each option's hx-vals carrying
// both its own theme code AND the passed-in CSRFToken (design.md D3/D4).
func TestThemeSwitcher_ListsAllThemesInOrder(t *testing.T) {
	const token = "deadbeef"
	var buf bytes.Buffer
	if err := templ.Handler(ThemeSwitcher(ThemeSwitcherProps{Current: "graphite", CSRFToken: token})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render ThemeSwitcher: %v", err)
	}
	body := buf.String()

	gotOptionCount := strings.Count(body, "hx-post=\"/ui/theme/switch\"")
	if gotOptionCount != len(Themes) {
		t.Errorf("ThemeSwitcher rendered %d options, want %d (len(Themes)):\n%s", gotOptionCount, len(Themes), body)
	}

	lastIdx := -1
	for _, theme := range Themes {
		idx := strings.Index(body, `hx-vals="`+jsonAttrEscape(themeVals(theme, token))+`"`)
		if idx == -1 {
			t.Fatalf("ThemeSwitcher missing an hx-vals option for theme %q carrying token %q:\n%s", theme, token, body)
		}
		if idx <= lastIdx {
			t.Errorf("ThemeSwitcher option %q rendered out of Themes order:\n%s", theme, body)
		}
		lastIdx = idx
	}
}

// TestThemeSwitcher_TriggerShowsCurrentTitleCased asserts the trigger's visible text
// includes the title-cased Current value (design.md D4), never the raw lowercase code.
func TestThemeSwitcher_TriggerShowsCurrentTitleCased(t *testing.T) {
	var buf bytes.Buffer
	if err := templ.Handler(ThemeSwitcher(ThemeSwitcherProps{Current: "apex", CSRFToken: "tok"})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render ThemeSwitcher: %v", err)
	}
	body := buf.String()
	if !strings.Contains(body, "Apex") {
		t.Errorf("ThemeSwitcher trigger should show the title-cased current theme %q:\n%s", "Apex", body)
	}
	if strings.Contains(body, ">apex<") {
		t.Errorf("ThemeSwitcher trigger should not show the raw lowercase theme code:\n%s", body)
	}
}

// jsonAttrEscape mirrors templ's own HTML-attribute escaping of the double quotes in the
// themeVals JSON literal, so the test asserts against exactly what templ writes to the
// response (the same &#34; escaping LangSwitcher's own test file relies on).
func jsonAttrEscape(s string) string {
	return strings.ReplaceAll(s, `"`, fmt.Sprintf("&#%d;", '"'))
}
