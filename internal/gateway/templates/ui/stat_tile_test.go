package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

// renderStatTrendIcon renders just the trend icon for one Trend value —
// helper for the table below.
func renderStatTrendIcon(t *testing.T, trend string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := templ.Handler(statTrendIcon(trend)).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render statTrendIcon(%q): %v", trend, err)
	}
	return buf.String()
}

// statTrendIcon has a closed vocabulary, so every value is asserted here:
// each renders exactly one <svg> with the right class list, or none. Asserts the class list and element count only
// — never path data, colour hex, or element order (ai/go-conventions.md
// §Testing).
func TestStatTrendIcon_ClosedVocabulary(t *testing.T) {
	cases := []struct {
		trend     string
		wantClass string // "" means no <svg> at all
	}{
		{"", ""},
		{"up", "h-5 w-5 text-success shrink-0"},
		{"down", "h-5 w-5 text-error shrink-0"},
		{"up-neutral", "h-5 w-5 text-neutral shrink-0"},
		{"down-neutral", "h-5 w-5 text-neutral shrink-0"},
	}
	for _, tc := range cases {
		t.Run(tc.trend, func(t *testing.T) {
			body := renderStatTrendIcon(t, tc.trend)
			gotCount := strings.Count(body, "<svg")
			if tc.wantClass == "" {
				if gotCount != 0 {
					t.Errorf("Trend %q: want no <svg>, got %d", tc.trend, gotCount)
				}
				return
			}
			if gotCount != 1 {
				t.Errorf("Trend %q: want exactly one <svg>, got %d", tc.trend, gotCount)
			}
			wantAttr := `class="` + tc.wantClass + `"`
			if !strings.Contains(body, wantAttr) {
				t.Errorf("Trend %q: want svg with %s, got:\n%s", tc.trend, wantAttr, body)
			}
		})
	}
}
