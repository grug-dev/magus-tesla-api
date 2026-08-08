package layouts

import "github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"

// navItems is the sidebar navigation for authenticated pages, rendered by BaseAuth
// through ui.NavShell. Keeping it here (not inline in the .templ) means the single
// source of nav links is plain, testable Go.
//
// active is the current request path (threaded from BaseAuth); each live entry's
// Active flag is set by comparing its Href to active. Placeholder entries carry
// Placeholder: true and link to "#" with a "Soon" badge (future work — no stub route
// is built this change).
func navItems(active string) []ui.NavItem {
	return []ui.NavItem{
		{Label: "Dashboard", Href: "/dashboard", Active: active == "/dashboard", Icon: "dashboard"},
		{Label: "Manual Records", Href: "/charges", Active: active == "/charges", Icon: "ev_station"},
		{Label: "Supercharger Stats", Href: "/supercharger-stats", Active: active == "/supercharger-stats", Icon: "analytics"},
		{Label: "Settings", Icon: "settings", Placeholder: true},
	}
}