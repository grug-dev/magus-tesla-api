# SEO & social-share metadata — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `SEO metatags`, `metatags`, `meta tags`, `Open Graph`, `og tags`, `link preview`,
  `share card`, `canonical URL`, `robots tag`, `noindex`, `og:image`
- **Internal name:** `seoHead` — the templ component in
  `internal/gateway/templates/layouts/base.templ`, fed by `ui.Site`
  (`internal/gateway/templates/ui/site.go`) and `handlers.SiteMiddleware`
  (`internal/gateway/handlers/site.go`)

## Component map

Every SEO tag in the app comes from one component. There is no second place.

| Layer | File | Role |
|---|---|---|
| layout | `internal/gateway/templates/layouts/base.templ` | `seoHead(title, indexable)` renders every tag. `seoImagePath` names the share image. `baseShell(title, indexable)` calls it once, inside `<head>`. |
| layout | `internal/gateway/templates/layouts/base.templ` | `Base` passes `indexable = true`. `BaseAuth` passes `false`. |
| context | `internal/gateway/templates/ui/site.go` | `ui.Site` holds `BaseURL` + `Path`. `Canonical()` and `Asset()` build absolute URLs. `WithSite` / `SiteFromContext` carry it on the request. |
| middleware | `internal/gateway/handlers/site.go` | `SiteMiddleware(baseURL)` puts `ui.Site` on every request context. |
| wiring | `internal/gateway/gateway.go` | `Deps.BaseURL` field. Registers `SiteMiddleware`. |
| wiring | `cmd/web/main.go` | Passes `cfg.BaseURL` into `gateway.Deps`. |
| config | `internal/config/config.go` | `Config.BaseURL`, read from the `BASE_URL` env var. |
| strings | `internal/gateway/i18n/catalog.go` | `KeySEODescription`, `KeySEOHomeTitle`, `KeySEOImageAlt`, `KeyBrandMagusMonitor`, `KeyBrandPageTitle`. |
| strings | `internal/gateway/i18n/i18n.go` | `OGLocale(ctx)` returns `es_CO` or `en_US`. |
| asset | `internal/gateway/static/img/magus-logo.png` | The share image. Embedded by `gateway.go`'s `//go:embed static`. |
| crawler files | `internal/gateway/handlers/site.go` | `RobotsTxt` + `SitemapXML`. Also `robotsDisallow` and `sitemapPaths`, the two closed lists. |
| PWA manifest | `internal/gateway/handlers/site.go` | `WebManifest` + `manifestThemeColor`. Generated, not static, so the install prompt is translated. |
| structured data | `internal/gateway/templates/layouts/jsonld.go` | `seoJSONLD` builds the `WebApplication` node. Returns a struct, not JSON. |
| icons | `internal/gateway/templates/layouts/base.templ` | `faviconLinks` renders the three icon `<link>` tags. |
| icons | `internal/gateway/static/img/favicon/` | Linked from the page: `favicon-32x32.png`, `favicon-16x16.png`, `favicon.ico`, `apple-touch-icon.png`. Used by the manifest: the 192 and 512 PNGs. Unused: `favicon-180x180.png`, `favicon-48x48.png`. |
| routes | `internal/gateway/gateway.go` | `/robots.txt`, `/sitemap.xml`, `/site.webmanifest`, and a 301 from `/favicon.ico`. |
| tests | `internal/gateway/seo_test.go` | Pins the tags, the absolute URLs, the ES/EN switch, robots, sitemap, and the JSON-LD. |
| docs | `internal/gateway/AGENTS.md` §"SEO & social-share metadata" | The full rule set and the rejected options. |

## How maintenance works

**To change what a share card says**, edit the catalog key, not the layout. Every string in
`seoHead` goes through `i18n.T`. Both `ES` and `EN` are required.

**To change the share image**, replace `internal/gateway/static/img/magus-logo.png`. Use a
1200x630 PNG under 300 KB. Change `seoImagePath` only if you rename the file.

**To add a new tag**, add it inside `seoHead`. Do not add a `<meta>` tag to a page. A page that
needs its own value gets a new catalog key instead.

**To make a page public or private**, pick its layout. `layouts.Base` is public and indexable.
`layouts.BaseAuth` is private and gets `noindex, nofollow` only.

**To change the site domain**, set `BASE_URL` in `.env`. Nothing in the gateway is edited.

