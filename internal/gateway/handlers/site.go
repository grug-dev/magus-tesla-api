// site.go is the gateway's crawler-facing surface: SiteMiddleware — the
// request's single resolution point for the absolute-URL facts the document
// <head> needs (ui.Site: the app's public base URL plus the path being served)
// — plus the two files a search engine fetches by fixed name, RobotsTxt and
// SitemapXML.
//
// SiteMiddleware is the URL sibling of PreferencesMiddleware (preferences.go),
// which resolves language and theme the same way: one middleware, one context
// value, zero parameter threading through every page.
//
// RobotsTxt and SitemapXML are plain package-level functions, not methods on
// *Handler like every other route in this package. That is deliberate and it is
// the point: they read NOTHING but ui.Site off the request context — no
// database, no account, no module port. A future edit that needs h.Deps here is
// a design smell, not a missing receiver.
package handlers

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// SiteMiddleware puts ui.Site{BaseURL: baseURL, Path: <request path>} on every
// request context, so layouts.seoHead can build the canonical URL, og:url and
// og:image without any handler passing them down.
//
// baseURL comes from config.Config.BaseURL (wired through gateway.Deps from
// cmd/web) — the SAME value that already builds the OAuth redirect URIs, so
// there is no second place to keep a public domain in step.
//
// It deliberately does NOT read the Host header instead. Host is attacker-
// controlled on any request, and reflecting it into <link rel="canonical"> and
// og:url is how a site ends up advertising someone else's domain as its own.
// Configured value only.
//
// Registration order does not matter for this one: it depends on nothing but
// the request itself.
func SiteMiddleware(baseURL string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := ui.WithSite(c.Request.Context(), ui.Site{
			BaseURL: baseURL,
			Path:    c.Request.URL.Path,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// robotsDisallow is the closed list of route prefixes a crawler must not fetch —
// every authenticated page, every htmx fragment endpoint, and the OAuth hops.
// Keep it in step with the route table in gateway.go: a new authenticated route
// under an existing prefix needs no edit here, a new TOP-LEVEL one does.
//
// This is belt-and-braces, not the primary defence. Those routes already redirect
// an anonymous request to /login AND render robots noindex through
// layouts.BaseAuth (see layouts.seoHead). robots.txt only saves the crawl budget
// that would be spent discovering that.
//
// robots.txt is NOT an access control. It is a public file that names every path
// listed in it, so it must never be used to hide a secret URL — everything below
// is already discoverable from the signed-in navigation.
var robotsDisallow = []string{
	"/ui/",
	"/auth/",
	"/connect/",
	"/logout",
	"/healthz",
	"/dashboard",
	"/settings",
	"/external-charges",
	"/supercharger-stats",
}

// sitemapPaths is every URL this site wants indexed.
//
// "/" is deliberately ABSENT even though it is the domain a person shares.
// Handler.Home 302-redirects an anonymous visitor to /login, and a sitemap entry
// that redirects is reported by Google Search Console as "Page with redirect" and
// dropped. /login IS the site's one indexable page today. If "/" ever renders its
// own landing page, add it here and the redirect test in seo_test.go will already
// have failed to remind you.
var sitemapPaths = []string{"/login"}

// RobotsTxt serves /robots.txt. It must live at the domain root — a crawler
// fetches exactly that path and nowhere else — which is why it is a route and not
// a file under /static.
//
// The Sitemap line is omitted when no base URL is configured: the sitemap
// directive takes an absolute URL only, and pointing a crawler at a malformed one
// is worse than not advertising the sitemap at all.
func RobotsTxt(c *gin.Context) {
	site := ui.SiteFromContext(c.Request.Context())

	var b strings.Builder
	b.WriteString("User-agent: *\n")
	for _, path := range robotsDisallow {
		b.WriteString("Disallow: " + path + "\n")
	}
	if sitemap := site.Asset("/sitemap.xml"); sitemap != "" {
		b.WriteString("\nSitemap: " + sitemap + "\n")
	}

	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(b.String()))
}

// SitemapXML serves /sitemap.xml in the sitemaps.org 0.9 schema.
//
// Every <loc> must be an absolute URL, so with no base URL configured there is no
// valid document to serve and the route answers 404 rather than emitting one with
// broken or relative locations.
//
// No <lastmod>: this app does not track when a page's content last changed, and a
// made-up timestamp is worse than an absent one — it teaches a crawler to
// re-fetch on a schedule that means nothing. <changefreq> and <priority> are
// omitted for the same reason; Google ignores both.
func SitemapXML(c *gin.Context) {
	site := ui.SiteFromContext(c.Request.Context())

	var b strings.Builder
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, path := range sitemapPaths {
		loc := site.Asset(path)
		if loc == "" {
			c.Status(http.StatusNotFound)
			return
		}
		b.WriteString("  <url><loc>" + loc + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")

	c.Header("Cache-Control", "public, max-age=3600")
	c.Data(http.StatusOK, "application/xml; charset=utf-8", []byte(b.String()))
}
