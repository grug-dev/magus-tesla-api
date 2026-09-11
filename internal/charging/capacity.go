package charging

import (
	"context"
	"fmt"
	"math"
)

// defaultPackCapacityKWh is the fallback used whenever no vehicle-specific
// measurement exists yet -- packCapacityKWh's own "no row" branch, and
// VerifySession's "tesla_id is nil" branch (RD11), both read this same named
// constant rather than repeating the literal 62.0 in two places.
const defaultPackCapacityKWh = 62.0

// packCapacityLookup is the narrow read seam packCapacityKWh needs: the newest
// measured capacity for one vehicle, or nil when none exists yet. Both
// service.go's store interface (via dbStore.latestMeasuredCapacity) and
// session_verifier.go's *sessionVerifier (via its own latestMeasuredCapacity
// method) satisfy this structurally -- Go interfaces need no "implements"
// declaration -- so packCapacityKWh stays callable from both existing call
// sites without joining SessionVerifier to store's fake-testability seam, and
// without giving store a database-shaped dependency it does not otherwise have
// (design.md Context fact 9).
type packCapacityLookup interface {
	latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error)
}

// packCapacityKWh returns the usable pack capacity in kWh for the vehicle
// identified by teslaID (RD11 -- this seam took a vin string before this
// change; it takes the vehicle's stable tesla_id now, matching the key
// monthly_effective_capacity is keyed on, RD5). It is the seam resolveEnergy's
// energy derivation (service.go) divides by, and the seam
// derivedStartBatteryPct's caller (session_verifier.go's VerifySession) also
// divides by, when deriving a start percentage from an end percentage and
// energy. Two call directions, one seam: never duplicated.
//
// Reads charging's own monthly_effective_capacity table through lookup,
// returning the newest measured value for teslaID. Returns
// defaultPackCapacityKWh when no measured row exists yet for this vehicle --
// which is every vehicle, until RM52's job (monthly_capacity.go) has actually
// run at least once and found enough evidence (roadmap RD4). Behaviour is
// therefore UNCHANGED until the first month is computed.
//
// Unexported: it is an implementation detail of its two callers' own capacity
// derivation, and nothing outside this module may divide by a pack capacity
// behind the module's back.
func packCapacityKWh(ctx context.Context, lookup packCapacityLookup, teslaID int64) (float64, error) {
	measured, err := lookup.latestMeasuredCapacity(ctx, teslaID)
	if err != nil {
		return 0, fmt.Errorf("charging: resolving pack capacity for tesla_id %d: %w", teslaID, err)
	}
	if measured == nil {
		return defaultPackCapacityKWh, nil
	}
	return *measured, nil
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
