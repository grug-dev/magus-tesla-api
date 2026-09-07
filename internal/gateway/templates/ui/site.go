package ui

import (
	"context"
	"strings"
)

// Site carries the per-request absolute-URL facts the document <head> needs and
// cannot derive on its own: the app's public base URL and the path being served.
//
// Why it exists: every SEO/social tag in layouts.seoHead (canonical, og:url,
// og:image, twitter:image) must be an ABSOLUTE url. A relative href is legal in
// <link rel="canonical"> but is silently ignored by most social-card scrapers,
// which fetch the page out of band and have no origin to resolve it against. The
// base URL is deployment config (config.Config.BaseURL — http://localhost:8080 in
// dev, https://<domain> in production), so it can only reach a template through
// the request.
//
// It is carried on the context exactly like the active theme (theme.go) and the
// active language (i18n.WithLang): one middleware resolves it once per request,
// every layout reads it with no parameter threading through the page callers.
type Site struct {
	// BaseURL is the public origin with NO trailing slash, e.g.
	// "https://usemagus.cloud". WithSite trims the slash so callers never have
	// to, and so Canonical/Asset can always join with a plain "/".
	BaseURL string
	// Path is the request path, e.g. "/" or "/login". Query strings are
	// deliberately excluded: the canonical URL of a page is the path, and
	// echoing arbitrary query parameters into <link rel="canonical"> is how a
	// site ends up advertising an unbounded set of duplicate URLs.
	Path string
}

// Canonical is the absolute URL of the page being served — the value for both
// <link rel="canonical"> and og:url. Returns "" when no base URL is configured,
// which is the signal seoHead uses to omit the tag rather than emit a broken one.
func (s Site) Canonical() string {
	if s.BaseURL == "" {
		return ""
	}
	if s.Path == "" || s.Path == "/" {
		return s.BaseURL + "/"
	}
	return s.BaseURL + s.Path
}

// Asset turns a root-relative static path ("/static/img/magus-logo.png") into the
// absolute URL a social scraper needs for og:image. Returns "" when no base URL
// is configured, for the same reason Canonical does.
func (s Site) Asset(path string) string {
	if s.BaseURL == "" {
		return ""
	}
	return s.BaseURL + "/" + strings.TrimPrefix(path, "/")
}

// siteCtxKey is an unexported type so this package's key can never collide with
// one set by another package on the same context (standard Go context-key
// idiom) — mirrors themeCtxKey and i18n.ctxKey.
type siteCtxKey struct{}

// WithSite returns a copy of ctx carrying site as the request's URL facts,
// normalizing the base URL by stripping any trailing slash so Canonical and
// Asset never produce a double slash.
func WithSite(ctx context.Context, site Site) context.Context {
	site.BaseURL = strings.TrimRight(site.BaseURL, "/")
	return context.WithValue(ctx, siteCtxKey{}, site)
}

// SiteFromContext returns the Site carried on ctx, or the zero Site when no
// middleware ran (a bare context.Background() in a unit test, say). The
// two-result type assertion is required, not optional defensiveness: templ
// panics at runtime on an invalid type assertion against a context value — the
// same reason i18n.FromContext and ThemeFromContext use the comma-ok form.
//
// A zero Site yields "" from Canonical and Asset, and seoHead omits every tag
// that would have needed an absolute URL. A page with no canonical tag is a
// small SEO loss; a page advertising "http:///login" is a broken one.
func SiteFromContext(ctx context.Context) Site {
	site, _ := ctx.Value(siteCtxKey{}).(Site)
	return site
}
