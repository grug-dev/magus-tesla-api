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

	// The day's own two raw battery readings, both NOT NULL on
	// vehicle_metrics and therefore plain values, not pointers. They feed
	// TeslaRange100PctKmCalc, which -- unlike every other figure here --
	// needs no predecessor day, so it is computed on days the two pointers
	// above leave nil.
	BatteryRangeKm  float64
	BatteryLevelPct int
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
	var teslaRangeKm, teslaLevelPct float64

	for _, d := range days {
		// Accumulated BEFORE the skip below, on purpose. Tesla's own range
		// projection reads two raw values off this day's own row and needs
		// no predecessor, so a day the three bucket figures cannot use is
		// still a perfectly good day for this one. A level of 0 is dropped
		// rather than counted: it is the divisor, and a vehicle reporting
		// 0% carries no range information to add.
		if d.BatteryLevelPct > 0 {
			teslaRangeKm += d.BatteryRangeKm
			teslaLevelPct += float64(d.BatteryLevelPct)
		}

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

	// A ratio of SUMS, not the mean of the daily ratios. It weights each day
	// by its own battery level, and that is the weight accuracy calls for:
	// battery_level_pct is an integer, so its 1-point rounding is a fixed
	// absolute error and a day at 99% is about five times more accurate than
	// a day at 20%.
	var teslaRange100Pct float64
	if teslaLevelPct > 0 {
		teslaRange100Pct = teslaRangeKm / teslaLevelPct * 100
	}

	return VehicleMonthlyMetrics{
		TeslaRange100PctKmCalc: teslaRange100Pct, // delta:allow: a ratio of sums, not a day-over-day delta

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
