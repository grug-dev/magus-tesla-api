// vehicle_stats_period.go contains the Vehicle Stats page's ?start=&end=
// range parser and its period-list builder. Mirrors
// external_charges_range.go's split: the parser validates the window, the
// builder produces the selectable periods.
//
// This page's grain is a whole calendar MONTH or a whole calendar YEAR --
// never a day. That is not a UI preference: the page reads analytics'
// per-month rollup, so a window that starts or ends mid-month cannot be
// answered by the underlying rows. Every window this file produces is
// therefore month-aligned on both ends, and the parser rejects one that is
// not.
package handlers

import (
	"fmt"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// vehicleStatsRangeMaxDays is the hard cap on the window width. 366 days --
// one leap year -- because a whole year is the widest window the period
// control can express, so anything wider reached the endpoint as a
// hand-written URL. Deliberately NOT copied from superchargerRangeMaxDays
// (400): AGENTS.md requires each endpoint to size its own cap from its
// source table, and this one reads vehicle_monthly_metrics, which holds at
// most twelve rows per vehicle-year.
const vehicleStatsRangeMaxDays = 366

// monthKeys maps a calendar month to its catalogue key, indexed by
// time.Month so a caller subscripts instead of branching. Index 0 is unused
// and holds the empty Key so the array lines up with time.January == 1.
var monthKeys = [13]i18n.Key{
	"",
	i18n.KeyMonth01, i18n.KeyMonth02, i18n.KeyMonth03, i18n.KeyMonth04,
	i18n.KeyMonth05, i18n.KeyMonth06, i18n.KeyMonth07, i18n.KeyMonth08,
	i18n.KeyMonth09, i18n.KeyMonth10, i18n.KeyMonth11, i18n.KeyMonth12,
}

// parseVehicleStatsRange validates the ?start=&end= query params for
// GET /ui/vehicle-stats. today is the caller's resolved calendar day
// (browserToday(c)) and minDate is the account's analysis_start_date, both
// threaded through rather than read internally so this stays a pure
// function of its arguments.
//
// Returns the parsed window and ok=true, or the zero values and ok=false
// when the request is malformed. The handler renders HTTP 400 on ok=false.
//
// Validation, in order:
//  1. Both absent -> the CURRENT month. The ticket requires the current
//     year-month to be selectable, and it is the period a person opening the
//     page is asking about.
//  2. Only one present, or either unparseable as YYYY-MM-DD -> reject.
//  3. end before start -> reject.
//  4. Either bound not month-aligned (start not a 1st, end not a month's
//     last day) -> reject. See the file comment: the monthly rollup cannot
//     answer a partial month, so a half-month window would silently return
//     whole months and misreport what was asked.
//  5. end after the current month's end -> reject. This is the ticket's "no
//     future" rule. It compares against the month's END, not today, so the
//     current month stays selectable for its whole duration.
//  6. start before the analysis_start_date's month -> reject. The ticket's
//     lower-bound rule, enforced on the SERVER: the period control never
//     offers an earlier period, so only a hand-written URL reaches this.
//  7. Window wider than vehicleStatsRangeMaxDays -> reject.
func parseVehicleStatsRange(c *gin.Context, today, minDate time.Time) (start, end time.Time, ok bool) {
	rawStart := c.Query("start")
	rawEnd := c.Query("end")
	if rawStart == "" && rawEnd == "" {
		return startOfMonth(today), endOfMonth(today), true
	}

	s, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	e, err := time.Parse("2006-01-02", rawEnd)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if e.Before(s) {
		return time.Time{}, time.Time{}, false
	}
	if !s.Equal(startOfMonth(s)) || !e.Equal(endOfMonth(e)) {
		return time.Time{}, time.Time{}, false
	}
	if e.After(endOfMonth(today)) {
		return time.Time{}, time.Time{}, false
	}
	if s.Before(startOfMonth(minDate)) {
		return time.Time{}, time.Time{}, false
	}
	if e.Sub(s).Hours()/24 > float64(vehicleStatsRangeMaxDays) {
		return time.Time{}, time.Time{}, false
	}
	return s, e, true
}

// buildVehicleStatsPeriods builds every period the control offers, newest
// first, for the span [minDate, today]. Each year contributes a whole-year
// entry followed by that year's selectable months, so the list reads
// "2026 / septiembre / agosto / ... / 2025 / diciembre / ...".
//
// Both ends of every window are CLAMPED to the allowed span: a whole-year
// entry for the current year ends at the current month, and one for the
// account's first year starts at the analysis_start_date's month. Clamping
// here, at the only place windows are constructed, is what makes the
// parser's rules 5 and 6 unreachable through the UI -- the control cannot
// offer an out-of-range period in the first place.
//
// Active is computed by exact match against the passed window, mirroring
// buildExternalChargesPresets. Only one entry can match, because no two
// entries produce the same window.
func buildVehicleStatsPeriods(c *gin.Context, today, minDate, start, end time.Time) []fragments.VehicleStatsPeriod {
	ctx := c.Request.Context()
	minMonth := startOfMonth(minDate)
	maxMonth := startOfMonth(today)

	// A minDate in the future (an account created today reads its own
	// signup day, and a browser clock can sit behind it) would make the
	// loops below produce nothing. Collapsing the span to the current month
	// keeps the control non-empty and still offers no future period.
	if minMonth.After(maxMonth) {
		minMonth = maxMonth
	}

	periods := make([]fragments.VehicleStatsPeriod, 0, 13)
	for year := maxMonth.Year(); year >= minMonth.Year(); year-- {
		firstMonth := time.January
		if year == minMonth.Year() {
			firstMonth = minMonth.Month()
		}
		lastMonth := time.December
		if year == maxMonth.Year() {
			lastMonth = maxMonth.Month()
		}

		yearStart := monthStart(year, firstMonth)
		yearEnd := endOfMonth(monthStart(year, lastMonth))
		periods = append(periods, fragments.VehicleStatsPeriod{
			Label:    fmt.Sprintf("%d — %s", year, i18n.T(ctx, i18n.KeyVehicleStatsWholeYear)),
			StartStr: yearStart.Format("2006-01-02"),
			EndStr:   yearEnd.Format("2006-01-02"),
			Active:   start.Equal(yearStart) && end.Equal(yearEnd),
		})

		for m := lastMonth; m >= firstMonth; m-- {
			ms := monthStart(year, m)
			me := endOfMonth(ms)
			periods = append(periods, fragments.VehicleStatsPeriod{
				Label:    fmt.Sprintf("%s %d", i18n.T(ctx, monthKeys[m]), year),
				StartStr: ms.Format("2006-01-02"),
				EndStr:   me.Format("2006-01-02"),
				Active:   start.Equal(ms) && end.Equal(me),
				IsMonth:  true,
			})
		}
	}
	return periods
}

// monthStart returns midnight UTC on the 1st of the given year and month.
// The UTC-midnight representation matches startOfMonth/endOfMonth, which
// every window in this file is compared against.
func monthStart(year int, m time.Month) time.Time {
	return time.Date(year, m, 1, 0, 0, 0, 0, time.UTC) // tz:allow: builds a bare calendar month, not a "now" — the caller already resolved today through browserToday
}

// activePeriodLabel returns the label of the period currently selected, for
// the control's own button. Falls back to the raw window when no entry
// matches -- only reachable from a hand-written URL that passed every
// parser rule but lines up with no offered period.
func activePeriodLabel(periods []fragments.VehicleStatsPeriod, start, end time.Time) string {
	for _, p := range periods {
		if p.Active {
			return p.Label
		}
	}
	return fmt.Sprintf("%s — %s", start.Format("2006-01"), end.Format("2006-01"))
}
