package ui

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// TestThemeSwitcher_EveryThemeIsAnOptionCarryingCSRF asserts the switcher renders
// one switch option per entry in Themes, and that EVERY option's hx-vals carries
// both its own theme code and the passed-in CSRFToken (design.md D3/D4). Missing
// a token on one option would leave a switch path that fails CSRF at runtime, so
// this is a security assertion, not a layout one.
//
// MAG-39: the ordering half of the former TestThemeSwitcher_ListsAllThemesInOrder
// was dropped — the order options appear in is layout, checked by eye.
func TestThemeSwitcher_EveryThemeIsAnOptionCarryingCSRF(t *testing.T) {
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

	for _, theme := range Themes {
		if !strings.Contains(body, `hx-vals="`+jsonAttrEscape(themeVals(theme, token))+`"`) {
			t.Errorf("ThemeSwitcher missing an hx-vals option for theme %q carrying token %q:\n%s", theme, token, body)
		}
	}
}

// MAG-39: TestThemeSwitcher_TriggerShowsCurrentTitleCased was removed. The
// casing of the trigger's visible label is appearance.

// jsonAttrEscape mirrors templ's own HTML-attribute escaping of the double quotes in the
// themeVals JSON literal, so the test asserts against exactly what templ writes to the
// response (the same &#34; escaping LangSwitcher's own test file relies on).
func jsonAttrEscape(s string) string {
	return strings.ReplaceAll(s, `"`, fmt.Sprintf("&#%d;", '"'))
}
