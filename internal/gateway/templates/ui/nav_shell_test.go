package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"
	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
)

// TestNavShell_RendersIconsAndSoonBadges asserts the evolved NavShell renders an
// inline <svg> icon per item, lights the active item with menu-active, and appends
// a "Soon" badge ONLY on placeholder items (which also force Href="#"). No external
// CDN / Material Symbols stylesheet is involved — icons are inline SVG via ui.Icon.
func TestNavShell_RendersIconsAndSoonBadges(t *testing.T) {
	items := []NavItem{
		{Label: "Dashboard", Href: "/dashboard", Active: true, Icon: "dashboard"},
		{Label: "Manual Records", Href: "/charges", Icon: "ev_station"},
		{Label: "Supercharger Stats", Icon: "analytics", Placeholder: true},
		{Label: "Settings", Icon: "settings", Placeholder: true},
	}

	// Rendered under English so the "Soon" badge text below matches the
	// catalogue's KeyNavSoonBadge EN value (i18n.T(ctx, ...) now drives the
	// badge text; ctx defaults to es otherwise).
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	var buf bytes.Buffer
	if err := templ.Handler(NavShell(items, nil)).Component.Render(ctx, &buf); err != nil {
		t.Fatalf("render NavShell: %v", err)
	}
	body := buf.String()

	// One inline <svg> per item (4 icons → at least 4 svg opens).
	if got := strings.Count(body, "<svg"); got != 4 {
		t.Errorf("want 4 inline <svg> icons (one per item), got %d", got)
	}
	// No external CDN / icon-font link.
	if strings.Contains(body, "fonts.googleapis.com") || strings.Contains(body, "Material+Symbols") {
		t.Errorf("nav shell must not load an external icon CDN:\n%s", body)
	}
	// "Soon" badge appears only on the two placeholder items.
	if got := strings.Count(body, "Soon"); got != 2 {
		t.Errorf("want 2 'Soon' badges (two placeholders), got %d", got)
	}
	// Only live items keep their Href; placeholders link to "#".
	if !strings.Contains(body, `href="/dashboard"`) || !strings.Contains(body, `href="/charges"`) {
		t.Errorf("live nav items should link to their real href:\n%s", body)
	}
	if got := strings.Count(body, `href="#"`); got != 2 {
		t.Errorf("want 2 placeholder '#' hrefs, got %d", got)
	}
	// Exactly one active item lit (Dashboard).
	if got := strings.Count(body, "menu-active"); got != 1 {
		t.Errorf("want exactly 1 menu-active entry, got %d", got)
	}
}

// TestIcon_ClosedVocabulary asserts every shipped glyph renders an <svg> and an
// unknown name degrades to no markup (the closed-vocabulary contract).
func TestIcon_ClosedVocabulary(t *testing.T) {
	for _, name := range []string{"dashboard", "ev_station", "analytics", "settings", "menu", "battery", "speed", "groups"} {
		var buf bytes.Buffer
		if err := templ.Handler(Icon(IconProps{Name: name})).Component.Render(context.Background(), &buf); err != nil {
			t.Fatalf("render Icon %q: %v", name, err)
		}
		if !strings.Contains(buf.String(), "<svg") {
			t.Errorf("glyph %q should render an <svg>", name)
		}
		if !strings.Contains(buf.String(), `fill="currentColor"`) {
			t.Errorf("glyph %q svg should use currentColor fill (re-skin safe)", name)
		}
	}
	// Unknown glyph → blank, not a broken glyph.
	var buf bytes.Buffer
	if err := templ.Handler(Icon(IconProps{Name: "nope"})).Component.Render(context.Background(), &buf); err != nil {
		t.Fatalf("render unknown Icon: %v", err)
	}
	if strings.TrimSpace(buf.String()) != "" {
		t.Errorf("unknown glyph should render nothing, got %q", buf.String())
	}
}

// TestNavShell_RendersSectionTitles asserts section titles (menu-title) appear
// above the first item of each named section, and whitespace separators (mt-2)
// appear on items that leave a section.
func TestNavShell_RendersSectionTitles(t *testing.T) {
	items := []NavItem{
		{Label: "Dashboard", Href: "/dashboard", Icon: "dashboard"},
		{Label: "Manual Records", Href: "/charges", Icon: "ev_station", SectionLabel: "Charging"},
		{Label: "Supercharger Stats", Href: "/supercharger-stats", Icon: "analytics", SectionLabel: "Charging"},
		{Label: "Vehicle Stats", Icon: "speed", Placeholder: true, SectionLabel: "Insights"},
		{Label: "Community Benchmark", Icon: "groups", Placeholder: true, SectionLabel: "Insights"},
		{Label: "Settings", Icon: "settings", Placeholder: true},
	}
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	var buf bytes.Buffer
	if err := templ.Handler(NavShell(items, nil)).Component.Render(ctx, &buf); err != nil {
		t.Fatalf("render NavShell: %v", err)
	}
	body := buf.String()

	// Exactly two section titles (Charging, Insights)
	if got := strings.Count(body, "menu-title"); got != 2 {
		t.Errorf("want 2 menu-title rows, got %d", got)
	}
	if !strings.Contains(body, "Charging") || !strings.Contains(body, "Insights") {
		t.Errorf("want both section labels rendered:\n%s", body)
	}

	// Soon appears only on the three placeholders
	if got := strings.Count(body, "Soon"); got != 3 {
		t.Errorf("want 3 'Soon' badges, got %d", got)
	}

	// Whitespace separator (mt-2) on Settings (leaves Insights section)
	if !strings.Contains(body, `class="mt-2"`) {
		t.Errorf("want mt-2 class on item leaving section (Settings):\n%s", body)
	}
}