**To block a new route from crawlers**, add its prefix to `robotsDisallow` in
`handlers/site.go`. A route under a prefix already listed needs no edit.

**To add a page to the sitemap**, add its path to `sitemapPaths` in the same file. The
page must return 200. A redirecting path is dropped by Search Console.

**To change the structured data**, edit `seoJSONLD` in `layouts/jsonld.go`. Add only fields
this project has real data for.

**To change the install prompt** (the name a phone shows on the home screen), edit
`WebManifest` in `handlers/site.go`. The strings come from the i18n catalog.

**To change the favicon**, replace the files in `internal/gateway/static/img/favicon/`. Keep
the names `faviconLinks` uses. No code change is needed. To add a new icon format, add both
the file and its `<link>` tag. The test `os.Stat`s every href, so a tag with no file fails.

## Conventions & gotchas

- **The base URL renders no page.** `Handler.Home` sends an anonymous visitor from `/` to
  `/login` with a 302. Scrapers follow the redirect. So the share card for the domain is
  `/login`'s card. `pages.Home` still compiles but is never shown.
  _Source: `internal/gateway/handlers/handlers.go` — `Handler.Home`._

- **Every URL tag must be absolute.** A scraper fetches the page outside the browser. It has no
  origin to resolve a relative URL. A relative `og:image` makes the preview blank.
  _Source: `internal/gateway/templates/ui/site.go` — `Site.Asset`._

- **The origin comes from config, never from the `Host` header.** `Host` is attacker-controlled
  on any request. Reflecting it into `canonical` lets a page advertise someone else's domain.
  _Source: `internal/gateway/handlers/site.go` — `SiteMiddleware`._

- **`BASE_URL` now has two jobs.** It built the OAuth redirect URIs. It also builds every share
  URL. Leave it at the localhost default in production and every preview points at
  `localhost:8080`.
  _Source: `.env.example`; `internal/config/config.go` — `Config.BaseURL`._

- **An empty base URL drops tags, it does not break them.** `Canonical()` and `Asset()` return
  `""`. `seoHead` then skips `canonical`, `og:url`, and both image tags. A missing tag is a
  small loss. A malformed one is a bigger loss.
  _Source: `internal/gateway/templates/ui/site.go`._

- **Spanish is the default with no special case.** A crawler is a request with no `lang` cookie.
  `i18n.FromContext` falls back to `account.LanguageES`. So the catalog default IS the SEO
  default. Nothing in `seoHead` is pinned to one language.
  _Source: `internal/gateway/i18n/i18n.go` — `FromContext`, `OGLocale`._

- **`og:locale` is not `<html lang>`.** Open Graph wants `es_CO`, not `es`. A crawler ignores a
  bare `es`. The two values cannot be shared.
  _Source: `internal/gateway/i18n/i18n.go` — `OGLocale`._

- **Private pages get `noindex` and nothing else.** No description, no image. A crawler never
  reaches them. The tags would only leak a page name if a route were ever exposed by mistake.
  _Source: `internal/gateway/templates/layouts/base.templ` — `seoHead`._

- **`KeyBrandMagusMonitor` is stored in normal case.** It feeds `og:site_name`, where all-caps
  reads as shouting. The login `<h1>` uppercases it with the `uppercase` CSS class. One key
  serves both.
  _Source: `internal/gateway/i18n/catalog.go`; `internal/gateway/templates/pages/login.templ`._

- **`og:image:width` and `og:image:height` are not emitted, on purpose.** They would be a claim
  about a file this repo does not check. A wrong size is worse than no size.
  _Source: `internal/gateway/templates/layouts/base.templ` — `seoHead`._

- **Never hand-write a `<script>` tag for JSON-LD.** templ treats a script element's contents
  as literal text. An `@templ.Raw(...)` inside one is sent to the browser word for word. It
  compiles and `go vet` passes. Use `templ.JSONScript(...).WithType("application/ld+json")`.
  `WithType` is required. The default type is `application/json`, which no crawler reads.
  _Source: `internal/gateway/templates/layouts/base.templ` — `seoHead`; templ v0.3 `JSONScript`._

- **The JSON-LD states only what is true.** No `aggregateRating`, no `offers`, no `price`.
  This project has no rating and no published price. Google penalises invented fields by
  hand. `seo_test.go` asserts they stay absent.
  _Source: `internal/gateway/templates/layouts/jsonld.go`._

