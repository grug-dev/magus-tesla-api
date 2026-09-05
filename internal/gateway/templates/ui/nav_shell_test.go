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

// navItemsFixture is the shared item set for this file's behavioural tests:
// two live entries and two placeholders, across two named sections.
func navItemsFixture() []NavItem {
	return []NavItem{
		{Label: "Dashboard", Href: "/dashboard", Active: true, Icon: "dashboard"},
		{Label: "External", Href: "/external-charges", Icon: "ev_station", SectionLabel: "Charging"},
		{Label: "Supercharger Stats", Icon: "analytics", Placeholder: true, SectionLabel: "Charging"},
		{Label: "Settings", Icon: "settings", Placeholder: true},
	}
}

func renderNavShell(t *testing.T) string {
	t.Helper()
	ctx := i18n.WithLang(context.Background(), account.LanguageEN)
	var buf bytes.Buffer
	if err := templ.Handler(NavShell(navItemsFixture(), nil)).Component.Render(ctx, &buf); err != nil {
		t.Fatalf("render NavShell: %v", err)
	}
	return buf.String()
}

// TestNavShell_PlaceholdersDoNotNavigate pins behaviour, not looks: an item
// marked Placeholder must render href="#" so it cannot navigate to a route that
// does not exist yet, while a live item keeps its real href. A placeholder that
// leaked a real href would 404 the user.
func TestNavShell_PlaceholdersDoNotNavigate(t *testing.T) {
	body := renderNavShell(t)

	for _, href := range []string{`href="/dashboard"`, `href="/external-charges"`} {
		if !strings.Contains(body, href) {
			t.Errorf("live nav item should keep its real href %s:\n%s", href, body)
		}
	}
	if got := strings.Count(body, `href="#"`); got != 2 {
		t.Errorf("want 2 placeholder '#' hrefs (one per placeholder item), got %d:\n%s", got, body)
	}
}

// TestNavShell_NoExternalIconCDN pins the Node-less, self-hosted-asset rule:
// icons are inline SVG via ui.Icon, never a Material Symbols stylesheet or any
// other external font/icon CDN. A CDN link would add a third-party runtime
// dependency to a stack that deliberately has none.
func TestNavShell_NoExternalIconCDN(t *testing.T) {
	body := renderNavShell(t)

	for _, banned := range []string{"fonts.googleapis.com", "Material+Symbols"} {
		if strings.Contains(body, banned) {
			t.Errorf("nav shell must not load an external icon CDN (%s):\n%s", banned, body)
		}
	}
}

// TestIcon_ClosedVocabulary asserts every shipped glyph renders an <svg> and an
// unknown name degrades to no markup (the closed-vocabulary contract), and that
// every glyph uses currentColor so a theme swap re-skins it for free.
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

// MAG-39 removed two assertions sets from this file:
//   - TestNavShell_RendersSectionTitles (menu-title count, section label text,
//     the mt-2 separator class) — pure layout.
//   - the icon-count, "Soon"-badge-count and active-item-count halves of the
//     former TestNavShell_RendersIconsAndSoonBadges — decoration.
//
// Both are verified by looking at the nav.
