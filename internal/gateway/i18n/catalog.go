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

	// ==========================================================================
	// RM24-gateway-translate-all-pages (tier 3): every remaining page, fragment,
	// and handler-produced string across the gateway. Added in one single-writer
	// pass (design.md D4) so no downstream task ever edits this file again.
	// ==========================================================================

	// --- brand / proper nouns (design.md D2 — ES=EN, cataloged, not allowlisted) ---
	KeyBrandMagus     Key = "brand.magus"
	KeyBrandTeslaCore Key = "brand.tesla_core"

	// --- home page (templates/pages/home.templ) ---
	KeyHomeSignedInAs   Key = "home.signed_in_as"
	KeyHomeViewVehicles Key = "home.view_vehicles"
	KeyHomeSignInGoogle Key = "home.sign_in_google"

	// --- login page (templates/pages/login.templ) ---
	KeyLoginTagline        Key = "login.tagline"
	KeyLoginContinueGoogle Key = "login.continue_google"
	KeyLoginPrivacy        Key = "login.privacy"
	KeyLoginTerms          Key = "login.terms"
	KeyLoginSupport        Key = "login.support"

	// --- dashboard page (templates/pages/dashboard.templ, dashboard.go) ---
	KeyDashboardStaleBadge         Key = "dashboard.stale_badge"
	KeyDashboardOdometer           Key = "dashboard.odometer"
	KeyDashboardInterior           Key = "dashboard.interior"
	KeyDashboardExterior           Key = "dashboard.exterior"
	KeyDashboardStatus             Key = "dashboard.status"
	KeyDashboardLastUpdated        Key = "dashboard.last_updated"
	KeyDashboardRangePrefix        Key = "dashboard.range_prefix"
	KeyDashboardVehicleStatusTitle Key = "dashboard.vehicle_status_title"
	KeyDashboardBatteryTitle       Key = "dashboard.battery_title"
	KeyDashboardAwaitingSnapshot   Key = "dashboard.awaiting_snapshot"
	KeyDashboardChargeLimit        Key = "dashboard.charge_limit"

	// --- dashboard status (handlers.go: dashStatus, mapDashboardSnapshot — D5) ---
	KeyDashboardStatusCharging        Key = "dashboard_status.charging"
	KeyDashboardStatusParked          Key = "dashboard_status.parked"
	KeyDashboardStatusSoftwareVersion Key = "dashboard_status.software_version"

	// --- charge form, shared by charge_create_form.templ + charge_row_edit.templ (D2) ---
	KeyChargesFormTitle           Key = "charges_form.title"
	KeyChargesFormDate            Key = "charges_form.date"
	KeyChargesFormEnergyAdded     Key = "charges_form.energy_added"
	KeyChargesFormPrice           Key = "charges_form.price"
	KeyChargesFormCurrency        Key = "charges_form.currency"
	KeyChargesFormLocation        Key = "charges_form.location"
	KeyChargesFormHome            Key = "charges_form.home"
	KeyChargesFormWork            Key = "charges_form.work"
	KeyChargesFormOther           Key = "charges_form.other"
	KeyChargesFormStartedAt       Key = "charges_form.started_at"
	KeyChargesFormEndedAt         Key = "charges_form.ended_at"
	KeyChargesFormStartBatteryPct Key = "charges_form.start_battery_pct"
	KeyChargesFormEndBatteryPct   Key = "charges_form.end_battery_pct"
	KeyChargesFormMoreDetails     Key = "charges_form.more_details"
	KeyChargesFormChargingType    Key = "charges_form.charging_type"
	KeyChargesFormAC              Key = "charges_form.ac"
	KeyChargesFormDC              Key = "charges_form.dc"
	KeyChargesFormLocationLabel   Key = "charges_form.location_label"
	KeyChargesFormNotes           Key = "charges_form.notes"
	KeyChargesFormLogCharge       Key = "charges_form.log_charge"
	KeyChargesFormVehicle         Key = "charges_form.vehicle"
	KeyChargesFormLocationKind    Key = "charges_form.location_kind"
	KeyChargesFormSave            Key = "charges_form.save"
	KeyChargesFormCancel          Key = "charges_form.cancel"

	// --- charge row (templates/fragments/charge_row.templ) ---
	KeyChargesRowEdit           Key = "charges_row.edit"
	KeyChargesRowDelete         Key = "charges_row.delete"
	KeyChargesRowConfirmMessage Key = "charges_row.confirm_message"
	KeyChargesRowConfirmTitle   Key = "charges_row.confirm_title"
	KeyChargesRowConfirmLabel   Key = "charges_row.confirm_label"

	// --- charges list (templates/fragments/charges_list.templ) ---
	KeyChargesListTitle   Key = "charges_list.title"
	KeyChargesListRefresh Key = "charges_list.refresh"
	KeyChargesListEmpty   Key = "charges_list.empty"

	// --- charges page (templates/pages/charges.templ) ---
	KeyChargesPageTitle           Key = "charges_page.title"
	KeyChargesPageBackToDashboard Key = "charges_page.back_to_dashboard"

	// --- history chart (templates/fragments/history.templ, handlers/history.go) ---
	KeyHistoryAwaitingSnapshots Key = "history.awaiting_snapshots"
	KeyHistoryOdometerTitle     Key = "history.odometer_title"
	KeyHistoryBatteryTitle      Key = "history.battery_title"
	KeyHistoryDaysPreset        Key = "history.days_preset"
	KeyHistoryNoSnapshotTooltip Key = "history.no_snapshot_tooltip"

	// --- consumed chart (templates/fragments/history.templ, handlers/history.go) ---
	KeyHistoryConsumedTitle          Key = "history.consumed_title"
	KeyHistoryConsumedPctClause      Key = "history.consumed_pct_clause"
	KeyHistoryConsumedSpanClause     Key = "history.consumed_span_clause"
	KeyHistoryConsumedFlaggedClause  Key = "history.consumed_flagged_clause"
	KeyHistoryConsumedNoDataTooltip  Key = "history.consumed_no_data_tooltip"
	KeyHistoryChargeTypeManual       Key = "history.charge_type_manual"
	KeyHistoryChargeTypeSupercharger Key = "history.charge_type_supercharger"

	// --- supercharger stats (templates/fragments/supercharger_stats.templ) ---
	KeySuperchargerMonthsPreset  Key = "supercharger.months_preset"
	KeySuperchargerSessions      Key = "supercharger.sessions"
	KeySuperchargerEnergy        Key = "supercharger.energy"
	KeySuperchargerCost          Key = "supercharger.cost"
	KeySuperchargerAvgKWhSession Key = "supercharger.avg_kwh_session"
	KeySuperchargerKWhPerMonth   Key = "supercharger.kwh_per_month"
	KeySuperchargerEmpty         Key = "supercharger.empty"
	KeySuperchargerDate          Key = "supercharger.date"
	KeySuperchargerSite          Key = "supercharger.site"
	KeySuperchargerCountry       Key = "supercharger.country"
	KeySuperchargerBillingType   Key = "supercharger.billing_type"

	// --- vehicles fragment, dead code but explicitly in scope (design.md Discoveries #3) ---
	KeyVehiclesBatteryLabel     Key = "vehicles.battery_label"
	KeyVehiclesRangeLabel       Key = "vehicles.range_label"
	KeyVehiclesChargingLabel    Key = "vehicles.charging_label"
	KeyVehiclesOdometerLabel    Key = "vehicles.odometer_label"
	KeyVehiclesInsideLabel      Key = "vehicles.inside_label"
	KeyVehiclesOutsideLabel     Key = "vehicles.outside_label"
	KeyVehiclesLocked           Key = "vehicles.locked"
	KeyVehiclesUnlocked         Key = "vehicles.unlocked"
	KeyVehiclesSentryLabel      Key = "vehicles.sentry_label"
	KeyVehiclesNotReported      Key = "vehicles.not_reported"
	KeyVehiclesOn               Key = "vehicles.on"
	KeyVehiclesOff              Key = "vehicles.off"
	KeyVehiclesLastUpdatedLabel Key = "vehicles.last_updated_label"
	KeyVehiclesStaleBadge       Key = "vehicles.stale_badge"
	KeyVehiclesNoDataYet        Key = "vehicles.no_data_yet"

	// --- nav-header "Last seen" (templates/fragments/nav_header.templ,
	// handlers.go: relativeLastSeen — design.md D3/Discoveries #2, closes tier 2's D9) ---
	KeyNavHeaderLastSeen              Key = "nav_header.last_seen"
	KeyNavHeaderLastSeenDays          Key = "nav_header.last_seen_days"
	KeyNavHeaderLastSeenDaysPlural    Key = "nav_header.last_seen_days_plural"
	KeyNavHeaderLastSeenHours         Key = "nav_header.last_seen_hours"
	KeyNavHeaderLastSeenHoursPlural   Key = "nav_header.last_seen_hours_plural"
	KeyNavHeaderLastSeenMinutesPlural Key = "nav_header.last_seen_minutes_plural"
	KeyNavHeaderLastSeenJustNow       Key = "nav_header.last_seen_just_now"

	// --- confirm dialog (templates/ui/confirm_dialog.templ) ---
	KeyConfirmDialogTitle   Key = "confirm_dialog.title"
	KeyConfirmDialogCancel  Key = "confirm_dialog.cancel"
	KeyConfirmDialogConfirm Key = "confirm_dialog.confirm"
	KeyConfirmDialogDelete  Key = "confirm_dialog.delete"
	KeyConfirmDialogClose   Key = "confirm_dialog.close"

	// --- vehicles notices (handlers.go: vehiclesFor) ---
	KeyVehiclesNoticeCouldNotLoadVehicles  Key = "vehicles_notice.could_not_load_vehicles"
	KeyVehiclesNoticeTelemetryUnavailable  Key = "vehicles_notice.telemetry_unavailable"
	KeyVehiclesNoticeCouldNotReachTesla    Key = "vehicles_notice.could_not_reach_tesla"
	KeyVehiclesNoticeSessionExpired        Key = "vehicles_notice.session_expired"
	KeyVehiclesNoticeCouldNotLoadFromTesla Key = "vehicles_notice.could_not_load_from_tesla"
	KeyVehiclesNoticeNoVehiclesFound       Key = "vehicles_notice.no_vehicles_found"

	// --- dashboard notice (handlers.go: dashboardFor). NOTE: the identical
	// "Telemetry unavailable…" notice dashboardFor also sets reuses
	// KeyVehiclesNoticeTelemetryUnavailable above (D2 reuse) — only the
	// "could not load dashboard" text is new here. ---
	KeyDashboardNoticeCouldNotLoadDashboard Key = "dashboard_notice.could_not_load_dashboard"

	// --- oauth / connect errors (handlers.go: bare-text c.String 4xx/5xx bodies) ---
	KeyOAuthErrorInvalidVehicle              Key = "oauth_error.invalid_vehicle"
	KeyOAuthErrorInvalidVehicleID            Key = "oauth_error.invalid_vehicle_id"
	KeyOAuthErrorCouldNotValidateVehicle     Key = "oauth_error.could_not_validate_vehicle"
	KeyOAuthErrorVehicleNotInAccount         Key = "oauth_error.vehicle_not_in_account"
	KeyOAuthErrorCouldNotStartTeslaConnect   Key = "oauth_error.could_not_start_tesla_connect"
	KeyOAuthErrorInvalidOAuthState           Key = "oauth_error.invalid_oauth_state"
	KeyOAuthErrorTeslaConnectFailed          Key = "oauth_error.tesla_connect_failed"
	KeyOAuthErrorCouldNotSaveTeslaConnection Key = "oauth_error.could_not_save_tesla_connection"
	KeyOAuthErrorCouldNotStartLogin          Key = "oauth_error.could_not_start_login"
	KeyOAuthErrorGoogleLoginFailed           Key = "oauth_error.google_login_failed"
	KeyOAuthErrorCouldNotProvisionAccount    Key = "oauth_error.could_not_provision_account"

	// --- charges validation + errors (handlers/charges.go) ---
	KeyChargesErrorSelectVehicle                    Key = "charges_error.select_vehicle"
	KeyChargesErrorDateRequired                     Key = "charges_error.date_required"
	KeyChargesErrorInvalidDateFormat                Key = "charges_error.invalid_date_format"
	KeyChargesErrorEnergyRequired                   Key = "charges_error.energy_required"
	KeyChargesErrorEnergyPositive                   Key = "charges_error.energy_positive"
	KeyChargesErrorPriceRequired                    Key = "charges_error.price_required"
	KeyChargesErrorPriceNonNegative                 Key = "charges_error.price_non_negative"
	KeyChargesErrorLocationRequired                 Key = "charges_error.location_required"
	KeyChargesErrorBatteryPctRequired               Key = "charges_error.battery_pct_required"
	KeyChargesErrorStartBatteryPctRange             Key = "charges_error.start_battery_pct_range"
	KeyChargesErrorEndBatteryPctRange               Key = "charges_error.end_battery_pct_range"
	KeyChargesErrorCouldNotLoadVehicles             Key = "charges_error.could_not_load_vehicles"
	KeyChargesErrorCouldNotLoadEntries              Key = "charges_error.could_not_load_entries"
	KeyChargesErrorCouldNotSaveEntry                Key = "charges_error.could_not_save_entry"
	KeyChargesErrorCouldNotDeleteEntry              Key = "charges_error.could_not_delete_entry"
	KeyChargesErrorBatterySuggestion                Key = "charges_error.battery_suggestion"
	KeyChargesErrorCouldNotStartChargeLog           Key = "charges_error.could_not_start_charge_log"
	KeyChargesErrorCouldNotRefreshChargeLog         Key = "charges_error.could_not_refresh_charge_log"
	KeyChargesErrorInvalidID                        Key = "charges_error.invalid_id"
	KeyChargesErrorEntryNotFound                    Key = "charges_error.entry_not_found"
	KeyChargesErrorCouldNotValidateVehicleOwnership Key = "charges_error.could_not_validate_vehicle_ownership"
	KeyChargesErrorVehicleNotOwned                  Key = "charges_error.vehicle_not_owned"
	KeyChargesErrorInvalidCSRFToken                 Key = "charges_error.invalid_csrf_token"

	// --- language switch errors (handlers/lang.go) ---
	KeyLangSwitchErrorUnsupportedLanguage   Key = "lang_switch_error.unsupported_language"
	KeyLangSwitchErrorCouldNotSaveLanguage  Key = "lang_switch_error.could_not_save_language"
	KeyLangSwitchErrorCouldNotBuildRedirect Key = "lang_switch_error.could_not_build_redirect"

	// --- charges list table headers (fragments/charges_list.templ) ---
	// Deliberately NOT reusing KeyChargesFormDate/Vehicle/Price, KeySuperchargerEnergy,
	// or KeyDashboardBatteryTitle: a table column header is a different semantic role
	// with different length constraints than a form label or card title (D2's reuse
	// mandate targets the same string in the same role, not merely the same word) —
	// leader decision, gap found in wave 2 review of charges_list.templ.
	KeyChargesListHeaderDate       Key = "charges_list.header_date"
	KeyChargesListHeaderVehicle    Key = "charges_list.header_vehicle"
	KeyChargesListHeaderEnergy     Key = "charges_list.header_energy"
	KeyChargesListHeaderPrice      Key = "charges_list.header_price"
	KeyChargesListHeaderCostPerKWh Key = "charges_list.header_cost_per_kwh"
	KeyChargesListHeaderBattery    Key = "charges_list.header_battery"
	KeyChargesListHeaderDuration   Key = "charges_list.header_duration"
	KeyChargesListHeaderActions    Key = "charges_list.header_actions"

	// --- page <title> composition (layouts.Base/BaseAuth call sites in every
	// templates/pages/*.templ) — gap #2 found by the wave-2A worker: the
	// tier-3 inventory missed every hardcoded "<Page> — Magus" browser-tab
	// title. One interpolated brand-suffix key (D3) instead of five literal
	// title keys — the "— Magus" suffix is one editorial decision, and each
	// page name already has its own catalog key (KeyNavDashboard,
	// KeyChargesPageTitle, KeyNavSuperchargerStats) or, for login, the new
	// KeyLoginSignIn below (home reuses bare KeyBrandMagus, no suffix).
	KeyBrandPageTitle Key = "brand.page_title"
	KeyLoginSignIn    Key = "login.sign_in"
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

	// --- RM24-gateway-translate-all-pages (tier 3) ---

	KeyBrandMagus:     {ES: "Magus", EN: "Magus"},
	KeyBrandTeslaCore: {ES: "TESLA CORE", EN: "TESLA CORE"},

	KeyHomeSignedInAs:   {ES: "Sesión iniciada como", EN: "Signed in as"},
	KeyHomeViewVehicles: {ES: "Ver tus vehículos", EN: "View your vehicles"},
	KeyHomeSignInGoogle: {ES: "Iniciar sesión con Google", EN: "Sign in with Google"},

	KeyLoginTagline:        {ES: "Monitoreo de flota de precisión", EN: "Precision Fleet Monitoring"},
	KeyLoginContinueGoogle: {ES: "Continuar con Google", EN: "Continue with Google"},
	KeyLoginPrivacy:        {ES: "Privacidad", EN: "Privacy"},
	KeyLoginTerms:          {ES: "Términos", EN: "Terms"},
	KeyLoginSupport:        {ES: "Soporte", EN: "Support"},

	KeyDashboardStaleBadge:         {ES: "Desactualizado", EN: "Stale"},
	KeyDashboardOdometer:           {ES: "Odómetro", EN: "Odometer"},
	KeyDashboardInterior:           {ES: "Interior", EN: "Interior"},
	KeyDashboardExterior:           {ES: "Exterior", EN: "Exterior"},
	KeyDashboardStatus:             {ES: "Estado", EN: "Status"},
	KeyDashboardLastUpdated:        {ES: "Última actualización", EN: "Last updated"},
	KeyDashboardRangePrefix:        {ES: "Autonomía", EN: "Range"},
	KeyDashboardVehicleStatusTitle: {ES: "Estado del vehículo", EN: "Vehicle Status"},
	KeyDashboardBatteryTitle:       {ES: "Batería", EN: "Battery"},
	KeyDashboardAwaitingSnapshot:   {ES: "Esperando el primer dato", EN: "Awaiting first snapshot"},
	KeyDashboardChargeLimit:        {ES: "Límite %d%%", EN: "Limit %d%%"},

	KeyDashboardStatusCharging:        {ES: "Cargando", EN: "Charging"},
	KeyDashboardStatusParked:          {ES: "Estacionado", EN: "Parked"},
	KeyDashboardStatusSoftwareVersion: {ES: "Software v%s", EN: "Software v%s"},

	KeyChargesFormTitle:           {ES: "Registrar una carga", EN: "Log a charge"},
	KeyChargesFormDate:            {ES: "Fecha", EN: "Date"},
	KeyChargesFormEnergyAdded:     {ES: "Energía agregada (kWh)", EN: "Energy added (kWh)"},
	KeyChargesFormPrice:           {ES: "Precio", EN: "Price"},
	KeyChargesFormCurrency:        {ES: "Moneda", EN: "Currency"},
	KeyChargesFormLocation:        {ES: "Ubicación", EN: "Location"},
	KeyChargesFormHome:            {ES: "Casa", EN: "Home"},
	KeyChargesFormWork:            {ES: "Trabajo", EN: "Work"},
	KeyChargesFormOther:           {ES: "Otro", EN: "Other"},
	KeyChargesFormStartedAt:       {ES: "Hora de inicio", EN: "Started at"},
	KeyChargesFormEndedAt:         {ES: "Hora de fin", EN: "Ended at"},
	KeyChargesFormStartBatteryPct: {ES: "% de batería inicial", EN: "Start battery %"},
	KeyChargesFormEndBatteryPct:   {ES: "% de batería final", EN: "End battery %"},
	KeyChargesFormMoreDetails:     {ES: "Más detalles", EN: "More details"},
	KeyChargesFormChargingType:    {ES: "Tipo de carga", EN: "Charging type"},
	KeyChargesFormAC:              {ES: "AC", EN: "AC"},
	KeyChargesFormDC:              {ES: "DC", EN: "DC"},
	KeyChargesFormLocationLabel:   {ES: "Etiqueta de ubicación", EN: "Location label"},
	KeyChargesFormNotes:           {ES: "Notas", EN: "Notes"},
	KeyChargesFormLogCharge:       {ES: "Registrar carga", EN: "Log charge"},
	KeyChargesFormVehicle:         {ES: "Vehículo", EN: "Vehicle"},
	KeyChargesFormLocationKind:    {ES: "Tipo de ubicación", EN: "Location kind"},
	KeyChargesFormSave:            {ES: "Guardar", EN: "Save"},
	KeyChargesFormCancel:          {ES: "Cancelar", EN: "Cancel"},

	KeyChargesRowEdit:           {ES: "Editar", EN: "Edit"},
	KeyChargesRowDelete:         {ES: "Eliminar", EN: "Delete"},
	KeyChargesRowConfirmMessage: {ES: "¿Eliminar la carga de %s registrada el %s? Esta acción no se puede deshacer.", EN: "Delete the %s charge logged on %s? This cannot be undone."},
	KeyChargesRowConfirmTitle:   {ES: "Eliminar entrada de carga", EN: "Delete charge entry"},
	KeyChargesRowConfirmLabel:   {ES: "Eliminar entrada", EN: "Delete entry"},

	KeyChargesListTitle:   {ES: "Tus registros", EN: "Your entries"},
	KeyChargesListRefresh: {ES: "Actualizar", EN: "Refresh"},
	KeyChargesListEmpty:   {ES: "Aún no hay cargas registradas. Usa el formulario de arriba para registrar tu primera carga.", EN: "No charge entries yet. Use the form above to log your first charge."},

	KeyChargesPageTitle:           {ES: "Registro de cargas", EN: "Charge log"},
	KeyChargesPageBackToDashboard: {ES: "Volver al panel", EN: "Back to dashboard"},

	KeyHistoryAwaitingSnapshots: {ES: "Esperando los datos nocturnos", EN: "Awaiting nightly snapshots"},
	KeyHistoryOdometerTitle:     {ES: "Historial del odómetro", EN: "Odometer history"},
	KeyHistoryBatteryTitle:      {ES: "Historial de batería", EN: "Battery history"},
	KeyHistoryDaysPreset:        {ES: "%d días", EN: "%d days"},
	KeyHistoryNoSnapshotTooltip: {ES: "%s · sin dato", EN: "%s · no snapshot"},

	KeyHistoryConsumedTitle:          {ES: "Batería consumida", EN: "Battery consumed"},
	KeyHistoryConsumedPctClause:      {ES: "%s%% consumida", EN: "%s%% consumed"},
	KeyHistoryConsumedSpanClause:     {ES: "%s%% · abarca %d días", EN: "%s%% · covers %d days"},
	KeyHistoryConsumedFlaggedClause:  {ES: "posible registro de carga faltante (%s)", EN: "possible missing charge record (%s)"},
	KeyHistoryConsumedNoDataTooltip:  {ES: "%s · sin dato", EN: "%s · no data"},
	KeyHistoryChargeTypeManual:       {ES: "manual", EN: "manual"},
	KeyHistoryChargeTypeSupercharger: {ES: "Supercharger", EN: "Supercharger"},

	KeySuperchargerMonthsPreset:  {ES: "%d meses", EN: "%d months"},
	KeySuperchargerSessions:      {ES: "Sesiones", EN: "Sessions"},
	KeySuperchargerEnergy:        {ES: "Energía", EN: "Energy"},
	KeySuperchargerCost:          {ES: "Costo", EN: "Cost"},
	KeySuperchargerAvgKWhSession: {ES: "kWh prom. / sesión", EN: "Avg kWh / session"},
	KeySuperchargerKWhPerMonth:   {ES: "kWh por mes", EN: "kWh per month"},
	KeySuperchargerEmpty:         {ES: "No hay sesiones de Supercharger en esta ventana.", EN: "No Supercharger sessions in this window."},
	KeySuperchargerDate:          {ES: "Fecha", EN: "Date"},
	KeySuperchargerSite:          {ES: "Sitio", EN: "Site"},
	KeySuperchargerCountry:       {ES: "País", EN: "Country"},
	KeySuperchargerBillingType:   {ES: "Tipo de facturación", EN: "Billing Type"},

	KeyVehiclesBatteryLabel:     {ES: "Batería:", EN: "Battery:"},
	KeyVehiclesRangeLabel:       {ES: "Autonomía:", EN: "Range:"},
	KeyVehiclesChargingLabel:    {ES: "Cargando:", EN: "Charging:"},
	KeyVehiclesOdometerLabel:    {ES: "Odómetro:", EN: "Odometer:"},
	KeyVehiclesInsideLabel:      {ES: "Interior:", EN: "Inside:"},
	KeyVehiclesOutsideLabel:     {ES: "Exterior:", EN: "Outside:"},
	KeyVehiclesLocked:           {ES: "Bloqueado", EN: "Locked"},
	KeyVehiclesUnlocked:         {ES: "Desbloqueado", EN: "Unlocked"},
	KeyVehiclesSentryLabel:      {ES: "Centinela:", EN: "Sentry:"},
	KeyVehiclesNotReported:      {ES: "No reportado", EN: "Not reported"},
	KeyVehiclesOn:               {ES: "Encendido", EN: "On"},
	KeyVehiclesOff:              {ES: "Apagado", EN: "Off"},
	KeyVehiclesLastUpdatedLabel: {ES: "Última actualización:", EN: "Last updated:"},
	KeyVehiclesStaleBadge:       {ES: "⚠ Desactualizado", EN: "⚠ Stale"},
	KeyVehiclesNoDataYet:        {ES: "Aún no hay datos — esperando el primer dato nocturno.", EN: "No data yet — awaiting first nightly snapshot."},

	KeyNavHeaderLastSeen:              {ES: "Visto por última vez", EN: "Last seen"},
	KeyNavHeaderLastSeenDays:          {ES: "hace 1 día", EN: "1 day ago"},
	KeyNavHeaderLastSeenDaysPlural:    {ES: "hace %d días", EN: "%d days ago"},
	KeyNavHeaderLastSeenHours:         {ES: "hace 1 hora", EN: "1 hour ago"},
	KeyNavHeaderLastSeenHoursPlural:   {ES: "hace %d horas", EN: "%d hours ago"},
	KeyNavHeaderLastSeenMinutesPlural: {ES: "hace %d minutos", EN: "%d minutes ago"},
	KeyNavHeaderLastSeenJustNow:       {ES: "justo ahora", EN: "just now"},

	KeyConfirmDialogTitle:   {ES: "¿Estás seguro?", EN: "Are you sure?"},
	KeyConfirmDialogCancel:  {ES: "Cancelar", EN: "Cancel"},
	KeyConfirmDialogConfirm: {ES: "Confirmar", EN: "Confirm"},
	KeyConfirmDialogDelete:  {ES: "Eliminar", EN: "Delete"},
	KeyConfirmDialogClose:   {ES: "Cerrar", EN: "Close"},

	KeyVehiclesNoticeCouldNotLoadVehicles:  {ES: "No se pudieron cargar tus vehículos. Inténtalo de nuevo.", EN: "Could not load your vehicles. Please try again."},
	KeyVehiclesNoticeTelemetryUnavailable:  {ES: "Telemetría no disponible — mostrando solo la identidad del vehículo.", EN: "Telemetry unavailable — showing vehicle identity only."},
	KeyVehiclesNoticeCouldNotReachTesla:    {ES: "No se pudo contactar tu conexión con Tesla. Inténtalo de nuevo.", EN: "Could not reach your Tesla connection. Please try again."},
	KeyVehiclesNoticeSessionExpired:        {ES: "Tu sesión de Tesla expiró. Vuelve a conectar tu Tesla.", EN: "Your Tesla session expired. Please reconnect your Tesla."},
	KeyVehiclesNoticeCouldNotLoadFromTesla: {ES: "No se pudieron cargar tus vehículos desde Tesla. Inténtalo de nuevo.", EN: "Could not load your vehicles from Tesla. Please try again."},
	KeyVehiclesNoticeNoVehiclesFound:       {ES: "No se encontraron vehículos en tu cuenta de Tesla.", EN: "No vehicles found on your Tesla account."},

	KeyDashboardNoticeCouldNotLoadDashboard: {ES: "No se pudo cargar tu panel. Inténtalo de nuevo.", EN: "Could not load your dashboard. Please try again."},

	KeyOAuthErrorInvalidVehicle:              {ES: "vehículo inválido", EN: "invalid vehicle"},
	KeyOAuthErrorInvalidVehicleID:            {ES: "id de vehículo inválido", EN: "invalid vehicle id"},
	KeyOAuthErrorCouldNotValidateVehicle:     {ES: "no se pudo validar el vehículo", EN: "could not validate vehicle"},
	KeyOAuthErrorVehicleNotInAccount:         {ES: "el vehículo no pertenece a tu cuenta", EN: "vehicle not in your account"},
	KeyOAuthErrorCouldNotStartTeslaConnect:   {ES: "no se pudo iniciar la conexión con Tesla", EN: "could not start Tesla connect"},
	KeyOAuthErrorInvalidOAuthState:           {ES: "estado de oauth inválido", EN: "invalid oauth state"},
	KeyOAuthErrorTeslaConnectFailed:          {ES: "falló la conexión con Tesla", EN: "Tesla connect failed"},
	KeyOAuthErrorCouldNotSaveTeslaConnection: {ES: "no se pudo guardar la conexión con Tesla", EN: "could not save Tesla connection"},
	KeyOAuthErrorCouldNotStartLogin:          {ES: "no se pudo iniciar sesión", EN: "could not start login"},
	KeyOAuthErrorGoogleLoginFailed:           {ES: "falló el inicio de sesión con Google", EN: "google login failed"},
	KeyOAuthErrorCouldNotProvisionAccount:    {ES: "no se pudo aprovisionar la cuenta", EN: "could not provision account"},

	KeyChargesErrorSelectVehicle:                    {ES: "Selecciona un vehículo.", EN: "Please select a vehicle."},
	KeyChargesErrorDateRequired:                     {ES: "La fecha es obligatoria.", EN: "Date is required."},
	KeyChargesErrorInvalidDateFormat:                {ES: "Formato de fecha inválido.", EN: "Invalid date format."},
	KeyChargesErrorEnergyRequired:                   {ES: "La energía agregada es obligatoria.", EN: "Energy added is required."},
	KeyChargesErrorEnergyPositive:                   {ES: "La energía debe ser un número positivo.", EN: "Energy must be a positive number."},
	KeyChargesErrorPriceRequired:                    {ES: "El precio es obligatorio.", EN: "Price is required."},
	KeyChargesErrorPriceNonNegative:                 {ES: "El precio debe ser un número no negativo.", EN: "Price must be a non-negative number."},
	KeyChargesErrorLocationRequired:                 {ES: "La ubicación es obligatoria.", EN: "Location is required."},
	KeyChargesErrorBatteryPctRequired:               {ES: "El porcentaje de batería es obligatorio.", EN: "Battery percentage is required."},
	KeyChargesErrorStartBatteryPctRange:             {ES: "El porcentaje de batería inicial debe ser un número entero entre 0 y 100.", EN: "Start battery percentage must be an integer between 0 and 100."},
	KeyChargesErrorEndBatteryPctRange:               {ES: "El porcentaje de batería final debe ser un número entero entre 0 y 100.", EN: "End battery percentage must be an integer between 0 and 100."},
	KeyChargesErrorCouldNotLoadVehicles:             {ES: "No se pudieron cargar tus vehículos — inténtalo de nuevo.", EN: "Could not load your vehicles — please try again."},
	KeyChargesErrorCouldNotLoadEntries:              {ES: "No se pudieron cargar tus registros — inténtalo de nuevo.", EN: "Could not load your entries — please try again."},
	KeyChargesErrorCouldNotSaveEntry:                {ES: "No se pudo guardar tu registro — inténtalo de nuevo.", EN: "Could not save your entry — please try again."},
	KeyChargesErrorCouldNotDeleteEntry:              {ES: "No se pudo eliminar el registro — inténtalo de nuevo.", EN: "Could not delete entry — please try again."},
	KeyChargesErrorBatterySuggestion:                {ES: "Última: %d%%", EN: "Latest: %d%%"},
	KeyChargesErrorCouldNotStartChargeLog:           {ES: "no se pudo iniciar el registro de cargas", EN: "could not start charge log"},
	KeyChargesErrorCouldNotRefreshChargeLog:         {ES: "no se pudo actualizar el registro de cargas", EN: "could not refresh charge log"},
	KeyChargesErrorInvalidID:                        {ES: "id inválido", EN: "invalid id"},
	KeyChargesErrorEntryNotFound:                    {ES: "registro no encontrado", EN: "entry not found"},
	KeyChargesErrorCouldNotValidateVehicleOwnership: {ES: "no se pudo validar la propiedad del vehículo", EN: "could not validate vehicle ownership"},
	KeyChargesErrorVehicleNotOwned:                  {ES: "el vehículo no pertenece a esta cuenta", EN: "vehicle not owned by this account"},
	KeyChargesErrorInvalidCSRFToken:                 {ES: "token csrf inválido", EN: "invalid csrf token"},

	KeyLangSwitchErrorUnsupportedLanguage:   {ES: "idioma no soportado", EN: "unsupported language"},
	KeyLangSwitchErrorCouldNotSaveLanguage:  {ES: "no se pudo guardar la preferencia de idioma", EN: "could not save language preference"},
	KeyLangSwitchErrorCouldNotBuildRedirect: {ES: "no se pudo construir la redirección", EN: "could not build redirect"},

	KeyChargesListHeaderDate:       {ES: "Fecha", EN: "Date"},
	KeyChargesListHeaderVehicle:    {ES: "Vehículo", EN: "Vehicle"},
	KeyChargesListHeaderEnergy:     {ES: "Energía", EN: "Energy"},
	KeyChargesListHeaderPrice:      {ES: "Precio", EN: "Price"},
	KeyChargesListHeaderCostPerKWh: {ES: "Costo/kWh", EN: "Cost/kWh"},
	KeyChargesListHeaderBattery:    {ES: "Batería", EN: "Battery"},
	KeyChargesListHeaderDuration:   {ES: "Duración", EN: "Duration"},
	KeyChargesListHeaderActions:    {ES: "Acciones", EN: "Actions"},

	KeyBrandPageTitle: {ES: "%s — Magus", EN: "%s — Magus"},
	KeyLoginSignIn:    {ES: "Iniciar sesión", EN: "Sign in"},
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
