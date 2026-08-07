package battery

import (
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// The derive tests exercise deriveEfficiency and socReadings fully OFFLINE and with
// zero fakes: both are pure functions over plain telemetry.Snapshot values (design.md
// D1/D1b/D2/D-ok), so there is no I/O seam to fake. Expected values are hand-computed
// from the design.md formulas below, never by calling deriveEfficiency/socReadings
// themselves, so the test actually proves the arithmetic rather than restating it.
//
// telemetry.Snapshot.OdometerKm is already kilometre-native as of RM7 tier 2
// (battery-adopt-snapshot-unit-fields design D1/D2): the fixtures below supply km
// values directly, and every expected distance/energy/Wh-per-km figure is
// hand-computed from those km values. This module — including its tests —
// performs no mile-to-km conversion of its own.

const epsilon = 1e-9

func approxEqual(a, b float64) bool {
	diff := a - b
	return diff < epsilon && diff > -epsilon
}

// snap builds a minimal telemetry.Snapshot with only the fields deriveEfficiency and
// socReadings read: OdometerKm and BatteryLevelPct, with UsableBatteryLevelPct left
// nil unless usable is non-nil.
func snap(odometerKm float64, batteryLevelPct int, usable *int) telemetry.Snapshot {
	return telemetry.Snapshot{
		OdometerKm:            odometerKm,
		BatteryLevelPct:       batteryLevelPct,
		UsableBatteryLevelPct: usable,
	}
}

func intPtr(v int) *int { return &v }

// TestDeriveEfficiency_KnownCapacity_NetConsumption covers spec.md "A vehicle with two
// or more snapshots, known capacity, and net consumption gets a computed value". Also
// serves as task 2.3's independently-derived case: the expected Wh/km below is
// hand-computed from a known km distance and known kWh, not carried over from any
// prior (miles-based) implementation.
func TestDeriveEfficiency_KnownCapacity_NetConsumption(t *testing.T) {
	start := snap(1000, 80, nil) // km
	end := snap(1100, 60, nil)   // km
	const kWhIn = 5.0
	const capacityKWh = 75.0

	// Hand-computed per design.md D1, straight from the km-native fixtures:
	// distance = 1100 - 1000 = 100 km
	// deltaSoC = socEnd - socStart = 60 - 80 = -20 (net discharge)
	// energy   = kWhIn - capacityKWh*deltaSoC/100 = 5 - 75*(-20)/100 = 5 + 15 = 20 kWh
	// WhPerKm  = energy*1000/distance = 20000 / 100 = 200
	wantDistance := 100.0
	wantEnergy := kWhIn - capacityKWh*(-20.0)/100.0
	wantWhPerKm := wantEnergy * 1000 / wantDistance
	wantFromKm := 1000.0
	wantToKm := 1100.0
	wantBatteryDeltaPct := 80.0 - 60.0 // start - end, positive = net consumption

	got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, kWhIn, capacityKWh, true)
	if !ok {
		t.Fatal("want ok=true")
	}
	if got.Approximate {
		t.Error("want Approximate=false when capacity is known")
	}
	if !approxEqual(got.WhPerKm, wantWhPerKm) {
		t.Errorf("WhPerKm: want %v, got %v", wantWhPerKm, got.WhPerKm)
	}
	if !approxEqual(got.FromKm, wantFromKm) {
		t.Errorf("FromKm: want %v, got %v", wantFromKm, got.FromKm)
	}
	if !approxEqual(got.ToKm, wantToKm) {
		t.Errorf("ToKm: want %v, got %v", wantToKm, got.ToKm)
	}
	if !approxEqual(got.BatteryDeltaPct, wantBatteryDeltaPct) {
		t.Errorf("BatteryDeltaPct: want %v, got %v", wantBatteryDeltaPct, got.BatteryDeltaPct)
	}
}

// TestDeriveEfficiency_UnknownCapacity_ApproximateTrue covers spec.md "Unknown car_type
// still returns a value, marked approximate": same snapshots/kWhIn as the known-capacity
// case, but capacityKnown=false must drop the capacity term entirely — a non-zero
// capacityKWh argument passed in must have zero effect on the result.
func TestDeriveEfficiency_UnknownCapacity_ApproximateTrue(t *testing.T) {
	start := snap(1000, 80, nil) // km
	end := snap(1100, 60, nil)   // km
	const kWhIn = 5.0
	const capacityKWh = 75.0 // must be ignored since capacityKnown=false

	// Hand-computed: energy = kWhIn alone (no capacity term); distance = 1100-1000 = 100 km.
	wantDistance := 100.0
	wantWhPerKm := kWhIn * 1000 / wantDistance

	got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, kWhIn, capacityKWh, false)
	if !ok {
		t.Fatal("want ok=true")
	}
	if !got.Approximate {
		t.Error("want Approximate=true when capacity is unknown")
	}
	if !approxEqual(got.WhPerKm, wantWhPerKm) {
		t.Errorf("WhPerKm: want %v (no capacity term subtracted), got %v", wantWhPerKm, got.WhPerKm)
	}
}

