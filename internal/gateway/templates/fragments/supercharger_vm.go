package fragments

// SuperchargerStatsView is the complete view model for the Supercharger Stats
// page/fragment — the month selector, KPI tiles, kWh/month trend chart, and the
// sessions table. ALL numeric values, formatted strings, and chart heights are
// pre-computed by the handler; the template renders them verbatim (mirrors
// HistoryView — ai/htmx-conventions.md §"No business logic in templates").
type SuperchargerStatsView struct {
	// Presets is the ordered list of 3/6/12-month RangePreset entries the
	// selector renders. Each carries a pre-formatted absolute ?start=&end=
	// href and an Active flag (mirrors HistoryView.Presets). Nil on a
	// malformed/400 request — no selector is rendered (design.md D1).
	Presets []RangePreset
	// Tiles holds the four KPI values (Sessions, Energy, Cost, Avg kWh/session).
	Tiles SuperchargerTiles
	// Chart is the kWh-per-month trend chart. Reuses the existing
	// HistoryChart/HistoryBar types verbatim (design.md "View model" section) —
	// no new chart-bar type is introduced for this page.
	Chart HistoryChart
	// Sessions is every session in the window, mapped to a row view model
	// (unpaginated — the month window + read cap already bound the row count).
	Sessions []SuperchargerRowVM
	// Empty is true when zero sessions fall in the window; drives the
	// page-level empty state.
	Empty bool
}

// SuperchargerTiles holds the four pre-formatted KPI strings shown as
// ui.StatTile values. All arithmetic and currency-safe aggregation happens in
// the handler — the template does no formatting or math.
type SuperchargerTiles struct {
	// Sessions is the session count in the window, e.g. "14".
	Sessions string
	// Energy is the total kWh summed across sessions with a known EnergyKWh,
	// e.g. "612.4 kWh".
	Energy string
	// CostLines is one formatted line per currency, e.g.
	// ["132.40 USD", "58,000.00 COP"] — never a single summed total across
	// currencies (D4). Nil/empty when no session has both a known cost and
	// currency.
	CostLines []string
	// AvgKWh is the average energy per session with a known EnergyKWh, e.g.
	// "43.7 kWh"; "—" when no session has a known EnergyKWh (divide-by-zero
	// guard).
	AvgKWh string
}

// SuperchargerRowVM is one row of the sessions table — every field is a
// pre-formatted display string, no domain-type methods reachable from the
// template. Carries no CountryCode/BillingType — charging.Session has
// neither field (design.md D4).
type SuperchargerRowVM struct {
	// DateLabel is ChargeStartDateTime formatted for display.
	DateLabel string
	// SiteLabel is the session's SiteLocationName.
	SiteLabel string
	// EnergyLabel is "N.NN kWh", or "—" when EnergyKWh is nil.
	EnergyLabel string
	// CostLabel is "N.NN <currency>", or "—" when TotalCost or Currency is nil.
	CostLabel string
	// StartBatteryPctLabel is "N%", or "—" when StartBatteryPct is nil.
	StartBatteryPctLabel string
	// EndBatteryPctLabel is "N%", or "—" when EndBatteryPct is nil.
	EndBatteryPctLabel string
	// StartBatteryPctEstLabel is "N%", or "—" when StartBatteryPctEst is nil.
	StartBatteryPctEstLabel string
	// EndBatteryPctEstLabel is "N%", or "—" when EndBatteryPctEst is nil.
	EndBatteryPctEstLabel string
}
