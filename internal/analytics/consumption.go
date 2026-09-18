// File consumption.go owns the six per-day derived-consumption figures --
// distance travelled, battery used, battery CONSUMED (charge-corrected), km
// per battery point, estimated full-pack range, and calendar days spanned --
// computed from one snapshot pair plus that pair's charge total.
//
// This derivation used to live in internal/telemetry (deriveConsumption in
// service.go), which computed it once per capture and persisted the five
// results as _calc columns on vehicle_snapshots. Nothing inside telemetry
// ever read them back: their only consumer was this module, which copied them
// verbatim onto vehicle_metrics. RM29-telemetry-drop-derived-columns
// (MAG-26 tier 4) moves the derivation here and drops the columns, so the
// figures have exactly one owner -- the module that consumes them
// (design.md D1, roadmap D6).
//
// That move was a CHARACTERIZATION move, not a rewrite (roadmap D10), and the
// body stayed telemetry's operand for operand. MAG-81 is the first deliberate
// change to the arithmetic since: the charge correction moved in from
// consumed.go, so the efficiency ratio divides by what the day really
// consumed instead of by the raw battery drop (see deriveConsumption). The
// figures telemetry computed are all still here and still computed the same
// way; one new figure joined them and one divisor changed.
//
// Still fully offline: no I/O, plain telemetry.Snapshot values plus one
// float in, a value struct out (mirrors consumed.go's and derive.go's
// zero-I/O style). The charge MATCHING -- deciding which records fall in the
// span -- stays in consumed.go; only the already-summed total crosses over.
package analytics

