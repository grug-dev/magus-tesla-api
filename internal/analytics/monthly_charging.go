package analytics

import (
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// chargeTally accumulates one charging source's month: total energy, total cost,
// how many records counted, and the ending-battery-percentage distribution across
// those records. A record with no EndBatteryPct still counts (add still increments
// count) but contributes to no bucket -- mirrors monthly_figures.go's
// bucketAccumulator's own "count without contributing to every sum" shape.
type chargeTally struct {
	energyKWh float64
	cost      float64
	count     int
	dist      EndingBatteryDist
}

// add folds one record into the tally. energyKWh and cost are the record's own
// already-dereferenced values -- callers pass 0 for a nil source field (a nil
// contributes 0 to its sum, and the record still counts), never a fabricated
// non-zero default.
func (t *chargeTally) add(energyKWh, cost float64, endBatteryPct *int) {
	t.energyKWh += energyKWh
	t.cost += cost
	t.count++
	if endBatteryPct != nil {
		t.dist.addToBucket(*endBatteryPct)
	}
}

// addToBucket increments the one bucket pct falls into. Edges are half-open --
// [0,20) [20,40) [40,60) [60,80) -- except the last, [80,100], which is closed so
// a percentage of exactly 100 has a home.
func (d *EndingBatteryDist) addToBucket(pct int) {
	switch {
	case pct < 20:
		d.Bucket0To20++
	case pct < 40:
		d.Bucket20To40++
	case pct < 60:
		d.Bucket40To60++
	case pct < 80:
		d.Bucket60To80++
	default:
		d.Bucket80To100++
	}
}

// aggregateChargingMonth folds one month's external charges and Supercharger
// sessions into three tallies: external AC, external DC, Supercharger. entries and
// sessions are assumed already scoped to one vehicle; sessions is assumed already
// fetched with the one-day-each-side widened window the caller uses to cross a UTC/
// platform-zone day boundary -- this function does the platform-zone filtering that
// widening exists to make possible.
func aggregateChargingMonth(entries []charging.Entry, sessions []charging.Session, monthStart, monthEnd time.Time) (ac, dc, sc chargeTally) {
	for _, e := range entries {
		if e.ChargingType == nil {
			continue // no type, no bucket -- skipped entirely, not even counted
		}
		energy := 0.0
		if e.EnergyAddedKWh != nil {
			energy = *e.EnergyAddedKWh
		}
		switch *e.ChargingType {
		case "AC":
			ac.add(energy, e.Price, e.EndBatteryPct)
		case "DC":
			dc.add(energy, e.Price, e.EndBatteryPct)
		}
	}

	for _, s := range sessions {
		day := clock.CalendarDay(s.ChargeStopDateTime, clock.Zone())
		if day.Before(monthStart) || day.After(monthEnd) {
			continue // the widened fetch returns some neighboring-month sessions on purpose
		}
		energy := 0.0
		if s.EnergyKWh != nil {
			energy = *s.EnergyKWh
		}
		cost := 0.0
		if s.TotalCost != nil {
			cost = *s.TotalCost
		}
		sc.add(energy, cost, s.EndBatteryPct)
	}

	return ac, dc, sc
}

// monthBounds returns the first and last calendar day of the month containing
// period, using only day-count arithmetic on the caller-supplied value -- never a
// fresh "midnight of some day" construction, which only internal/clock may build.
// Only period's year and calendar month matter, matching SyncMonth's own
// any-day-in-month contract.
func monthBounds(period time.Time) (start, end time.Time) {
	start = period.AddDate(0, 0, 1-period.Day())
	end = start.AddDate(0, 1, -1)
	return start, end
}
