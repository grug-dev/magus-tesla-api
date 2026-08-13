package layouts

import (
	"context"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// navItems is the sidebar navigation for authenticated pages, rendered by BaseAuth
// through ui.NavShell. Keeping it here (not inline in the .templ) means the single
// source of nav links is plain, testable Go.
//
// active is the current request path (threaded from BaseAuth); each live entry's
// Active flag is set by comparing its Href to active. Placeholder entries carry
// Placeholder: true and link to "#" with a "Soon" badge (future work — no stub route
// is built this change).
//
// ctx carries the request's resolved language (design.md D5/D9): this is a plain Go
// helper called *from* a templ block, so it takes ctx as an explicit argument (unlike
// a templ component, which receives ctx implicitly) — both consume the same
// i18n.T(ctx, ...) surface.
func navItems(ctx context.Context, active string) []ui.NavItem {
	return []ui.NavItem{
		{Label: i18n.T(ctx, i18n.KeyNavDashboard), Href: "/dashboard", Active: active == "/dashboard", Icon: "dashboard"},
		{Label: i18n.T(ctx, i18n.KeyNavManualRecords), Href: "/charges", Active: active == "/charges", Icon: "ev_station"},
		{Label: i18n.T(ctx, i18n.KeyNavSuperchargerStats), Href: "/supercharger-stats", Active: active == "/supercharger-stats", Icon: "analytics"},
		{Label: i18n.T(ctx, i18n.KeyNavSettings), Icon: "settings", Placeholder: true},
	}
}