import (
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// consumptionCalc carries the derived consumption figures and their
// day-over-day deltas for one snapshot pair plus that day's charge
// correction. All fields are pointers with one NULL convention throughout:
// nil means "not computable", never zero -- a stored 0.0 is a truthful
// reading (a parked day, or a genuinely unchanged reading), not an absence.
//
// A named struct rather than several return values on purpose: same-typed
// pointer returns are trivially swappable at the call site with no compiler
// complaint -- exactly the class of error a characterization test can miss
// when its fixture happens to be symmetric. Each field assignment here is
// self-checking.
type consumptionCalc struct {
	DistanceTraveledKmCalc *float64
	BatteryUsedPctCalc     *int

	// ConsumedPct is the charge-corrected battery percentage this day really
	// spent: BatteryUsedPctCalc plus the chargePct argument. It is the figure
	// persisted as vehicle_metrics.consumed_pct AND the divisor behind the two
	// efficiency fields below.
	//
	// It lives on this struct rather than being recomputed by the caller so the
	// formula has exactly ONE home. consumed.go used to compute the identical
	// sum a second time for the column; two copies of one formula is two places
	// to edit and one to forget.
	ConsumedPct *float64

	KmPerPctCalc         *float64
	EstimatedRangeKmCalc *float64
	DaysSpannedCalc      *int

	// The four tyre-pressure day-over-day deltas, one per wheel, in PSI.
	// Same nil convention as the fields above: nil means "not computable",
	// never a fabricated 0. Computed by dayOverDayDelta below, called once
	// per wheel inside deriveConsumption.
	TpmsPressureFLPSIDeltaCalc *float64
	TpmsPressureFRPSIDeltaCalc *float64
	TpmsPressureRLPSIDeltaCalc *float64
	TpmsPressureRRPSIDeltaCalc *float64
}

// dayOverDayDelta is the shared nil-safe day-over-day subtraction reused by
// every `_delta_calc` field this package computes: cur minus prev. Returns
// nil if EITHER operand is nil -- there is no second number to subtract, so
// no delta can be truthfully reported. Never fabricates a 0: a 0.0 delta
// already means "no change" elsewhere in this schema, so treating a missing
// reading as "no change" would make every 0.0 ambiguous.
//
// The caller alone decides whether prev can exist at all -- this helper only
// ever sees the two values it is handed.
func dayOverDayDelta(prev, cur *float64) *float64 {
	if prev == nil || cur == nil {
		return nil
	}
	delta := *cur - *prev
	return &delta
}

// deriveConsumption computes the derived-consumption figures and their
// day-over-day deltas for cur by comparing it against prev, its predecessor
// for the same (account_id, tesla_id), corrected by chargePct -- the
// battery percentage the vehicle GAINED from every charge record matching
// the span (prev, cur], summed by consumed.go's sumSuperchargerPctBetween +
// sumManualPctBetween. A day with no charge record passes 0.
//
// prev == nil (the vehicle's first-ever snapshot, or a capture gap with
// nothing earlier stored at all) returns the zero consumptionCalc: every
// field stays nil. Otherwise distance/battery-used/days-spanned are always
// computed, even when negative (overnight charge) or zero (parked day) -- a
// truthful reading is always returned, never clamped.
//
// ConsumedPct is BatteryUsedPctCalc + chargePct, and it is also the DIVISOR
// behind the two efficiency fields (KmPerPctCalc, EstimatedRangeKmCalc),
// which are computed ONLY when ConsumedPct > 0: a zero or negative divisor
// has no truthful ratio and both stay nil.
//
// The divisor is charge-corrected rather than the RAW BatteryUsedPctCalc,
// which knows nothing about charging: on a day the vehicle both drove and
// charged, the battery ends HIGHER than it started, so the raw figure alone
// is negative or zero and would silently NULL a day's efficiency even
// though a perfectly computable charge-corrected value exists in the very
// next column. The raw figure is still reported as BatteryUsedPctCalc; it
// is simply no longer the divisor.
//
// This is the one place the charge correction is computed. consumed.go reads
// ConsumedPct back rather than re-adding the same two operands, so the
// formula cannot drift between the column and the ratio derived from it.
//
// DaysSpannedCalc counts WHOLE CALENDAR DAYS via CapturedDate subtraction,
// not elapsed hours between capture instants -- both operands are bare dates
// normalized to UTC midnight (telemetry's dateOnly), so the quotient is
// exact and a >1 result means the poller genuinely missed nights.
func deriveConsumption(prev *telemetry.Snapshot, cur telemetry.Snapshot, chargePct float64) consumptionCalc {
	if prev == nil {
		return consumptionCalc{} // no predecessor -- every figure stays nil.
	}

	var calc consumptionCalc

	distance := cur.OdometerKm - prev.OdometerKm
	calc.DistanceTraveledKmCalc = &distance

	batteryUsed := prev.BatteryLevelPct - cur.BatteryLevelPct
	calc.BatteryUsedPctCalc = &batteryUsed

	days := int(cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24)
	calc.DaysSpannedCalc = &days

	// The charge-corrected percentage, computed once and used twice --
	// persisted as vehicle_metrics.consumed_pct (consumed.go reads it back off
	// this struct) and used as the divisor below.
	consumed := float64(batteryUsed) + chargePct
	calc.ConsumedPct = &consumed

	if consumed > 0 { // only a positive divisor yields a truthful ratio
		kmPerPct := distance / consumed
		calc.KmPerPctCalc = &kmPerPct
		estRange := kmPerPct * 100
		calc.EstimatedRangeKmCalc = &estRange
	}

	// One delta per wheel, cur minus prev. nil when either wheel reading is
	// missing -- a fabricated 0.0 would be indistinguishable from a real
	// no-change reading. Runs only when prev exists; the guard above already
	// handles a day with no predecessor at all.
	calc.TpmsPressureFLPSIDeltaCalc = dayOverDayDelta(prev.TpmsPressureFLPSI, cur.TpmsPressureFLPSI)
	calc.TpmsPressureFRPSIDeltaCalc = dayOverDayDelta(prev.TpmsPressureFRPSI, cur.TpmsPressureFRPSI)
	calc.TpmsPressureRLPSIDeltaCalc = dayOverDayDelta(prev.TpmsPressureRLPSI, cur.TpmsPressureRLPSI)
	calc.TpmsPressureRRPSIDeltaCalc = dayOverDayDelta(prev.TpmsPressureRRPSI, cur.TpmsPressureRRPSI)

	return calc
}
