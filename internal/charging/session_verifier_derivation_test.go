// session_verifier_derivation_test.go — offline unit tests (no DB, no
// testcontainers, no pool) for the two pure functions added by MAG-36
// (charging-add-derived-start-battery-pct): derivedStartBatteryPct (capacity.go)
// and needsDerivedStartBatteryPct (session_verifier.go). Implements design.md's
// Test Contract Group A (A1-A7) and Group B (B1-B5).
//
// Package charging (NOT charging_test): both functions under test are
// deliberately unexported (design.md D8, mirroring derivedEnergyKWh's and
// packCapacityKWh's established privacy — "nothing outside this module may
// divide by a pack capacity behind the module's back," capacity.go's own doc
// comment). A package charging_test file, restricted to this module's exported
// symbols, cannot reference either function and would not compile. This is
// exactly the situation entry_status_test.go already solved for the identical
// reason (design.md D11) — this file follows its precedent.
//
// Every expected value below is copied VERBATIM from design.md §Test Contract,
// authored before this file existed (ai/go-conventions.md §Testing authoring
// order: "author their expected values up front… before the implementation
// exists") — this file asserts that contract, not whatever the implementation
// happens to produce.
package charging

import "testing"

// --- Group A: derivedStartBatteryPct (capacity.go) ---

// TestDerivedStartBatteryPct_Table implements design.md's Test Contract Group A
// (A1-A7) verbatim. Compare *int results directly — no float tolerance needed,
// the function's output type is *int. Assert nil explicitly for every nil case,
// never a zero value.
func TestDerivedStartBatteryPct_Table(t *testing.T) {
	cases := []struct {
		id          string
		capacityKWh float64
		energyKWh   *float64
		endPct      *int
		want        *int
	}{
		// A1: normal case — the exact algebraic inverse of derivedEnergyKWh's own
		// "small delta: 64->74 => 6.20" precedent case.
		{"A1", 62.0, ptrF64(6.20), intPtr(74), intPtr(64)},
		// A2: negative/out-of-range (D3) — the row's own energy is larger than
		// what an end of 10 can absorb at this capacity; result is deeply
		// negative.
		{"A2", 62.0, ptrF64(62.00), intPtr(10), nil},
		// A3: nil energy (D5) — the row has no kWh fee.
		{"A3", 62.0, nil, intPtr(74), nil},
		// A4: nil end percentage — defensive; VerifySession's own gate
		// (needsDerivedStartBatteryPct) never calls this function with a nil
		// endPct in practice, but the pure function is safe standalone,
		// mirroring derivedEnergyKWh's identical nil-tolerance on both its
		// inputs.
		{"A4", 62.0, ptrF64(6.20), nil, nil},
		// A5: exact lower boundary — the largest energy this capacity can
		// absorb over a full 0->100 range, landing exactly on 0.
		{"A5", 62.0, ptrF64(62.00), intPtr(100), intPtr(0)},
		// A6: exact upper boundary — zero energy added means the pack didn't
		// move; start equals end exactly.
		{"A6", 62.0, ptrF64(0.00), intPtr(100), intPtr(100)},
		// A7: rounding pin (mirrors derivedEnergyKWh's own A8): raw is exactly
		// 48.5; math.Round must round AWAY FROM ZERO to 49, not to the even 48
		// a banker's-rounding implementation would produce. Deliberately uses a
		// non-round capacity so this is not a no-op case. What must NOT
		// change (design.md): if this ever asserts 48, the rounding was
		// silently switched to round-half-to-even.
		{"A7", 62.35, ptrF64(0.93525), intPtr(50), intPtr(49)},
		// A8: a real-world reading the owner asked to pin (MAG-36 follow-up, NOT
		// part of design.md's original Test Contract Group A — every case above
		// is). A near-empty-to-29% session: 29 - 17.14/62.0*100 = 29 - 27.645161
		// = 1.354838..., which math.Round takes to 1. It guards the low end of
		// the range the way A5/A6 guard the exact boundaries: the result is
		// small and positive, so an off-by-one in the rounding or a stray
		// truncation to int would show up here as 0 or 2 rather than 1.
		{"A8", 62.0, ptrF64(17.14), intPtr(29), intPtr(1)},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := derivedStartBatteryPct(tc.capacityKWh, tc.energyKWh, tc.endPct)
			// Assert nil explicitly for every nil case, never a zero value:
			// compare pointer-nil-ness first, then dereferenced *int equality.
			if tc.want == nil {
				if got != nil {
					t.Errorf("derivedStartBatteryPct(%v, energyKWh=%v, endPct=%v) = %d, want nil",
						tc.capacityKWh, ptrFloatDebug(tc.energyKWh), ptrIntDebug(tc.endPct), *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("derivedStartBatteryPct(%v, energyKWh=%v, endPct=%v) = nil, want %d",
					tc.capacityKWh, ptrFloatDebug(tc.energyKWh), ptrIntDebug(tc.endPct), *tc.want)
			}
			if *got != *tc.want {
				t.Errorf("derivedStartBatteryPct(%v, energyKWh=%v, endPct=%v) = %d, want %d",
					tc.capacityKWh, ptrFloatDebug(tc.energyKWh), ptrIntDebug(tc.endPct), *got, *tc.want)
			}
		})
	}
}

// ptrFloatDebug and ptrIntDebug render a possibly-nil pointer for a test
// failure message only, via fmt's native nil-pointer formatting — never used
// for comparison.
func ptrFloatDebug(v *float64) interface{} {
	if v == nil {
		return "nil"
	}
	return *v
}

func ptrIntDebug(v *int) interface{} {
	if v == nil {
		return "nil"
	}
	return *v
}

// --- Group B: needsDerivedStartBatteryPct (session_verifier.go) ---

// TestNeedsDerivedStartBatteryPct_Table implements design.md's Test Contract
// Group B (B1-B5) verbatim. Compare bool results directly.
func TestNeedsDerivedStartBatteryPct_Table(t *testing.T) {
	cases := []struct {
		id       string
		startPct *int
		endPct   *int
		want     bool
	}{
		// B1: the trigger fires — start absent, end supplied.
		{"B1", nil, intPtr(74), true},
		// B2: a caller-supplied start is never recomputed (D2) — true
		// regardless of what endPct is, including a value that would
		// otherwise derive cleanly.
		{"B2", intPtr(50), intPtr(74), false},
		// B3: nothing to derive from — matches the existing "clear both" call
		// shape (RM31 T4), untouched by this change.
		{"B3", nil, nil, false},
		// B4: start-only supply (RM31 T2's shape) — no end to derive from, and
		// a start was supplied anyway.
		{"B4", intPtr(50), nil, false},
		// B5: edge case: an explicit, legitimate 0 is a non-nil pointer, not
		// the same as "absent." Proves the check is pointer-nil, not
		// value-zero.
		{"B5", intPtr(0), intPtr(74), false},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			got := needsDerivedStartBatteryPct(tc.startPct, tc.endPct)
			if got != tc.want {
				t.Errorf("needsDerivedStartBatteryPct(startPct=%v, endPct=%v) = %v, want %v",
					ptrIntDebug(tc.startPct), ptrIntDebug(tc.endPct), got, tc.want)
			}
		})
	}
}
