package telemetry

import (
	"math"
	"testing"
	"time"
)

// These tests exercise deriveConsumption and dayStart, the two new pure Go
// functions introduced by telemetry-add-derived-consumption-columns (MAG-10,
// design.md D7/D8). Both are pure: no DB, no network, no live Tesla API call —
// they are the primary "Unit tests: included" surface for this change
// (tasks.md T6.1/T6.2).

// consumptionFloatTol bounds the float-division ratio assertions (km_per_pct_calc,
// estimated_range_km_calc) — per tasks.md T6.1's instruction to use a tolerance
// rather than exact equality, since these are floating-point ratios.
const consumptionFloatTol = 1e-9

// assertFloatPtr compares a *float64 result against an expected *float64,
// treating nil as a distinct, must-match state (D9: nil is a deliberate
// three-state signal, never conflated with 0).
func assertFloatPtr(t *testing.T, field string, got, want *float64) {
	t.Helper()
	switch {
	case want == nil && got == nil:
		return
	case want == nil && got != nil:
		t.Errorf("%s: want nil, got *%v", field, *got)
	case want != nil && got == nil:
		t.Errorf("%s: want *%v, got nil", field, *want)
	default:
		if math.Abs(*got-*want) > consumptionFloatTol {
			t.Errorf("%s: want ~%v, got %v", field, *want, *got)
		}
	}
}

// assertIntPtr compares a *int result against an expected *int. nil must
// match nil exactly (D9: nil vs a truthful, possibly negative, integer).
func assertIntPtr(t *testing.T, field string, got, want *int) {
	t.Helper()
	switch {
	case want == nil && got == nil:
		return
	case want == nil && got != nil:
		t.Errorf("%s: want nil, got *%v", field, *got)
	case want != nil && got == nil:
		t.Errorf("%s: want *%v, got nil", field, *want)
	default:
		if *got != *want {
			t.Errorf("%s: want *%v, got *%v", field, *want, *got)
		}
	}
}

// TestDeriveConsumption covers tasks.md T6.1(a)-(e): the five cases design.md's
// "Test Blast Radius" section calls out. CapturedDate values below are UTC-
// midnight-normalized time.Time literals, matching the dateOnly/rowToSnapshot
// convention deriveConsumption's day-count math (D8) relies on.
func TestDeriveConsumption(t *testing.T) {
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)
	day2 := day0.AddDate(0, 0, 2)
	day3 := day0.AddDate(0, 0, 3)
	day5 := day0.AddDate(0, 0, 5)

	tests := []struct {
		name string
		prev *Snapshot
		cur  Snapshot

		wantDistance    *float64
		wantBatteryUsed *int
		wantKmPerPct    *float64
		wantEstRange    *float64
		wantDays        *int
	}{
		{
			// (a) Normal drive day — mirrors the ticket's worked example:
			// 40 km / 12% -> ~3.33 km/pct -> ~333 km estimated range.
			name: "normal drive day",
			prev: &Snapshot{OdometerKm: 10000, BatteryLevelPct: 80, CapturedDate: day0},
			cur:  Snapshot{OdometerKm: 10040, BatteryLevelPct: 68, CapturedDate: day1},

			wantDistance:    ptrFloat64(40),
			wantBatteryUsed: ptrInt(12),
			wantKmPerPct:    ptrFloat64(40.0 / 12.0),
			wantEstRange:    ptrFloat64((40.0 / 12.0) * 100),
			wantDays:        ptrInt(1),
		},
		{
			// (b) Charging day — net battery INCREASE overnight (charged more than
			// driven): BatteryUsedPctCalc goes negative, both efficiency fields
			// stay nil (D2), while distance (a small drive before/after the
			// charge) is still populated, non-nil.
			name: "charging day - negative battery used, distance still populated",
			prev: &Snapshot{OdometerKm: 10040, BatteryLevelPct: 68, CapturedDate: day1},
			cur:  Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day2},

			wantDistance:    ptrFloat64(5),
			wantBatteryUsed: ptrInt(-22),
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        ptrInt(1),
		},
		{
			// (c) Parked/zero-delta day — battery level unchanged overnight
			// (idle drain exactly offset, or genuinely parked): a zero divisor
			// is division by zero, so both efficiency fields stay nil (D2).
			name: "parked day - zero battery used",
			prev: &Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day2},
			cur:  Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day3},

			wantDistance:    ptrFloat64(0),
			wantBatteryUsed: ptrInt(0),
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        ptrInt(1),
		},
		{
			// (d) Multi-day gap — a missed night means DaysSpannedCalc > 1.
			// distance/battery-used must hold the FULL multi-day total, never a
			// per-day average (D1): 80 km and 30 pct across 2 days stay 80/30,
			// not 40/15.
			name: "multi-day gap - raw total, not per-day average",
			prev: &Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day3},
			cur:  Snapshot{OdometerKm: 10125, BatteryLevelPct: 60, CapturedDate: day5},

			wantDistance:    ptrFloat64(80),
			wantBatteryUsed: ptrInt(30),
			wantKmPerPct:    ptrFloat64(80.0 / 30.0),
			wantEstRange:    ptrFloat64((80.0 / 30.0) * 100),
			wantDays:        ptrInt(2),
		},
		{
			// (e) First-ever snapshot — no predecessor: all five fields stay nil
			// (D8/D10), regardless of cur's own readings.
			name: "no predecessor - all five nil",
			prev: nil,
			cur:  Snapshot{OdometerKm: 10125, BatteryLevelPct: 60, CapturedDate: day5},

			wantDistance:    nil,
			wantBatteryUsed: nil,
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveConsumption(tc.prev, tc.cur)

			assertFloatPtr(t, "DistanceTraveledKmCalc", got.DistanceTraveledKmCalc, tc.wantDistance)
			assertIntPtr(t, "BatteryUsedPctCalc", got.BatteryUsedPctCalc, tc.wantBatteryUsed)
			assertFloatPtr(t, "KmPerPctCalc", got.KmPerPctCalc, tc.wantKmPerPct)
			assertFloatPtr(t, "EstimatedRangeKmCalc", got.EstimatedRangeKmCalc, tc.wantEstRange)
			assertIntPtr(t, "DaysSpannedCalc", got.DaysSpannedCalc, tc.wantDays)

			// EstimatedRangeKmCalc must always equal KmPerPctCalc*100 whenever both
			// are non-nil (D9's restated invariant) — checked directly against the
			// function's own output, not just against the table's expectation.
			if got.KmPerPctCalc != nil && got.EstimatedRangeKmCalc != nil {
				if math.Abs(*got.EstimatedRangeKmCalc-(*got.KmPerPctCalc*100)) > consumptionFloatTol {
					t.Errorf("EstimatedRangeKmCalc (%v) != KmPerPctCalc*100 (%v)", *got.EstimatedRangeKmCalc, *got.KmPerPctCalc*100)
				}
			}
		})
	}
}

