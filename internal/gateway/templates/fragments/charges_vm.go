// Package fragments holds the htmx-swappable HTML fragment components and their
// presentation view models. View models are pure Go structs — no manualcharge.*,
// no pgtype.*, no vendor-suffixed types — so templates stay logic-free.
package fragments

// ChargeEntryVM is the gateway presentation model for one manual charge entry.
// All derived values are pre-computed by the handler; templates do no arithmetic.
// No manualcharge.Entry, pgtype, or time.Duration in this struct (design.md D6).
type ChargeEntryVM struct {
	ID              string // UUID formatted as string for URL path params
	VehicleLabel    string // DisplayName from the registered vehicle (looked up by TeslaID)
	ChargedOnLabel  string // formatted date, e.g. "Mon Jan 2, 2006"
	EnergyKWh       string // formatted, e.g. "12.50 kWh"
	PriceLabel      string // formatted with currency, e.g. "15000.00 COP"
	Currency        string // ISO 4217 code, e.g. "COP"
	CostPerKWhLabel string // formatted, e.g. "1200.00 COP/kWh"; empty string if nil
	BatteryDelta    string // e.g. "+12%"; empty string if nil
	DurationLabel   string // e.g. "1h 30m"; empty string if nil
	ChargingType    string // "AC", "DC", or "" if nil
	LocationKind    string // "HOME", "WORK", "OTHER", or "" if nil
	LocationLabel   string // free text or ""
	Notes           string // free text or ""
	// Raw values for the inline edit form (pre-populated inputs).
	RawChargedOn       string // "2006-01-02" (HTML date input format)
	RawEnergyKWh       string // "12.50"
	RawPrice           string // "15000.00"
	RawStartedAt       string // "2006-01-02T15:04" (datetime-local) or ""
	RawEndedAt         string // "2006-01-02T15:04" or ""
	RawStartBatteryPct string // "80" or ""
	RawEndBatteryPct   string // "92" or ""
	TeslaID            int64  // for vehicle picker pre-selection in edit form
	VIN                string // durable vehicle key for display
	VehicleValue       string // "{TeslaID}:{VIN}" — pre-built form value for the edit form's hidden vehicle input (keeps the template logic-free, D6)
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

// ChargesPageData is the full-page data for the charge log page and its fragments.
// All fields are presentation-ready; no domain types or pgtype values.
type ChargesPageData struct {
	Entries       []ChargeEntryVM
	ActiveTeslaID int64  // 0 if no vehicle filter; used to pre-select the picker
	CSRFToken     string // for form hidden inputs
	EmptyState    bool   // true when Entries is empty and no error occurred
	Error         string // non-empty if a reader error degraded the page gracefully

	// DefaultStartedAt / DefaultEndedAt are the today's-date defaults (D1) for the
	// optional started_at / ended_at inputs on the create form, pre-formatted by the
	// handler as a datetime-local value "YYYY-MM-DDTHH:MM" (UTC midnight). The template
	// emits them verbatim into the input's value attribute — no time math in markup
	// (the gateway's standing "no business logic in templates" rule). Empty when there
	// is no usable default; both inputs stay OPTIONAL — clearing either still submits.
	DefaultStartedAt string
	DefaultEndedAt   string

	// StartBatteryPctSuggestion is a placeholder / helper-label string for the
	// start_battery_pct field built from the active vehicle's latest telemetry
	// snapshot BatteryLevelPct (D2). Empty string when no telemetry snapshot exists
	// for the active vehicle (graceful empty — render no suggestion in that case).
	// Example non-empty value: "Latest: 73%". The template renders it as the
	// input's placeholder attribute; the field remains a normal required integer.
	StartBatteryPctSuggestion string
}
