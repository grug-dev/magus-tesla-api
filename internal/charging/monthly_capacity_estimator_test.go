// monthly_capacity_estimator_test.go — offline unit tests (no DB) for the pure
// monthly-capacity estimator (monthly_capacity.go) and the packCapacityKWh seam
// (capacity.go), added by RM52-charging-add-monthly-effective-capacity (MAG-32).
// Implements design.md's Test Contract Group A (A1-A9).
//
// Package charging (NOT charging_test): estimateEffectiveCapacity, median,
// packCapacityKWh, and packCapacityLookup are all unexported -- implementation
// details of this module's own capacity derivation (design.md D3/D4/D9),
// mirroring entry_status_test.go's and session_verifier_derivation_test.go's
// identical reason for living in this package.
//
// Every expected value below is copied VERBATIM from design.md's Test
// Contract, authored before this file existed (ai/go-conventions.md §Testing
// authoring order: "author their expected values up front… before the
// implementation exists"). This file asserts THAT contract, not whatever the
// implementation happens to produce. Comparison of *float64 results reuses
// this package's existing almostEqual helper (entry_status_test.go) rather
// than redefining it.
package charging

import (
	"context"
	"errors"
	"testing"
)

// --- A1-A5: estimateEffectiveCapacity ---

// TestEstimateEffectiveCapacity_Table implements design.md Test Contract
// A1-A5 verbatim: the minSamples boundary, the odd median, the even median,
// the delta-gate dropping both a row and its outlier value, and the gate's
// >= boundary.
func TestEstimateEffectiveCapacity_Table(t *testing.T) {
	cases := []struct {
		id              string
		samples         []capacitySample
		wantCapacity    *float64
		wantSampleCount int
	}{
		// A1: two samples, deltas 20, 25 -- both pass the delta gate but land
		// below minSamples (3). NULL capacity, sample_count = 2.
		{
			id: "A1",
			samples: []capacitySample{
				{CapacityKWh: 60.0, BatteryDeltaPct: 20},
				{CapacityKWh: 64.0, BatteryDeltaPct: 25},
			},
			wantCapacity:    nil,
			wantSampleCount: 2,
		},
		// A2: three samples, deltas all >=15, capacities 60.0/62.0/64.0 --
		// odd count at exactly minSamples: the middle value, boundary
		// inclusive.
		{
			id: "A2",
			samples: []capacitySample{
				{CapacityKWh: 60.0, BatteryDeltaPct: 20},
				{CapacityKWh: 62.0, BatteryDeltaPct: 18},
				{CapacityKWh: 64.0, BatteryDeltaPct: 30},
			},
			wantCapacity:    ptrF64(62.0),
			wantSampleCount: 3,
		},
		// A3: four samples, deltas all >=15, capacities 58/60/64/70 -- even
		// count: average of the two middle values ((60.0+64.0)/2 = 62.0).
		{
			id: "A3",
			samples: []capacitySample{
				{CapacityKWh: 58.0, BatteryDeltaPct: 16},
				{CapacityKWh: 60.0, BatteryDeltaPct: 20},
				{CapacityKWh: 64.0, BatteryDeltaPct: 25},
				{CapacityKWh: 70.0, BatteryDeltaPct: 40},
			},
			wantCapacity:    ptrF64(62.0),
			wantSampleCount: 4,
		},
		// A4: four samples -- three with delta >=15 (60/62/64), one with
		// delta 10 (capacity 120.0, an outlier that would skew the result if
		// counted). The gate drops that row BEFORE the median runs: it is
		// excluded from both the median and sample_count, and its outlier
		// value never reaches the median.
		{
			id: "A4",
			samples: []capacitySample{
				{CapacityKWh: 60.0, BatteryDeltaPct: 20},
				{CapacityKWh: 62.0, BatteryDeltaPct: 18},
				{CapacityKWh: 64.0, BatteryDeltaPct: 30},
				{CapacityKWh: 120.0, BatteryDeltaPct: 10},
			},
			wantCapacity:    ptrF64(62.0),
			wantSampleCount: 3,
		},
		// A5: two samples with delta >=15, plus one with delta 14 (just
		// under the gate). The gate's boundary is >= minDeltaPct, not >, so
		// 14 is dropped, leaving 2 -- below minSamples.
		{
			id: "A5",
			samples: []capacitySample{
				{CapacityKWh: 60.0, BatteryDeltaPct: 20},
				{CapacityKWh: 64.0, BatteryDeltaPct: 25},
				{CapacityKWh: 999.0, BatteryDeltaPct: 14},
			},
			wantCapacity:    nil,
			wantSampleCount: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			gotCapacity, gotSampleCount := estimateEffectiveCapacity(tc.samples)
			assertCapacityPtr(t, gotCapacity, tc.wantCapacity)
			if gotSampleCount != tc.wantSampleCount {
				t.Errorf("sampleCount = %d, want %d", gotSampleCount, tc.wantSampleCount)
			}
		})
	}
}

