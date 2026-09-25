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
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// monthKey identifies a calendar month independent of day-of-month, time
// zone, or which module's DB mapping produced the time.Time — two different
// modules' "first day of the month" values are not guaranteed to compare
// equal with time.Time.Equal, even when they name the same month.
type monthKey int

func monthKeyOf(t time.Time) monthKey {
	return monthKey(t.Year()*100 + int(t.Month()))
}

// gasolineAccumulator sums an eligible month's distance and its
// gasoline-equivalent gallons, so the period total is SUM(distance) /
// SUM(gallons) — a plain ratio of sums, never an average of monthly ratios.
// Unlike effAccumulator, every input it needs (distance, cost, price)
// already lines up per month, so no distance-weighting step is needed.
type gasolineAccumulator struct {
	distanceKm float64
	gallons    float64
}

// add folds one month in, ONLY when it is eligible: cost > 0 (a month with
// no charging cost recorded would add free distance and inflate the figure)
// AND a price was resolved for it (no default price is ever assumed). An
// ineligible month's distance is also excluded, not only its cost.
func (g *gasolineAccumulator) add(distanceKm, cost, price float64) {
	if cost <= 0 || price <= 0 {
		return
	}
	g.distanceKm += distanceKm
	g.gallons += cost / price
}

// value returns the period's km-per-gallon, or 0 when no month was
// eligible — the caller's signal to hide the tile.
func (g gasolineAccumulator) value() float64 {
	if g.gallons <= 0 {
		return 0
	}
	return g.distanceKm / g.gallons
}

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

// value returns the combined ratio, or 0 when no month contributed. 0 is the
// "not comparable" signal trendOf already skips on, so it needs no second flag.
func (e effAccumulator) value() float64 {
	if e.weight <= 0 {
		return 0
	}
	return e.weightedSum / e.weight
}

// label renders the combined figure, or an em dash when no month contributed.
func (e effAccumulator) label() string {
	if e.weight <= 0 {
		return emDash
	}
	return formatKmPerPct(e.value())
}

// vehicleStatsTotals is the period's raw, unformatted roll-up. It exists so the
// SAME numbers can be produced for two windows — the selected period and the one
// before it — and compared, which formatted display strings cannot be.
type vehicleStatsTotals struct {
	distanceKm    float64
	eff           effAccumulator
	energyKWh     float64
	cost          float64
	sessions      int
	latestRangeKm float64
	currency      string
	gasoline      gasolineAccumulator
}

// sumVehicleStatsMonths rolls months up into the raw totals.
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
// priceByMonth resolves the gasoline cost-parity tile's per-month price,
// keyed by monthKeyOf. A nil map (the previous-period call site, which needs
// no price read) makes every lookup miss, so the tile's accumulator simply
// stays empty — Go's map read on nil is a safe zero-value/false, no extra
// guard needed.
func sumVehicleStatsMonths(months []analytics.VehicleMonthlyMetrics, priceByMonth map[monthKey]float64) vehicleStatsTotals {
	t := vehicleStatsTotals{currency: "COP"}
	for _, m := range months {
		t.distanceKm += m.AllDistanceKm
		t.eff.add(m.AllKmPerPctCalc, m.AllDistanceKm)
		t.energyKWh += m.ExtACEnergyKWh + m.ExtDCEnergyKWh + m.SCEnergyKWh
		cost := m.ExtACCost + m.ExtDCCost + m.SCCost
		t.cost += cost
		t.sessions += m.ExtACEntryCount + m.ExtDCEntryCount + m.SCSessionCount
		if price, ok := priceByMonth[monthKeyOf(m.Period)]; ok {
			t.gasoline.add(m.AllDistanceKm, cost, price)
		}

		// months arrives oldest first (the query's ORDER BY period), so the
		// last non-zero reading seen is the most recent one.
		if m.TeslaRange100PctKmCalc > 0 {
			t.latestRangeKm = m.TeslaRange100PctKmCalc
		}
		if m.Currency != "" {
			t.currency = m.Currency
		}
	}
	return t
}

