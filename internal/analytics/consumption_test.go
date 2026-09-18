package analytics

import (
	"math"
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// These tests exercise deriveConsumption, moved into this module from
// internal/telemetry/service.go by RM29-telemetry-drop-derived-columns (MAG-26
// tier 4, design.md D1/D5). TestDeriveConsumption and
// TestDeriveConsumption_NilPrevReturnsCurUnchanged below are PORTED verbatim
// from internal/telemetry/consumption_test.go, with every expected value
// UNCHANGED (roadmap D10 — this is a characterization move, not a rewrite;
// the assertions moved, they were never re-derived from the new
// implementation). TestDeriveConsumption_MultiDayGap and
// TestDeriveConsumption_ZeroDivisorGuard are new, authored directly from
// design.md's Test Contract Fixtures D and E (fixed before this file was
// written, per ai/go-conventions.md "author expected values up front").
//
// TestDayStart_ComfortablyInsideLocalDay and TestDayStart_LocalDayBehindUTCDay
// are NOT ported: dayStart is deleted by telemetry's wave 4 (design.md D8),
// and the same-day-recapture guarantee it protected is re-asserted, in this
// change, by the telemetry DB-integration test
// TestReader_SnapshotPrecedingDay_SameDayRecaptureNotItsOwnPredecessor against
// SnapshotPrecedingDay's captured_date < @day predicate (design.md D2), not by
// anything in this module.
//
// Per the Test-Execution-Policy, these tests are written but NOT run by the
// worker; go vet ./... compiles them as a signature-drift signal. The owner
// runs `go test ./internal/analytics/...` and reports the result.

// consumptionFloatTol bounds the float-division ratio assertions
// (km_per_pct_calc, estimated_range_km_calc) — ported unchanged from
// telemetry's own tolerance (telemetry-add-derived-consumption-columns tasks.md
// T6.1).
const consumptionFloatTol = 1e-9

// assertFloatPtr compares a *float64 result against an expected *float64,
// treating nil as a distinct, must-match state (D9: nil is a deliberate
// three-state signal, never conflated with 0). Ported unchanged from
// telemetry/consumption_test.go.
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
// Ported unchanged from telemetry/consumption_test.go.
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

// TestDeriveConsumption is PORTED from internal/telemetry/consumption_test.go
// (cases (a)-(e)). Only the receiving type changed: deriveConsumption now
// returns a consumptionCalc value instead of mutating a Snapshot, since
// Snapshot no longer carries the five fields (design.md D5, D-hard-constraint
// of this dispatch: fixtures below use ONLY raw Snapshot fields — OdometerKm,
// BatteryLevelPct, CapturedDate — never the deleted _calc fields).
//
// MAG-81 added the chargePct argument and the ConsumedPct field. Cases (a)-(e)
// keep their original expected values by passing chargePct 0, which makes
// ConsumedPct identical to BatteryUsedPctCalc and the divisor identical to
// what it was — so the characterization bar (roadmap D10) still holds for
// every input the old signature could express. Cases (f)-(g) are new and
// cover what the old signature could NOT express: a day whose charge was
// actually recorded.
func TestDeriveConsumption(t *testing.T) {
	day0 := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)
	day2 := day0.AddDate(0, 0, 2)
	day3 := day0.AddDate(0, 0, 3)
	day5 := day0.AddDate(0, 0, 5)

	tests := []struct {
		name      string
		prev      *telemetry.Snapshot
		cur       telemetry.Snapshot
		chargePct float64

		wantDistance    *float64
		wantBatteryUsed *int
		wantConsumed    *float64
		wantKmPerPct    *float64
		wantEstRange    *float64
		wantDays        *int
	}{
		{
			// (a) Normal drive day — mirrors the ticket's worked example:
			// 40 km / 12% -> ~3.33 km/pct -> ~333 km estimated range.
			name: "normal drive day",
			prev: &telemetry.Snapshot{OdometerKm: 10000, BatteryLevelPct: 80, CapturedDate: day0},
			cur:  telemetry.Snapshot{OdometerKm: 10040, BatteryLevelPct: 68, CapturedDate: day1},

			wantDistance:    fp(40),
			wantBatteryUsed: intPtr(12),
			wantConsumed:    fp(12),
			wantKmPerPct:    fp(40.0 / 12.0),
			wantEstRange:    fp((40.0 / 12.0) * 100),
			wantDays:        intPtr(1),
		},
		{
			// (b) Charging day with NO charge record — net battery INCREASE
			// overnight (charged more than driven) and nothing to correct it
			// with: ConsumedPct stays negative, both efficiency fields stay nil
			// (D2), while distance (a small drive before/after the charge) is
			// still populated, non-nil.
			//
			// MAG-81: this is the case that survives the fix. Without a charge
			// record there is no truthful divisor to recover — this day is a
			// charge GAP, which is what deriveVehicleMetrics flags. Case (f)
			// below is the same shape WITH the record.
			name: "charging day, charge not recorded - negative consumed, distance still populated",
			prev: &telemetry.Snapshot{OdometerKm: 10040, BatteryLevelPct: 68, CapturedDate: day1},
			cur:  telemetry.Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day2},

			wantDistance:    fp(5),
			wantBatteryUsed: intPtr(-22),
			wantConsumed:    fp(-22),
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        intPtr(1),
		},
		{
			// (c) Parked/zero-delta day — battery level unchanged overnight
			// (idle drain exactly offset, or genuinely parked): a zero divisor
			// is division by zero, so both efficiency fields stay nil (D2).
			name: "parked day - zero battery used",
			prev: &telemetry.Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day2},
			cur:  telemetry.Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day3},

			wantDistance:    fp(0),
			wantBatteryUsed: intPtr(0),
			wantConsumed:    fp(0),
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        intPtr(1),
		},
		{
			// (d) Multi-day gap — a missed night means DaysSpannedCalc > 1.
			// distance/battery-used must hold the FULL multi-day total, never a
			// per-day average (D1): 80 km and 30 pct across 2 days stay 80/30,
			// not 40/15.
			name: "multi-day gap - raw total, not per-day average",
			prev: &telemetry.Snapshot{OdometerKm: 10045, BatteryLevelPct: 90, CapturedDate: day3},
			cur:  telemetry.Snapshot{OdometerKm: 10125, BatteryLevelPct: 60, CapturedDate: day5},

			wantDistance:    fp(80),
			wantBatteryUsed: intPtr(30),
			wantConsumed:    fp(30),
			wantKmPerPct:    fp(80.0 / 30.0),
			wantEstRange:    fp((80.0 / 30.0) * 100),
			wantDays:        intPtr(2),
		},
		{
			// (e) First-ever snapshot — no predecessor: all six fields stay nil
			// (D8/D10), regardless of cur's own readings AND regardless of
			// chargePct — a charge with no predecessor day has nothing to
			// correct, so ConsumedPct stays nil rather than becoming the bare
			// charge total.
			name:      "no predecessor - all six nil",
			prev:      nil,
			cur:       telemetry.Snapshot{OdometerKm: 10125, BatteryLevelPct: 60, CapturedDate: day5},
			chargePct: 30,

			wantDistance:    nil,
			wantBatteryUsed: nil,
			wantConsumed:    nil,
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        nil,
		},
		{
			// (f) MAG-81, the bug this change fixes: the vehicle drove AND
			// charged, and the charge IS recorded. The raw battery figure is
			// negative (the pack ended fuller than it started), but the day
			// really consumed 17% — 30.28 km / 17% ≈ 1.78 km per point.
			//
			// Numbers are the owner's real 2026-09-14 row: before the fix this
			// day stored NULL efficiency, because the divisor was the raw -40
			// instead of the corrected 17.
			name:      "drove and charged, charge recorded - efficiency now computable",
			prev:      &telemetry.Snapshot{OdometerKm: 4544.121, BatteryLevelPct: 52, CapturedDate: day1},
			cur:       telemetry.Snapshot{OdometerKm: 4574.396, BatteryLevelPct: 92, CapturedDate: day2},
			chargePct: 57,

			wantDistance:    fp(4574.396 - 4544.121),
			wantBatteryUsed: intPtr(-40),
			wantConsumed:    fp(17),
			wantKmPerPct:    fp((4574.396 - 4544.121) / 17.0),
			wantEstRange:    fp(((4574.396 - 4544.121) / 17.0) * 100),
			wantDays:        intPtr(1),
		},
		{
			// (g) Charge recorded but the day still ended net-positive — the
			// owner topped the pack up beyond what the driving spent. The
			// correction applies, ConsumedPct is still <= 0, and the divisor
			// guard still holds: no truthful ratio exists for a day that gained
			// charge on balance, so both efficiency fields stay nil.
			//
			// This is the guard's remaining job after MAG-81. It must not be
			// "fixed" by taking an absolute value or clamping to a floor.
			name:      "charged more than driven, charge recorded - guard still holds",
			prev:      &telemetry.Snapshot{OdometerKm: 10000, BatteryLevelPct: 30, CapturedDate: day1},
			cur:       telemetry.Snapshot{OdometerKm: 10012, BatteryLevelPct: 80, CapturedDate: day2},
			chargePct: 45,

			wantDistance:    fp(12),
			wantBatteryUsed: intPtr(-50),
			wantConsumed:    fp(-5),
			wantKmPerPct:    nil,
			wantEstRange:    nil,
			wantDays:        intPtr(1),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := deriveConsumption(tc.prev, tc.cur, tc.chargePct)

			assertFloatPtr(t, "DistanceTraveledKmCalc", got.DistanceTraveledKmCalc, tc.wantDistance)
			assertIntPtr(t, "BatteryUsedPctCalc", got.BatteryUsedPctCalc, tc.wantBatteryUsed)
			assertFloatPtr(t, "ConsumedPct", got.ConsumedPct, tc.wantConsumed)
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

			// MAG-81's invariant: the efficiency divides by ConsumedPct, the
			// CORRECTED figure, never by the raw BatteryUsedPctCalc. Checked
			// against the function's own two outputs so a future edit cannot
			// swap the divisor back without failing here — case (f)'s negative
			// BatteryUsedPctCalc makes that swap impossible to miss.
			if got.KmPerPctCalc != nil {
				want := *got.DistanceTraveledKmCalc / *got.ConsumedPct
				if math.Abs(*got.KmPerPctCalc-want) > consumptionFloatTol {
					t.Errorf("KmPerPctCalc (%v) != DistanceTraveledKmCalc/ConsumedPct (%v)", *got.KmPerPctCalc, want)
				}
			}

			// The divisor guard, stated as a rule rather than per-case: the two
			// efficiency fields exist if and only if ConsumedPct > 0.
			if wantEfficiency := got.ConsumedPct != nil && *got.ConsumedPct > 0; wantEfficiency != (got.KmPerPctCalc != nil) {
				t.Errorf("efficiency presence mismatch: ConsumedPct=%v, KmPerPctCalc=%v", got.ConsumedPct, got.KmPerPctCalc)
			}
		})
	}
}

