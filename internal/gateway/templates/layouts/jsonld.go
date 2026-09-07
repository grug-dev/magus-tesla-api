package layouts

import (
	"context"
	"strings"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// JSON-LD structured data — the machine-readable description of the product that
// Google, Bing and the AI answer engines read instead of guessing from the prose.
// It is emitted by layouts.seoHead on PUBLIC pages only, inside a single
// <script type="application/ld+json"> block.
//
// Only claims this repo can actually stand behind go in here. There is no
// aggregateRating, no offers/price and no reviewCount, even though every SEO
// checklist asks for them: those are the fields Google manually penalises when
// they are invented, and this project has no rating and no published price to
// state. A structured-data block is a set of assertions, not a wish list.
//
// Shape: a WebApplication with an Organization publisher — the honest description
// of "a web app you sign into", as opposed to Organization alone (which describes
// a company, not the product) or SoftwareApplication (which implies something you
// install).

// jsonLDOrganization is the publisher node nested inside jsonLDWebApplication.
// Split out because schema.org nests it as its own typed object, not because it
// is reused.
type jsonLDOrganization struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
	Logo string `json:"logo,omitempty"`
}

// jsonLDWebApplication is the top-level node. Every omitempty field is one that
// depends on a configured base URL — with none, the field disappears rather than
// carrying a relative or malformed URL (same rule the meta tags follow).
type jsonLDWebApplication struct {
	Context             string             `json:"@context"`
	Type                string             `json:"@type"`
	Name                string             `json:"name"`
	URL                 string             `json:"url,omitempty"`
	Description         string             `json:"description"`
	ApplicationCategory string             `json:"applicationCategory"`
	OperatingSystem     string             `json:"operatingSystem"`
	InLanguage          string             `json:"inLanguage"`
	Image               string             `json:"image,omitempty"`
	Publisher           jsonLDOrganization `json:"publisher"`
}

// seoJSONLD builds the JSON-LD document for the request. It returns the STRUCT,
// not serialized bytes, because the caller hands it to templ.JSONScript, which
// owns the encoding.
//
// That division matters. templ treats the contents of a <script> element as
// literal text — an `@templ.Raw(...)` written inside one is emitted verbatim, not
// evaluated — so hand-writing the tag around a pre-marshalled string silently
// ships the source line to the browser. templ.JSONScript(...).WithType(...)
// writes the element itself and encodes the data with encoding/json, whose
// encoder HTML-escapes <, > and & by default, so a "</script>" inside any catalog
// string cannot close the tag early. WithType is required: JSONScript defaults to
// "application/json", and only "application/ld+json" is read as structured data.
//
// Language follows the request like every other user-facing string. InLanguage
// derives from i18n.OGLocale by swapping the separator — schema.org wants BCP 47
// ("es-CO") where Open Graph wants "es_CO". Deriving it keeps ONE locale
// vocabulary; a second hand-written map is how the two drift apart.
func seoJSONLD(ctx context.Context) jsonLDWebApplication {
	site := ui.SiteFromContext(ctx)
	logo := site.Asset(seoImagePath)
	name := i18n.T(ctx, i18n.KeyBrandMagusMonitor)

	return jsonLDWebApplication{
		Context:     "https://schema.org",
		Type:        "WebApplication",
		Name:        name,
		URL:         site.Asset("/"),
		Description: i18n.T(ctx, i18n.KeySEODescription),
		// schema.org's own enumeration. The product is a monitoring dashboard
		// for a vehicle you own, not a business or finance tool.
		ApplicationCategory: "UtilitiesApplication",
		// It runs in a browser and installs nothing. "Web" is the value Google's
		// own structured-data examples use for this case.
		OperatingSystem: "Web",
		InLanguage:      strings.ReplaceAll(i18n.OGLocale(ctx), "_", "-"),
		Image:           logo,
		Publisher: jsonLDOrganization{
			Type: "Organization",
			Name: name,
			URL:  site.Asset("/"),
			Logo: logo,
		},
	}
}