- **`inLanguage` is not `og:locale`.** schema.org wants `es-CO`. Open Graph wants `es_CO`.
  The code derives one from the other. Do not write a second locale map.
  _Source: `internal/gateway/templates/layouts/jsonld.go` — `seoJSONLD`._

- **`robots.txt` and `sitemap.xml` must be routes, not static files.** A crawler fetches
  those two exact root paths. A file under `/static` is never found.
  _Source: `internal/gateway/gateway.go`; `internal/gateway/handlers/site.go`._

- **`robots.txt` is not access control.** The file is public. It names every path it lists.
  Never put a secret URL in it. Everything in `robotsDisallow` is already visible in the
  signed-in navigation.
  _Source: `internal/gateway/handlers/site.go` — `robotsDisallow`._

- **The sitemap does not list `/`.** That path 302-redirects. Search Console drops a
  redirecting sitemap entry as "Page with redirect". Only `/login` is listed.
  _Source: `internal/gateway/handlers/site.go` — `sitemapPaths`._

- **The sitemap has no `<lastmod>`, and that is deliberate.** This app does not track when a
  page changed. A made-up date teaches a crawler a schedule that means nothing.
  _Source: `internal/gateway/handlers/site.go` — `SitemapXML`._

- **Favicons are not gated on `indexable`.** A private page still needs a tab icon. So
  `faviconLinks` sits in `baseShell`, outside `seoHead`.
  _Source: `internal/gateway/templates/layouts/base.templ` — `faviconLinks`._

- **Never link an icon file that does not exist.** It is a 404 on every page load. The build
  does not catch it. `seo_test.go` runs `os.Stat` on every icon href instead. There is no
  `favicon.svg` tag because there is no SVG file.
  _Source: `internal/gateway/seo_test.go` — `TestSEO_FaviconLinksOnEveryPage`._

- **Unused icons still cost binary size.** Everything under `static/` is embedded by
  `//go:embed`. `favicon-180x180.png` duplicates `apple-touch-icon.png`.
  `favicon-48x48.png` is covered by the `.ico`. Neither is referenced.
  _Source: `internal/gateway/gateway.go` — `staticFS`._

- **`manifestThemeColor` is the one correct raw hex in the gateway.** A manifest is JSON
  read by the operating system. It cannot resolve a CSS token. The value is graphite's
  `--color-base-100`, because graphite is the default theme. A manifest holds one colour
  and is fetched once at install. If graphite changes, change the constant by hand.
  _Source: `internal/gateway/handlers/site.go` — `manifestThemeColor`._

- **No manifest icon may claim `purpose: "maskable"`.** A maskable icon needs a safe zone
  drawn into the art. These icons do not have one. Claiming it clips the logo on every
  Android launcher. A test asserts none claims it.
  _Source: `internal/gateway/handlers/site.go` — `manifestIcon`._

- **Serve the manifest as `application/manifest+json`.** `application/json` is dropped
  silently by some browsers. No error appears. The install prompt just never shows.
  _Source: `internal/gateway/handlers/site.go` — `WebManifest`._

- **`start_url` is `/`, which redirects, and that is correct here.** An installed app
  should open where the user belongs. A session goes to `/dashboard`, no session to
  `/login`. Pinning a real page would freeze the launcher at install time.
  _Source: `internal/gateway/handlers/site.go` — `WebManifest`._

- **The bare `/favicon.ico` needs its own route.** A browser requests it before it parses
  any HTML. The `<link>` tags alone are not enough. `gateway.go` 301s it to the real asset.
  _Source: `internal/gateway/gateway.go`._

- **Not built, and that is a decision:** `hreflang` and a web app manifest. `hreflang` has
  no meaning here. Language is a cookie on the SAME URL. There is no per-language URL to
  point at.
  _Source: `internal/gateway/AGENTS.md` §"SEO & social-share metadata"._

- **`make i18n-guard` does not cover these strings.** It scans text nodes, not attributes. A
  hardcoded `content="..."` would pass the guard. The bilingual rule still binds.
  _Source: `Makefile` — `i18n-guard`; `CLAUDE.md` §Non-negotiables._

## Related KB

- `input-port/gateway/dashboard.md` — a `BaseAuth` page, so it is `noindex`.
