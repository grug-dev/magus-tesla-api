package i18n

import "github.com/cristianpena/magus-tesla-api/internal/account"

// entry holds one Key's parallel translations. Both languages live on the
// same line in the catalog literal below (design.md D1) so a reviewer sees
// both strings in one diff hunk and a missing language is a visibly empty
// field, not a separate file or PR to cross-reference.
type entry struct{ ES, EN string }

// missingKeyMarker prefixes the render-time fallback for a Key that has no
// catalog entry at all (design.md D2, first failure mode: a typo'd constant,
// or — this should never compile since Key is a defined string type — a raw
// string literal cast that bypasses the compiler). A visible "!!key.name"
// marker is deliberate: a blank string in the UI is easy to miss in a
// screenshot or a quick manual QA pass; "!!nav.dashboard" is not. It never
// panics and never 500s.
const missingKeyMarker = "!!"

// Key constants — the closed vocabulary for this tier's gold-standard surface
// (the layout/nav shell): the sidebar nav item labels, the "Soon" placeholder
// badge, the sidebar open/close aria-labels, NavLogout's "Log out", the
// nav-header connect-prompt + its four status words, and the lang-switcher's
// own aria-label + "Español"/"English" list-item labels. Every new
// user-facing label added anywhere in the gateway from this point forward
// gets its own Key here with both ES and EN populated — see
// internal/gateway/AGENTS.md "i18n" section.
const (
	// --- sidebar nav (layouts/nav.go, templates/ui/nav_shell.templ) ---
	KeyNavDashboard         Key = "nav.dashboard"
	KeyNavManualRecords     Key = "nav.manual_records"
	KeyNavSuperchargerStats Key = "nav.supercharger_stats"
	KeyNavSettings          Key = "nav.settings"
	KeyNavSoonBadge         Key = "nav.soon_badge"
	KeyNavOpenSidebar       Key = "nav.open_sidebar"
	KeyNavCloseSidebar      Key = "nav.close_sidebar"
	KeyNavLogout            Key = "nav.logout"

	// --- nav-header fragment (templates/fragments/nav_header.templ) ---
	KeyNavHeaderNoTesla           Key = "nav_header.no_tesla"
	KeyNavHeaderConnectLink       Key = "nav_header.connect_link"
	KeyNavHeaderSwitchVehicleAria Key = "nav_header.switch_vehicle_aria"
	KeyNavHeaderStatusConnected   Key = "nav_header.status_connected"
	KeyNavHeaderStatusAsleep      Key = "nav_header.status_asleep"
	KeyNavHeaderStatusAwaiting    Key = "nav_header.status_awaiting"
	KeyNavHeaderStatusUnavailable Key = "nav_header.status_unavailable"

	// --- language switcher (templates/ui/lang_switcher.templ) ---
	KeyLangSwitcherAria    Key = "lang_switcher.aria"
	KeyLangSwitcherSpanish Key = "lang_switcher.spanish"
	KeyLangSwitcherEnglish Key = "lang_switcher.english"
)

// catalog is the entire translation vocabulary. TestCatalog_AllKeysHaveBothLanguages
// (catalog_test.go) is the actual enforcement of "every key has both
// languages" — this map literal is the only place that rule can be violated,
// and that test fails at `go test` time if it ever is.
var catalog = map[Key]entry{
	KeyNavDashboard:         {ES: "Panel", EN: "Dashboard"},
	KeyNavManualRecords:     {ES: "Registros manuales", EN: "Manual Records"},
	KeyNavSuperchargerStats: {ES: "Estadísticas Supercharger", EN: "Supercharger Stats"},
	KeyNavSettings:          {ES: "Configuración", EN: "Settings"},
	KeyNavSoonBadge:         {ES: "Pronto", EN: "Soon"},
	KeyNavOpenSidebar:       {ES: "Abrir menú lateral", EN: "open sidebar"},
	KeyNavCloseSidebar:      {ES: "Cerrar menú lateral", EN: "close sidebar"},
	KeyNavLogout:            {ES: "Cerrar sesión", EN: "Log out"},

	KeyNavHeaderNoTesla:           {ES: "Ningún Tesla conectado.", EN: "No Tesla connected."},
	KeyNavHeaderConnectLink:       {ES: "Conecta tu Tesla", EN: "Connect your Tesla"},
	KeyNavHeaderSwitchVehicleAria: {ES: "Cambiar de vehículo", EN: "Switch vehicle"},
	KeyNavHeaderStatusConnected:   {ES: "Conectado", EN: "Connected"},
	KeyNavHeaderStatusAsleep:      {ES: "Dormido", EN: "Asleep"},
	KeyNavHeaderStatusAwaiting:    {ES: "Esperando primer dato", EN: "Awaiting first snapshot"},
	KeyNavHeaderStatusUnavailable: {ES: "No disponible", EN: "Unavailable"},

	// Language names are conventionally not translated — both fields carry the
	// same string. This is NOT a special case for TestCatalog_AllKeysHaveBothLanguages
	// (design.md D2): that test only asserts non-empty, never that the two
	// strings differ.
	KeyLangSwitcherAria:    {ES: "Cambiar idioma", EN: "Change language"},
	KeyLangSwitcherSpanish: {ES: "Español", EN: "Español"},
	KeyLangSwitcherEnglish: {ES: "English", EN: "English"},
}

// translate resolves key in lang. Two distinct failure modes, two distinct
// answers (design.md D2):
//   - key not in catalog at all -> a visible missingKeyMarker + key marker.
//   - key exists but is missing lang's string (Go zero value "") -> falls
//     back to the ES string, since ES is the catalog's base/default locale.
//     This is a render-time backstop only; TestCatalog_AllKeysHaveBothLanguages
//     is the real enforcement mechanism.
func translate(lang string, key Key) string {
	e, ok := catalog[key]
	if !ok {
		return missingKeyMarker + string(key)
	}
	if lang == account.LanguageEN {
		return e.EN
	}
	return e.ES
}
