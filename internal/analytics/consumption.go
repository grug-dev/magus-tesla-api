// File consumption.go owns the five per-day derived-consumption figures --
// distance travelled, battery used, km per battery point, estimated full-pack
// range, and calendar days spanned -- computed from one snapshot pair.
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
// The move is a CHARACTERIZATION move, not a rewrite (roadmap D10): the body
// below is telemetry's, operand for operand, with one mechanical change --
// it populates and returns a consumptionCalc instead of mutating and
// returning a Snapshot that no longer carries the fields (design.md D5).
// Fully offline: no I/O, plain telemetry.Snapshot values in, a value struct
// out (mirrors consumed.go's and derive.go's zero-I/O style).
package analytics

import (
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// consumptionCalc carries the five derived consumption figures for one
// snapshot pair (design.md D5). All five are pointers with the identical
// NULL convention the vehicle_snapshots columns carried and the
// vehicle_metrics columns still carry: nil means "not computable", never
// zero -- a stored 0.0 is a truthful reading (a parked day), not an absence.
//
// A named struct rather than five return values on purpose: five same-typed
// pointer returns are trivially swappable at the call site with no compiler
// complaint -- exactly the class of error a characterization test can miss
// when its fixture happens to be symmetric. Each field assignment here is
// self-checking.
type consumptionCalc struct {
	DistanceTraveledKmCalc *float64
	BatteryUsedPctCalc     *int
	KmPerPctCalc           *float64
	EstimatedRangeKmCalc   *float64
	DaysSpannedCalc        *int

	// The four tyre-pressure day-over-day deltas
	// (RM50-analytics-add-tire-pressure-variance design.md D1/D2), one per
	// wheel, in PSI. Same nil convention as the five fields above: nil means
	// "not computable", never a fabricated 0. Computed by tpmsDeltaPSI below,
	// called once per wheel inside deriveConsumption.
	TpmsPressureFLPSICalc *float64
	TpmsPressureFRPSICalc *float64
	TpmsPressureRLPSICalc *float64
	TpmsPressureRRPSICalc *float64
}

// tpmsDeltaPSI computes one wheel's day-over-day tyre-pressure delta
// (design.md D2, condition 2): cur minus prev, in PSI. Returns nil if
// EITHER operand is nil -- there is no second number to subtract, so no
// delta can be truthfully reported. Never fabricates a 0: a 0.0 delta
// already means "no pressure change" elsewhere in this schema, so treating
// a missing reading as "no change" would make every 0.0 ambiguous
// (design.md D2's rejected alternative).
//
// This helper only ever needs to handle condition 2 (a missing wheel
// reading) -- condition 1 (no predecessor day at all) is already handled by
// deriveConsumption's own `if prev == nil` guard before this is called.
func tpmsDeltaPSI(prev, cur *float64) *float64 {
	if prev == nil || cur == nil {
		return nil
	}
	delta := *cur - *prev
	return &delta
}

// deriveConsumption computes the five derived-consumption figures for cur by
// comparing it against prev, its predecessor for the same (account_id,
// tesla_id). prev == nil (the vehicle's first-ever snapshot, or a capture gap
// with nothing earlier stored at all) returns the zero consumptionCalc: all
// five fields stay nil. Otherwise distance/battery-used/days-spanned are
// always computed, even when negative (overnight charge) or zero (parked
// day) -- a truthful reading is always returned, never clamped. The two
// efficiency fields (KmPerPctCalc, EstimatedRangeKmCalc) are computed ONLY
// when BatteryUsedPctCalc > 0: a zero or negative divisor has no truthful
// ratio and both stay nil.
//
// DaysSpannedCalc counts WHOLE CALENDAR DAYS via CapturedDate subtraction,
// not elapsed hours between capture instants -- both operands are bare dates
// normalized to UTC midnight (telemetry's dateOnly), so the quotient is
// exact and a >1 result means the poller genuinely missed nights.
//
// Every operand order, guard and constant below is carried over unchanged
// from telemetry's original. Do not "improve" the arithmetic: the roadmap D10
// bar for this move is byte-for-byte identical output for identical inputs,
// and design.md's Test Contract (Fixtures A-E) pins it.
func deriveConsumption(prev *telemetry.Snapshot, cur telemetry.Snapshot) consumptionCalc {
	if prev == nil {
		return consumptionCalc{} // no predecessor -- all five figures stay nil.
	}

	var calc consumptionCalc

	distance := cur.OdometerKm - prev.OdometerKm
	calc.DistanceTraveledKmCalc = &distance

	batteryUsed := prev.BatteryLevelPct - cur.BatteryLevelPct
	calc.BatteryUsedPctCalc = &batteryUsed

	days := int(cur.CapturedDate.Sub(prev.CapturedDate).Hours() / 24)
	calc.DaysSpannedCalc = &days

	if batteryUsed > 0 { // only a positive divisor yields a truthful ratio
		kmPerPct := distance / float64(batteryUsed)
		calc.KmPerPctCalc = &kmPerPct
		estRange := kmPerPct * 100
		calc.EstimatedRangeKmCalc = &estRange
	}

	// RM50-analytics-add-tire-pressure-variance design.md D1/D2: one delta
	// per wheel, cur minus prev, nil if either operand's own raw reading is
	// nil. Runs in this same "prev != nil" branch -- condition 1 (no
	// predecessor at all) is already excluded by the guard above.
	calc.TpmsPressureFLPSICalc = tpmsDeltaPSI(prev.TpmsPressureFLPSI, cur.TpmsPressureFLPSI)
	calc.TpmsPressureFRPSICalc = tpmsDeltaPSI(prev.TpmsPressureFRPSI, cur.TpmsPressureFRPSI)
	calc.TpmsPressureRLPSICalc = tpmsDeltaPSI(prev.TpmsPressureRLPSI, cur.TpmsPressureRLPSI)
	calc.TpmsPressureRRPSICalc = tpmsDeltaPSI(prev.TpmsPressureRRPSI, cur.TpmsPressureRRPSI)

	return calc
}
