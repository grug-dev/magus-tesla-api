// Package fragments holds the htmx-swappable HTML fragment components and their
// presentation view models. View models are pure Go structs — no charging.*,
// no pgtype.*, no vendor-suffixed types — so templates stay logic-free.
package fragments

// ExternalChargeEntryVM is the gateway presentation model for one manual charge entry.
// All derived values are pre-computed by the handler; templates do no arithmetic.
// No charging.Entry, pgtype, or time.Duration in this struct (design.md D6).
type ExternalChargeEntryVM struct {
	ID             string // UUID formatted as string for URL path params
	VehicleLabel   string // DisplayName from the registered vehicle (looked up by TeslaID)
	ChargedOnLabel string // formatted date, e.g. "Mon Jan 2, 2006"
	// ChargedOnShortLabel is the SAME date as MM-DD ("09-04"), rendered instead of
	// ChargedOnLabel below the `sm` breakpoint (MAG-46 step 3.2). Both strings are
	// always in the HTML and CSS picks one (AGENTS.md §Mobile R4) — the gateway
	// never knows the viewport, so it cannot choose server-side.
	//
	// MM-DD is deliberately language-neutral: it needs no translated month name, so
	// the column has the same width in ES and EN, and it matches the format the
	// history chart already uses on its x-axis.
	ChargedOnShortLabel string
	EnergyKWh           string // formatted, e.g. "12.50 kWh"
	PriceLabel          string // formatted with currency, e.g. "15000.00 COP"
	Currency            string // ISO 4217 code, e.g. "COP"
	CostPerKWhLabel     string // formatted, e.g. "1200.00 COP/kWh"; "—" when not computable
	BatteryDelta        string // e.g. "+12%"; "—" when not computable
	DurationLabel       string // e.g. "1h 30m"; "—" when not computable
	ChargingType        string // "AC", "DC", or "" if nil
	LocationKind        string // "HOME", "WORK", "OTHER", or "" if nil
	LocationLabel       string // free text or ""
	Notes               string // free text or ""
	// Raw values for the inline edit form (pre-populated inputs).
	RawChargedOn       string // "2006-01-02" (HTML date input format)
	RawEnergyKWh       string // "12.50"
	RawPrice           string // "15000.00"
	RawStartedAt       string // "2006-01-02T15:04" (datetime-local) or ""
	RawEndedAt         string // "2006-01-02T15:04" or ""
	RawStartBatteryPct string // "80" or ""
	RawEndBatteryPct   string // "92" or ""
	RawOdometerKm      string // "45210" or "" — mirrors RawEnergyKWh/RawPrice (leader addition, 2026-08-29, design.md §D-Values gap closed by tasks.md 2.1)
	TeslaID            int64  // the entry's owning vehicle (identity key, not rendered — asserted by TestExternalChargeEntryVMFromEntry)
	VIN                string // durable vehicle key (identity key, not rendered — asserted by TestExternalChargeEntryVMFromEntry)

	// Status is a possible future display label (unused by this tier's markup,
	// kept for symmetry with every other VM field pair). RawStatus is the raw
	// persisted status string ("IN_PROGRESS" | "DONE") used to drive the edit
	// form's status <select> `selected` binding. Added by
	// RM33-gateway-update-charge-form (design.md §D-Fields).
	Status    string
	RawStatus string

	// RequiredEndedAt / RequiredEndBatteryPct are computed by the handler from
	// charging.RequiredFieldsFor(e.Status) — never computed in the template
	// (design.md §D-Fields). They drive the `required` attribute on the
	// ended_at / end_battery_pct inputs of the inline edit row.
	RequiredEndedAt       bool
	RequiredEndBatteryPct bool

	// Complete is the handler-computed completeness signal driving the
	// completeness dot's colour (design.md §D-Dot) — computed via
	// entryComplete(e) (handlers/external_charges_tiles.go, Wave 2), NEVER computed in
	// the template. true renders the green (success) dot; false renders the
	// yellow (warning) dot.
	Complete bool

	// BatteryRange is the pre-formatted "22% → 70%" string when both
	// StartBatteryPct and EndBatteryPct are present, else "—" (D11/D14).
	// Rendered ALONGSIDE BatteryDelta, not in place of it.
	BatteryRange string
}

// ExternalChargeFormValues carries the raw, unparsed POST values a user submitted, so a
// 4xx/5xx re-render can echo exactly what they typed — including a value that
// failed validation — rather than a blank or default field (roadmap D15).
// Every field is the literal c.PostForm(name) string; no parsing, no trimming
// beyond what parseExternalChargeForm already applies for its own validation.
type ExternalChargeFormValues struct {
	Status          string // "IN_PROGRESS" | "DONE" | "" (fresh page load)
	EnergyAddedKWh  string
	Price           string
	LocationKind    string
	StartBatteryPct string
	EndBatteryPct   string
	ChargingType    string
	LocationLabel   string
	Notes           string
	OdometerKm      string
}

// VehicleOptionVM is one option in the nav-header vehicle context-switcher <select>
// (see fragments.NavHeaderVM.Vehicles). The manual-charge create/edit form has no
// vehicle picker of its own — it sources the vehicle from the switcher (design.md D4).
type VehicleOptionVM struct {
	TeslaID     int64
	VIN         string
	DisplayName string
	Value       string // "{TeslaID}:{VIN}" — the combined form value the handler parses
	Selected    bool   // pre-computed; true for exactly one option (the auto-selected vehicle)
}

