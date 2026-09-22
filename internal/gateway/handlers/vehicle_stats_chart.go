// vehicle_stats_chart.go builds the Vehicle Stats page's month-by-month chart
// and resolves the previous period the KPI trends compare against.
//
// Both work off the months slice the page already fetched, or a second call to
// the same port — never a new module surface.
package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// buildVehicleStatsMonthChart builds one bar per calendar month in [start, end],
// showing distance driven.
//
// # The axis is CALENDAR-driven, not data-driven
//
// Every month in the window gets exactly one slot, in order, whether or not a
// row exists for it. A month with no stored row renders at zero height with its
// label kept and Present=false. That is the same fixed-axis rule
// buildOdometerChart follows, and here it matters more than usual: this table is
// sparse BY MONTH (the nightly poller only ever wrote the current and previous
// month, and nothing backfilled the rest), so a data-driven axis would quietly
// relabel the gaps away and show a tidy chart of a year that was never recorded.
// The empty slots ARE the information.
//
// Distance is the metric because it is the period's headline figure — the chart
// is the shape of the number above it. Its tooltip carries that month's
// efficiency too, so the two headline tiles are both explained by one hover.
func buildVehicleStatsMonthChart(ctx context.Context, months []analytics.VehicleMonthlyMetrics, start, end time.Time) fragments.VehicleStatsMonthChart {
	anchor := startOfMonth(start)
	numMonths := monthsBetween(anchor, startOfMonth(end)) + 1
	if numMonths <= 1 {
		// One month has no shape to show, and one bar is not a chart.
		return fragments.VehicleStatsMonthChart{}
	}

	byMonth := make(map[int]analytics.VehicleMonthlyMetrics, len(months))
	for _, m := range months {
		idx := monthsBetween(anchor, startOfMonth(m.Period))
		if idx < 0 || idx >= numMonths {
			continue // defensive: the port is window-bounded, so this cannot fire
		}
		byMonth[idx] = m
	}

	maxKm := 0.0
	for _, m := range byMonth {
		if m.AllDistanceKm > maxKm {
			maxKm = m.AllDistanceKm
		}
	}

	bars := make([]fragments.HistoryBar, 0, numMonths)
	for i := 0; i < numMonths; i++ {
		month := anchor.AddDate(0, i, 0)
		label := month.Format("2006-01")
		m, present := byMonth[i]
		if !present {
			bars = append(bars, fragments.HistoryBar{
				Label:   label,
				Tooltip: fmt.Sprintf("%s · %s", label, i18n.T(ctx, i18n.KeyVehicleStatsMonthNoData)),
			})
			continue
		}
		height := 0
		if maxKm > 0 {
			height = int(m.AllDistanceKm / maxKm * 100)
		}
		eff := emDash
		if m.AllKmPerPctCalc > 0 {
			eff = formatKmPerPct(m.AllKmPerPctCalc)
		}
		bars = append(bars, fragments.HistoryBar{
			HeightPct: height,
			Label:     label,
			Present:   true,
			Tooltip:   fmt.Sprintf("%s · %s · %s", label, formatKm(m.AllDistanceKm), eff),
		})
	}

	return fragments.VehicleStatsMonthChart{
		Show: true,
		Chart: fragments.HistoryChart{
			Bars:          bars,
			LabelVertical: labelVerticalFor(numMonths),
		},
	}
}

// vehicleStatsPrevWindow returns the period immediately before [start, end], of
// the same length in whole months, for the KPI trend comparison.
//
// Returns ok=false when that period starts before the account's
// analysis_start_date month: there is no comparable data there, and comparing
// against a partially-tracked period would show a fall that is really just the
// start of tracking.
//
// Month arithmetic only — time.Time.AddDate normalizes a year rollback, so a
// January period correctly resolves to the prior December with no special case.
func vehicleStatsPrevWindow(start, end, minDate time.Time) (prevStart, prevEnd time.Time, ok bool) {
	n := monthsBetween(startOfMonth(start), startOfMonth(end)) + 1
	prevEnd = endOfMonth(startOfMonth(start).AddDate(0, -1, 0))
	prevStart = startOfMonth(prevEnd).AddDate(0, -(n - 1), 0)
	if prevStart.Before(startOfMonth(minDate)) {
		return time.Time{}, time.Time{}, false
	}
	return prevStart, prevEnd, true
}
