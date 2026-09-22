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
	KeyNavDashboard          Key = "nav.dashboard"
	KeyNavExternalCharges    Key = "nav.external_charges"
	KeyNavSuperchargerStats  Key = "nav.supercharger_stats"
	KeyNavVehicleStats       Key = "nav.vehicle_stats"
	KeyNavCommunityBenchmark Key = "nav.community_benchmark"
	KeyNavSectionCharging    Key = "nav.section.charging"
	KeyNavSectionInsights    Key = "nav.section.insights"
	KeyNavSettings           Key = "nav.settings"
	KeyNavSoonBadge          Key = "nav.soon_badge"
	KeyNavOpenSidebar        Key = "nav.open_sidebar"
	KeyNavCloseSidebar       Key = "nav.close_sidebar"
	KeyNavLogout             Key = "nav.logout"

	// --- nav-header fragment (templates/fragments/nav_header.templ) ---
	// Section heading for the dashboard's connect call-to-action. Separate from the
	// nav_header keys below even though the wording overlaps: the two surfaces are
	// edited for different reasons (a sidebar line vs a full-width empty state), and
	// sharing a key would make a copy change to one silently rewrite the other.
	KeyDashboardConnectTitle Key = "dashboard_connect.title"
	KeyDashboardConnectDesc  Key = "dashboard_connect.desc"

	KeyNavHeaderNoTesla           Key = "nav_header.no_tesla"
	KeyNavHeaderConnectLink       Key = "nav_header.connect_link"
	KeyNavHeaderSwitchVehicleAria Key = "nav_header.switch_vehicle_aria"
	KeyNavHeaderBatteryAria       Key = "nav_header.battery_aria"
	KeyNavHeaderUpdatedToday      Key = "nav_header.updated_today"
	KeyNavHeaderUpdatedYesterday  Key = "nav_header.updated_yesterday"
	KeyNavHeaderUpdatedDaysAgo    Key = "nav_header.updated_days_ago"

	// --- language switcher (templates/ui/lang_switcher.templ) ---
	KeyLangSwitcherAria    Key = "lang_switcher.aria"
	KeyLangSwitcherSpanish Key = "lang_switcher.spanish"
	KeyLangSwitcherEnglish Key = "lang_switcher.english"

	// --- theme switcher (templates/ui/theme_switcher.templ) — RM42 tier 2.
	// Theme NAMES (Apex/Graphite/Halloween) are deliberately NOT catalogue keys
	// (roadmap D11) — only the switcher's own label + accessible name are here.
	// Section heading for the /settings theme card (AGENTS.md §"Every section is
	// titled and described"). Deliberately NOT KeyThemeSwitcherLabel: that is the
	// switcher control's own field label, and reusing it would print "Tema" twice on
	// the same card AND couple the section title to the control's copy.
	KeySettingsThemeTitle Key = "settings_theme.title"
	KeySettingsThemeDesc  Key = "settings_theme.desc"

	KeyThemeSwitcherLabel Key = "theme_switcher.label"
	KeyThemeSwitcherAria  Key = "theme_switcher.aria"

	// ==========================================================================
	// RM24-gateway-translate-all-pages (tier 3): every remaining page, fragment,
	// and handler-produced string across the gateway. Added in one single-writer
	// pass (design.md D4) so no downstream task ever edits this file again.
	// ==========================================================================

	// --- brand / proper nouns (design.md D2 — ES=EN, cataloged, not allowlisted) ---
	KeyBrandMagus Key = "brand.magus"
	// KeyBrandMagusMonitor is the product name in normal case ("Magus Monitor").
	// It is cased for READING — og:site_name in layouts.seoHead sends it to a
	// share card, where "MAGUS MONITOR" would read as shouting. The login page's
	// <h1> renders it in caps with the `uppercase` CSS class, so the visual
	// treatment lives in the markup and the string stays reusable.
	KeyBrandMagusMonitor Key = "brand.magus_monitor"

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
	KeyDashboardMaxRangeCharges    Key = "dashboard.max_range_charges"
	KeyDashboardStatus             Key = "dashboard.status"
	KeyDashboardLastUpdated        Key = "dashboard.last_updated"
	KeyDashboardRangePrefix        Key = "dashboard.range_prefix"
	KeyDashboardVehicleStatusTitle Key = "dashboard.vehicle_status_title"
	KeyDashboardBatteryTitle       Key = "dashboard.battery_title"
	KeyDashboardAwaitingSnapshot   Key = "dashboard.awaiting_snapshot"
	KeyDashboardChargeLimit        Key = "dashboard.charge_limit"

	// --- dashboard Vehicle Status subsections (RM50-gateway-add-travel-progress-subsection D6) ---
	KeyDashboardTravelProgressTitle   Key = "dashboard.travel_progress_title"
	KeyDashboardTravelProgressDesc    Key = "dashboard.travel_progress_desc"
	KeyDashboardInteriorExteriorTitle Key = "dashboard.interior_exterior_title"
	KeyDashboardInteriorExteriorDesc  Key = "dashboard.interior_exterior_desc"
	KeyDashboardDistanceTraveled      Key = "dashboard.distance_traveled"
	KeyDashboardBatteryUsed           Key = "dashboard.battery_used"
	KeyDashboardEfficiency            Key = "dashboard.efficiency"

	// --- dashboard Tire pressure subsection (RM50-gateway-add-tire-pressure-subsection D8) ---
	KeyDashboardTirePressureTitle Key = "dashboard.tire_pressure_title"
	KeyDashboardTirePressureDesc  Key = "dashboard.tire_pressure_desc"
	KeyDashboardTireFL            Key = "dashboard.tire_fl"
	KeyDashboardTireFR            Key = "dashboard.tire_fr"
	KeyDashboardTireRL            Key = "dashboard.tire_rl"
	KeyDashboardTireRR            Key = "dashboard.tire_rr"
	KeyDashboardDeltaDesc         Key = "dashboard.delta_desc"

	// --- dashboard status (handlers.go: dashStatus, mapDashboardSnapshot — D5) ---
	KeyDashboardStatusCharging        Key = "dashboard_status.charging"
	KeyDashboardStatusParked          Key = "dashboard_status.parked"
	KeyDashboardStatusSoftwareVersion Key = "dashboard_status.software_version"

	// --- charge form, shared by external_charge_create_form.templ + external_charge_row_edit.templ (D2) ---
	KeyChargesFormTitle           Key = "charges_form.title"
	KeyChargesFormDate            Key = "charges_form.date"
	KeyChargesFormEnergyAdded     Key = "charges_form.energy_added"
	KeyChargesFormPrice           Key = "charges_form.price"
	KeyChargesFormLocation        Key = "charges_form.location"
	KeyChargesFormHome            Key = "charges_form.home"
	KeyChargesFormWork            Key = "charges_form.work"
	KeyChargesFormOther           Key = "charges_form.other"
	KeyChargesFormStartedAt       Key = "charges_form.started_at"
	KeyChargesFormEndedAt         Key = "charges_form.ended_at"
	KeyChargesFormStartBatteryPct Key = "charges_form.start_battery_pct"
	// KeyChargesFormStartBatteryPctHelp is the field-help line telling the
	// user an empty start_battery_pct gets computed by internal/charging,
	// not rejected. Shown on both charge forms.
	KeyChargesFormStartBatteryPctHelp Key = "charges_form.start_battery_pct_help"
	KeyChargesFormEndBatteryPct       Key = "charges_form.end_battery_pct"
	KeyChargesFormOptionalDetails     Key = "charges_form.optional_details"
	// Short helper copy under each form's title. Both state RULES THAT LIVE IN GO
	// (charging.resolveEnergy's derivation and charging.RequiredFieldsFor's sets) —
	// if either rule changes, these strings are part of that change.
	KeyChargesFormCreateHint    Key = "charges_form.create_hint"
	KeyChargesFormEditHint      Key = "charges_form.edit_hint"
	KeyChargesFormChargingType  Key = "charges_form.charging_type"
	KeyChargesFormAC            Key = "charges_form.ac"
	KeyChargesFormDC            Key = "charges_form.dc"
	KeyChargesFormLocationLabel Key = "charges_form.location_label"
	KeyChargesFormNotes         Key = "charges_form.notes"
	KeyChargesFormLogCharge     Key = "charges_form.log_charge"
	KeyChargesFormVehicle       Key = "charges_form.vehicle"
	KeyChargesFormSave          Key = "charges_form.save"
	KeyChargesFormCancel        Key = "charges_form.cancel"
	// --- status control + odometer, added by RM33-gateway-update-charge-form ---
	KeyChargesFormStatus           Key = "charges_form.status"
	KeyChargesFormStatusInProgress Key = "charges_form.status_in_progress"
	KeyChargesFormStatusDone       Key = "charges_form.status_done"
	KeyChargesFormOdometer         Key = "charges_form.odometer"
	// --- "this charge was free" checkbox, added by RM51-gateway-add-free-charge-and-month-preset (design.md §D-Checkbox) ---
	KeyChargesFormPriceConfirmed Key = "charges_form.price_confirmed"

	// --- charge row (templates/fragments/external_charge_row.templ) ---
	KeyChargesRowEdit           Key = "charges_row.edit"
	KeyChargesRowDelete         Key = "charges_row.delete"
	KeyChargesRowConfirmMessage Key = "charges_row.confirm_message"
	KeyChargesRowConfirmTitle   Key = "charges_row.confirm_title"
	KeyChargesRowConfirmLabel   Key = "charges_row.confirm_label"

	// --- charges list (templates/fragments/external_charges_list.templ) ---
	KeyChargesListTitle Key = "charges_list.title"
	KeyChargesListDesc  Key = "charges_list.desc"
	KeyChargesListEmpty Key = "charges_list.empty"

	// --- charges page (templates/pages/external_charges.templ) ---
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
	KeySuperchargerMonthsPreset Key = "supercharger.months_preset"
	// Section headings for the Supercharger page's two cards — the summary tiles
	// (previously untitled) and the sessions table (title but no description).
	KeySuperchargerSummaryTitle Key = "supercharger_summary.title"
	KeySuperchargerSummaryDesc  Key = "supercharger_summary.desc"
	KeySuperchargerSessionsDesc Key = "supercharger_sessions.desc"

	KeySuperchargerSessions      Key = "supercharger.sessions"
	KeySuperchargerEnergy        Key = "supercharger.energy"
	KeySuperchargerCost          Key = "supercharger.cost"
	KeySuperchargerAvgKWhSession Key = "supercharger.avg_kwh_session"
	KeySuperchargerKWhPerMonth   Key = "supercharger.kwh_per_month"
	KeySuperchargerEmpty         Key = "supercharger.empty"
	KeySuperchargerDate          Key = "supercharger.date"

	// --- vehicle stats page (pages/vehicle_stats.templ,
	// fragments/vehicle_stats.templ) ---
	KeyVehicleStatsEmpty             Key = "vehicle_stats.empty"
	KeyVehicleStatsPeriodAria        Key = "vehicle_stats.period_aria"
	KeyVehicleStatsPeriodLabel       Key = "vehicle_stats.period_label"
	KeyVehicleStatsWholeYear         Key = "vehicle_stats.whole_year"
	KeyVehicleStatsSummaryTitle      Key = "vehicle_stats.summary_title"
	KeyVehicleStatsDistance          Key = "vehicle_stats.distance"
	KeyVehicleStatsEfficiency        Key = "vehicle_stats.efficiency"
	KeyVehicleStatsEnergy            Key = "vehicle_stats.energy"
	KeyVehicleStatsSessions          Key = "vehicle_stats.sessions"
	KeyVehicleStatsCost              Key = "vehicle_stats.cost"
	KeyVehicleStatsCostPerKm         Key = "vehicle_stats.cost_per_km"
	KeyVehicleStatsRangeFull         Key = "vehicle_stats.range_full"
	KeyVehicleStatsRangeFullDesc     Key = "vehicle_stats.range_full_desc"
	KeyVehicleStatsErrorCouldNotLoad Key = "vehicle_stats.error.could_not_load"

	// --- vehicle stats "why" section (fragments/vehicle_stats.templ) ---
	KeyVehicleStatsPageSubtitle   Key = "vehicle_stats.page_subtitle"
	KeyVehicleStatsWhyTitle       Key = "vehicle_stats.why_title"
	KeyVehicleStatsWhyDesc        Key = "vehicle_stats.why_desc"
	KeyVehicleStatsHowDrivenTitle Key = "vehicle_stats.how_driven_title"
	KeyVehicleStatsHowDrivenDesc  Key = "vehicle_stats.how_driven_desc"
	KeyVehicleStatsWeekday        Key = "vehicle_stats.weekday"
	KeyVehicleStatsWeekend        Key = "vehicle_stats.weekend"
	KeyVehicleStatsDayCount       Key = "vehicle_stats.day_count"
	KeyVehicleStatsChargeCount    Key = "vehicle_stats.charge_count"

	// --- vehicle stats missing-charge-records warning ---
	KeyVehicleStatsGapsTitle         Key = "vehicle_stats.gaps_title"
	KeyVehicleStatsGapsDesc          Key = "vehicle_stats.gaps_desc"
	KeyVehicleStatsGapManual         Key = "vehicle_stats.gap_manual"
	KeyVehicleStatsGapSupercharger   Key = "vehicle_stats.gap_supercharger"
	KeyVehicleStatsGapActionManual   Key = "vehicle_stats.gap_action_manual"
	KeyVehicleStatsGapActionSC       Key = "vehicle_stats.gap_action_sc"
	KeyVehicleStatsGapsMore          Key = "vehicle_stats.gaps_more"
	KeyVehicleStatsMonthNoData       Key = "vehicle_stats.month_no_data"
	KeyVehicleStatsMonthChartTitle   Key = "vehicle_stats.month_chart_title"
	KeyVehicleStatsMonthChartDesc    Key = "vehicle_stats.month_chart_desc"
	KeyVehicleStatsSummaryDescPrev   Key = "vehicle_stats.summary_desc_prev"
	KeyVehicleStatsSummaryDescNoPrev Key = "vehicle_stats.summary_desc_no_prev"
	KeyVehicleStatsEnergyMixTitle    Key = "vehicle_stats.energy_mix_title"
	KeyVehicleStatsEnergyMixDesc     Key = "vehicle_stats.energy_mix_desc"
	KeyVehicleStatsSourceAC          Key = "vehicle_stats.source_ac"
	KeyVehicleStatsSourceDC          Key = "vehicle_stats.source_dc"
	KeyVehicleStatsSourceSC          Key = "vehicle_stats.source_sc"
	KeyVehicleStatsSourceShare       Key = "vehicle_stats.source_share"
	KeyVehicleStatsEndBattTitle      Key = "vehicle_stats.end_battery_title"
	KeyVehicleStatsEndBattDesc       Key = "vehicle_stats.end_battery_desc"
	KeyVehicleStatsEndBattEmpty      Key = "vehicle_stats.end_battery_empty"
	KeyVehicleStatsEndBattShare      Key = "vehicle_stats.end_battery_share"

	// --- calendar month names, shared (handlers/vehicle_stats_period.go) ---
	// Indexed by time.Month, so a caller maps 1-12 without a switch. Spanish
	// month names are lowercase by orthography -- that is correct, not a typo.
	KeyMonth01 Key = "month.01"
	KeyMonth02 Key = "month.02"
	KeyMonth03 Key = "month.03"
	KeyMonth04 Key = "month.04"
	KeyMonth05 Key = "month.05"
	KeyMonth06 Key = "month.06"
	KeyMonth07 Key = "month.07"
	KeyMonth08 Key = "month.08"
	KeyMonth09 Key = "month.09"
	KeyMonth10 Key = "month.10"
	KeyMonth11 Key = "month.11"
	KeyMonth12 Key = "month.12"

	// --- supercharger status column header (fragments/supercharger_stats.templ) ---
	KeySuperchargerStatus Key = "supercharger.status"

	KeySuperchargerSite           Key = "supercharger.site"
	KeySuperchargerStartBattery   Key = "supercharger.start_battery"
	KeySuperchargerEndBattery     Key = "supercharger.end_battery"
	KeySuperchargerBatteryPctHelp Key = "supercharger.battery_pct_help"

	// --- supercharger in-progress-session guidance alert
	// (fragments/supercharger_stats.templ), owner decision 2026-09-04 ---
	KeySuperchargerStatusHelp Key = "supercharger.status_help"

	KeySuperchargerActions Key = "supercharger.actions"

	// --- supercharger row edit (fragments/supercharger_row.templ, supercharger_row_edit.templ)
	// RM31-gateway-add-session-battery-edit design.md D3/D8/D9/D10 ---
	KeySuperchargerRowEdit   Key = "supercharger_row.edit"
	KeySuperchargerRowSave   Key = "supercharger_row.save"
	KeySuperchargerRowCancel Key = "supercharger_row.cancel"

	// --- supercharger status badge (fragments/supercharger_row.templ),
	// RM41-gateway-add-session-status-column design.md, roadmap D11 — deliberately
	// NOT reusing KeyChargesBadgeInProgress/KeyChargesBadgeDone: a different table's
	// badge is its own semantic role, same precedent as charges_list's header block ---
	KeySuperchargerBadgeInProgress     Key = "supercharger_badge.in_progress"
	KeySuperchargerBadgeDoneCalculated Key = "supercharger_badge.done_calculated"
	// Phone-width copy for the SAME status. "Finalizada (calculada)" needs ~166 px
	// in a column that has ~129 px on a 375 px screen, and a ui.Badge cannot wrap
	// (fixed height, width: fit-content) — it just widens the table. Both strings
	// are always in the HTML and CSS picks one (AGENTS.md §Mobile R4).
	KeySuperchargerBadgeDoneCalculatedShort Key = "supercharger_badge.done_calculated_short"
	KeySuperchargerBadgeDone                Key = "supercharger_badge.done"

	// --- supercharger row validation + errors (handlers/supercharger.go)
	// RM31-gateway-add-session-battery-edit design.md D3/D8/D9/D10 ---
	KeySuperchargerErrorInvalidID       Key = "supercharger_error.invalid_id"
	KeySuperchargerErrorSessionNotFound Key = "supercharger_error.session_not_found"
	KeySuperchargerErrorMalformedBody   Key = "supercharger_error.malformed_body"
	KeySuperchargerErrorStartRange      Key = "supercharger_error.start_range"
	KeySuperchargerErrorEndRange        Key = "supercharger_error.end_range"
	KeySuperchargerErrorCouldNotSave    Key = "supercharger_error.could_not_save"

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

	// --- shared form vocabulary (templates/ui/) ---
	// Rendered by ui.Field when FieldProps.Optional is set. Deliberately generic
	// (no charges_ prefix): it is the ui/ kit's own string, reusable by every
	// future form, and belongs to the kit's closed vocabulary rather than to one
	// page's namespace.
	KeyFormOptional Key = "form.optional"

	// --- charges validation + errors (handlers/external_charges.go) ---
	KeyChargesErrorSelectVehicle                    Key = "charges_error.select_vehicle"
	KeyChargesErrorDateRequired                     Key = "charges_error.date_required"
	KeyChargesErrorInvalidDateFormat                Key = "charges_error.invalid_date_format"
	KeyChargesErrorEnergyPositive                   Key = "charges_error.energy_positive"
	KeyChargesErrorPriceNonNegative                 Key = "charges_error.price_non_negative"
	KeyChargesErrorLocationRequired                 Key = "charges_error.location_required"
	KeyChargesErrorBatteryPctRequired               Key = "charges_error.battery_pct_required"
	KeyChargesErrorStartBatteryPctRange             Key = "charges_error.start_battery_pct_range"
	KeyChargesErrorEndBatteryPctRange               Key = "charges_error.end_battery_pct_range"
	KeyChargesErrorEndBeforeStart                   Key = "charges_error.end_before_start"
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
	// --- status + odometer validation, added by RM33-gateway-update-charge-form ---
	KeyChargesErrorEndedAtRequired Key = "charges_error.ended_at_required"
	KeyChargesErrorStatusInvalid   Key = "charges_error.status_invalid"
	KeyChargesErrorOdometerInvalid Key = "charges_error.odometer_invalid"

	// --- one-IN_PROGRESS-per-day conflict (handlers/external_charges.go: inProgressConflictOn).
	// Carries a single %s verb for the conflicting charged_on date, formatted
	// YYYY-MM-DD by the handler — the template never formats a date.
	KeyChargesErrorInProgressExists Key = "charges_error.in_progress_exists"

	// KeyChargesErrorDateBeforeAnalysisStart carries a single %s verb for the account's
	// analysis start date, formatted YYYY-MM-DD by the handler — mirrors
	// KeyChargesErrorInProgressExists's exact shape (RM49 tier 2, MAG-55).
	KeyChargesErrorDateBeforeAnalysisStart Key = "charges_error.date_before_analysis_start"

	// KeyChargesErrorCouldNotValidateAnalysisStartDate is the design.md D3 lookup-failure
	// message, mirroring KeyChargesErrorCouldNotValidateVehicleOwnership's shape
	// (RM49 tier 2, MAG-55).
	KeyChargesErrorCouldNotValidateAnalysisStartDate Key = "charges_error.could_not_validate_analysis_start_date"

	// --- charges success notices (handlers/external_charges.go: ExternalChargeCreate success path) ---
	KeyChargesNoticeEntryCreated Key = "charges_notice.entry_created"

	// --- language switch errors (handlers/lang.go) ---
	KeyLangSwitchErrorUnsupportedLanguage   Key = "lang_switch_error.unsupported_language"
	KeyLangSwitchErrorCouldNotSaveLanguage  Key = "lang_switch_error.could_not_save_language"
	KeyLangSwitchErrorCouldNotBuildRedirect Key = "lang_switch_error.could_not_build_redirect"

	// --- theme switch errors (handlers/preferences.go) — RM42 tier 2 ---
	KeyThemeSwitchErrorUnsupportedTheme  Key = "theme_switch_error.unsupported_theme"
	KeyThemeSwitchErrorCouldNotSaveTheme Key = "theme_switch_error.could_not_save_theme"

	// --- charges list table headers (fragments/external_charges_list.templ) ---
	// Deliberately NOT reusing KeyChargesFormDate/Vehicle/Price, KeySuperchargerEnergy,
	// or KeyDashboardBatteryTitle: a table column header is a different semantic role
	// with different length constraints than a form label or card title (D2's reuse
	// mandate targets the same string in the same role, not merely the same word) —
	// leader decision, gap found in wave 2 review of external_charges_list.templ.
	KeyChargesListHeaderDate       Key = "charges_list.header_date"
	KeyChargesListHeaderEnergy     Key = "charges_list.header_energy"
	KeyChargesListHeaderPrice      Key = "charges_list.header_price"
	KeyChargesListHeaderCostPerKWh Key = "charges_list.header_cost_per_kwh"
	KeyChargesListHeaderBattery    Key = "charges_list.header_battery"
	KeyChargesListHeaderDuration   Key = "charges_list.header_duration"
	KeyChargesListHeaderActions    Key = "charges_list.header_actions"
	// --- added by RM33-gateway-add-entries-dashboard (design.md §D9/§D11) ---
	KeyChargesListHeaderStatus       Key = "charges_list.header_status"
	KeyChargesListHeaderBatteryRange Key = "charges_list.header_battery_range"

	// --- charges date-filter presets (fragments/external_charges_list.templ), design.md §D-Presets ---
	KeyChargesRangeLast7Days Key = "charges_range.last_7_days"
	KeyChargesRangeThisMonth Key = "charges_range.this_month"
	// --- "last month" preset, added by RM51-gateway-add-free-charge-and-month-preset (design.md §D-Order) ---
	KeyChargesRangeLastMonth Key = "charges_range.last_month"

	// --- charges aggregation tiles (fragments/external_charges_list.templ), design.md §D-Tiles ---
	// Summary-card heading for the charges tiles (AGENTS.md §"Every section is
	// titled and described"). The card holding the four tiles had no title at all.
	KeyChargesSummaryTitle Key = "charges_summary.title"
	KeyChargesSummaryDesc  Key = "charges_summary.desc"

	KeyChargesTileSessions Key = "charges_tile.sessions"
	KeyChargesTileEnergy   Key = "charges_tile.energy"
	KeyChargesTileCost     Key = "charges_tile.cost"
	KeyChargesTileAvgKWh   Key = "charges_tile.avg_kwh_session"

	// --- charges status badge (fragments/external_charge_row.templ) — deliberately NOT
	// reusing KeyChargesFormStatusInProgress/Done, same table/form
	// semantic-role precedent as the header block above (design.md §D-Dot) ---
	KeyChargesBadgeInProgress Key = "charges_badge.in_progress"
	KeyChargesBadgeDone       Key = "charges_badge.done"

	// --- completeness dot tooltips (fragments/external_charge_row.templ), design.md §D-Dot ---
	KeyChargesDotCompleteTooltip   Key = "charges_dot.complete_tooltip"
	KeyChargesDotIncompleteTooltip Key = "charges_dot.incomplete_tooltip"

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

	// --- SEO / social-share metadata (layouts.seoHead, MAG-seo) ---
	// These are the strings a search engine and a link preview (WhatsApp,
	// Slack, X, LinkedIn) show for the PUBLIC pages — the landing page and
	// /login. They are catalog entries like every other user-facing string,
	// not literals in the layout: a crawler with no cookie gets the catalog's
	// default language, which is Spanish (i18n.FromContext falls back to
	// account.LanguageES), so "Spanish by default" needs no special case here.
	// KeySEOHomeTitle is the landing page's <title> AND its og:title — it is a
	// full sentence, not the "%s — Magus Monitor" composition, because the
	// home page has no page name to interpolate and a bare "Magus Monitor"
	// tells a search result nothing about what the product is.
	KeySEOHomeTitle   Key = "seo.home_title"
	KeySEODescription Key = "seo.description"
	KeySEOImageAlt    Key = "seo.image_alt"

	// --- RM34-gateway-block-inactive-login (tier 2) ---
	// pages.AccountBlocked() — the page GoogleCallback renders at HTTP 403 for
	// an account whose status is not Active (design.md D6/D20).
	KeyAccountBlockedTitle   Key = "account_blocked.title"
	KeyAccountBlockedMessage Key = "account_blocked.message"

	// --- iOS install hint (ui.InstallHint, RD16) ---
	// The three strings of the "add this to your home screen" hint shown to
	// iOS visitors only. Android needs no copy at all: Chrome reads
	// /site.webmanifest and raises its own install prompt, in the OS
	// language. Safari never fires beforeinstallprompt, so on iOS the only
	// install path is the Share sheet — a path the user has to be told about.
	// The body names the two Share-sheet entries VERBATIM as iOS spells them
	// in each language ("Añadir a pantalla de inicio" / "Add to Home Screen");
	// a prettier translation would send the user looking for a row that does
	// not exist.
	KeyInstallHintTitle   Key = "install_hint.title"
	KeyInstallHintBody    Key = "install_hint.body"
	KeyInstallHintDismiss Key = "install_hint.dismiss"
)