// TestDeriveConsumption_NilPrevReturnsCurUnchanged is PORTED from
// internal/telemetry/consumption_test.go, adapted only to the consumptionCalc
// return shape (there is no "cur" to mutate/leave-unchanged any more — the
// original's point, that a nil prev returns an all-nil result without
// touching anything else, is now expressed by asserting the zero
// consumptionCalc value directly).
func TestDeriveConsumption_NilPrevReturnsCurUnchanged(t *testing.T) {
	cur := telemetry.Snapshot{
		OdometerKm:      12345.6,
		BatteryLevelPct: 42,
		ChargingState:   "Disconnected",
		CarVersion:      "2026.20.1",
	}

	got := deriveConsumption(nil, cur, 0)

	if got.DistanceTraveledKmCalc != nil || got.BatteryUsedPctCalc != nil ||
		got.ConsumedPct != nil ||
		got.KmPerPctCalc != nil || got.EstimatedRangeKmCalc != nil || got.DaysSpannedCalc != nil {
		t.Errorf("deriveConsumption(nil, cur, 0): want all six derived fields nil, got %+v", got)
	}
	// RM50-analytics-add-tire-pressure-variance design.md D2, condition 1 —
	// no predecessor at all, so all four tyre-pressure deltas stay nil too,
	// regardless of cur's own TPMS readings.
	if got.TpmsPressureFLPSIDeltaCalc != nil || got.TpmsPressureFRPSIDeltaCalc != nil ||
		got.TpmsPressureRLPSIDeltaCalc != nil || got.TpmsPressureRRPSIDeltaCalc != nil {
		t.Errorf("deriveConsumption(nil, cur): want all four TPMS deltas nil, got %+v", got)
	}
}

