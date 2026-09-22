package fragments

// VehicleStatsView is the gateway presentation model for the Vehicle Stats
// page. Every value on it is pre-computed by the handler; the template does
// no arithmetic, unit conversion, or currency math — the same contract
// SuperchargerStatsView and ExternalChargesPageData carry.
//
// The KPI tiles land in the next step and add their own fields here.
type VehicleStatsView struct {
	// Periods is the closed list of selectable periods, newest first, built
	// by the handler from the account's analysis_start_date and today. nil
	// means render no period control at all — the no-chrome empty state a
	// malformed window gets, mirroring ExternalChargesPageData.NoFilterChrome.
	Periods []VehicleStatsPeriod

	// PeriodLabel is the selected period's own label, shown on the control's
	// button. Empty when Periods is nil.
	PeriodLabel string

	// Tiles holds the seven KPI values, already formatted. Read only when
	// Empty is false.
	Tiles VehicleStatsTiles

	// Why holds the explanatory blocks rendered under the KPIs — the half of
	// the page that answers why those numbers look the way they do. Read only
	// when Empty is false.
	Why VehicleStatsWhy

	// Empty is true when there is nothing to show for the selected vehicle
	// and period — no vehicle resolved on the session, or no stored month in
	// the window. The template renders the single empty-state card.
	//
	// One flag covers both causes on purpose: they collapse to the identical
	// render, the same way ExternalChargesPageData's own empty state does.
	Empty bool

	// Error is a translated, user-facing message shown instead of the
	// content when a read fails. Empty string means no error.
	Error string
}

// VehicleStatsPeriod is one selectable period in the page's period control —
// either a whole calendar year or a single calendar month.
//
// Each entry carries its OWN complete window, so the control's menu item can
// be a self-contained hx-get with both ?start= and ?end= already in the URL.
// That is what lets this page keep the platform's one date-filter vocabulary
// (AGENTS.md §HTTP date-filter convention) with no client-side JavaScript: a
// native <select> option can carry only one value, a menu item carries a
// whole URL.
type VehicleStatsPeriod struct {
	// Label is the already-translated display text, e.g. "septiembre 2026"
	// or "2026 — Año completo". The template never formats a date.
	Label string

	// StartStr / EndStr are the window's bounds as YYYY-MM-DD, always
	// month-aligned and always inside [analysis_start_date, current month].
	StartStr string
	EndStr   string

	// Active marks the entry matching the window currently rendered. Exactly
	// one entry is active on a request that came from the control itself.
	Active bool

	// IsMonth distinguishes a month entry from its year heading, so the
	// template can indent the months under their year without parsing the
	// label back apart.
	IsMonth bool
}

// VehicleStatsTiles are the page's seven KPI values, each an already-formatted
// display string. The handler does every sum, ratio and unit; the template
// prints what it is given — the same contract ExternalChargeTiles carries.
//
// A value that cannot be computed is an em dash, never "0": a zero here would
// claim a measurement that was never taken.
type VehicleStatsTiles struct {
	Distance   string // e.g. "1,284 km" — the period's total
	Efficiency string // e.g. "2.9 km/%" — the stored monthly ratios, distance-weighted
	Energy     string // e.g. "243.0 kWh" — external AC + DC + Supercharger
	Sessions   string // e.g. "12" — the three charge counts added
	Cost       string // e.g. "512,300.00 COP" — the three costs added
	CostPerKm  string // e.g. "399.00 COP/km" — total cost over total km

	// RangeFull is the range at a full battery, from Tesla's own reported
	// figure — e.g. "412 km". Over a multi-month period it is the most recent
	// month that has a reading, NOT a sum or an average: it is a battery-health
	// reading, not a quantity that accumulates.
	RangeFull string
}

// VehicleStatsWhy is the page's explanatory half: three blocks, each answering
// one of the headline figures above it.
//
// Deliberately NOT more stat tiles. A tile states a number; these blocks state
// a COMPARISON or a DISTRIBUTION, which is the shape that explains one. Rendering
// them as tiles would flatten the hierarchy the KPI section exists to create.
type VehicleStatsWhy struct {
	// Weekday / Weekend answer the efficiency figure: the same car returns a
	// different km/% depending on how it is driven.
	Weekday VehicleStatsSplit
	Weekend VehicleStatsSplit

	// Sources answer the cost figure — a Supercharger-heavy period costs more
	// per kWh than a home-charged one. Always three entries, in a fixed order
	// (external AC, external DC, Supercharger), even when one has no energy:
	// a missing row would hide the very fact that explains a cheap period.
	Sources []VehicleStatsSource

	// Buckets is the ending-battery distribution, five entries in ascending
	// order. This is where the "Avg. End Battery" KPI went: the stored columns
	// are five bucket COUNTS, never an average, so a distribution is the only
	// honest rendering of them.
	Buckets []VehicleStatsBucket

	// BucketsEmpty is true when every bucket count is zero — no charge in the
	// period carried an ending battery reading. The template then says so
	// instead of drawing five empty bars, which would read as "you always
	// finish at 0%".
	BucketsEmpty bool
}

// VehicleStatsSplit is one side of the weekday/weekend comparison, already
// formatted.
type VehicleStatsSplit struct {
	Label      string // translated, e.g. "Entre semana"
	Distance   string // e.g. "980 km"
	Efficiency string // e.g. "2.9 km/%" — this side's stored monthly ratios, distance-weighted; "—" when none
	Days       string // e.g. "21 días" — how many days stand behind the figures
}

// VehicleStatsSource is one charging source's share of the period, already
// formatted.
type VehicleStatsSource struct {
	Label  string // translated, e.g. "Supercharger"
	Energy string // e.g. "83.0 kWh"
	Cost   string // e.g. "99,600.00 COP"
	// CostPerKWh is what actually explains the cost figure — e.g.
	// "1,200.00 COP/kWh". Em dash when this source added no energy.
	CostPerKWh string
	// SharePct is this source's share of the period's total energy, 0-100,
	// driving the meter's width. An integer because it feeds a meter, not a
	// reader: the precise number is Energy, right beside it.
	SharePct int
	Sessions string // translated + labelled, e.g. "Cargas: 4" — it is this source's share of the Sessions KPI, so it must be readable as such
}

// VehicleStatsBucket is one ending-battery band, already formatted.
type VehicleStatsBucket struct {
	Label string // e.g. "60-80%"
	Count string // e.g. "7"
	// SharePct is this band's share of all charges that reported an ending
	// battery, 0-100, driving the meter's height.
	SharePct int
}
