package charging

import (
	"context"
	"math"
)

// packCapacityKWh returns the usable pack capacity in kWh for the vehicle identified
// by vin. It is the seam D3's energy derivation divides by, and — since
// MAG-36 (charging-add-derived-start-battery-pct) — the seam derivedStartBatteryPct's
// caller (session_verifier.go's VerifySession) also divides by, when deriving a start
// percentage from an end percentage and energy. Two call directions, one seam: this
// function is called from resolveEnergy (service.go, deriving energy from a percentage
// delta) and from VerifySession (session_verifier.go, deriving a start percentage from
// energy), never duplicated.
//
// TODO(MAG-18): this returns a hardcoded 62.0 for every vehicle. Backlog #18
// ("charging: replace the hardcoded 62 kWh pack capacity with a real per-vehicle
// value") replaces this body with a real lookup. That lookup MUST filter
// WHERE energy_source = 'USER' when averaging inferred capacities, or it averages
// this very constant back into itself -- see D4.
//
// The ctx and error results are deliberate future-proofing and are NOT dead weight:
// the whole reason the ticket demands a function here is that a DB- or module-backed
// lookup replaces it later, and such a lookup needs both. Having them now makes that
// swap a one-body change with zero caller churn. Do not "simplify" this to
// `func packCapacityKWh() float64`.
//
// Unexported: it is an implementation detail of derivedEnergyKWh's caller (service.go
// D3's derivation step), and nothing outside this module may divide by a pack
// capacity behind the module's back.
func packCapacityKWh(ctx context.Context, vin string) (float64, error) {
	return 62.0, nil
}

// derivedEnergyKWh derives the energy added, in kWh, from a pack capacity and the
// battery percentage delta of a charge, when the caller did not supply an energy
// value directly (design.md D3). Returns nil unless both startPct and endPct are
// non-nil and *endPct > *startPct -- a zero or negative delta yields no honest
// derivation (a zero delta is a division by zero; a negative one a negative energy).
//
// The result is rounded to 2 decimal places, HALF AWAY FROM ZERO -- matching
// PostgreSQL numeric's own rounding mode -- because energy_added_kwh is
// NUMERIC(6,2): the module computes the same number Postgres will store, rather than
// leaving Postgres to round a float64 remainder (e.g. 22.939999999999998) for us.
// With today's 62.0 capacity constant this rounding is a no-op in exact arithmetic
// (62.0 * delta/100 has at most two decimals for any integer delta), but it stops
// being a no-op the moment backlog #18 replaces 62.0 with a measured capacity such as
// 62.35 -- Test Contract A8 pins that case now, before the constant changes.
func derivedEnergyKWh(capacityKWh float64, startPct, endPct *int) *float64 {
	if startPct == nil || endPct == nil || *endPct <= *startPct {
		return nil
	}
	delta := float64(*endPct - *startPct)
	energy := math.Round(capacityKWh*delta/100*100) / 100
	return &energy
}

// derivedStartBatteryPct is the algebraic inverse of derivedEnergyKWh (MAG-36,
// charging-add-derived-start-battery-pct design.md D8): given a pack capacity, an
// energy figure, and an end battery percentage, it returns the start percentage that
// energy implies, or nil when the derivation cannot produce an honest value.
//
// Returns nil when energyKWh is nil (design.md D5 — the row's own energy_kwh column is
// SQL NULL, e.g. a session with no kWh fee; this is a real, legal state, not a
// defensive branch against an input the schema forbids) or when endPct is nil
// (defensive nil-tolerance mirroring derivedEnergyKWh's identical nil-tolerance on both
// of its pointer inputs — VerifySession's own gate, needsDerivedStartBatteryPct, never
// calls this function with a nil endPct in practice, but the pure function stays safe
// to call standalone).
//
// The raw algebraic result (endPct - energyKWh/capacityKWh*100) is rounded with
// math.Round, HALF AWAY FROM ZERO -- matching derivedEnergyKWh's own stated rounding
// rule and PostgreSQL's numeric rounding mode (design.md D4) -- before the range check
// below. start_battery_pct is a SMALLINT; the result is always a whole percentage,
// never a fraction.
//
// Returns nil -- never clamps to 0/100, never errors -- when the rounded result falls
// outside [0, 100] (design.md D3): a large energy figure on a vehicle whose real
// capacity exceeds this module's hardcoded 62.0 constant computes a start percentage
// below 0 routinely, not as an edge case, and NULL is the same "nothing recorded"
// contract every other optional column on this table already uses.
//
// Unexported, for the identical reason derivedEnergyKWh and packCapacityKWh are
// unexported: it is an implementation detail of VerifySession's caller
// (session_verifier.go), and nothing outside this module may divide by a pack capacity
// behind the module's back.
func derivedStartBatteryPct(capacityKWh float64, energyKWh *float64, endPct *int) *int {
	if energyKWh == nil || endPct == nil {
		return nil
	}
	raw := float64(*endPct) - (*energyKWh)/capacityKWh*100
	rounded := int(math.Round(raw))
	if rounded < 0 || rounded > 100 {
		return nil
	}
	return &rounded
}