// TestDeriveConsumption_TpmsDeltas_AllFourWheelsPresent covers design.md's
// Test Contract Fixture 1 — a predecessor exists and both days report all
// four wheels. Expected deltas are cur minus prev (not prev minus cur), and
// include at least one negative delta to prove sign is not clamped.
func TestDeriveConsumption_TpmsDeltas_AllFourWheelsPresent(t *testing.T) {
	prev := telemetry.Snapshot{
		OdometerKm:        1000.0,
		BatteryLevelPct:   80,
		CapturedDate:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(35.0),
		TpmsPressureFRPSI: fp(35.5),
		TpmsPressureRLPSI: fp(36.0),
		TpmsPressureRRPSI: fp(36.5),
	}
	cur := telemetry.Snapshot{
		OdometerKm:        1050.0,
		BatteryLevelPct:   70,
		CapturedDate:      time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(38.5), // +3.5
		TpmsPressureFRPSI: fp(34.0), // -1.5 (negative delta -- sign not clamped)
		TpmsPressureRLPSI: fp(36.0), // 0.0
		TpmsPressureRRPSI: fp(40.0), // +3.5
	}

	got := deriveConsumption(&prev, cur, 0)

	assertFloatPtr(t, "TpmsPressureFLPSIDeltaCalc", got.TpmsPressureFLPSIDeltaCalc, fp(3.5))
	assertFloatPtr(t, "TpmsPressureFRPSIDeltaCalc", got.TpmsPressureFRPSIDeltaCalc, fp(-1.5))
	assertFloatPtr(t, "TpmsPressureRLPSIDeltaCalc", got.TpmsPressureRLPSIDeltaCalc, fp(0.0))
	assertFloatPtr(t, "TpmsPressureRRPSIDeltaCalc", got.TpmsPressureRRPSIDeltaCalc, fp(3.5))
}

