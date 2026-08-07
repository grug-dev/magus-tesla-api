package telemetry

import (
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// snapshot_from_test.go covers tasks 5.2 and 5.3 of telemetry-store-display-units.
// snapshotFrom is the write-path conversion boundary (design D3): it maps a
// *tesla.VehicleDataTesla into a Snapshot by calling the tesla adapter's Km()/PSI()
// companions, so all conversion arithmetic lives in internal/tesla, never here.
//
// This file replaces the deleted tpms_test.go, which tested a Snapshot-level
// TpmsPressure*PSI() companion method and a *float64 bar field — both removed by
// design D4 (the renamed TpmsPressureFLPSI field collides with the method name that
// used to exist; the package cannot compile with both). That test's purpose (proving
// the NULL-vs-zero invariant) is carried forward here, at snapshotFrom, and in
// TestTPMS_NilRoundTrip (db_tpms_integration_test.go) for the read-path half that
// snapshotFrom itself cannot produce — see TestSnapshotFrom_TPMS_UnreportedVsZero
// below for why.

// TestSnapshotFrom_ConvertsToDisplayUnits (task 5.2) asserts that a known miles
// odometer/range and bar TPMS reading produce the correct converted km/PSI values on
// the resulting Snapshot. Expected values are literals, not milesToKm/barToPSI —
// those constants no longer exist in this module (task 3.3); asserting against the
// same factor the conversion uses would pin the field wiring but not the factor
// itself. Matches the house style in internal/tesla's TestMetricCompanions and
// TestTpmsPressurePSICompanions (same input values, same expected literals).
func TestSnapshotFrom_ConvertsToDisplayUnits(t *testing.T) {
	data := &tesla.VehicleDataTesla{ID: 42}
	data.ChargeState.BatteryRange = 50     // -> 80.4672 km
	data.VehicleState.Odometer = 12000     // -> 19312.128 km
	data.VehicleState.TpmsPressureFL = 2.9 // -> 42.0609439417 PSI
	data.VehicleState.TpmsPressureFR = 2.8 // -> 40.6105665644 PSI
	data.VehicleState.TpmsPressureRL = 2.7 // -> 39.1601891871 PSI
	data.VehicleState.TpmsPressureRR = 2.6 // -> 37.7098118098 PSI
	data.ClimateState.InsideTemp = 21.5
	data.ClimateState.OutsideTemp = 9.0

	captured := time.Date(2026, 8, 7, 3, 30, 0, 0, time.UTC)
	snap := snapshotFrom(uuid.New(), 42, captured, time.UTC, data, []byte(`{}`))

	const eps = 1e-9
	if math.Abs(snap.BatteryRangeKm-80.4672) > eps {
		t.Errorf("BatteryRangeKm: want 80.4672, got %v", snap.BatteryRangeKm)
	}
	if math.Abs(snap.OdometerKm-19312.128) > eps {
		t.Errorf("OdometerKm: want 19312.128, got %v", snap.OdometerKm)
	}
	// Temperature is assigned straight from the DTO — no conversion (task 3.6): the
	// Fleet API already reports Celsius.
	if snap.InsideTempC != 21.5 {
		t.Errorf("InsideTempC: want 21.5 unconverted, got %v", snap.InsideTempC)
	}
	if snap.OutsideTempC != 9.0 {
		t.Errorf("OutsideTempC: want 9.0 unconverted, got %v", snap.OutsideTempC)
	}

	if snap.TpmsPressureFLPSI == nil || math.Abs(*snap.TpmsPressureFLPSI-42.0609439417) > eps {
		t.Errorf("TpmsPressureFLPSI: want 42.0609439417, got %v", snap.TpmsPressureFLPSI)
	}
	if snap.TpmsPressureFRPSI == nil || math.Abs(*snap.TpmsPressureFRPSI-40.6105665644) > eps {
		t.Errorf("TpmsPressureFRPSI: want 40.6105665644, got %v", snap.TpmsPressureFRPSI)
	}
	if snap.TpmsPressureRLPSI == nil || math.Abs(*snap.TpmsPressureRLPSI-39.1601891871) > eps {
		t.Errorf("TpmsPressureRLPSI: want 39.1601891871, got %v", snap.TpmsPressureRLPSI)
	}
	if snap.TpmsPressureRRPSI == nil || math.Abs(*snap.TpmsPressureRRPSI-37.7098118098) > eps {
		t.Errorf("TpmsPressureRRPSI: want 37.7098118098, got %v", snap.TpmsPressureRRPSI)
	}
}

// TestSnapshotFrom_TPMSZeroBarIsNonNilZeroPSI (task 5.3, the provable half) asserts
// the D12/DSA3 half of the NULL-vs-zero invariant that snapshotFrom can actually
// demonstrate: a truthfully reported 0.0 bar reading (e.g. a flat tire) converts to a
// non-nil 0.0 PSI on the resulting Snapshot. The conversion happens BEFORE ptr()
// wraps (design D3), so a real zero is never mistaken for "not reported".
func TestSnapshotFrom_TPMSZeroBarIsNonNilZeroPSI(t *testing.T) {
	data := &tesla.VehicleDataTesla{ID: 1}
	data.VehicleState.TpmsPressureFL = 0.0 // a genuine flat-tire reading, not "absent"

	snap := snapshotFrom(uuid.New(), 1, time.Now().UTC(), time.UTC, data, []byte(`{}`))

	if snap.TpmsPressureFLPSI == nil {
		t.Fatal("TpmsPressureFLPSI: want non-nil *0.0 for a truthful 0.0 bar reading, got nil — D12 violated")
	}
	if *snap.TpmsPressureFLPSI != 0.0 {
		t.Errorf("TpmsPressureFLPSI: want *0.0, got *%v", *snap.TpmsPressureFLPSI)
	}
}

// TestSnapshotFrom_TPMS_UnreportedVsZero documents and proves the OTHER half of task
// 5.3's stated invariant ("an unreported TPMS reading stays nil through
// snapshotFrom") — and why it is proven at a different layer than the write path.
//
// tesla.VehicleStateTesla's four TPMS fields are plain float64, not *float64 (see
// internal/tesla/types.go and the archived design doc for
// telemetry-add-tire-pressure-columns, Risks section — this is a pre-existing,
// documented limitation, not something this change introduces). Go's JSON
// unmarshaling maps a missing or null "tpms_pressure_fl" key to the float64 zero
// value, identically to a real 0.0 reading. There is therefore no DTO state
// representing "unreported" that is distinguishable from "reported 0.0" — snapshotFrom
// ALWAYS produces a non-nil *float64 PSI value from any *tesla.VehicleDataTesla; it
// cannot structurally return nil for these four fields, and asserting otherwise here
// would be a false assertion about this codebase (exactly what the pipeline's
// anti-gaming rule forbids).
//
// The nil case genuinely exists and IS exercised, just on the READ path: a row
// captured before the TPMS columns existed (pre-migration) has SQL NULL in these
// columns, and rowToSnapshot (mapping.go) maps that to a nil *float64 — proven
// end-to-end through the real DB by TestTPMS_NilRoundTrip in
// db_tpms_integration_test.go, which constructs a Snapshot with nil TPMS fields
// directly (bypassing snapshotFrom, exactly because snapshotFrom cannot produce that
// state) and asserts the round-trip stays nil. This test exists so that boundary is
// explicit and searchable, not silently missing.
func TestSnapshotFrom_TPMS_UnreportedVsZero(t *testing.T) {
	unreported := &tesla.VehicleDataTesla{ID: 2} // TpmsPressureFL defaults to 0.0 — Go cannot tell "absent" from "reported 0.0" here
	reportedZero := &tesla.VehicleDataTesla{ID: 3}
	reportedZero.VehicleState.TpmsPressureFL = 0.0

	got1 := snapshotFrom(uuid.New(), 2, time.Now().UTC(), time.UTC, unreported, []byte(`{}`))
	got2 := snapshotFrom(uuid.New(), 3, time.Now().UTC(), time.UTC, reportedZero, []byte(`{}`))

	if got1.TpmsPressureFLPSI == nil || got2.TpmsPressureFLPSI == nil {
		t.Fatal("both must be non-nil *0.0 — snapshotFrom cannot distinguish unreported from a truthful zero given the current tesla.VehicleStateTesla DTO shape")
	}
	if *got1.TpmsPressureFLPSI != 0.0 || *got2.TpmsPressureFLPSI != 0.0 {
		t.Errorf("want both *0.0, got %v and %v", *got1.TpmsPressureFLPSI, *got2.TpmsPressureFLPSI)
	}
}
