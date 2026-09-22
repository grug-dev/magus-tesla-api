// vehicle_stats_tiles.go rolls the selected period's monthly rows up into both
// halves of the page: the KPI tiles that state the answer, and the "why" blocks
// that explain them. Mirrors external_charges_tiles.go's shape — pure functions
// over the exact slice the read port returned, producing already-formatted
// display strings.
//
// Both builders take the SAME months slice, so the two halves can never
// describe different data.
package handlers

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// emDash is what a tile shows when its value cannot be computed — no rows, or
// a divisor of zero. Never a "0", which would read as a measured zero.
const emDash = "—"

// effAccumulator combines the per-month efficiency ratios this module already
// stores into one figure for the whole period.
//
// # Why it does not divide two summed columns
//
// It is tempting to compute total km ÷ total consumed %, but those two columns
// do NOT line up: all_distance_km and all_consumed_pct sum over EVERY
// computable day, while all_km_per_pct_calc is the ratio over only the days
// whose consumed_pct is POSITIVE. A day that charged more than it drove has a
// negative consumed_pct, so dividing the two sums puts that day's kilometres in
// the numerator AND lets its negative percentage shrink the denominator. Both
// errors push the ratio up, and on real data the result came out about DOUBLE
// the stored figure (715.4 / 123.00 = 5.8 against a stored 2.94). A vehicle
// whose month is net-charging has a negative all_consumed_pct outright, which
// the naive formula cannot render at all.
//
// So the stored ratio is the only correct per-month figure, and this type
// combines them instead of recomputing from the sums.
//
// # The weight, and its one limitation
//
// Ratios are weighted by each month's distance. For a SINGLE month that is
// exact: the weight cancels and the result is the stored ratio itself, which is
// the whole reason this shape was chosen over a special case.
//
// Across several months it is an APPROXIMATION. The exact combined ratio needs
// each month's restricted sums — distance and consumed % over positive days
// only — and this table stores neither, so they cannot be recovered. Distance
// is the closest available proxy for the missing denominator's weight. Making a
// year exact means storing those two sums; that is a schema change, deliberately
// not taken here (MAG-87).
type effAccumulator struct {
	weightedSum float64 // Σ (ratio × distance)
	weight      float64 // Σ distance
}

// add folds one month in. A month with no positive-consumption day stores a
// ratio of 0 — this table's documented "no data" value — so it is SKIPPED
// entirely rather than averaged in as a zero, which would drag the period's
// figure toward nothing. A month with no distance carries no weight and is
// skipped for the same reason.
func (e *effAccumulator) add(ratio, distanceKm float64) {
	if ratio <= 0 || distanceKm <= 0 {
		return
	}
	e.weightedSum += ratio * distanceKm
	e.weight += distanceKm
}

// label renders the combined figure, or an em dash when no month contributed.
func (e effAccumulator) label() string {
	if e.weight <= 0 {
		return emDash
	}
	return formatKmPerPct(e.weightedSum / e.weight)
}

