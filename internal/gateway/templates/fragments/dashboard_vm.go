package fragments

// TireWheelVM is one wheel's tire-pressure tile: the current reading, its trend
// direction, and its formatted day-over-day delta. Mirrors SuperchargerTiles'
// "one struct per repeated shape" pattern (internal/gateway/templates/fragments/
// supercharger_vm.go) rather than four separate flat string fields per wheel.
type TireWheelVM struct {
	// Value is the wheel's current pressure ("42.1 PSI"), or "—" when the raw
	// reading is absent (the vehicle did not report TPMS at capture, or the row
	// predates the RM50 tier 1 migration — analytics.VehicleStatus's own doc
	// comment on TpmsPressureFLPSI). Never a fabricated value.
	Value string
	// Trend is "up", "down", or "" for ui.StatTileProps.Trend (tier 2's
	// mechanism, reused verbatim — no second trend mechanism is introduced).
	// "" covers two distinct cases: the delta is absent (unknown), or the delta
	// is exactly 0.0 (a real "no change" reading) — see D5.
	Trend string
	// Delta is the tile's stat-desc line ("+0.4 vs prev. day", RD10), or "" when
	// the delta is absent (StatTile renders no stat-desc line at all when Desc
	// is empty — never a fabricated "0.0 vs prev. day" for an unknown delta).
	Delta string
}

// DashboardData drives the single-vehicle dashboard page (the SELECTED vehicle's
// latest telemetry, rendered as the Apex/Kinetic bento grid). Every display field
// is a pre-computed string produced by the handler — the template does no
// arithmetic, no unit conversion, no time math (ai/htmx-conventions.md
// §"No business logic in templates").
//
// Degradation is signaled by distinct flags rather than free-form notice strings:
//
//   - NeedsConnect         — the account has no registered vehicles; the page
//     shows a "Connect your Tesla" prompt instead of the bento.
//   - TelemetryUnavailable — the telemetry Reader errored; the page shows a
//     warning Alert (Notice carries the message) and a
//     placeholder bento (vehicle identity only).
//   - HasSnapshot == false (and the above two false) — the vehicle is registered
//     but no nightly snapshot exists yet; the bento renders
//     with "—" placeholder values and an "Awaiting first
//     snapshot" subtitle (no alert — not an error).
type DashboardData struct {
	NeedsConnect         bool
	TelemetryUnavailable bool
	Notice               string // shown only when TelemetryUnavailable is true

	// Vehicle identity for the selected vehicle (empty when NeedsConnect).
	VehicleName  string
	VIN          string
	VehicleImage string // "/static/img/<carType><ExteriorColor>.png", or "/static/img/defaultCar.png" when unset/unknown

	// HasSnapshot is true when a latest nightly snapshot exists for the selected
	// vehicle. When false, all metric fields below are empty and the template
	// renders "—" placeholders.
	HasSnapshot bool

	// --- Hero "Vehicle Status" panel ---
	StatusLabel string // "Charging" | "Parked"; "" when no snapshot
	SoftwareVer string // "Software v11.1.2"; "" when not reported
	LastUpdated string // "2006-01-02" (UTC calendar date); "" when no snapshot
	IsStale     bool   // true when CapturedAt is older than stalenessThreshold

	// --- Hero mini-stat grid ---
	Odometer    string // "20,088 km" (whole km, thousands-separated)
	InsideTemp  string // "22 °C"
	OutsideTemp string // "15 °C"
	// MaxRangeCharges is the vehicle's lifetime count of charges to its true
	// 100% Maximum-Battery-Range limit, already formatted ("12"). "—" when the
	// vehicle never reported it or the stored row predates the column — the
	// same placeholder the temperatures use. A reported 0 renders as "0".
	MaxRangeCharges string

	// --- Travel Progress subsection (RM50 tier 2) ---
	// DistanceTraveled is the latest computed day's driven distance ("45 km"), or "—"
	// when the day has no predecessor (nil DistanceTraveledKmCalc) or there is no
	// snapshot at all. Never a fabricated 0.
	DistanceTraveled string
	// BatteryUsed is the latest computed day's battery percent used ("12.3%"), or "—"
	// under the same nil rule as DistanceTraveled.
	BatteryUsed string

	// --- Tire pressure (PSI) subsection (RM50 tier 4) ---
	TirePressureFL TireWheelVM
	TirePressureFR TireWheelVM
	TirePressureRL TireWheelVM
	TirePressureRR TireWheelVM

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

	// Locked and SentryMode drive the header's Locked/Sentry ui.Badge pills
	// (RM38-gateway-read-dashboard-from-metrics, design.md D2/D4). Both are
	// three-state pointers sourced verbatim from analytics.VehicleStatus: nil
	// means the vehicle_metrics row predates the RM38 migration (or has not been
	// recomputed since) — no badge renders for that field. Never fabricate a
	// false from a nil.
	Locked     *bool
	SentryMode *bool
}
