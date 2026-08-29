package charging

import (
	"context"
	"math"
)

// packCapacityKWh returns the usable pack capacity in kWh for the vehicle identified
// by vin. It is the seam D3's energy derivation divides by.
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