// buildVehicleStatsTiles computes the seven KPI values over months, the exact
// slice MonthlyMetricsBetween returned, so the tiles and any later chart are
// provably the same data.
//
// # Efficiency combines the STORED monthly ratios — it does not divide two sums
//
// See effAccumulator for why: the two sum columns and the stored ratio are taken
// over different day sets, so dividing the sums roughly doubles the answer.
//
// Cost per km IS a plain ratio of sums, and correctly so: all_distance_km over
// every computable day is exactly the distance the period's money bought. Its
// denominator has no positive-only restriction to respect.
//
// # Range at full battery is the LATEST month, not a roll-up
//
// TeslaRange100PctKmCalc is a battery-health reading, not a quantity that
// accumulates: adding twelve months of it means nothing, and averaging them
// would be a mean of ratios whose weights (the days behind each figure) this
// table does not expose. So a multi-month period reports the most recent
// month that has a reading. A stored 0 means "no day in that month had one"
// (the port's own contract), so zeros are skipped rather than treated as a
// measured zero range.
func buildVehicleStatsTiles(months []analytics.VehicleMonthlyMetrics) fragments.VehicleStatsTiles {
	var distanceKm, energyKWh, cost, latestRangeKm float64
	var sessions int
	var eff effAccumulator
	currency := "COP"

	for _, m := range months {
		distanceKm += m.AllDistanceKm
		eff.add(m.AllKmPerPctCalc, m.AllDistanceKm)
		energyKWh += m.ExtACEnergyKWh + m.ExtDCEnergyKWh + m.SCEnergyKWh
		cost += m.ExtACCost + m.ExtDCCost + m.SCCost
		sessions += m.ExtACEntryCount + m.ExtDCEntryCount + m.SCSessionCount

		// months arrives oldest first (the query's ORDER BY period), so the
		// last non-zero reading seen is the most recent one.
		if m.TeslaRange100PctKmCalc > 0 {
			latestRangeKm = m.TeslaRange100PctKmCalc
		}
		if m.Currency != "" {
			currency = m.Currency
		}
	}

	t := fragments.VehicleStatsTiles{
		Distance:   formatKm(distanceKm),
		Efficiency: eff.label(),
		Energy:     fmt.Sprintf("%.1f kWh", energyKWh),
		Sessions:   strconv.Itoa(sessions),
		Cost:       formatMoney(cost, currency),
		CostPerKm:  emDash,
		RangeFull:  emDash,
	}
	if distanceKm > 0 {
		t.CostPerKm = formatMoney(cost/distanceKm, currency) + "/km"
	}
	if latestRangeKm > 0 {
		t.RangeFull = formatKm(latestRangeKm)
	}
	return t
}

// buildVehicleStatsWhy computes the page's explanatory half over the SAME
// months slice the tiles were built from, so the two halves can never describe
// different data.
//
// The weekday and weekend efficiencies combine each side's STORED monthly ratio
// through effAccumulator, for exactly the reason the headline figure does — the
// weekday sum columns carry the same positive-only mismatch as the overall ones.
//
// Cost per kWh IS a plain ratio of sums: a source's total cost over its total
// energy, neither of which has a restricted day set.
func buildVehicleStatsWhy(ctx context.Context, months []analytics.VehicleMonthlyMetrics) fragments.VehicleStatsWhy {
	var wdKm, weKm float64
	var wdEff, weEff effAccumulator
	var wdDays, weDays int
	var acKWh, acCost, dcKWh, dcCost, scKWh, scCost float64
	var acN, dcN, scN int
	var buckets [5]int
	currency := "COP"

	for _, m := range months {
		wdKm += m.WeekdayDistanceKm
		wdEff.add(m.WeekdayKmPerPctCalc, m.WeekdayDistanceKm)
		wdDays += m.WeekdayDayCount
		weKm += m.WeekendDistanceKm
		weEff.add(m.WeekendKmPerPctCalc, m.WeekendDistanceKm)
		weDays += m.WeekendDayCount

		acKWh += m.ExtACEnergyKWh
		acCost += m.ExtACCost
		acN += m.ExtACEntryCount
		dcKWh += m.ExtDCEnergyKWh
		dcCost += m.ExtDCCost
		dcN += m.ExtDCEntryCount
		scKWh += m.SCEnergyKWh
		scCost += m.SCCost
		scN += m.SCSessionCount

		// The three distributions are summed into one: the page asks how full
		// the car is left, not which plug did it. Which plug is the block
		// above.
		for _, d := range [3]analytics.EndingBatteryDist{m.ExtACEndingBatteryDist, m.ExtDCEndingBatteryDist, m.SCEndingBatteryDist} {
			buckets[0] += d.Bucket0To20
			buckets[1] += d.Bucket20To40
			buckets[2] += d.Bucket40To60
			buckets[3] += d.Bucket60To80
			buckets[4] += d.Bucket80To100
		}
		if m.Currency != "" {
			currency = m.Currency
		}
	}

	totalKWh := acKWh + dcKWh + scKWh
	return fragments.VehicleStatsWhy{
		Weekday: buildSplit(ctx, i18n.KeyVehicleStatsWeekday, wdKm, wdEff, wdDays),
		Weekend: buildSplit(ctx, i18n.KeyVehicleStatsWeekend, weKm, weEff, weDays),
		Sources: []fragments.VehicleStatsSource{
			buildSource(ctx, i18n.KeyVehicleStatsSourceAC, acKWh, acCost, acN, totalKWh, currency),
			buildSource(ctx, i18n.KeyVehicleStatsSourceDC, dcKWh, dcCost, dcN, totalKWh, currency),
			buildSource(ctx, i18n.KeyVehicleStatsSourceSC, scKWh, scCost, scN, totalKWh, currency),
		},
		Buckets:      buildBuckets(buckets),
		BucketsEmpty: buckets[0]+buckets[1]+buckets[2]+buckets[3]+buckets[4] == 0,
	}
}

