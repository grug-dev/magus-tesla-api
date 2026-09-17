// start_battery_source_test.go — offline unit tests (no DB) for
// resolveStartBatteryPct (service.go). Package charging (NOT charging_test):
// the function under test is unexported, mirroring price_source_test.go's and
// entry_status_test.go's own package choice.
//
// Every expected value below was fixed before this file existed
// (ai/go-conventions.md §Testing authoring order) — this file asserts what the
// rule must do, not whatever the implementation happens to produce.
//
// Case mapping (contract row -> subtest name):
//
//	A1  no_inputs_present
//	A2  energy_alone_does_not_derive
//	A3  end_percentage_alone_does_not_derive
//	A4  derives_from_energy_and_end_percentage
//	A5  out_of_range_derivation_yields_nothing
//	A6  caller_value_returned_as_is
//	A7  caller_value_wins_over_energy
//	A8  caller_value_wins_over_end_percentage
//	A9  caller_value_wins_even_when_derivable
package charging

import (
	"context"
	"testing"
)

// TestResolveStartBatteryPct_Table runs every presence/absence combination of
// (StartBatteryPct, EndBatteryPct, energy) the derivation rule must handle,
// against a fixed 62.0 kWh pack capacity via fakePackCapacityLookup (reused
// from monthly_capacity_estimator_test.go — no measured capacity row exists
// in any case here, so every derivation runs against the hardcoded default).
func TestResolveStartBatteryPct_Table(t *testing.T) {
	lookup := fakePackCapacityLookup{capacity: ptrF64(62.0)}
	ctx := context.Background()

	cases := []struct {
		name       string
		startPct   *int
		endPct     *int
		energy     *float64
		wantPct    *int
		wantSource *StartBatterySource
	}{
		{
			name: "no_inputs_present",
		},
		{
			name:   "energy_alone_does_not_derive",
			energy: ptrF64(10.0),
		},
		{
			name:   "end_percentage_alone_does_not_derive",
			endPct: ptrInt(74),
		},
		{
			name:       "derives_from_energy_and_end_percentage",
			endPct:     ptrInt(74),
			energy:     ptrF64(6.20),
			wantPct:    ptrInt(64),
			wantSource: startSourcePtr(StartBatterySourceEstimated),
		},
		{
			// A derivation is attempted (a capacity lookup runs) but the raw
			// result lands far outside [0, 100] -- 10 - 62.00/62.0*100 = -90.
			// Never clamped, never reported as USER.
			name:   "out_of_range_derivation_yields_nothing",
			endPct: ptrInt(10),
			energy: ptrF64(62.00),
		},
		{
			name:       "caller_value_returned_as_is",
			startPct:   ptrInt(50),
			wantPct:    ptrInt(50),
			wantSource: startSourcePtr(StartBatterySourceUser),
		},
		{
			name:       "caller_value_wins_over_energy",
			startPct:   ptrInt(50),
			energy:     ptrF64(10.0),
			wantPct:    ptrInt(50),
			wantSource: startSourcePtr(StartBatterySourceUser),
		},
		{
			name:       "caller_value_wins_over_end_percentage",
			startPct:   ptrInt(50),
			endPct:     ptrInt(90),
			wantPct:    ptrInt(50),
			wantSource: startSourcePtr(StartBatterySourceUser),
		},
		{
			// The never-recompute guarantee: all three inputs would allow a
			// derivation, but the caller's own value still wins.
			name:       "caller_value_wins_even_when_derivable",
			startPct:   ptrInt(50),
			endPct:     ptrInt(90),
			energy:     ptrF64(20.0),
			wantPct:    ptrInt(50),
			wantSource: startSourcePtr(StartBatterySourceUser),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Entry{StartBatteryPct: tc.startPct, EndBatteryPct: tc.endPct}
			gotPct, gotSource, err := resolveStartBatteryPct(ctx, lookup, e, tc.energy)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertIntPtrEqual(t, "start percentage", gotPct, tc.wantPct)
			assertSourcePtrEqual(t, "source", gotSource, tc.wantSource)
		})
	}
}

func startSourcePtr(s StartBatterySource) *StartBatterySource { return &s }

// ptrInt is a local *int helper for this file's table, mirroring ptrF64's
// shape in this same package (entry_status_test.go). charging_test's own
// ptrInt (db_integration_test.go) lives in a different package and is not
// visible here.
func ptrInt(i int) *int { return &i }

func assertIntPtrEqual(t *testing.T, label string, got, want *int) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", label, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, *want)
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", label, *got, *want)
	}
}

func assertSourcePtrEqual(t *testing.T, label string, got, want *StartBatterySource) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", label, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, *want)
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", label, *got, *want)
	}
}
