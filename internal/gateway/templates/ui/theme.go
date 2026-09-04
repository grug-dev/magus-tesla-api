package ui

import "context"

// Themes is the closed, presentation-facing theme vocabulary (roadmap RM42 D10) — the
// single source the dropdown iterates AND make theme-guard reconciles against account's
// own copy and static/input.css's two registration shapes. Order is display order in the
// dropdown. This package takes no domain imports (see ui.go's package doc: "no domain
// imports, no business logic"), so these are plain string literals, not
// account.ThemeApex/ThemeGraphite/ThemeHalloween — the duplication with account's own
// copy is deliberate and kept honest by make theme-guard (design.md D5).
var Themes = []string{"apex", "graphite", "halloween"}

// DefaultTheme is returned by ThemeFromContext/IsSupportedTheme's normalization path for
// any value outside Themes — mirrors account.ThemeGraphite's value without importing
// internal/account.
const DefaultTheme = "graphite"

// IsSupportedTheme reports whether v is one of the Themes vocabulary codes.
func IsSupportedTheme(v string) bool {
	for _, t := range Themes {
		if v == t {
			return true
		}
	}
	return false
}

// ctxKey is an unexported type so this package's context key can never collide with a
// key set by another package importing the same context.Context (standard Go
// context-key idiom) — mirrors i18n.ctxKey exactly.
type ctxKey struct{}

var themeCtxKey ctxKey

// WithTheme returns a copy of ctx carrying theme as the active render theme. It performs
// NO validation — callers normalize before calling (mirrors i18n.WithLang's contract:
// PreferencesMiddleware resolves to a supported theme before ever calling this).
func WithTheme(ctx context.Context, theme string) context.Context {
	return context.WithValue(ctx, themeCtxKey, theme)
}

// ThemeFromContext returns the active theme carried on ctx, defaulting to DefaultTheme on
// anything but a value that IsSupportedTheme accepts: a missing key (no
// PreferencesMiddleware ran — e.g. a bare context.Background() in a unit test), a value of
// the wrong type, or an unsupported string all resolve the same way. The two-result
// (comma-ok) type assertion is required, not optional defensiveness — mirrors
// i18n.FromContext's own documented reason: templ's docs state that accessing a
// non-existent context key or an invalid type assertion triggers a runtime panic; a bare
// `.(string)` assertion here would panic exactly where this function exists to prevent
// that.
func ThemeFromContext(ctx context.Context) string {
	theme, _ := ctx.Value(themeCtxKey).(string)
	if IsSupportedTheme(theme) {
		return theme
	}
	return DefaultTheme
}