// buildSplit formats one side of the weekday/weekend comparison. dayCount is
// rendered because it is what makes the comparison fair: "2.6 km/%" over three
// weekend days says much less than the same figure over twenty.
func buildSplit(ctx context.Context, labelKey i18n.Key, km float64, eff effAccumulator, dayCount int) fragments.VehicleStatsSplit {
	return fragments.VehicleStatsSplit{
		Label:      i18n.T(ctx, labelKey),
		Distance:   formatKm(km),
		Efficiency: eff.label(),
		Days:       fmt.Sprintf(i18n.T(ctx, i18n.KeyVehicleStatsDayCount), dayCount),
	}
}

// buildSource formats one charging source's row. CostPerKWh is the figure that
// actually explains the period's cost, so it is computed here rather than left
// for a reader to divide two numbers in their head.
//
// Sessions is LABELLED here, not left as a bare number: the row already shows a
// kWh figure and a money figure, both of which carry their own unit, so an
// unlabelled third integer beside them reads as ambiguous. It is also the
// breakdown of the "Charging sessions" KPI above, and a reader must be able to
// see that the three rows add up to it.
func buildSource(ctx context.Context, labelKey i18n.Key, kWh, cost float64, sessions int, totalKWh float64, currency string) fragments.VehicleStatsSource {
	perKWh := emDash
	if kWh > 0 {
		perKWh = formatMoney(cost/kWh, currency) + "/kWh"
	}
	share := 0
	if totalKWh > 0 {
		share = int(math.Round(kWh / totalKWh * 100))
	}
	return fragments.VehicleStatsSource{
		Label:      i18n.T(ctx, labelKey),
		Energy:     fmt.Sprintf("%.1f kWh", kWh),
		Cost:       formatMoney(cost, currency),
		CostPerKWh: perKWh,
		SharePct:   share,
		Sessions:   fmt.Sprintf(i18n.T(ctx, i18n.KeyVehicleStatsChargeCount), sessions),
	}
}

// bucketLabels is the closed, language-neutral vocabulary of the five ending-
// battery bands. Percent ranges need no translation, so they carry no catalogue
// key — the block's heading above them is what carries the meaning.
var bucketLabels = [5]string{"0-20%", "20-40%", "40-60%", "60-80%", "80-100%"}

// buildBuckets formats the ending-battery distribution. Share is of the charges
// that REPORTED an ending battery, not of every charge in the period: a charge
// with no reading is absent from every bucket, so including it in the divisor
// would shrink all five bars against a total they do not belong to.
func buildBuckets(counts [5]int) []fragments.VehicleStatsBucket {
	total := 0
	for _, c := range counts {
		total += c
	}
	out := make([]fragments.VehicleStatsBucket, 0, len(counts))
	for i, c := range counts {
		share := 0
		if total > 0 {
			share = int(math.Round(float64(c) / float64(total) * 100))
		}
		out = append(out, fragments.VehicleStatsBucket{
			Label:    bucketLabels[i],
			Count:    strconv.Itoa(c),
			SharePct: share,
		})
	}
	return out
}
