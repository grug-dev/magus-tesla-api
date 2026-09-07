package telemetry

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// These tests exercise the four TPMS pressure columns added by migration
// 20260802000001_add_tpms_pressure_columns.sql and renamed/rescaled to PSI by
// migration 20260806000001_store_display_units_vehicle_snapshots.sql
// (telemetry-store-display-units design D1). The columns now store PSI directly —
// conversion happens once, at capture time, in snapshotFrom (design D3) — so these
// store-level tests insert Snapshot literals with PSI values directly (bypassing
// snapshotFrom, which is unit-tested separately in service_snapshot_from_test.go)
// and assert the DB round-trip preserves them faithfully. There is no read-time PSI
// companion any more (design D4): TpmsPressureFLPSI etc. are plain *float64 fields,
// not methods.
//
// go test ./... is green with no manual DB setup as long as Docker is running
// locally (ai/go-conventions.md §persistence). No live Tesla API call fires in any
// of these tests.
//
// Coverage (T6.1 per tasks.md, updated for telemetry-store-display-units T5.4):
//   (a) Non-nil round-trip: non-zero PSI values survive insert -> read faithfully.
//   (b) Nil round-trip: nil -> SQL NULL -> nil (not zero) — the "not reported /
//       pre-migration" invariant.
//   (c) Zero-value non-nil: ptr(0.0) -> non-NULL *0.0 (D12/DSA3 truthful zero).
//   (d) A realistic PSI value (as snapshotFrom would compute via the tesla
//       adapter's TpmsPressure*PSI() companions) round-trips within float32
//       precision through the REAL column.

const tpmsFloatTol = 1e-5 // float32->float64 widening may lose sub-1e-5 precision

// tpmsRow carries the 4 TPMS columns TestTPMS_NilRoundTrip and
// TestTPMS_ZeroNonNilRoundTrip assert. It replaces the deleted
// telemetrydb.ListSnapshotsByVehicle sqlc query (RM44-telemetry-add-query-logging
// D5/D13): both tests now read their row back with a raw SQL SELECT via the
// pool directly, independent of this module's own reader queries.
type tpmsRow struct {
	TpmsPressureFlPsi pgtype.Float4
	TpmsPressureFrPsi pgtype.Float4
	TpmsPressureRlPsi pgtype.Float4
	TpmsPressureRrPsi pgtype.Float4
}