// catalog is the entire translation vocabulary. TestCatalog_AllKeysHaveBothLanguages
// (catalog_test.go) is the actual enforcement of "every key has both
// languages" — this map literal is the only place that rule can be violated,
// and that test fails at `go test` time if it ever is.
var catalog = map[Key]entry{
	KeyNavDashboard:          {ES: "Panel", EN: "Dashboard"},
	KeyNavExternalCharges:    {ES: "Externas", EN: "External"},
	KeyNavSuperchargerStats:  {ES: "Supercharger", EN: "Supercharger"},
	KeyNavVehicleStats:       {ES: "Estadísticas", EN: "Vehicle Stats"},
	KeyNavCommunityBenchmark: {ES: "Comparativa", EN: "Community Benchmark"},
	KeyNavSectionCharging:    {ES: "Carga", EN: "Charging"},
	KeyNavSectionInsights:    {ES: "Análisis", EN: "Insights"},
	KeyNavSettings:           {ES: "Configuración", EN: "Settings"},
	KeyNavSoonBadge:          {ES: "Pronto", EN: "Soon"},
	KeyNavOpenSidebar:        {ES: "Abrir menú lateral", EN: "open sidebar"},
	KeyNavCloseSidebar:       {ES: "Cerrar menú lateral", EN: "close sidebar"},
	KeyNavLogout:             {ES: "Cerrar sesión", EN: "Log out"},

	KeyDashboardConnectTitle: {ES: "Ningún Tesla conectado", EN: "No Tesla connected"},
	KeyDashboardConnectDesc:  {ES: "Conecta tu cuenta de Tesla para ver la batería, el odómetro y el historial de tu vehículo.", EN: "Connect your Tesla account to see your vehicle's battery, odometer and history."},

	KeyNavHeaderNoTesla:           {ES: "Ningún Tesla conectado.", EN: "No Tesla connected."},
	KeyNavHeaderConnectLink:       {ES: "Conecta tu Tesla", EN: "Connect your Tesla"},
	KeyNavHeaderSwitchVehicleAria: {ES: "Cambiar de vehículo", EN: "Switch vehicle"},
	KeyNavHeaderBatteryAria:       {ES: "Nivel de batería", EN: "Battery level"},
	KeyNavHeaderUpdatedToday:      {ES: "hoy", EN: "today"},
	KeyNavHeaderUpdatedYesterday:  {ES: "ayer", EN: "yesterday"},
	KeyNavHeaderUpdatedDaysAgo:    {ES: "hace %d días", EN: "%d days ago"},

	// Language names are conventionally not translated — both fields carry the
	// same string. This is NOT a special case for TestCatalog_AllKeysHaveBothLanguages
	// (design.md D2): that test only asserts non-empty, never that the two
	// strings differ.
	KeyLangSwitcherAria:    {ES: "Cambiar idioma", EN: "Change language"},
	KeyLangSwitcherSpanish: {ES: "Español", EN: "Español"},
	KeyLangSwitcherEnglish: {ES: "English", EN: "English"},

	KeySettingsThemeTitle: {ES: "Apariencia", EN: "Appearance"},
	KeySettingsThemeDesc:  {ES: "Elige los colores de la aplicación. El cambio se aplica al instante y se guarda en tu cuenta.", EN: "Choose the app's colours. The change applies instantly and is saved to your account."},

	KeyThemeSwitcherLabel: {ES: "Tema", EN: "Theme"},
	KeyThemeSwitcherAria:  {ES: "Cambiar tema", EN: "Change theme"},

	// --- RM24-gateway-translate-all-pages (tier 3) ---

	KeyBrandMagus:        {ES: "Magus", EN: "Magus"},
	KeyBrandMagusMonitor: {ES: "Magus Monitor", EN: "Magus Monitor"},

	KeyHomeSignedInAs:   {ES: "Sesión iniciada como", EN: "Signed in as"},
	KeyHomeViewVehicles: {ES: "Ver tus vehículos", EN: "View your vehicles"},
	KeyHomeSignInGoogle: {ES: "Iniciar sesión con Google", EN: "Sign in with Google"},

	KeyLoginTagline:        {ES: "Inteligencia de flota Tesla para Colombia", EN: "Tesla Fleet Intelligence for Colombia"},
	KeyLoginContinueGoogle: {ES: "Continuar con Google", EN: "Continue with Google"},
	KeyLoginPrivacy:        {ES: "Privacidad", EN: "Privacy"},
	KeyLoginTerms:          {ES: "Términos", EN: "Terms"},
	KeyLoginSupport:        {ES: "Soporte", EN: "Support"},

	KeySEOHomeTitle:   {ES: "Magus Monitor — Inteligencia de flota Tesla para Colombia", EN: "Magus Monitor — Tesla Fleet Intelligence for Colombia"},
	KeySEODescription: {ES: "Magus Monitor conecta tu cuenta Tesla y convierte los datos de tu vehículo en un panel diario: batería, autonomía, odómetro, cargas y eficiencia. Hecho para Colombia, en kilómetros y grados Celsius.", EN: "Magus Monitor connects your Tesla account and turns your vehicle data into a daily dashboard: battery, range, odometer, charges and efficiency. Built for Colombia, in kilometres and degrees Celsius."},
	KeySEOImageAlt:    {ES: "Logo de Magus Monitor", EN: "Magus Monitor logo"},

	KeyDashboardStaleBadge:         {ES: "Desactualizado", EN: "Stale"},
	KeyDashboardOdometer:           {ES: "Odómetro", EN: "Odometer"},
	KeyDashboardInterior:           {ES: "Interior", EN: "Interior"},
	KeyDashboardExterior:           {ES: "Exterior", EN: "Exterior"},
	KeyDashboardMaxRangeCharges:    {ES: "Cargas 100%", EN: "100% Charges"},
	KeyDashboardStatus:             {ES: "Estado", EN: "Status"},
	KeyDashboardLastUpdated:        {ES: "Última actualización", EN: "Last updated"},
	KeyDashboardRangePrefix:        {ES: "Autonomía", EN: "Range"},
	KeyDashboardVehicleStatusTitle: {ES: "Estado del vehículo", EN: "Vehicle Status"},
	KeyDashboardBatteryTitle:       {ES: "Carga de batería", EN: "Battery Charge"},
	KeyDashboardAwaitingSnapshot:   {ES: "Esperando el primer dato", EN: "Awaiting first snapshot"},
	KeyDashboardChargeLimit:        {ES: "Límite %d%%", EN: "Limit %d%%"},

	KeyDashboardTravelProgressTitle:   {ES: "Progreso de viaje", EN: "Travel Progress"},
	KeyDashboardTravelProgressDesc:    {ES: "Distancia recorrida y batería usada en el último día calculado.", EN: "Distance travelled and battery used on the last computed day."},
	KeyDashboardInteriorExteriorTitle: {ES: "Interior / Exterior", EN: "Interior / Exterior"},
	KeyDashboardInteriorExteriorDesc:  {ES: "Temperatura dentro y fuera del vehículo.", EN: "Temperature inside and outside the vehicle."},
	KeyDashboardDistanceTraveled:      {ES: "Distancia recorrida", EN: "Distance travelled"},
	KeyDashboardBatteryUsed:           {ES: "Batería usada", EN: "Battery used"},
	KeyDashboardEfficiency:            {ES: "Eficiencia", EN: "Efficiency"},

	KeyDashboardTirePressureTitle: {ES: "Presión de llantas (PSI)", EN: "Tire pressure (PSI)"},
	KeyDashboardTirePressureDesc:  {ES: "Presión actual de cada llanta y su cambio respecto al día anterior.", EN: "Current pressure per wheel and its change versus the previous day."},
	KeyDashboardTireFL:            {ES: "Delantera izquierda", EN: "Front left"},
	KeyDashboardTireFR:            {ES: "Delantera derecha", EN: "Front right"},
	KeyDashboardTireRL:            {ES: "Trasera izquierda", EN: "Rear left"},
	KeyDashboardTireRR:            {ES: "Trasera derecha", EN: "Rear right"},
	KeyDashboardDeltaDesc:         {ES: "%s vs. día anterior", EN: "%s vs prev. day"},

	KeyDashboardStatusCharging:        {ES: "Cargando", EN: "Charging"},
	KeyDashboardStatusParked:          {ES: "Estacionado", EN: "Parked"},
	KeyDashboardStatusSoftwareVersion: {ES: "Software v%s", EN: "Software v%s"},

	KeyChargesFormTitle:               {ES: "Registrar una carga", EN: "Log a charge"},
	KeyChargesFormDate:                {ES: "Fecha", EN: "Date"},
	KeyChargesFormEnergyAdded:         {ES: "Energía agregada (kWh)", EN: "Energy added (kWh)"},
	KeyChargesFormPrice:               {ES: "Precio", EN: "Price"},
	KeyChargesFormLocation:            {ES: "Ubicación", EN: "Location"},
	KeyChargesFormHome:                {ES: "Casa", EN: "Home"},
	KeyChargesFormWork:                {ES: "Trabajo", EN: "Work"},
	KeyChargesFormOther:               {ES: "Otro", EN: "Other"},
	KeyChargesFormStartedAt:           {ES: "Hora de inicio", EN: "Started at"},
	KeyChargesFormEndedAt:             {ES: "Hora de fin", EN: "Ended at"},
	KeyChargesFormStartBatteryPct:     {ES: "% de batería inicial", EN: "Start battery %"},
	KeyChargesFormStartBatteryPctHelp: {ES: "Opcional. Déjalo vacío y lo calculamos con la energía y el % final.", EN: "Optional. Leave it empty and we calculate it from the energy and the end %."},
	KeyChargesFormEndBatteryPct:       {ES: "% de batería final", EN: "End battery %"},
	KeyChargesFormOptionalDetails:     {ES: "Detalles opcionales", EN: "Optional details"},
	KeyChargesFormCreateHint:          {ES: "Si dejas Energía vacía, se estimará a partir de la diferencia de batería, una vez que el porcentaje inicial y el final estén definidos. Una carga En progreso solo requiere Fecha y Ubicación.", EN: "Leave Energy empty and it will be estimated from the battery difference, once both the start and end percentages are set. An In progress charge only requires Date and Location."},
	KeyChargesFormEditHint:            {ES: "Para cambiar el estado a Finalizada, Hora de fin y % de batería final son obligatorios.", EN: "To change the status to Done, Ended at and End battery % are required."},
	KeyChargesFormChargingType:        {ES: "Tipo de carga", EN: "Charging type"},
	KeyChargesFormAC:                  {ES: "AC — Carga lenta (casa/destino)", EN: "AC — Slow charging (home/destination)"},
	KeyChargesFormDC:                  {ES: "DC — Carga rápida (Supercargador)", EN: "DC — Fast charging (Supercharger)"},
	KeyChargesFormLocationLabel:       {ES: "Etiqueta de ubicación", EN: "Location label"},
	KeyChargesFormNotes:               {ES: "Notas", EN: "Notes"},
	KeyChargesFormLogCharge:           {ES: "Registrar carga", EN: "Log charge"},
	KeyChargesFormVehicle:             {ES: "Vehículo", EN: "Vehicle"},
	KeyChargesFormSave:                {ES: "Guardar", EN: "Save"},
	KeyChargesFormCancel:              {ES: "Cancelar", EN: "Cancel"},
	KeyChargesFormStatus:              {ES: "Estado", EN: "Status"},
	KeyChargesFormStatusInProgress:    {ES: "En progreso", EN: "In progress"},
	KeyChargesFormStatusDone:          {ES: "Finalizada", EN: "Done"},
	KeyChargesFormOdometer:            {ES: "Odómetro (km)", EN: "Odometer (km)"},
	KeyChargesFormPriceConfirmed:      {ES: "Esta carga fue gratis", EN: "This charge was free"},

	KeyChargesRowEdit:           {ES: "Editar", EN: "Edit"},
	KeyChargesRowDelete:         {ES: "Eliminar", EN: "Delete"},
	KeyChargesRowConfirmMessage: {ES: "¿Eliminar la carga de %s registrada el %s? Esta acción no se puede deshacer.", EN: "Delete the %s charge logged on %s? This cannot be undone."},
	KeyChargesRowConfirmTitle:   {ES: "Eliminar entrada de carga", EN: "Delete charge entry"},
	KeyChargesRowConfirmLabel:   {ES: "Eliminar entrada", EN: "Delete entry"},

	KeyChargesListTitle: {ES: "Tus registros", EN: "Your entries"},
	KeyChargesListDesc:  {ES: "Cargas que registraste manualmente.", EN: "Charges you logged manually."},
	KeyChargesListEmpty: {ES: "Aún no hay cargas registradas. Usa el formulario de arriba para registrar tu primera carga.", EN: "No charge entries yet. Use the form above to log your first charge."},

	KeyChargesPageTitle:           {ES: "Cargas externas", EN: "External charges"},
	KeyChargesPageBackToDashboard: {ES: "Volver al panel", EN: "Back to dashboard"},

	KeyHistoryAwaitingSnapshots: {ES: "Esperando los datos nocturnos", EN: "Awaiting nightly snapshots"},
	KeyHistoryOdometerTitle:     {ES: "Distancia recorrida", EN: "Distance Traveled"},
	KeyHistoryBatteryTitle:      {ES: "Carga de batería al final del día", EN: "End-of-Day Battery Charge"},
	KeyHistoryDaysPreset:        {ES: "%d días", EN: "%d days"},
	KeyHistoryNoSnapshotTooltip: {ES: "%s · sin dato", EN: "%s · no snapshot"},

	KeyHistoryConsumedTitle:          {ES: "Consumo de batería", EN: "Battery Consumption"},
	KeyHistoryConsumedPctClause:      {ES: "%s%% consumida", EN: "%s%% consumed"},
	KeyHistoryConsumedSpanClause:     {ES: "%s%% · abarca %d días", EN: "%s%% · covers %d days"},
	KeyHistoryConsumedFlaggedClause:  {ES: "posible registro de carga faltante (%s)", EN: "possible missing charge record (%s)"},
	KeyHistoryConsumedNoDataTooltip:  {ES: "%s · sin dato", EN: "%s · no data"},
	KeyHistoryChargeTypeManual:       {ES: "manual", EN: "manual"},
	KeyHistoryChargeTypeSupercharger: {ES: "Supercharger", EN: "Supercharger"},

	KeySuperchargerMonthsPreset: {ES: "%d meses", EN: "%d months"},
	KeySuperchargerSummaryTitle: {ES: "Resumen", EN: "Summary"},
	KeySuperchargerSummaryDesc:  {ES: "Totales de las sesiones de Supercargador en el periodo seleccionado.", EN: "Totals for the Supercharger sessions in the selected period."},
	KeySuperchargerSessionsDesc: {ES: "Sesiones de Supercargador registradas por Tesla.", EN: "Supercharger sessions recorded by Tesla."},

	KeySuperchargerSessions:          {ES: "Sesiones", EN: "Sessions"},
	KeySuperchargerEnergy:            {ES: "Energía", EN: "Energy"},
	KeySuperchargerCost:              {ES: "Costo", EN: "Cost"},
	KeySuperchargerAvgKWhSession:     {ES: "kWh prom. / sesión", EN: "Avg kWh / session"},
	KeySuperchargerKWhPerMonth:       {ES: "kWh por mes", EN: "kWh per month"},
	KeySuperchargerEmpty:             {ES: "No hay sesiones de Supercharger en esta ventana.", EN: "No Supercharger sessions in this window."},
	KeyVehicleStatsEmpty:             {ES: "No hay datos para este periodo.", EN: "No data for this period."},
	KeyVehicleStatsPeriodAria:        {ES: "Elegir periodo", EN: "Choose period"},
	KeyVehicleStatsPeriodLabel:       {ES: "Periodo", EN: "Period"},
	KeyVehicleStatsWholeYear:         {ES: "Año completo", EN: "Whole year"},
	KeyVehicleStatsSummaryTitle:      {ES: "Resumen del periodo", EN: "Period summary"},
	KeyVehicleStatsDistance:          {ES: "Distancia", EN: "Distance"},
	KeyVehicleStatsEfficiency:        {ES: "Eficiencia", EN: "Efficiency"},
	KeyVehicleStatsEnergy:            {ES: "Energía cargada", EN: "Energy added"},
	KeyVehicleStatsSessions:          {ES: "Cargas", EN: "Charging sessions"},
	KeyVehicleStatsCost:              {ES: "Costo de carga", EN: "Charging cost"},
	KeyVehicleStatsCostPerKm:         {ES: "Costo por km", EN: "Cost per km"},
	KeyVehicleStatsRangeFull:         {ES: "Autonomía al 100%", EN: "Range at full battery"},
	KeyVehicleStatsRangeFullDesc:     {ES: "mes más reciente", EN: "most recent month"},
	KeyVehicleStatsErrorCouldNotLoad: {ES: "No se pudieron cargar las estadísticas. Inténtalo de nuevo.", EN: "Could not load the stats. Please try again."},

	KeyVehicleStatsPageSubtitle:   {ES: "Cómo se comportó tu Tesla en este periodo", EN: "How your Tesla performed in this period"},
	KeyVehicleStatsWhyTitle:       {ES: "Por qué", EN: "Why"},
	KeyVehicleStatsWhyDesc:        {ES: "Qué hay detrás de las cifras de arriba.", EN: "What is behind the figures above."},
	KeyVehicleStatsHowDrivenTitle: {ES: "Cómo condujiste", EN: "How you drove"},
	KeyVehicleStatsHowDrivenDesc:  {ES: "La eficiencia cambia según el tipo de viaje.", EN: "Efficiency changes with the kind of trip."},
	KeyVehicleStatsWeekday:        {ES: "Entre semana", EN: "Weekdays"},
	KeyVehicleStatsWeekend:        {ES: "Fin de semana", EN: "Weekends"},
	KeyVehicleStatsDayCount:       {ES: "Días con datos: %d", EN: "Days with data: %d"},
	KeyVehicleStatsChargeCount:    {ES: "Cargas: %d", EN: "Charges: %d"},

	KeyVehicleStatsGapsTitle:         {ES: "Registros de carga faltantes", EN: "Missing charge records"},
	KeyVehicleStatsGapsDesc:          {ES: "En estos días el consumo de batería no coincide con los registros de carga. Falta un registro o está incompleto, así que la energía y el costo de arriba quedan bajos, y la eficiencia puede verse mejor de lo real.", EN: "On these days the battery use does not match the charge records. A record is missing or incomplete, so the energy and cost above are too low, and the efficiency may look better than it was."},
	KeyVehicleStatsGapManual:         {ES: "Carga manual", EN: "Manual charge"},
	KeyVehicleStatsGapSupercharger:   {ES: "Supercharger", EN: "Supercharger"},
	KeyVehicleStatsGapActionManual:   {ES: "Agregar registro", EN: "Add record"},
	KeyVehicleStatsGapActionSC:       {ES: "Completar batería", EN: "Complete battery"},
	KeyVehicleStatsGapsMore:          {ES: "y %d días más", EN: "and %d more days"},
	KeyVehicleStatsMonthNoData:       {ES: "sin datos", EN: "no data"},
	KeyVehicleStatsMonthChartTitle:   {ES: "Distancia por mes", EN: "Distance by month"},
	KeyVehicleStatsMonthChartDesc:    {ES: "Cómo se reparte la distancia del periodo entre sus meses.", EN: "How the period's distance is spread across its months."},
	KeyVehicleStatsSummaryDescPrev:   {ES: "Comparado con el periodo anterior.", EN: "Compared with the previous period."},
	KeyVehicleStatsSummaryDescNoPrev: {ES: "No hay un periodo anterior con datos para comparar.", EN: "There is no previous period with data to compare."},
	KeyVehicleStatsEnergyMixTitle:    {ES: "De dónde vino la energía", EN: "Where the energy came from"},
	KeyVehicleStatsEnergyMixDesc:     {ES: "El precio por kWh es lo que explica el costo total.", EN: "The price per kWh is what explains the total cost."},
	KeyVehicleStatsSourceAC:          {ES: "Carga AC", EN: "AC charging"},
	KeyVehicleStatsSourceDC:          {ES: "Carga DC", EN: "DC charging"},
	KeyVehicleStatsSourceSC:          {ES: "Supercharger", EN: "Supercharger"},
	KeyVehicleStatsSourceShare:       {ES: "%s de la energía", EN: "%s of the energy"},
	KeyVehicleStatsEndBattTitle:      {ES: "Con cuánta batería terminas", EN: "How full you leave it"},
	KeyVehicleStatsEndBattDesc:       {ES: "Cuántas cargas terminaron en cada rango.", EN: "How many charges ended in each band."},
	KeyVehicleStatsEndBattEmpty:      {ES: "Ninguna carga del periodo registró la batería final.", EN: "No charge in this period recorded its ending battery."},
	KeyVehicleStatsEndBattShare:      {ES: "%s de las cargas", EN: "%s of charges"},

	KeyMonth01:          {ES: "enero", EN: "January"},
	KeyMonth02:          {ES: "febrero", EN: "February"},
	KeyMonth03:          {ES: "marzo", EN: "March"},
	KeyMonth04:          {ES: "abril", EN: "April"},
	KeyMonth05:          {ES: "mayo", EN: "May"},
	KeyMonth06:          {ES: "junio", EN: "June"},
	KeyMonth07:          {ES: "julio", EN: "July"},
	KeyMonth08:          {ES: "agosto", EN: "August"},
	KeyMonth09:          {ES: "septiembre", EN: "September"},
	KeyMonth10:          {ES: "octubre", EN: "October"},
	KeyMonth11:          {ES: "noviembre", EN: "November"},
	KeyMonth12:          {ES: "diciembre", EN: "December"},
	KeySuperchargerDate: {ES: "Fecha", EN: "Date"},

	KeySuperchargerStatus: {ES: "Estado", EN: "Status"},

	KeySuperchargerSite:         {ES: "Sitio", EN: "Site"},
	KeySuperchargerStartBattery: {ES: "Batería inicial", EN: "Start battery"},
	KeySuperchargerEndBattery:   {ES: "Batería final", EN: "End battery"},
	KeySuperchargerBatteryPctHelp: {
		ES: "Tesla no nos provee los porcentajes de batería inicial y final. Te aconsejamos que siempre intentes recordarlos al usar un Supercharger, para tener mejor precisión en los análisis. Sin embargo, conociendo solo el porcentaje final, el sistema calculará el porcentaje inicial aproximado que tenía el vehículo.",
		EN: "Tesla does not give us the start and end battery percentages. We recommend you always try to remember them when you use a Supercharger, so the analysis is more accurate. Still, if you only know the end percentage, the system will calculate the approximate start percentage the vehicle had.",
	},

	KeySuperchargerStatusHelp: {
		ES: "Si una sesión aparece como «En progreso», te falta registrar sus porcentajes. Edítala y completa el porcentaje final para que el sistema pueda calcular el inicial.",
		EN: `If a session shows as "In progress", its percentages are still missing. Edit it and fill in the end percentage so the system can calculate the start one.`,
	},

	KeySuperchargerActions: {ES: "Acciones", EN: "Actions"},

	KeySuperchargerRowEdit:   {ES: "Editar", EN: "Edit"},
	KeySuperchargerRowSave:   {ES: "Guardar", EN: "Save"},
	KeySuperchargerRowCancel: {ES: "Cancelar", EN: "Cancel"},

	KeySuperchargerBadgeInProgress:          {ES: "En progreso", EN: "In progress"},
	KeySuperchargerBadgeDoneCalculated:      {ES: "Finalizada (calculada)", EN: "Done (calculated)"},
	KeySuperchargerBadgeDoneCalculatedShort: {ES: "Calculada", EN: "Calculated"},
	KeySuperchargerBadgeDone:                {ES: "Finalizada", EN: "Done"},

	KeySuperchargerErrorInvalidID:       {ES: "id inválido", EN: "invalid id"},
	KeySuperchargerErrorSessionNotFound: {ES: "sesión no encontrada", EN: "session not found"},
	KeySuperchargerErrorMalformedBody:   {ES: "cuerpo de la solicitud mal formado", EN: "malformed request body"},
	KeySuperchargerErrorStartRange:      {ES: "El porcentaje de batería inicial debe ser un número entero entre 0 y 100.", EN: "Start battery percentage must be an integer between 0 and 100."},
	KeySuperchargerErrorEndRange:        {ES: "El porcentaje de batería final debe ser un número entero entre 0 y 100.", EN: "End battery percentage must be an integer between 0 and 100."},
	KeySuperchargerErrorCouldNotSave:    {ES: "No se pudo guardar la sesión — inténtalo de nuevo.", EN: "Could not save the session — please try again."},

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

	KeyFormOptional: {ES: "(opcional)", EN: "(optional)"},

	KeyChargesErrorSelectVehicle:                     {ES: "Selecciona un vehículo.", EN: "Please select a vehicle."},
	KeyChargesErrorDateRequired:                      {ES: "La fecha es obligatoria.", EN: "Date is required."},
	KeyChargesErrorInvalidDateFormat:                 {ES: "Formato de fecha inválido.", EN: "Invalid date format."},
	KeyChargesErrorEnergyPositive:                    {ES: "La energía debe ser un número positivo.", EN: "Energy must be a positive number."},
	KeyChargesErrorPriceNonNegative:                  {ES: "El precio debe ser un número no negativo.", EN: "Price must be a non-negative number."},
	KeyChargesErrorLocationRequired:                  {ES: "La ubicación es obligatoria.", EN: "Location is required."},
	KeyChargesErrorBatteryPctRequired:                {ES: "El porcentaje de batería es obligatorio.", EN: "Battery percentage is required."},
	KeyChargesErrorStartBatteryPctRange:              {ES: "El porcentaje de batería inicial debe ser un número entero entre 0 y 100.", EN: "Start battery percentage must be an integer between 0 and 100."},
	KeyChargesErrorEndBatteryPctRange:                {ES: "El porcentaje de batería final debe ser un número entero entre 0 y 100.", EN: "End battery percentage must be an integer between 0 and 100."},
	KeyChargesErrorEndBeforeStart:                    {ES: "La hora de finalización no puede ser anterior a la hora de inicio.", EN: "End time cannot be before start time."},
	KeyChargesErrorCouldNotLoadVehicles:              {ES: "No se pudieron cargar tus vehículos — inténtalo de nuevo.", EN: "Could not load your vehicles — please try again."},
	KeyChargesErrorCouldNotLoadEntries:               {ES: "No se pudieron cargar tus registros — inténtalo de nuevo.", EN: "Could not load your entries — please try again."},
	KeyChargesErrorCouldNotSaveEntry:                 {ES: "No se pudo guardar tu registro — inténtalo de nuevo.", EN: "Could not save your entry — please try again."},
	KeyChargesErrorCouldNotDeleteEntry:               {ES: "No se pudo eliminar el registro — inténtalo de nuevo.", EN: "Could not delete entry — please try again."},
	KeyChargesErrorBatterySuggestion:                 {ES: "Última: %d%%", EN: "Latest: %d%%"},
	KeyChargesErrorCouldNotStartChargeLog:            {ES: "no se pudo iniciar el registro de cargas", EN: "could not start charge log"},
	KeyChargesErrorCouldNotRefreshChargeLog:          {ES: "no se pudo actualizar el registro de cargas", EN: "could not refresh charge log"},
	KeyChargesErrorInvalidID:                         {ES: "id inválido", EN: "invalid id"},
	KeyChargesErrorEntryNotFound:                     {ES: "registro no encontrado", EN: "entry not found"},
	KeyChargesErrorCouldNotValidateVehicleOwnership:  {ES: "no se pudo validar la propiedad del vehículo", EN: "could not validate vehicle ownership"},
	KeyChargesErrorVehicleNotOwned:                   {ES: "el vehículo no pertenece a esta cuenta", EN: "vehicle not owned by this account"},
	KeyChargesErrorInvalidCSRFToken:                  {ES: "token csrf inválido", EN: "invalid csrf token"},
	KeyChargesErrorEndedAtRequired:                   {ES: "La hora de fin es obligatoria cuando el estado es Finalizada.", EN: "Ended at is required when status is Done."},
	KeyChargesErrorStatusInvalid:                     {ES: "Estado inválido.", EN: "Invalid status."},
	KeyChargesErrorOdometerInvalid:                   {ES: "El odómetro debe ser un número entero no negativo.", EN: "Odometer must be a non-negative whole number."},
	KeyChargesErrorInProgressExists:                  {ES: "Ya existe una carga en progreso para el %s.", EN: "There is already a charge in progress for %s."},
	KeyChargesErrorDateBeforeAnalysisStart:           {ES: "La fecha no puede ser anterior al %s, el inicio del análisis de tu cuenta.", EN: "Date cannot be before %s, when your account's analysis starts."},
	KeyChargesErrorCouldNotValidateAnalysisStartDate: {ES: "no se pudo validar la fecha de inicio del análisis", EN: "could not validate analysis start date"},
	KeyChargesNoticeEntryCreated:                     {ES: "Registro agregado correctamente.", EN: "Entry saved successfully."},

	KeyLangSwitchErrorUnsupportedLanguage:   {ES: "idioma no soportado", EN: "unsupported language"},
	KeyLangSwitchErrorCouldNotSaveLanguage:  {ES: "no se pudo guardar la preferencia de idioma", EN: "could not save language preference"},
	KeyLangSwitchErrorCouldNotBuildRedirect: {ES: "no se pudo construir la redirección", EN: "could not build redirect"},

	KeyThemeSwitchErrorUnsupportedTheme:  {ES: "tema no soportado", EN: "unsupported theme"},
	KeyThemeSwitchErrorCouldNotSaveTheme: {ES: "no se pudo guardar el tema", EN: "could not save theme"},

	KeyChargesListHeaderDate:       {ES: "Fecha", EN: "Date"},
	KeyChargesListHeaderEnergy:     {ES: "Energía", EN: "Energy"},
	KeyChargesListHeaderPrice:      {ES: "Precio", EN: "Price"},
	KeyChargesListHeaderCostPerKWh: {ES: "Costo/kWh", EN: "Cost/kWh"},
	// CHANGED by RM33-gateway-add-entries-dashboard: this key now labels ONLY
	// the delta column (a separate Battery Range header exists alongside it),
	// so the generic "Carga de batería"/"Battery Charge" copy is replaced with
	// a delta-specific label.
	KeyChargesListHeaderBattery:      {ES: "Δ Batería", EN: "Battery Δ"},
	KeyChargesListHeaderDuration:     {ES: "Duración", EN: "Duration"},
	KeyChargesListHeaderActions:      {ES: "Acciones", EN: "Actions"},
	KeyChargesListHeaderStatus:       {ES: "Estado", EN: "Status"},
	KeyChargesListHeaderBatteryRange: {ES: "Rango de batería", EN: "Battery range"},

	KeyChargesRangeLast7Days: {ES: "Últimos 7 días", EN: "Last 7 days"},
	KeyChargesRangeThisMonth: {ES: "Este mes", EN: "This month"},
	KeyChargesRangeLastMonth: {ES: "Mes pasado", EN: "Last month"},

	KeyChargesSummaryTitle: {ES: "Resumen", EN: "Summary"},
	KeyChargesSummaryDesc:  {ES: "Totales de las cargas que registraste en el periodo seleccionado.", EN: "Totals for the charges you logged in the selected period."},

	KeyChargesTileSessions: {ES: "Sesiones", EN: "Sessions"},
	KeyChargesTileEnergy:   {ES: "Energía", EN: "Energy"},
	KeyChargesTileCost:     {ES: "Costo", EN: "Cost"},
	KeyChargesTileAvgKWh:   {ES: "kWh prom. / sesión", EN: "Avg kWh / session"},

	KeyChargesBadgeInProgress: {ES: "En progreso", EN: "In progress"},
	KeyChargesBadgeDone:       {ES: "Finalizada", EN: "Done"},

	KeyChargesDotCompleteTooltip:   {ES: "Completo", EN: "Complete"},
	KeyChargesDotIncompleteTooltip: {ES: "Incompleto", EN: "Incomplete"},

	KeyBrandPageTitle: {ES: "%s — Magus Monitor", EN: "%s — Magus Monitor"},
	KeyLoginSignIn:    {ES: "Iniciar sesión", EN: "Sign in"},

	KeyAccountBlockedTitle:   {ES: "Cuenta en revisión", EN: "Account under review"},
	KeyAccountBlockedMessage: {ES: "Estamos validando tu acceso. Dentro de poco tu cuenta estará activa. Lo hacemos así para evitar que usuarios desconocidos entren a la app. Estamos en fase de pruebas, y tu ayuda es muy importante para nosotros.", EN: "We are validating your access. Your account will be active soon. We do it this way to keep unknown users out of the app. We are in a testing phase, and your help is very important to us."},

	KeyInstallHintTitle:   {ES: "Instala Magus en tu iPhone", EN: "Install Magus on your iPhone"},
	KeyInstallHintBody:    {ES: "Toca Compartir y luego «Añadir a pantalla de inicio».", EN: "Tap Share, then “Add to Home Screen”."},
	KeyInstallHintDismiss: {ES: "Entendido", EN: "Got it"},
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