// buildVehicleStatsTiles formats the seven KPI values, and when prev is non-nil
// also sets each tile's period-over-period trend.
//
// # Range at full battery is the LATEST month, and carries NO trend
//
// TeslaRange100PctKmCalc is a battery-health reading, not a quantity that
// accumulates: adding twelve months of it means nothing, and averaging them would
// be a mean of ratios whose weights (the days behind each figure) this table does
// not expose. So a multi-month period reports the most recent month that has a
// reading. A stored 0 means "no day in that month had one" (the port's own
// contract), so zeros are skipped rather than treated as a measured zero.
//
// It gets no arrow for the same reason: one month's reading against another's is
// mostly weather and driving style, so an arrow would invite a reader to see
// battery degradation in noise.
func buildVehicleStatsTiles(t vehicleStatsTotals, prev *vehicleStatsTotals) fragments.VehicleStatsTiles {
	tiles := fragments.VehicleStatsTiles{
		Distance:   formatKm(t.distanceKm),
		Efficiency: t.eff.label(),
		Energy:     fmt.Sprintf("%.1f kWh", t.energyKWh),
		Sessions:   strconv.Itoa(t.sessions),
		Cost:       formatMoney(t.cost, t.currency),
		CostPerKm:  emDash,
		RangeFull:  emDash,
	}
	if t.distanceKm > 0 {
		tiles.CostPerKm = formatMoney(t.cost/t.distanceKm, t.currency) + "/km"
	}
	if t.latestRangeKm > 0 {
		tiles.RangeFull = formatKm(t.latestRangeKm)
	}
	if v := t.gasoline.value(); v > 0 {
		tiles.KmPerGallon = formatKmPerGallon(v)
	}
	if prev == nil {
		return tiles
	}

	// Polarity per metric, not one rule for all. Distance, energy and session
	// count are NEUTRAL: driving more is neither good nor bad, and a reader who
	// drove more on purpose should not be shown a red arrow for it. Efficiency
	// is better when it rises. Cost and cost per km are the mirror — worse when
	// they rise — which is what "up-error"/"down-success" exist for.
	tiles.DistanceTrend, tiles.DistanceDelta = trendOf(t.distanceKm, prev.distanceKm, trendNeutral)
	tiles.EfficiencyTrend, tiles.EfficiencyDelta = trendOf(t.eff.value(), prev.eff.value(), trendHigherIsBetter)
	tiles.EnergyTrend, tiles.EnergyDelta = trendOf(t.energyKWh, prev.energyKWh, trendNeutral)
	tiles.SessionsTrend, tiles.SessionsDelta = trendOf(float64(t.sessions), float64(prev.sessions), trendNeutral)
	tiles.CostTrend, tiles.CostDelta = trendOf(t.cost, prev.cost, trendLowerIsBetter)
	tiles.CostPerKmTrend, tiles.CostPerKmDelta = trendOf(costPerKm(t), costPerKm(*prev), trendLowerIsBetter)
	return tiles
}

// costPerKm returns the period's cost per kilometre, or 0 when it has no
// distance — 0 makes trendOf skip the comparison, which is what "not comparable"
// should look like.
func costPerKm(t vehicleStatsTotals) float64 {
	if t.distanceKm <= 0 {
		return 0
	}
	return t.cost / t.distanceKm
}

// trend polarity vocabulary — which direction counts as an improvement.
type trendPolarity int

const (
	trendNeutral        trendPolarity = iota // a change is neither good nor bad
	trendHigherIsBetter                      // efficiency
	trendLowerIsBetter                       // cost
)

// trendOf compares now against before and returns the ui.StatTile trend value
// plus a signed percentage label.
//
// Returns no trend at all when either side is zero: a period with no previous
// data, or a previous period of zero, has no meaningful percentage — "+100% from
// nothing" is not information. It also returns nothing when the change rounds to
// 0%, so an unchanged figure carries no arrow rather than a misleading flat one.
func trendOf(now, before float64, polarity trendPolarity) (trend, delta string) {
	if before <= 0 || now <= 0 {
		return "", ""
	}
	pct := int(math.Round((now - before) / before * 100))
	if pct == 0 {
		return "", ""
	}

	up := pct > 0
	switch polarity {
	case trendHigherIsBetter:
		trend = map[bool]string{true: "up", false: "down"}[up]
	case trendLowerIsBetter:
		trend = map[bool]string{true: "up-error", false: "down-success"}[up]
	default:
		trend = map[bool]string{true: "up-neutral", false: "down-neutral"}[up]
	}
	return trend, fmt.Sprintf("%+d%%", pct)
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