// ExternalChargeTiles holds the four pre-formatted aggregation-tile strings shown as
// ui.StatTile values (design.md §D-Tiles). All arithmetic happens in the
// handler (buildExternalChargeTiles, handlers/external_charges_tiles.go) — the template does
// no formatting or math. Mirrors SuperchargerTiles's doc-comment style
// (supercharger_vm.go), single-currency (COP by construction, D13) so there
// is no CostLines-style per-currency slice here.
type ExternalChargeTiles struct {
	Sessions string // count in the window, e.g. "14"
	Energy   string // e.g. "12.5 kWh"; sums only non-nil EnergyAddedKWh entries
	Cost     string // e.g. "15000.00 COP"; sums Price unconditionally
	AvgKWh   string // Energy / non-nil-energy-count, e.g. "7.5 kWh"; "—" on zero count
}

// ExternalChargesPageData is the full-page data for the charge log page and its fragments.
// All fields are presentation-ready; no domain types or pgtype values.
type ExternalChargesPageData struct {
	Entries    []ExternalChargeEntryVM
	CSRFToken  string // for form hidden inputs
	EmptyState bool   // true when Entries is empty and no error occurred
	Error      string // non-empty if a reader error degraded the page gracefully

	// EditingID is the id of the ONE entry rendered as an inline edit form by
	// ExternalChargesList; every other row renders static. Empty means no row is being
	// edited (every ordinary render). This is what makes "only one row open at a
	// time" a server-side invariant rather than a client-side convention: the
	// list is the unit of truth, so a second Edit click re-renders the whole
	// region with a different single row open and the previous one necessarily
	// closed. Set by ExternalChargeRowEditFragment, by nothing else.
	EditingID string

	// Notice is the success counterpart of Error: a non-empty string renders a
	// success ui.Alert at the top of the create-form card. Set ONLY by
	// ExternalChargeCreate's success path (i18n.KeyChargesNoticeEntryCreated), so it is
	// empty on every fresh load, list refresh and error re-render — the message
	// appears once, on the response to the write that earned it, and disappears
	// on the next swap.
	Notice string

	// Presets is the ordered list of "last 7 days"/"this month" RangePreset
	// entries the filter selector renders (design.md §D-Presets). Nil on a
	// malformed window or no resolved vehicle — no selector is rendered
	// (mirrors SuperchargerStatsView.Presets's exact doc-comment shape).
	Presets []RangePreset

	// Tiles holds the four aggregation-tile values (design.md §D-Tiles),
	// computed over the SAME entries slice Entries is mapped from.
	Tiles ExternalChargeTiles

	// WindowStartStr / WindowEndStr are the already-resolved filter window,
	// pre-formatted "2006-01-02" (design.md §D-Include). Rendered into the
	// stable-id hidden inputs #external-charges-window-start/#external-charges-window-end so
	// the create form's hx-include always reads the current filter window.
	WindowStartStr string
	WindowEndStr   string

	// NoFilterChrome is true when no vehicle is resolved OR the requested
	// window is malformed (design.md §D-Empty state 1) — the presets, tiles,
	// AND table are all hidden; only ExternalChargesEmptyState() renders.
	NoFilterChrome bool

	// DefaultChargedOn is the today's-date default for the REQUIRED charged_on input
	// on the create form, pre-formatted by the handler as a date value "YYYY-MM-DD".
	//
	// DefaultStartedAt / DefaultEndedAt are the same day for the optional started_at /
	// ended_at inputs (D1), pre-formatted as a datetime-local value "YYYY-MM-DDTHH:MM"
	// (local midnight). All three are derived from ONE day value in buildExternalChargesPage,
	// so the "Date" field can never drift from "Started/Ended at".
	//
	// The day is the BROWSER's calendar day (browserToday), not UTC's — for a UTC-5
	// user, UTC has already rolled over to tomorrow after 19:00 local.
	//
	// The template emits all three verbatim into the input's value attribute — no time
	// math in markup (the gateway's standing "no business logic in templates" rule).
	// Empty when there is no usable default; started_at / ended_at stay OPTIONAL —
	// clearing either still submits.
	DefaultChargedOn string
	DefaultStartedAt string
	DefaultEndedAt   string

	// StartBatteryPctSuggestion is a placeholder / helper-label string for the
	// start_battery_pct field built from the active vehicle's latest telemetry
	// snapshot BatteryLevelPct (D2). Empty string when no telemetry snapshot exists
	// for the active vehicle (graceful empty — render no suggestion in that case).
	// Example non-empty value: "Latest: 73%". The template renders it as the
	// input's placeholder attribute; the field remains a normal required integer.
	StartBatteryPctSuggestion string

	// FormValues carries the raw submitted POST values for a 4xx/5xx re-render
	// of the create form, so no submitted value is lost on validation failure
	// (roadmap D15, design.md §D-Values). Zero-value ExternalChargeFormValues{} on a
	// fresh (non-error) page load, reproducing today's blank-field behavior.
	FormValues ExternalChargeFormValues

	// RequiredEndedAt / RequiredEndBatteryPct are computed by the handler from
	// charging.RequiredFieldsFor, never in the template (design.md §D-Fields).
	// They drive the `required` attribute on the create form's ended_at /
	// end_battery_pct inputs.
	RequiredEndedAt       bool
	RequiredEndBatteryPct bool
}