func queryTPMS(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]tpmsRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []tpmsRow
	for rows.Next() {
		var r tpmsRow
		if err := rows.Scan(&r.TpmsPressureFlPsi, &r.TpmsPressureFrPsi, &r.TpmsPressureRlPsi, &r.TpmsPressureRrPsi); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// TestTPMS_NonNilRoundTrip verifies that all four TPMS PSI fields with non-zero
// values round-trip faithfully through the store -> DB -> read path (T6.1a).
// The stored typed values are the PSI equivalents of the bar readings still visible,
// unconverted, in raw_data — mirroring what snapshotFrom actually computes via the
// tesla adapter's TpmsPressure*PSI() companions (2.5/2.6/2.4/2.5 bar * 14.503773773).
func TestTPMS_NonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930001)
	cleanupVehicle(t, pool, accountID, teslaID)

	// PSI equivalents of 2.5/2.6/2.4/2.5 bar (the values still visible, unconverted,
	// in raw_data below) — literals, not barToPSI (that constant no longer exists in
	// this module, design D3/telemetry AGENTS.md).
	fl, fr, rl, rr := 36.2594344325, 37.7098118098, 34.8090570552, 36.2594344325
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      clock.CalendarDay(captured, time.UTC),
		ChargingState:     "Disconnected",
		CarVersion:        "2026.20.1",
		RawData:           []byte(`{"vehicle_state":{"tpms_pressure_fl":2.5,"tpms_pressure_fr":2.6,"tpms_pressure_rl":2.4,"tpms_pressure_rr":2.5}}`),
		TpmsPressureFLPSI: &fl,
		TpmsPressureFRPSI: &fr,
		TpmsPressureRLPSI: &rl,
		TpmsPressureRRPSI: &rr,
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
	if s.TpmsPressureFLPSI == nil {
		t.Fatal("TpmsPressureFLPSI: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureFLPSI-fl) > tpmsFloatTol {
		t.Errorf("TpmsPressureFLPSI: want ~%v, got %v", fl, *s.TpmsPressureFLPSI)
	}
	if s.TpmsPressureFRPSI == nil {
		t.Fatal("TpmsPressureFRPSI: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureFRPSI-fr) > tpmsFloatTol {
		t.Errorf("TpmsPressureFRPSI: want ~%v, got %v", fr, *s.TpmsPressureFRPSI)
	}
	if s.TpmsPressureRLPSI == nil {
		t.Fatal("TpmsPressureRLPSI: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureRLPSI-rl) > tpmsFloatTol {
		t.Errorf("TpmsPressureRLPSI: want ~%v, got %v", rl, *s.TpmsPressureRLPSI)
	}
	if s.TpmsPressureRRPSI == nil {
		t.Fatal("TpmsPressureRRPSI: want non-nil, got nil")
	} else if math.Abs(*s.TpmsPressureRRPSI-rr) > tpmsFloatTol {
		t.Errorf("TpmsPressureRRPSI: want ~%v, got %v", rr, *s.TpmsPressureRRPSI)
	}
}

// TestTPMS_NilRoundTrip verifies that nil TPMS PSI fields store as SQL NULL and come
// back as nil — never as zero (T6.1b). D12: nil means "pre-migration / not reported".
// There is no PSI companion method to check any more (design D4: the field itself
// carries the unit) — the field-nil assertions below are the complete check.
func TestTPMS_NilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930002)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      clock.CalendarDay(captured, time.UTC),
		ChargingState:     "Disconnected",
		CarVersion:        "2026.20.1",
		RawData:           []byte(`{}`),
		TpmsPressureFLPSI: nil,
		TpmsPressureFRPSI: nil,
		TpmsPressureRLPSI: nil,
		TpmsPressureRRPSI: nil,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Verify raw pgtype: all must be Valid=false (SQL NULL).
	rows, err := queryTPMS(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if row.TpmsPressureFlPsi.Valid {
		t.Errorf("TpmsPressureFlPsi: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureFlPsi.Float32)
	}
	if row.TpmsPressureFrPsi.Valid {
		t.Errorf("TpmsPressureFrPsi: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureFrPsi.Float32)
	}
	if row.TpmsPressureRlPsi.Valid {
		t.Errorf("TpmsPressureRlPsi: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureRlPsi.Float32)
	}
	if row.TpmsPressureRrPsi.Valid {
		t.Errorf("TpmsPressureRrPsi: want Valid=false (SQL NULL), got Valid=true (value=%v)", row.TpmsPressureRrPsi.Float32)
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
	if s.TpmsPressureFLPSI != nil {
		t.Errorf("TpmsPressureFLPSI: want nil, got *%v", *s.TpmsPressureFLPSI)
	}
	if s.TpmsPressureFRPSI != nil {
		t.Errorf("TpmsPressureFRPSI: want nil, got *%v", *s.TpmsPressureFRPSI)
	}
	if s.TpmsPressureRLPSI != nil {
		t.Errorf("TpmsPressureRLPSI: want nil, got *%v", *s.TpmsPressureRLPSI)
	}
	if s.TpmsPressureRRPSI != nil {
		t.Errorf("TpmsPressureRRPSI: want nil, got *%v", *s.TpmsPressureRRPSI)
	}
}

// TestTPMS_ZeroNonNilRoundTrip verifies the D12/DSA3 invariant: ptr(0.0) is stored
// as non-NULL and read back as a non-nil *0.0 — never collapsed into nil (T6.1c).
// 0.0 bar converts to 0.0 PSI, so the zero-fidelity check is unit-agnostic.
func TestTPMS_ZeroNonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930003)
	cleanupVehicle(t, pool, accountID, teslaID)

	zero := 0.0
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      clock.CalendarDay(captured, time.UTC),
		ChargingState:     "Disconnected",
		CarVersion:        "2026.20.1",
		RawData:           []byte(`{}`),
		TpmsPressureFLPSI: &zero,
		TpmsPressureFRPSI: &zero,
		TpmsPressureRLPSI: &zero,
		TpmsPressureRRPSI: &zero,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Verify pgtype: all must be Valid=true (non-NULL), Float32=0.
	rows, err := queryTPMS(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if !row.TpmsPressureFlPsi.Valid {
		t.Error("TpmsPressureFlPsi: want Valid=true (non-NULL for truthful 0.0), got Valid=false — D12 violated")
	} else if row.TpmsPressureFlPsi.Float32 != 0 {
		t.Errorf("TpmsPressureFlPsi: want Float32=0, got %v", row.TpmsPressureFlPsi.Float32)
	}
	if !row.TpmsPressureFrPsi.Valid {
		t.Error("TpmsPressureFrPsi: want Valid=true, got Valid=false — D12 violated")
	}
	if !row.TpmsPressureRlPsi.Valid {
		t.Error("TpmsPressureRlPsi: want Valid=true, got Valid=false — D12 violated")
	}
	if !row.TpmsPressureRrPsi.Valid {
		t.Error("TpmsPressureRrPsi: want Valid=true, got Valid=false — D12 violated")
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
	if s.TpmsPressureFLPSI == nil {
		t.Error("TpmsPressureFLPSI: want non-nil *0.0, got nil — D12 violated")
	} else if *s.TpmsPressureFLPSI != 0.0 {
		t.Errorf("TpmsPressureFLPSI: want *0.0, got *%v", *s.TpmsPressureFLPSI)
	}
	if s.TpmsPressureFRPSI == nil {
		t.Error("TpmsPressureFRPSI: want non-nil *0.0, got nil — D12 violated")
	}
	if s.TpmsPressureRLPSI == nil {
		t.Error("TpmsPressureRLPSI: want non-nil *0.0, got nil — D12 violated")
	}
	if s.TpmsPressureRRPSI == nil {
		t.Error("TpmsPressureRRPSI: want non-nil *0.0, got nil — D12 violated")
	}
}

// TestTPMS_PSIValueRoundTripPrecision verifies that a realistic PSI value — the kind
// snapshotFrom actually produces by calling the tesla adapter's TpmsPressureFLPSI()
// companion on a 2.9 bar reading (design D3) — survives the REAL (float4) column
// round-trip within float32 precision. This replaces the old
// TestTPMS_PSICompanionConversion, which exercised a read-time bar->PSI companion
// method that design D4 deliberately removed: conversion now happens exactly once,
// at capture time, so there is nothing left to convert on the read path (spec
// "Latest Snapshot Read Port": no companion method is exposed). What remains
// testable and true at the store layer is that the already-converted PSI value is
// not corrupted by the float64->float32->float64 round-trip through the DB.
func TestTPMS_PSIValueRoundTripPrecision(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(930004)
	cleanupVehicle(t, pool, accountID, teslaID)

	// 2.9 bar * 14.503773773 = 42.0609439417 PSI (literal, not barToPSI — that
	// constant no longer exists in this module; the value mirrors
	// TestTpmsPressurePSICompanions in internal/tesla, which owns the conversion).
	const inputPSI = 42.0609439417
	fl := inputPSI
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:         accountID,
		TeslaID:           teslaID,
		CapturedAt:        captured,
		CapturedDate:      clock.CalendarDay(captured, time.UTC),
		ChargingState:     "Disconnected",
		CarVersion:        "2026.20.1",
		RawData:           []byte(`{"vehicle_state":{"tpms_pressure_fl":2.9}}`),
		TpmsPressureFLPSI: &fl,
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

	if s.TpmsPressureFLPSI == nil {
		t.Fatal("TpmsPressureFLPSI: want non-nil *float64, got nil")
	}
	// The float32 round-trip loses precision beyond ~7 significant digits (design D5:
	// REAL stays REAL — adequate for PSI readings).
	const tol = 1e-4
	if math.Abs(*s.TpmsPressureFLPSI-inputPSI) > tol {
		t.Errorf("TpmsPressureFLPSI: want ~%v (within %v), got %v", inputPSI, tol, *s.TpmsPressureFLPSI)
	}
}
