package layouts

import "github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"

// navItems is the sidebar navigation for authenticated pages, rendered by BaseAuth
// through ui.NavShell. Keeping it here (not inline in the .templ) means the single
// source of nav links is plain, testable Go.
//
// Active highlighting is not wired yet: BaseAuth receives only the page title, not the
// current request path. When a page needs the active item lit, thread the path into
// BaseAuth and set NavItem.Active per entry here.
func navItems() []ui.NavItem {
	return []ui.NavItem{
		{Label: "Dashboard", Href: "/dashboard"},
		{Label: "Charge log", Href: "/charges"},
	}
}