// TestDeriveEfficiency_FewerThanTwoSnapshots_NotOK covers spec.md "Fewer than two
// snapshots in the window".
func TestDeriveEfficiency_FewerThanTwoSnapshots_NotOK(t *testing.T) {
	cases := []struct {
		name      string
		snapshots []telemetry.Snapshot
	}{
		{"zero snapshots", nil},
		{"one snapshot", []telemetry.Snapshot{snap(1000, 80, nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := deriveEfficiency(tc.snapshots, 5.0, 75.0, true)
			if ok {
				t.Fatal("want ok=false")
			}
			if got != (Efficiency{}) {
				t.Errorf("want zero-value Efficiency, got %+v", got)
			}
		})
	}
}

// TestDeriveEfficiency_NonIncreasingOdometer_NotOK covers spec.md "No distance moved
// over the window".
func TestDeriveEfficiency_NonIncreasingOdometer_NotOK(t *testing.T) {
	cases := []struct {
		name       string
		startMiles float64
		endMiles   float64
	}{
		{"equal odometer", 1000, 1000},
		{"decreasing odometer", 1000, 900},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := snap(tc.startMiles, 80, nil)
			end := snap(tc.endMiles, 60, nil)

			got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, 5.0, 75.0, true)
			if ok {
				t.Fatal("want ok=false")
			}
			if got != (Efficiency{}) {
				t.Errorf("want zero-value Efficiency, got %+v", got)
			}
		})
	}
}

// TestDeriveEfficiency_NetChargeExceedsConsumption_NotOK covers spec.md "Net charge
// exceeds consumption over the window": known capacity, small kWhIn, large positive
// deltaSoC (net charge) drives energy <= 0.
func TestDeriveEfficiency_NetChargeExceedsConsumption_NotOK(t *testing.T) {
	start := snap(1000, 30, nil)
	end := snap(1100, 90, nil) // socEnd > socStart: net charge
	const kWhIn = 10.0
	const capacityKWh = 75.0

	// Hand-computed: deltaSoC = 90-30 = 60; energy = 10 - 75*60/100 = 10 - 45 = -35 <= 0.
	got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, kWhIn, capacityKWh, true)
	if ok {
		t.Fatal("want ok=false")
	}
	if got != (Efficiency{}) {
		t.Errorf("want zero-value Efficiency, got %+v", got)
	}
}

// TestSocReadings_UsableAtBothEndpoints covers spec.md "Usable battery level is used
// when present at both window endpoints".
func TestSocReadings_UsableAtBothEndpoints(t *testing.T) {
	start := snap(1000, 80, intPtr(78)) // nominal 80, usable 78
	end := snap(1100, 60, intPtr(58))   // nominal 60, usable 58

	socStart, socEnd := socReadings(start, end)
	if socStart != 78 {
		t.Errorf("socStart: want usable 78, got %v", socStart)
	}
	if socEnd != 58 {
		t.Errorf("socEnd: want usable 58, got %v", socEnd)
	}
}

// TestSocReadings_FallsBackWhenEitherEndpointNil covers spec.md "Usable battery level
// is used only when present at both window endpoints" — one subtest per case where
// UsableBatteryLevelPct is nil at start only, end only, or both: socReadings must fall
// back to BatteryLevelPct at BOTH endpoints in every case, never mixing usable at one
// endpoint with nominal at the other (design.md D2 phantom-ΔSoC guard).
func TestSocReadings_FallsBackWhenEitherEndpointNil(t *testing.T) {
	cases := []struct {
		name        string
		startUsable *int
		endUsable   *int
	}{
		{"nil at start only", nil, intPtr(58)},
		{"nil at end only", intPtr(78), nil},
		{"nil at both", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := snap(1000, 80, tc.startUsable)
			end := snap(1100, 60, tc.endUsable)

			socStart, socEnd := socReadings(start, end)
			if socStart != 80 {
				t.Errorf("socStart: want nominal 80 (fallback), got %v", socStart)
			}
			if socEnd != 60 {
				t.Errorf("socEnd: want nominal 60 (fallback), got %v", socEnd)
			}
		})
	}
}

// TestDeriveEfficiency_BatteryDeltaPctSignConvention locks in the intentional sign
// flip between deltaSoC (energy formula, positive = net charge) and the reported
// Efficiency.BatteryDeltaPct field (start − end, negative = net charge) — design.md
// T3.1's acceptance note. Both directions are asserted so a future refactor cannot
// silently unify or invert the convention.
func TestDeriveEfficiency_BatteryDeltaPctSignConvention(t *testing.T) {
	t.Run("net charge yields negative BatteryDeltaPct", func(t *testing.T) {
		start := snap(1000, 30, nil)
		end := snap(1100, 70, nil) // socEnd > socStart: net charge
		// capacityKnown=false so the capacity term cannot flip a positive kWhIn to
		// non-positive energy regardless of the (irrelevant here) deltaSoC magnitude.
		got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, 5.0, 75.0, false)
		if !ok {
			t.Fatal("want ok=true")
		}
		wantBatteryDeltaPct := 30.0 - 70.0 // start - end = -40, negative
		if !approxEqual(got.BatteryDeltaPct, wantBatteryDeltaPct) {
			t.Errorf("BatteryDeltaPct: want %v (negative, net charge), got %v", wantBatteryDeltaPct, got.BatteryDeltaPct)
		}
	})

	t.Run("net consumption yields positive BatteryDeltaPct", func(t *testing.T) {
		start := snap(1000, 70, nil)
		end := snap(1100, 30, nil) // socEnd < socStart: net consumption
		got, ok := deriveEfficiency([]telemetry.Snapshot{start, end}, 5.0, 75.0, false)
		if !ok {
			t.Fatal("want ok=true")
		}
		wantBatteryDeltaPct := 70.0 - 30.0 // start - end = 40, positive
		if !approxEqual(got.BatteryDeltaPct, wantBatteryDeltaPct) {
			t.Errorf("BatteryDeltaPct: want %v (positive, net consumption), got %v", wantBatteryDeltaPct, got.BatteryDeltaPct)
		}
	})
}
