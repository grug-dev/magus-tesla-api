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

	// HasPrev is true when a previous period with stored data was found, so the
	// summary card can say what the arrows are measured against. The arrows
	// themselves live on Tiles; this only drives the card's own description.
	HasPrev bool

	// MonthChart is the period's month-by-month shape — one bar per calendar
	// month of distance driven. Rendered only when the period spans more than
	// one month: a single month has no shape to show, and one bar is not a
	// chart.
	//
	// It exists because a year period otherwise collapses twelve months into
	// one number and loses everything about how they differed. The months are
	// already fetched for the tiles, so this costs no extra read.
	MonthChart VehicleStatsMonthChart

	// Gaps are the days in the period whose battery use the stored charge
	// records cannot account for — a charge record is missing or incomplete.
	// Empty means the period is fully accounted for and the whole section is
	// not rendered.
	//
	// It sits on the view, not inside Why, because it is not an explanation of
	// a figure — it is a warning ABOUT the figures: a missing charge
	// under-counts consumption, which pushes energy and cost down and
	// efficiency up.
	Gaps []VehicleStatsGap

	// GapsMore is how many further gap days exist beyond the ones in Gaps,
	// 0 when none were dropped. A year can flag more days than a reader will
	// scan, so the list is capped and the remainder is counted instead of
	// silently disappearing.
	GapsMore int

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
	//
	// It deliberately has NO trend pair below: one month's reading against
	// another's is mostly weather and driving style, and an arrow would invite
	// a reader to see battery degradation in noise.
	RangeFull string

	// *Trend / *Delta are the period-over-period comparison against the
	// immediately preceding period of the same length. Trend is a
	// ui.StatTileProps.Trend value; Delta is the signed percentage, e.g. "+12%".
	//
	// Both are EMPTY when there is nothing to compare — no previous period
	// stored, a previous value of zero, or a change that rounds to 0%. Empty
	// renders no arrow and no sub-label, so a first-ever month simply shows its
	// figures with no comparison rather than a fabricated one.
	//
	// The polarity differs per metric and is decided in the handler: distance,
	// energy and session count are neutral (driving more is neither good nor
	// bad), efficiency is better when it rises, and cost and cost per km are
	// worse when they rise.
	DistanceTrend, DistanceDelta     string
	EfficiencyTrend, EfficiencyDelta string
	EnergyTrend, EnergyDelta         string
	SessionsTrend, SessionsDelta     string
	CostTrend, CostDelta             string
	CostPerKmTrend, CostPerKmDelta   string
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

// VehicleStatsGap is one day the period could not fully account for, already
// formatted and already resolved to the page that fixes it.
//
// The two kinds need different words because they need different work, and the
// analytics module already tells them apart (its MissingChargingType): a MANUAL
// gap means the charge happened somewhere Tesla does not report and a record
// must be CREATED; a SUPERCHARGER gap means the session is already stored and
// only its two battery percentages are missing, so a record must be COMPLETED.
//
// The date arrives SPLIT into day and month rather than as one string. The cell
// makes the day number its visual anchor — a reader scans these to find "which
// days do I owe a record for" — and a pre-joined label could not be typeset in
// two sizes. The year is deliberately absent: the selected period already
// frames it, so repeating it in every cell is noise.
type VehicleStatsGap struct {
	// DayLabel is the day of the month, e.g. "10". Numeric, so it needs no
	// translation and stays the same width in both languages.
	DayLabel string

	// MonthLabel is the translated month name, e.g. "septiembre". Lowercase in
	// Spanish by orthography.
	MonthLabel string

	// KindLabel is the short badge text naming what is missing, e.g. "Carga
	// manual". Short on purpose: a ui.Badge cannot wrap, it only widens, so a
	// sentence here would stretch the cell.
	KindLabel string

	// ActionLabel is the short call to action, e.g. "Agregar registro". The
	// full explanation is the section's own description, said once — repeating
	// a sentence in every cell is what made the first version read as a log.
	ActionLabel string

	// ActionHref points at the page that fixes this day, with that single day
	// already applied as the ?start=&end= filter — so the reader lands on the
	// day in question, not on a page they must then filter themselves. Both
	// pages accept a single-day window. The whole cell is this link.
	ActionHref string
}

// VehicleStatsMonthChart wraps the month-by-month bar chart plus the flag that
// says whether to render it at all.
//
// Show is false for a single-month period. It is a separate field rather than a
// len(Bars) check because a year with one stored month legitimately produces one
// bar, and that still deserves the chart's axis — the emptiness is the point.
type VehicleStatsMonthChart struct {
	Show  bool
	Chart HistoryChart
}