// TestDeriveConsumption_NilPrevReturnsCurUnchanged is a focused regression for
// case (e): deriveConsumption(nil, cur) must return cur's OTHER fields
// (unrelated to the five derived ones) completely untouched — it is a
// pass-through, not a zeroing function.
func TestDeriveConsumption_NilPrevReturnsCurUnchanged(t *testing.T) {
	cur := Snapshot{
		OdometerKm:      12345.6,
		BatteryLevelPct: 42,
		ChargingState:   "Disconnected",
		CarVersion:      "2026.20.1",
	}

	got := deriveConsumption(nil, cur)

	if got.OdometerKm != cur.OdometerKm || got.BatteryLevelPct != cur.BatteryLevelPct ||
		got.ChargingState != cur.ChargingState || got.CarVersion != cur.CarVersion {
		t.Errorf("deriveConsumption(nil, cur) mutated unrelated fields: got %+v, want cur unchanged (%+v)", got, cur)
	}
	if got.DistanceTraveledKmCalc != nil || got.BatteryUsedPctCalc != nil ||
		got.KmPerPctCalc != nil || got.EstimatedRangeKmCalc != nil || got.DaysSpannedCalc != nil {
		t.Errorf("deriveConsumption(nil, cur): want all five derived fields nil, got %+v", got)
	}
}

// --- dayStart (tasks.md T6.2) ---

// TestDayStart_ComfortablyInsideLocalDay covers T6.2(a): a capture comfortably
// inside a calendar day in a non-UTC zone (America/Bogota, UTC-5) returns that
// day's LOCAL midnight as an absolute instant.
func TestDayStart_ComfortablyInsideLocalDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-15T20:00:00Z is 2026-01-15T15:00:00-05:00 in America/Bogota — the
	// same calendar day (Jan 15) in both UTC and local.
	capturedAt := time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)

	got := dayStart(capturedAt, loc)
	want := time.Date(2026, 1, 15, 0, 0, 0, 0, loc)
	if !got.Equal(want) {
		t.Errorf("dayStart(%v, %v) = %v, want %v (local midnight)", capturedAt, loc, got, want)
	}
	// dayStart returns an ABSOLUTE instant in loc, not a UTC-normalized
	// calendar date (design D7) — distinct in shape from dateOnly.
	if got.Location() != loc {
		t.Errorf("dayStart(...).Location() = %v, want %v (local-zone instant, not UTC)", got.Location(), loc)
	}
}

// TestDayStart_LocalDayBehindUTCDay covers T6.2(b): a capture whose UTC instant
// falls on one calendar day but whose LOCAL instant (negative-offset zone)
// falls on the PREVIOUS calendar day must return the LOCAL day's midnight, not
// the UTC day's — the same local-vs-UTC distinction dateOnly's own tests cover
// (dedupe_test.go), applied to dayStart's different (absolute-instant) return
// shape.
func TestDayStart_LocalDayBehindUTCDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-02T02:00:00Z is 2026-01-01T21:00:00-05:00 in America/Bogota:
	// UTC day is Jan 2, but the local day is still Jan 1.
	capturedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)

	got := dayStart(capturedAt, loc)
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, loc) // LOCAL day (Jan 1), not UTC day (Jan 2)
	if !got.Equal(want) {
		t.Errorf("dayStart(%v, %v) = %v, want %v (local day Jan 1, not UTC day Jan 2)", capturedAt, loc, got, want)
	}
	// Sanity: the wrong (UTC-day) answer would be Jan 2 local midnight —
	// explicitly assert we did NOT get that.
	wrongUTCDay := time.Date(2026, 1, 2, 0, 0, 0, 0, loc)
	if got.Equal(wrongUTCDay) {
		t.Errorf("dayStart(%v, %v) = %v, incorrectly used the UTC calendar day instead of the local one", capturedAt, loc, got)
	}
}