// TestDeriveConsumption_TpmsDeltas_WheelAbsentOnCur covers design.md's Test
// Contract Fixture 3 — a predecessor exists, but one wheel's reading is
// absent on cur. That wheel's delta must stay nil; the other three, present
// on both days, compute normally.
func TestDeriveConsumption_TpmsDeltas_WheelAbsentOnCur(t *testing.T) {
	prev := telemetry.Snapshot{
		OdometerKm:        1000.0,
		BatteryLevelPct:   80,
		CapturedDate:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(35.0),
		TpmsPressureFRPSI: fp(35.5),
		TpmsPressureRLPSI: fp(35.1),
		TpmsPressureRRPSI: fp(35.3),
	}
	cur := telemetry.Snapshot{
		OdometerKm:        1050.0,
		BatteryLevelPct:   70,
		CapturedDate:      time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(38.0),
		TpmsPressureFRPSI: fp(36.0),
		TpmsPressureRLPSI: nil, // absent this capture -- must stay nil, never 0
		TpmsPressureRRPSI: fp(38.3),
	}

	got := deriveConsumption(&prev, cur, 0)

	assertFloatPtr(t, "TpmsPressureFLPSIDeltaCalc", got.TpmsPressureFLPSIDeltaCalc, fp(3.0))
	assertFloatPtr(t, "TpmsPressureFRPSIDeltaCalc", got.TpmsPressureFRPSIDeltaCalc, fp(0.5))
	assertFloatPtr(t, "TpmsPressureRLPSIDeltaCalc", got.TpmsPressureRLPSIDeltaCalc, nil)
	assertFloatPtr(t, "TpmsPressureRRPSIDeltaCalc", got.TpmsPressureRRPSIDeltaCalc, fp(3.0))
}

// TestDeriveConsumption_TpmsDeltas_WheelAbsentOnPrev covers design.md's Test
// Contract Fixture 4 — a predecessor exists, but one wheel's reading is
// absent on prev instead of cur. Proves the guard checks BOTH operands, not
// only cur's.
func TestDeriveConsumption_TpmsDeltas_WheelAbsentOnPrev(t *testing.T) {
	prev := telemetry.Snapshot{
		OdometerKm:        1000.0,
		BatteryLevelPct:   80,
		CapturedDate:      time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(35.0),
		TpmsPressureFRPSI: fp(35.5),
		TpmsPressureRLPSI: fp(35.1),
		TpmsPressureRRPSI: nil, // absent on prev -- must stay nil, never 0
	}
	cur := telemetry.Snapshot{
		OdometerKm:        1050.0,
		BatteryLevelPct:   70,
		CapturedDate:      time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		TpmsPressureFLPSI: fp(38.0),
		TpmsPressureFRPSI: fp(36.0),
		TpmsPressureRLPSI: fp(38.1),
		TpmsPressureRRPSI: fp(40.3),
	}

	got := deriveConsumption(&prev, cur, 0)

	assertFloatPtr(t, "TpmsPressureFLPSIDeltaCalc", got.TpmsPressureFLPSIDeltaCalc, fp(3.0))
	assertFloatPtr(t, "TpmsPressureFRPSIDeltaCalc", got.TpmsPressureFRPSIDeltaCalc, fp(0.5))
	assertFloatPtr(t, "TpmsPressureRLPSIDeltaCalc", got.TpmsPressureRLPSIDeltaCalc, fp(3.0))
	assertFloatPtr(t, "TpmsPressureRRPSIDeltaCalc", got.TpmsPressureRRPSIDeltaCalc, nil)
}

