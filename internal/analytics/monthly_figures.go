package analytics

import "time"

// monthDay is one vehicle_metrics day's contribution to a month's derived
// figures -- decoupled from analyticsdb's generated row type so this
// derivation is testable with no database. Both pointers are nil together
// on a day with no computable predecessor (the same guarantee
// vehicle_metrics.consumed_pct's own column comment documents), and this
// code relies on that: it never checks one without the other.
type monthDay struct {
	Date        time.Time
	DistanceKm  *float64
	ConsumedPct *float64
}

// bucketAccumulator sums one day-subset's distance and consumption, and
// derives the efficiency ratio only over the days whose ConsumedPct is
// positive -- averaging the daily ratios instead would let one very short,
// high-percentage day dominate the month. A day with a computable but
// zero-or-negative ConsumedPct still counts toward dayCount (it was a
// real, tracked day) but contributes nothing to the ratio, mirroring
// vehicle_metrics.km_per_pct_calc's own divisor guard.
type bucketAccumulator struct {
	distanceKm     float64
	consumedPct    float64
	dayCount       int
	effDistanceKm  float64
	effConsumedPct float64
}

func (b *bucketAccumulator) add(distanceKm, consumedPct float64) {
	b.distanceKm += distanceKm
	b.consumedPct += consumedPct
	b.dayCount++

	if consumedPct > 0 {
		b.effDistanceKm += distanceKm
		b.effConsumedPct += consumedPct
	}
}

// kmPerPct returns 0 when no day in this bucket had a positive ConsumedPct
// -- there is nothing to divide by, and 0 is this table's documented "no
// data" value for every numeric column.
func (b bucketAccumulator) kmPerPct() float64 {
	if b.effConsumedPct <= 0 {
		return 0
	}

	return b.effDistanceKm / b.effConsumedPct
}

// isWeekend reports whether d falls on Saturday or Sunday. d is a DATE with
// no time-of-day component and no zone attached to it -- this needs no
// internal/clock call, unlike bucketing a raw timestamp would.
func isWeekend(d time.Time) bool {
	wd := d.Weekday()
	return wd == time.Saturday || wd == time.Sunday
}

// deriveMonthlyFigures computes every vehicle_metrics-derived field of
// VehicleMonthlyMetrics from one month's rows. A day whose DistanceKm or
// ConsumedPct is nil is skipped entirely -- it has no computable
// predecessor and contributes to no bucket's sums or counts (this is the
// only place that ambiguity is silently accepted, and it is accepted
// because vehicle_metrics itself already treats such a day as having
// nothing computable -- see AGENTS.md).
func deriveMonthlyFigures(days []monthDay) VehicleMonthlyMetrics {
	var all, weekday, weekend bucketAccumulator

	for _, d := range days {
		if d.DistanceKm == nil || d.ConsumedPct == nil {
			continue
		}

		all.add(*d.DistanceKm, *d.ConsumedPct)

		if isWeekend(d.Date) {
			weekend.add(*d.DistanceKm, *d.ConsumedPct)
		} else {
			weekday.add(*d.DistanceKm, *d.ConsumedPct)
		}
	}

	return VehicleMonthlyMetrics{
		AllDistanceKm:   all.distanceKm,
		AllConsumedPct:  all.consumedPct,
		AllKmPerPctCalc: all.kmPerPct(),
		AllDayCount:     all.dayCount,

		WeekdayDistanceKm:   weekday.distanceKm,
		WeekdayConsumedPct:  weekday.consumedPct,
		WeekdayKmPerPctCalc: weekday.kmPerPct(),
		WeekdayDayCount:     weekday.dayCount,

		WeekendDistanceKm:   weekend.distanceKm,
		WeekendConsumedPct:  weekend.consumedPct,
		WeekendKmPerPctCalc: weekend.kmPerPct(),
		WeekendDayCount:     weekend.dayCount,
	}
}
