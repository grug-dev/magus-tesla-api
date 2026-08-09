package fragments

// DashboardData drives the single-vehicle dashboard page (the SELECTED vehicle's
// latest telemetry, rendered as the Apex/Kinetic bento grid). Every display field
// is a pre-computed string produced by the handler — the template does no
// arithmetic, no unit conversion, no time math (ai/htmx-conventions.md
// §"No business logic in templates").
//
// Degradation is signaled by distinct flags rather than free-form notice strings:
//
//   - NeedsConnect         — the account has no registered vehicles; the page
//                            shows a "Connect your Tesla" prompt instead of the bento.
//   - TelemetryUnavailable — the telemetry Reader errored; the page shows a
//                            warning Alert (Notice carries the message) and a
//                            placeholder bento (vehicle identity only).
//   - HasSnapshot == false (and the above two false) — the vehicle is registered
//                            but no nightly snapshot exists yet; the bento renders
//                            with "—" placeholder values and an "Awaiting first
//                            snapshot" subtitle (no alert — not an error).
type DashboardData struct {
	NeedsConnect         bool
	TelemetryUnavailable bool
	Notice               string // shown only when TelemetryUnavailable is true

	// Vehicle identity for the selected vehicle (empty when NeedsConnect).
	VehicleName string
	VIN         string

	// HasSnapshot is true when a latest nightly snapshot exists for the selected
	// vehicle. When false, all metric fields below are empty and the template
	// renders "—" placeholders.
	HasSnapshot bool

	// --- Hero "Vehicle Status" panel ---
	StatusLabel string // "Charging" | "Parked"; "" when no snapshot
	SoftwareVer string // "Software v11.1.2"; "" when not reported
	LastUpdated string // "2006-01-02 15:04 UTC"; "" when no snapshot
	IsStale     bool   // true when CapturedAt is older than stalenessThreshold

	// --- Hero mini-stat grid ---
	Odometer    string // "20,088 km" (whole km, thousands-separated)
	InsideTemp  string // "22 °C"
	OutsideTemp string // "15 °C"

	// --- Battery card ---
	Battery     string // "94%"  — the big display number
	BatteryPct  string // "94"   — bare value for the <progress value=""> attribute
	RangeNow    string // "550 km"
	ChargeLimit string // "Limit 80%"; "" when not set

	// DefaultHistoryHref is the pre-formatted absolute href the #dashboard-history
	// region self-loads on first render (`hx-trigger="load"`), pointing at the
	// default 6-day window's start/end calendar dates. The handler computes it
	// once (dashboardFor) so the page template emits it verbatim — no time math
	// in the template (RM8 design D4, logic-free-template invariant). E.g.
	// "/ui/dashboard/history?start=2026-08-03&end=2026-08-09".
	DefaultHistoryHref string
}