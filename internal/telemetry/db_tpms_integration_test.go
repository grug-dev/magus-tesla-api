package telemetry

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the four TPMS pressure columns added by migration
// 20260802000001_add_tpms_pressure_columns.sql. They require a running Postgres
// provisioned by TestMain (see testdb_test.go — auto-provisioned via testcontainers-go
// when DATABASE_URL is unset). go test ./... is green with no manual DB setup as long
// as Docker is running locally (ai/go-conventions.md §persistence).
//
// Coverage (T6.1 per tasks.md):
//   (a) Non-nil round-trip: non-zero values survive insert → read faithfully.
//   (b) Nil round-trip: nil → SQL NULL → nil (not zero).
//   (c) Zero-value non-nil: ptr(0.0) → non-NULL *0.0 (D12/DSA3 truthful zero).
//   (d) PSI companion nil-safety: nil field → companion returns nil.
//   (e) PSI companion conversion: non-nil bar field → correct PSI (within 1e-5).
//
// No live Tesla API call fires in any of these tests.

const tpmsFloatTol = 1e-5 // float32→float64 widening may lose sub-1e-5 precision

// TestTPMS_NonNilRoundTrip verifies that all four TPMS fields with non-zero values
// round-trip faithfully through the store → DB → read path (T6.1a).
func TestTPMS_NonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930001)
	cleanupVehicle(t, pool, accountID, teslaID)

	fl, fr, rl, rr := 2.5, 2.6, 2.4, 2.5
	snap := Snapshot{
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     time.Now().UTC().Truncate(time.Microsecond),
		ChargingState:  "Disconnected",
		CarVersion:     "2026.20.1",
		RawData:        []byte(`{"vehicle_state":{"tpms_pressure_fl":2.5,"tpms_pressure_fr":2.6,"tpms_pressure_rl":2.4,"tpms_pressure_rr":2.5}}`),
		TpmsPressureFL: &fl,
		TpmsPressureFR: &fr,
		TpmsPressureRL: &rl,
		TpmsPressureRR: &rr,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]

	// (a) All four must be non-nil and within float tolerance (float32 round-trip).
	if s.TpmsPressureFL == nil {
		t.Fatal("TpmsPressureFL: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureFL-fl) > tpmsFloatTol {
		t.Errorf("TpmsPressureFL: want ~%v, got %v", fl, *s.TpmsPressureFL)
	}
	if s.TpmsPressureFR == nil {
		t.Fatal("TpmsPressureFR: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureFR-fr) > tpmsFloatTol {
		t.Errorf("TpmsPressureFR: want ~%v, got %v", fr, *s.TpmsPressureFR)
	}
	if s.TpmsPressureRL == nil {
		t.Fatal("TpmsPressureRL: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureRL-rl) > tpmsFloatTol {
		t.Errorf("TpmsPressureRL: want ~%v, got %v", rl, *s.TpmsPressureRL)
	}
	if s.TpmsPressureRR == nil {
		t.Fatal("TpmsPressureRR: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureRR-rr) > tpmsFloatTol {
		t.Errorf("TpmsPressureRR: want ~%v, got %v", rr, *s.TpmsPressureRR)
	}
}

// TestTPMS_NilRoundTrip verifies that nil TPMS fields store as SQL NULL and come
// back as nil — never as zero (T6.1b). D12: nil means "pre-migration / not reported".
func TestTPMS_NilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930002)
	cleanupVehicle(t, pool, accountID, teslaID)

	snap := Snapshot{
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     time.Now().UTC().Truncate(time.Microsecond),
		ChargingState:  "Disconnected",
		CarVersion:     "2026.20.1",
		RawData:        []byte(`{}`),
		TpmsPressureFL: nil,
		TpmsPressureFR: nil,
		TpmsPressureRL: nil,
		TpmsPressureRR: nil,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Verify raw pgtype: all must be Valid=false (SQL NULL).
	q := telemetrydb.New(pool)
	rows, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.TpmsPressureFl.Valid {
		t.Errorf("TpmsPressureFl: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureFl.Float32)
	}
	if row.TpmsPressureFr.Valid {
		t.Errorf("TpmsPressureFr: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureFr.Float32)
	}
	if row.TpmsPressureRl.Valid {
		t.Errorf("TpmsPressureRl: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureRl.Float32)
	}
	if row.TpmsPressureRr.Valid {
		t.Errorf("TpmsPressureRr: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureRr.Float32)
	}

	// Verify domain read path: all must be nil.
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]
	if s.TpmsPressureFL != nil {
		t.Errorf("TpmsPressureFL: want nil, got *%v", *s.TpmsPressureFL)
	}
	if s.TpmsPressureFR != nil {
		t.Errorf("TpmsPressureFR: want nil, got *%v", *s.TpmsPressureFR)
	}
	if s.TpmsPressureRL != nil {
		t.Errorf("TpmsPressureRL: want nil, got *%v", *s.TpmsPressureRL)
	}
	if s.TpmsPressureRR != nil {
		t.Errorf("TpmsPressureRR: want nil, got *%v", *s.TpmsPressureRR)
	}

	// (d) PSI companions on a nil-field snapshot must also return nil.
	if s.TpmsPressureFLPSI() != nil {
		t.Errorf("TpmsPressureFLPSI: want nil for nil field, got *%v", *s.TpmsPressureFLPSI())
	}
	if s.TpmsPressureFRPSI() != nil {
		t.Errorf("TpmsPressureFRPSI: want nil for nil field, got *%v", *s.TpmsPressureFRPSI())
	}
	if s.TpmsPressureRLPSI() != nil {
		t.Errorf("TpmsPressureRLPSI: want nil for nil field, got *%v", *s.TpmsPressureRLPSI())
	}
	if s.TpmsPressureRRPSI() != nil {
		t.Errorf("TpmsPressureRRPSI: want nil for nil field, got *%v", *s.TpmsPressureRRPSI())
	}
}

// TestTPMS_ZeroNonNilRoundTrip verifies the D12/DSA3 invariant: ptr(0.0) is stored
// as non-NULL and read back as a non-nil *0.0 — never collapsed into nil (T6.1c).
func TestTPMS_ZeroNonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930003)
	cleanupVehicle(t, pool, accountID, teslaID)

	zero := 0.0
	snap := Snapshot{
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     time.Now().UTC().Truncate(time.Microsecond),
		ChargingState:  "Disconnected",
		CarVersion:     "2026.20.1",
		RawData:        []byte(`{}`),
		TpmsPressureFL: &zero,
		TpmsPressureFR: &zero,
		TpmsPressureRL: &zero,
		TpmsPressureRR: &zero,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Verify pgtype: all must be Valid=true (non-NULL), Float32=0.
	q := telemetrydb.New(pool)
	rows, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if !row.TpmsPressureFl.Valid {
		t.Error("TpmsPressureFl: want Valid=true (non-NULL for truthful 0.0), got Valid=false — D12 violated")
	} else if row.TpmsPressureFl.Float32 != 0 {
		t.Errorf("TpmsPressureFl: want Float32=0, got %v", row.TpmsPressureFl.Float32)
	}
	if !row.TpmsPressureFr.Valid {
		t.Error("TpmsPressureFr: want Valid=true, got Valid=false — D12 violated")
	}
	if !row.TpmsPressureRl.Valid {
		t.Error("TpmsPressureRl: want Valid=true, got Valid=false — D12 violated")
	}
	if !row.TpmsPressureRr.Valid {
		t.Error("TpmsPressureRr: want Valid=true, got Valid=false — D12 violated")
	}

	// Verify domain read path: all must be non-nil *0.0.
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]
	if s.TpmsPressureFL == nil {
		t.Error("TpmsPressureFL: want non-nil *0.0, got nil — D12 violated")
	} else if *s.TpmsPressureFL != 0.0 {
		t.Errorf("TpmsPressureFL: want *0.0, got *%v", *s.TpmsPressureFL)
	}
	if s.TpmsPressureFR == nil {
		t.Error("TpmsPressureFR: want non-nil *0.0, got nil — D12 violated")
	}
	if s.TpmsPressureRL == nil {
		t.Error("TpmsPressureRL: want non-nil *0.0, got nil — D12 violated")
	}
	if s.TpmsPressureRR == nil {
		t.Error("TpmsPressureRR: want non-nil *0.0, got nil — D12 violated")
	}
}

// TestTPMS_PSICompanionConversion verifies the PSI companion returns a correct PSI
// value for a non-nil bar field from the DB round-trip (T6.1e).
func TestTPMS_PSICompanionConversion(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930004)
	cleanupVehicle(t, pool, accountID, teslaID)

	const inputBar = 2.5
	fl := inputBar
	snap := Snapshot{
		AccountID:      accountID,
		TeslaID:        teslaID,
		CapturedAt:     time.Now().UTC().Truncate(time.Microsecond),
		ChargingState:  "Disconnected",
		CarVersion:     "2026.20.1",
		RawData:        []byte(`{}`),
		TpmsPressureFL: &fl,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]

	// (e) PSI companion for the read-back value must be non-nil and approximately correct.
	psi := s.TpmsPressureFLPSI()
	if psi == nil {
		t.Fatal("TpmsPressureFLPSI: want non-nil *float64, got nil")
	}
	// Expected PSI from the float32 round-tripped bar value (precision may differ slightly).
	expectedPSI := float64(float32(inputBar)) * barToPSI
	const tol = 1e-4 // relaxed tolerance to accommodate float32 round-trip
	if math.Abs(*psi-expectedPSI) > tol {
		t.Errorf("TpmsPressureFLPSI: want ~%v (within %v), got %v", expectedPSI, tol, *psi)
	}
}