// TestDeriveConsumption_MultiDayGap covers design.md's Test Contract Fixture D
// — a seven-day capture gap. DaysSpannedCalc must be 7, not 1: an
// implementation that assumed one day, or that took the window's width
// instead of the true predecessor span, fails here.
func TestDeriveConsumption_MultiDayGap(t *testing.T) {
	prev := telemetry.Snapshot{
		OdometerKm:      1000.0,
		BatteryLevelPct: 90,
		CapturedDate:    time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
	}
	cur := telemetry.Snapshot{
		OdometerKm:      1210.0,
		BatteryLevelPct: 55,
		CapturedDate:    time.Date(2026, 8, 8, 0, 0, 0, 0, time.UTC),
	}

	got := deriveConsumption(&prev, cur, 0)

	assertFloatPtr(t, "DistanceTraveledKmCalc", got.DistanceTraveledKmCalc, fp(210.0))
	assertIntPtr(t, "BatteryUsedPctCalc", got.BatteryUsedPctCalc, intPtr(35))
	assertIntPtr(t, "DaysSpannedCalc", got.DaysSpannedCalc, intPtr(7))
	assertFloatPtr(t, "KmPerPctCalc", got.KmPerPctCalc, fp(6.0))
	assertFloatPtr(t, "EstimatedRangeKmCalc", got.EstimatedRangeKmCalc, fp(600.0))
}

// TestDeriveConsumption_ZeroDivisorGuard covers design.md's Test Contract
// Fixture E — a parked day with zero battery change. DistanceTraveledKmCalc
// and BatteryUsedPctCalc must be present (non-nil) as truthful zeros, while
// KmPerPctCalc/EstimatedRangeKmCalc stay nil: the guard is batteryUsed > 0, so
// zero is excluded exactly like a negative value — the boundary Fixture B's
// -45 (in TestDeriveConsumption case (b)-style negatives) does not reach.
func TestDeriveConsumption_ZeroDivisorGuard(t *testing.T) {
	prev := telemetry.Snapshot{
		OdometerKm:      3000.0,
		BatteryLevelPct: 70,
		CapturedDate:    time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
	}
	cur := telemetry.Snapshot{
		OdometerKm:      3000.0,
		BatteryLevelPct: 70,
		CapturedDate:    time.Date(2026, 8, 16, 0, 0, 0, 0, time.UTC),
	}

	got := deriveConsumption(&prev, cur, 0)

	if got.DistanceTraveledKmCalc == nil {
		t.Error("DistanceTraveledKmCalc: want non-nil (a truthful 0.0), got nil")
	} else {
		assertFloatPtr(t, "DistanceTraveledKmCalc", got.DistanceTraveledKmCalc, fp(0.0))
	}
	if got.BatteryUsedPctCalc == nil {
		t.Error("BatteryUsedPctCalc: want non-nil (a truthful 0), got nil")
	} else {
		assertIntPtr(t, "BatteryUsedPctCalc", got.BatteryUsedPctCalc, intPtr(0))
	}
	assertIntPtr(t, "DaysSpannedCalc", got.DaysSpannedCalc, intPtr(1))
	// MAG-81: the divisor is ConsumedPct now, so the guard is asserted on the
	// figure that actually divides. With no charge to correct it, it equals
	// the truthful 0 BatteryUsedPctCalc reports above.
	assertFloatPtr(t, "ConsumedPct", got.ConsumedPct, fp(0.0))
	if got.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil (divisor 0 is not > 0), got %v", *got.KmPerPctCalc)
	}
	if got.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil (same guard), got %v", *got.EstimatedRangeKmCalc)
	}
}