// assertCapacityPtr fails the test unless got and want are both nil, or both
// non-nil and equal within a tight tolerance. Assert nil/non-nil explicitly
// for this optional field, per internal/charging/AGENTS.md §Testing Notes.
func assertCapacityPtr(t *testing.T, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("got %v, want nil", *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("got nil, want %v", *want)
	}
	if !almostEqual(*got, *want) {
		t.Errorf("got %v, want %v", *got, *want)
	}
}

// --- A9: median, pinning D9's method seam ---

// TestMedian_SortsItsOwnCopy implements design.md Test Contract A9: median
// takes []capacitySample -- not a pre-sorted []float64 -- and sorts its own
// copy before computing the middle value (D9). Passing samples in UNSORTED
// capacity order on purpose fails loudly if a future edit reintroduces an
// "already sorted []float64" precondition. The differing BatteryDeltaPct
// values are ignored by median itself but keep the seam's shape honest -- a
// delta-weighted sibling method would read them.
func TestMedian_SortsItsOwnCopy(t *testing.T) {
	gated := []capacitySample{
		{CapacityKWh: 64.0, BatteryDeltaPct: 20},
		{CapacityKWh: 60.0, BatteryDeltaPct: 50},
		{CapacityKWh: 62.0, BatteryDeltaPct: 16},
	}
	got := median(gated)
	want := 62.0
	if !almostEqual(got, want) {
		t.Errorf("median(unsorted) = %v, want %v", got, want)
	}
}

// --- A6-A8: packCapacityKWh, via an in-package fake packCapacityLookup ---

// fakePackCapacityLookup is an in-package fake satisfying packCapacityLookup
// (design.md D3): it returns whatever capacity/error the test case
// configures, standing in for both real implementations
// (dbStore.latestMeasuredCapacity, sessionVerifier.latestMeasuredCapacity)
// without a database.
type fakePackCapacityLookup struct {
	capacity *float64
	err      error
}

func (f fakePackCapacityLookup) latestMeasuredCapacity(ctx context.Context, teslaID int64) (*float64, error) {
	return f.capacity, f.err
}

// TestPackCapacityKWh_Table implements design.md Test Contract A6-A8: no
// measured row, a measured value, and a lookup error -- each against the
// fake, never a real database.
func TestPackCapacityKWh_Table(t *testing.T) {
	ctx := context.Background()
	const teslaID = int64(1234567890)

	// A6: no measured row (nil, nil) -- returns defaultPackCapacityKWh.
	t.Run("A6_no_measured_row_falls_back_to_default", func(t *testing.T) {
		lookup := fakePackCapacityLookup{capacity: nil, err: nil}
		got, err := packCapacityKWh(ctx, lookup, teslaID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !almostEqual(got, defaultPackCapacityKWh) {
			t.Errorf("got %v, want %v (defaultPackCapacityKWh)", got, defaultPackCapacityKWh)
		}
	})

	// A7: a measured value (ptr(70.5), nil) -- returned as-is, no rounding,
	// no recomputation.
	t.Run("A7_measured_value_returned_as_is", func(t *testing.T) {
		lookup := fakePackCapacityLookup{capacity: ptrF64(70.5), err: nil}
		got, err := packCapacityKWh(ctx, lookup, teslaID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !almostEqual(got, 70.5) {
			t.Errorf("got %v, want 70.5", got)
		}
	})

	// A8: a lookup failure (nil, error) -- a real error, never silently
	// coerced to defaultPackCapacityKWh.
	t.Run("A8_lookup_error_is_never_coerced", func(t *testing.T) {
		wantErr := errors.New("boom")
		lookup := fakePackCapacityLookup{capacity: nil, err: wantErr}
		got, err := packCapacityKWh(ctx, lookup, teslaID)
		if err == nil {
			t.Fatalf("expected error, got nil (capacity %v)", got)
		}
		if !errors.Is(err, wantErr) {
			t.Errorf("error does not wrap the fake's error: %v", err)
		}
		if got != 0 {
			t.Errorf("got %v, want 0 on error", got)
		}
	})
}
