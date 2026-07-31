package fragments

// HistoryBar represents one bar in a history chart. All values are pre-computed
// by the handler — the template does NO arithmetic, unit conversion, or domain-type
// method calls (gateway spec invariant / ai/htmx-conventions.md §"No business
// logic in templates").
type HistoryBar struct {
	// HeightPct is the bar height as a percentage (0–100) of the chart canvas.
	// For the odometer chart: (delta km / max delta km) * 100; clamped ≥ 0.
	// For the battery chart: battery level % (0–100 directly).
	HeightPct int
	// Tooltip is the pre-rendered hover string shown in the bar's <title> element.
	// Odometer: "<date> · <km> km driven · odometer <cumulative> km"
	// Battery:  "<date> · <level>% · <range> km range"
	Tooltip string
}

// HistoryChart holds the bars and empty-state flag for one chart panel.
// When Empty is true the handler computed 0 bars (too few snapshots); the
// template renders dashHistoryEmpty() instead of the SVG.
type HistoryChart struct {
	Bars  []HistoryBar
	Empty bool
}

// HistoryView is the complete view model for the #dashboard-history region —
// the days selector plus both chart panels. ALL numeric values, heights, and
// tooltip strings are pre-computed; the template renders them verbatim.
type HistoryView struct {
	// Days is the selected/validated window (always one of Presets).
	Days int
	// Presets is the ordered list of allowed day-count presets (e.g. [6, 14, 30]).
	// Passed through from the handler so the template renders the selector from data,
	// not from embedded magic numbers.
	Presets []int
	// Odometer contains the km-driven-per-day bars (one delta per consecutive pair
	// of snapshots). May have fewer bars than Days when data is sparse.
	Odometer HistoryChart
	// Battery contains the battery-level-% bars (one per snapshot).
	// May have fewer bars than Days when data is sparse.
	Battery HistoryChart
}
