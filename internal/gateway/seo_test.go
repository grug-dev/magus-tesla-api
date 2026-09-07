package gateway_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestSEO_BaseURLRedirectsToLogin pins the fact the rest of this file depends
// on: "/" renders NOTHING for an anonymous visitor — Handler.Home 302-redirects
// it to /login (handlers.go). So the base URL's own share card and search
// snippet are /login's, because every scraper and crawler follows the redirect.
// If "/" ever grows a real landing page, this test breaks and the metadata
// assertions below have to gain a "/" case.
func TestSEO_BaseURLRedirectsToLogin(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/login" {
		t.Fatalf("GET / = %d -> %q, want 302 -> /login", w.Code, w.Header().Get("Location"))
	}
}

// TestSEO_LoginPageCarriesShareMetadata asserts the page an anonymous visitor
// actually lands on carries the search-engine and social-card metadata
// layouts.seoHead renders — in Spanish (the catalogue default a cookie-less
// request resolves to, which is exactly what a crawler is) and with ABSOLUTE
// URLs built from Deps.BaseURL.
//
// The absolute-URL assertions are the point of the test, not decoration: a
// relative og:image or og:url is the most common reason a link preview silently
// renders blank, and neither go vet nor any guard can catch it.
func TestSEO_LoginPageCarriesShareMetadata(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /login status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	const title = "Iniciar sesión — Magus Monitor"
	for _, want := range []string{
		`<title>` + title + `</title>`,
		`<meta name="description" content="Magus Monitor conecta tu cuenta Tesla`,
		`<meta name="robots" content="index, follow">`,
		`<meta property="og:type" content="website">`,
		`<meta property="og:site_name" content="Magus Monitor">`,
		`<meta property="og:locale" content="es_CO">`,
		`<meta property="og:title" content="` + title + `">`,
		`<meta name="twitter:card" content="summary_large_image">`,
		`<meta name="twitter:title" content="` + title + `">`,
		`<link rel="canonical" href="https://seo.test/login">`,
		`<meta property="og:url" content="https://seo.test/login">`,
		`<meta property="og:image" content="https://seo.test/static/img/magus-logo.png">`,
		`<meta name="twitter:image" content="https://seo.test/static/img/magus-logo.png">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login body missing %q", want)
		}
	}
	// A public page must NEVER carry noindex — that one word de-lists the site.
	if strings.Contains(body, "noindex") {
		t.Error("login page emits a noindex robots tag")
	}
}

// TestSEO_LoginPageBranding pins the two strings the owner asked for on /login:
// the MAGUS MONITOR wordmark (stored in normal case and uppercased by the
// `uppercase` CSS class, so og:site_name can reuse the same catalog entry) and
// the Colombia tagline. Spanish, because an anonymous request has no lang cookie.
func TestSEO_LoginPageBranding(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	body := w.Body.String()
	for _, want := range []string{"Magus Monitor", "Inteligencia de flota Tesla para Colombia"} {
		if !strings.Contains(body, want) {
			t.Errorf("login body missing %q", want)
		}
	}
	for _, gone := range []string{"TESLA CORE", "Monitoreo de flota de precisión", "Precision Fleet Monitoring"} {
		if strings.Contains(body, gone) {
			t.Errorf("login body still shows the retired string %q", gone)
		}
	}
}

// TestSEO_EnglishRequestSwitchesLocale asserts the metadata follows the request
// language like every other user-facing string — the "lang=en" cookie flips
// og:locale and the description to English. Spanish is the DEFAULT, not the only
// option, and nothing in seoHead is hardcoded to one language.
func TestSEO_EnglishRequestSwitchesLocale(t *testing.T) {
	eng := testEngine(t)
	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	req.AddCookie(&http.Cookie{Name: "lang", Value: "en"})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, req)
	body := w.Body.String()
	for _, want := range []string{
		`<meta property="og:locale" content="en_US">`,
		`<meta name="description" content="Magus Monitor connects your Tesla account`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("english login body missing %q", want)
		}
	}
}

// TestSEO_RobotsTxt asserts /robots.txt is served from the domain ROOT (a crawler
// fetches that exact path and nowhere else), blocks every authenticated prefix,
// leaves the public pages fetchable, and advertises an ABSOLUTE sitemap URL.
func TestSEO_RobotsTxt(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /robots.txt status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		"User-agent: *",
		"Disallow: /ui/",
		"Disallow: /auth/",
		"Disallow: /connect/",
		"Disallow: /dashboard",
		"Disallow: /settings",
		"Disallow: /external-charges",
		"Disallow: /supercharger-stats",
		"Sitemap: https://seo.test/sitemap.xml",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("robots.txt missing %q\n%s", want, body)
		}
	}
	// The two public paths must NOT be blocked. A "Disallow: /login" line would
	// de-list the only indexable page on the site, and nothing else would warn.
	for _, gone := range []string{"Disallow: /login", "Disallow: /\n"} {
		if strings.Contains(body, gone) {
			t.Errorf("robots.txt must not contain %q\n%s", gone, body)
		}
	}
}

