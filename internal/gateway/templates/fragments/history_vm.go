package fragments

import "time"

// HistoryBar represents one bar in a history chart. All values are pre-computed
// by the handler — the template does NO arithmetic, unit conversion, date
// formatting, or domain-type method calls (gateway spec invariant /
// ai/htmx-conventions.md §"No business logic in templates").
type HistoryBar struct {
	// HeightPct is the bar height as a percentage (0–100) of the chart canvas.
	// For the odometer chart: (delta km / max delta km) * 100; clamped ≥ 0.
	// For the battery chart: battery level % (0–100 directly).
	// A missing-day bar carries HeightPct=0.
	HeightPct int
	// Tooltip is the pre-rendered hover string shown in the bar's <title> element.
	// The date token is the bar's calendar day (MM-DD) — derived from the fixed
	// [start..end] axis in buildHistoryView, identical on both charts so the
	// MAG-7 odometer/battery offset is gone. Odometer: "<MM-DD> · <km> km driven · odometer <cumulative> km";
	// Battery: "<MM-DD> · <level>% · <range> km range"; a missing-day bar:
	// "<MM-DD> · no snapshot".
	Tooltip string
	// Label is the pre-formatted MM-DD date label rendered under the bar. On the
	// fixed [start..end] axis, every calendar day in the inclusive window gets
	// exactly one slot, so both charts always have identical Label slices by
	// construction. Computed by the handler; the template emits it verbatim.
	Label string
	// Present is true when a stored snapshot backs this bar; false for a
	// missing-day bar (no snapshot for that calendar day). The fixed axis keeps
	// the missing-day slot labeled with its own date so the axis is calendar-
	// driven, not snapshot-driven (RM8 design D3, MAG-7 fix). A Present=false
	// bar renders at zero height with its MM-DD label retained.
	Present bool
	// MarkerFlagged is true when this day was flagged by internal/battery's
	// gap detection (D10) -- the template renders a warning-colored marker
	// chip. Always false for every existing odometer/battery bar (they never
	// set this field).
	MarkerFlagged bool
	// MarkerSpan is true when this day's underlying DayConsumption spans
	// more than one calendar day (D20) -- the template renders an
	// info-colored marker chip, DISTINCT from the warning chip above.
	// Independent of MarkerFlagged: BOTH can be true on the same bar (D21),
	// in which case BOTH chips render and Tooltip states both facts
	// (design.md D-G4/D-G7). Always false for every existing odometer/
	// battery bar.
	MarkerSpan bool
}

// YAxisTick is one labelled horizontal reference line on a chart's y-axis.
// The handler pre-computes both the vertical position and the formatted value
// string so the template does no arithmetic (same invariant as HistoryBar).
type YAxisTick struct {
	// Pct is the tick's y position in the SVG viewBox units (0=top, 100=bottom).
	// A tick representing the chart's maximum value carries Pct=0 (top edge);
	// the zero-value tick carries Pct=100 (bottom edge). The template emits a
	// dashed <line> at this y and aligns the HTML label to it.
	Pct int
	// Label is the pre-formatted value+unit string (e.g. "12 km", "75%") shown
	// in the left gutter. Built by the handler from the chart's max value; the
	// template renders it verbatim.
	Label string
}

// HistoryChart holds the bars and empty-state flag for one chart panel.
// When Empty is true the handler computed 0 bars (too few snapshots); the
// template renders dashHistoryEmpty() instead of the SVG.
type HistoryChart struct {
	Bars  []HistoryBar
	Empty bool
	// LabelVertical selects the per-bar label orientation for this chart: false
	// renders horizontal, centered labels (used for the 6-bar default window,
	// where bars are wide); true renders labels rotated -90° so they fit narrow
	// bars (used for 14- and 30-bar windows). Set once by the handler from the
	// fixed window's bar count (labelVerticalFor(numBars) — true when
	// numBars >= 14); the template reads only this flag, it never compares the
	// window size or computes rotation itself (RM8 design D3).
	LabelVertical bool
	// YAxisTicks carries the optional y-axis reference lines + labels. When
	// nil/empty (the default for charts that opt out, e.g. the supercharger
	// month chart) the template renders exactly the legacy layout — no left
	// gutter, no gridlines. When non-empty, the template wraps the bar grid in
	// a flex row with a left label gutter and overlays dashed <line>s on the
	// SVG at each tick.Pct. Built once by the handler from the chart's scale
	// (relative max for odometer/consumed, absolute 0-100 for battery); the
	// template reads Pct/Label verbatim and does no value math.
	YAxisTicks []YAxisTick
}

// RangePreset is one button in the 6/14/30-day preset selector. The handler
// computes the absolute (Start, End) calendar-day window at render time
// (today-N .. today) and the template emits the absolute ?start=&end= href —
// no ?days= anywhere (RM8 design D4, Decision #3). StartStr/EndStr are
// pre-formatted YYYY-MM-DD strings so the template does no time formatting.
type RangePreset struct {
	// Label is the button copy ("6 days", "14 days", "30 days") — unchanged UX.
	Label string
	// StartStr is the pre-formatted YYYY-MM-DD start date (today-N, UTC midnight)
	// for the preset's absolute href.
	StartStr string
	// EndStr is the pre-formatted YYYY-MM-DD end date (today, UTC midnight) for
	// the preset's absolute href.
	EndStr string
	// Active is true when (Start, End) matches the window the handler is
	// rendering — the template marks the active button with btn-primary. A
	// custom (non-preset) window marks no preset active.
	Active bool
}

// HistoryView is the complete view model for the #dashboard-history region —
// the preset selector plus both chart panels over the fixed [start..end] axis.
// ALL numeric values, heights, tooltip strings, label strings, and preset
// absolute-date hrefs are pre-computed by the handler; the template renders
// them verbatim (RM8 design D3/D4 — logic-free template invariant).
type HistoryView struct {
	// Start is the requested window's start (UTC midnight, inclusive). Carried
	// so the template can render the active window's dates if needed; the bar
	// labels are the per-day MM-DD strings on the fixed axis.
	Start time.Time
	// End is the requested window's end (UTC midnight, inclusive).
	End time.Time
	// Presets is the ordered list of 6/14/30-day RangePreset entries the
	// selector renders. Each carries a pre-formatted absolute ?start=&end= href
	// and an Active flag.
	Presets []RangePreset
	// Odometer contains the km-driven-per-day bars over the fixed
	// [start..end] axis — exactly numDays bars, one per calendar day, with
	// missing-day bars Present=false at zero height. The pre-window start-1
	// snapshot seeds the first delta's basis and is NOT displayed as a bar.
	Odometer HistoryChart
	// Battery contains the battery-level-% bars over the same fixed
	// [start..end] axis — exactly numDays bars, so its Label slice is identical
	// to Odometer's by construction (the MAG-7 fix).
	Battery HistoryChart
	// Consumed contains the battery-consumed-%/day bars over the SAME fixed
	// [start..end] axis as Odometer/Battery, bucketed on
	// battery.DayConsumption.Date DIRECTLY (never effectiveDayUTC — D18/D18a,
	// design.md D-G2). Scaled RELATIVE to the window's max displayed value
	// (D19), not absolute 0-100 like Battery. HeightPct is math.Max(0,
	// ConsumedPct) scaled against that max for every bar (design.md D-G1) —
	// a flagged day (MarkerFlagged) always lands at 0 because Flagged implies
	// ConsumedPct <= 0 (tier 3 D5); a multi-day-span day (MarkerSpan) shows
	// its real value. A bar can carry BOTH markers (D21, design.md D-G4).
	Consumed HistoryChart
}
