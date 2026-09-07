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
// RobotsTxt, SitemapXML and WebManifest are plain package-level functions, not
// methods on *Handler like every other route in this package. That is deliberate
// and it is the point: they read NOTHING but ui.Site and the i18n catalogue off
// the request context — no database, no account, no module port. A future edit
// that needs h.Deps here is a design smell, not a missing receiver.
package handlers

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
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

// --- Web app manifest -------------------------------------------------------

// manifestThemeColor is the colour the OS paints around the installed app: the
// Android status bar, and the splash screen behind the icon while it loads.
//
// It is a literal hex, and it has to be — a manifest is JSON fetched by the
// operating system, which cannot read a CSS custom property. This is the ONE
// place in the module where a raw colour is correct rather than a violation of
// the semantic-token rule; everywhere else, use the theme tokens.
//
// The value is graphite's --color-base-100 (static/themes/graphite.css), because
// graphite is ui.DefaultTheme. A user who has picked apex or halloween still gets
// this colour on their home screen: a manifest carries exactly one theme_color
// and is fetched once at install time, long before any per-request preference is
// known. Matching the DEFAULT is the closest honest answer.
//
// If graphite's base-100 changes, change it here too. Nothing links the two.
const manifestThemeColor = "#0e1013"

// manifestIcon is one entry of the manifest's icons array.
//
// Purpose is always "any". It is deliberately never "maskable": a maskable icon
// must be drawn with a safe zone so the OS can crop it to a circle or a squircle
// without cutting the artwork, and these icons were not designed that way.
// Claiming maskable for an icon that is not produces a visibly clipped logo on
// every Android launcher — the same class of mistake as inventing an
// aggregateRating in the JSON-LD.
type manifestIcon struct {
	Src     string `json:"src"`
	Sizes   string `json:"sizes"`
	Type    string `json:"type"`
	Purpose string `json:"purpose"`
}

// webManifest is the W3C web app manifest document.
//
// It exists to give favicon-192x192.png and favicon-512x512.png a job: those two
// sizes are what Android reads when a user installs the site to their home
// screen, and nothing else in the app references them. Without a manifest they
// were dead weight embedded in the binary.
type webManifest struct {
	Name            string         `json:"name"`
	ShortName       string         `json:"short_name"`
	Description     string         `json:"description"`
	StartURL        string         `json:"start_url"`
	Scope           string         `json:"scope"`
	Display         string         `json:"display"`
	ThemeColor      string         `json:"theme_color"`
	BackgroundColor string         `json:"background_color"`
	Lang            string         `json:"lang"`
	Icons           []manifestIcon `json:"icons"`
}

// WebManifest serves /site.webmanifest.
//
// It is a route rather than a static file for the same reason every other
// user-facing string in this module is: name, short_name and description are
// read by a human on an install prompt, so they must follow the request language
// through the i18n catalogue. A static JSON file could only ever be one language.
//
// start_url is "/" on purpose even though that path redirects. For an installed
// app that is the CORRECT behaviour: opening the icon lands the user wherever
// they belong — /dashboard with a session, /login without one — instead of
// pinning the launcher to whichever page happened to be right at install time.
//
// The media type must be application/manifest+json. Serving it as
// application/json makes some browsers ignore the document silently.
func WebManifest(c *gin.Context) {
	ctx := c.Request.Context()

	doc := webManifest{
		Name:        i18n.T(ctx, i18n.KeyBrandMagusMonitor),
		ShortName:   i18n.T(ctx, i18n.KeyBrandMagus),
		Description: i18n.T(ctx, i18n.KeySEODescription),
		StartURL:    "/",
		Scope:       "/",
		// "standalone" drops the browser address bar, which is what makes an
		// installed PWA feel like an app rather than a bookmark.
		Display:         "standalone",
		ThemeColor:      manifestThemeColor,
		BackgroundColor: manifestThemeColor,
		// BCP 47, like the JSON-LD's inLanguage and unlike og:locale's
		// underscore form. Derived from the ONE locale vocabulary, never a
		// second hand-written map.
		Lang: strings.ReplaceAll(i18n.OGLocale(ctx), "_", "-"),
		Icons: []manifestIcon{
			{Src: "/static/img/favicon/favicon-192x192.png", Sizes: "192x192", Type: "image/png", Purpose: "any"},
			{Src: "/static/img/favicon/favicon-512x512.png", Sizes: "512x512", Type: "image/png", Purpose: "any"},
		},
	}

	c.Header("Cache-Control", "public, max-age=3600")
	body, err := json.Marshal(doc)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Data(http.StatusOK, "application/manifest+json; charset=utf-8", body)
}