// TestSEO_SitemapXML asserts the sitemap is valid 0.9 XML with ABSOLUTE <loc>
// values — a relative loc makes the whole document invalid, not just one entry.
//
// It also pins the deliberate ABSENCE of "/": Handler.Home 302-redirects, and a
// redirecting sitemap entry is dropped by Search Console as "Page with redirect".
func TestSEO_SitemapXML(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sitemap.xml", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /sitemap.xml status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xml") {
		t.Errorf("Content-Type = %q, want application/xml", ct)
	}
	body := w.Body.String()
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
		`<loc>https://seo.test/login</loc>`,
		`</urlset>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("sitemap missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "<loc>https://seo.test/</loc>") {
		t.Error(`sitemap lists "/", which 302-redirects to /login and is dropped by Search Console`)
	}
}

// TestSEO_JSONLD parses the structured-data block on the public page and checks
// the claims, not the byte string. Parsing is the point: an invalid JSON-LD block
// is ignored WHOLE by every consumer, and a substring assertion cannot tell a
// valid document from a truncated one.
func TestSEO_JSONLD(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	body := w.Body.String()

	const open = `<script type="application/ld+json">`
	i := strings.Index(body, open)
	if i < 0 {
		t.Fatal("login page has no application/ld+json block")
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, "</script>")
	if j < 0 {
		t.Fatal("ld+json block is never closed")
	}

	var doc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:j])), &doc); err != nil {
		t.Fatalf("ld+json is not valid JSON: %v\n%s", err, rest[:j])
	}
	want := map[string]string{
		"@context":            "https://schema.org",
		"@type":               "WebApplication",
		"name":                "Magus Monitor",
		"url":                 "https://seo.test/",
		"applicationCategory": "UtilitiesApplication",
		"operatingSystem":     "Web",
		"inLanguage":          "es-CO",
		"image":               "https://seo.test/static/img/magus-logo.png",
	}
	for k, v := range want {
		if got, _ := doc[k].(string); got != v {
			t.Errorf("ld+json %s = %q, want %q", k, got, v)
		}
	}
	pub, ok := doc["publisher"].(map[string]any)
	if !ok {
		t.Fatal("ld+json has no publisher object")
	}
	if got, _ := pub["@type"].(string); got != "Organization" {
		t.Errorf("publisher @type = %q, want Organization", got)
	}
	// Claims this project cannot stand behind must stay absent. Inventing them
	// is what Google manually penalises.
	for _, invented := range []string{"aggregateRating", "offers", "review", "priceCurrency"} {
		if _, present := doc[invented]; present {
			t.Errorf("ld+json asserts %q, which this project has no data for", invented)
		}
	}
}

// TestSEO_PrivatePageHasNoStructuredData asserts the ld+json block is public-only.
// /dashboard redirects anonymously, so this checks the one thing reachable here:
// the block never appears outside the indexable branch of seoHead.
func TestSEO_JSONLDIsPublicOnly(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if strings.Contains(w.Body.String(), "application/ld+json") {
		t.Error("an authenticated route served structured data")
	}
}

// TestSEO_FaviconRootRedirect asserts the bare /favicon.ico a browser requests
// before parsing any HTML resolves to the real asset.
func TestSEO_FaviconRootRedirect(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/favicon.ico", nil))
	if w.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /favicon.ico status = %d, want 301", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/static/img/favicon/favicon.ico" {
		t.Errorf("Location = %q", loc)
	}
}

// TestSEO_FaviconLinksOnEveryPage asserts the icon tags are NOT gated on
// indexable — a private page still needs a browser-tab icon.
func TestSEO_FaviconLinksOnEveryPage(t *testing.T) {
	eng := testEngine(t)
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	body := w.Body.String()
	for _, want := range []string{
		`href="/static/img/favicon/favicon-32x32.png"`,
		`href="/static/img/favicon/favicon-16x16.png"`,
		`href="/static/img/favicon/favicon.ico"`,
		`rel="apple-touch-icon"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("login head missing %q", want)
		}
	}
	// Every icon href must name a file that exists. A <link> to a missing file
	// is a 404 on every page load, and nothing else in the build would catch it.
	for _, path := range faviconHrefs(t, body) {
		if _, err := os.Stat("." + path); err != nil {
			t.Errorf("icon href %q has no file: %v", path, err)
		}
	}
}

// faviconHrefs pulls every /static/img/favicon/... href out of the rendered head.
func faviconHrefs(t *testing.T, body string) []string {
	t.Helper()
	var out []string
	const prefix = `href="/static/img/favicon/`
	for i := strings.Index(body, prefix); i >= 0; i = strings.Index(body, prefix) {
		body = body[i+len(`href="`):]
		end := strings.Index(body, `"`)
		if end < 0 {
			break
		}
		out = append(out, body[:end])
		body = body[end:]
	}
	if len(out) == 0 {
		t.Fatal("no favicon hrefs found in head")
	}
	return out
}
