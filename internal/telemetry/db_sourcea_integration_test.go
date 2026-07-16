package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise Source A of RM2-telemetry-add-charging-stats: the 6 nullable
// charge-enrichment columns on vehicle_snapshots. They require:
//   - DATABASE_URL set to a running Postgres with goose migrations applied (including
//     migration 20260716000002_enrich_vehicle_snapshots_charge.sql).
//
// They self-skip when DATABASE_URL is unset, so `go test ./...` stays green without
// a database (ai/go-conventions.md §persistence).
//
// Design compliance (A5):
//   - A5.1a: Insert a snapshot with all 6 charge fields non-nil; read back via
//             LatestSnapshotsByAccount; assert all 6 round-trip faithfully.
//   - A5.1b: Insert a snapshot with all 6 charge fields nil (pre-enrichment sim);
//             assert all 6 come back nil (not zero).
//   - A5.1c: D12 invariant — a truthful 0 stored as a non-nil pointer comes back
//             as a non-nil pointer pointing to 0, never as nil.

// ptrFloat64 is a test helper (package-private, test file only) to build a *float64.
func ptrFloat64(v float64) *float64 { return &v }

// ptrInt is a test helper to build a *int.
func ptrInt(v int) *int { return &v }

// ptrString is a test helper to build a *string.
func ptrString(v string) *string { return &v }

// TestSourceA_ChargeEnrichment_NonNilRoundTrip verifies that all 6 charge-enrichment
// fields round-trip faithfully when they carry non-nil, non-zero values (A5.1a).
// Demonstrates D12: a truthful value stored at write time is returned faithfully at read.
func TestSourceA_ChargeEnrichment_NonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(910001)
	cleanupVehicle(t, pool, accountID, teslaID)

	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Charging",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Charging"}}`),
		// All 6 Source A fields — non-nil, non-zero values.
		ChargeEnergyAdded:    ptrFloat64(12.5),  // kWh
		ChargerPower:         ptrInt(11),         // kW
		ChargerVoltage:       ptrInt(240),        // V
		ChargerActualCurrent: ptrInt(48),         // A
		UsableBatteryLevel:   ptrInt(73),         // %
		FastChargerType:      ptrString("Tesla"), // charger brand
	}

	// A helper that reads back via the dbStore read seam (same path as LatestSnapshotsByAccount).
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

	// A5.1a: all 6 fields must be non-nil and carry the correct value.
	if s.ChargeEnergyAdded == nil {
		t.Fatal("ChargeEnergyAdded: want non-nil, got nil")
	} else if *s.ChargeEnergyAdded != 12.5 {
		t.Errorf("ChargeEnergyAdded: want 12.5, got %v", *s.ChargeEnergyAdded)
	}

	if s.ChargerPower == nil {
		t.Fatal("ChargerPower: want non-nil, got nil")
	} else if *s.ChargerPower != 11 {
		t.Errorf("ChargerPower: want 11, got %v", *s.ChargerPower)
	}

	if s.ChargerVoltage == nil {
		t.Fatal("ChargerVoltage: want non-nil, got nil")
	} else if *s.ChargerVoltage != 240 {
		t.Errorf("ChargerVoltage: want 240, got %v", *s.ChargerVoltage)
	}

	if s.ChargerActualCurrent == nil {
		t.Fatal("ChargerActualCurrent: want non-nil, got nil")
	} else if *s.ChargerActualCurrent != 48 {
		t.Errorf("ChargerActualCurrent: want 48, got %v", *s.ChargerActualCurrent)
	}

	if s.UsableBatteryLevel == nil {
		t.Fatal("UsableBatteryLevel: want non-nil, got nil")
	} else if *s.UsableBatteryLevel != 73 {
		t.Errorf("UsableBatteryLevel: want 73, got %v", *s.UsableBatteryLevel)
	}

	if s.FastChargerType == nil {
		t.Fatal("FastChargerType: want non-nil, got nil")
	} else if *s.FastChargerType != "Tesla" {
		t.Errorf("FastChargerType: want \"Tesla\", got %q", *s.FastChargerType)
	}
}

// TestSourceA_ChargeEnrichment_NilRoundTrip verifies that when all 6 charge-enrichment
// fields are nil (simulating a pre-enrichment / vehicle-not-charging snapshot), they
// come back as nil — not as zero values (A5.1b/A5.1c nil case).
func TestSourceA_ChargeEnrichment_NilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(910002)
	cleanupVehicle(t, pool, accountID, teslaID)

	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Disconnected",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Disconnected"}}`),
		// All 6 Source A fields are nil — simulates inserting NULL params (pre-enrichment row).
		ChargeEnergyAdded:    nil,
		ChargerPower:         nil,
		ChargerVoltage:       nil,
		ChargerActualCurrent: nil,
		UsableBatteryLevel:   nil,
		FastChargerType:      nil,
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

	// A5.1b: all 6 fields must come back as nil (SQL NULL → nil pointer), not zero.
	if s.ChargeEnergyAdded != nil {
		t.Errorf("ChargeEnergyAdded: want nil, got %v", *s.ChargeEnergyAdded)
	}
	if s.ChargerPower != nil {
		t.Errorf("ChargerPower: want nil, got %v", *s.ChargerPower)
	}
	if s.ChargerVoltage != nil {
		t.Errorf("ChargerVoltage: want nil, got %v", *s.ChargerVoltage)
	}
	if s.ChargerActualCurrent != nil {
		t.Errorf("ChargerActualCurrent: want nil, got %v", *s.ChargerActualCurrent)
	}
	if s.UsableBatteryLevel != nil {
		t.Errorf("UsableBatteryLevel: want nil, got %v", *s.UsableBatteryLevel)
	}
	if s.FastChargerType != nil {
		t.Errorf("FastChargerType: want nil, got %q", *s.FastChargerType)
	}
}

// TestSourceA_ChargeEnrichment_TruthfulZeroStoredAndRead verifies D12: a truthful
// zero value (0 kWh charge_energy_added, 0 charger_power, etc.) is stored non-NULL
// and comes back as a non-nil pointer to 0 — not as nil. This proves the write path
// applies NO zero-is-absent heuristic (design DSA3/D12).
func TestSourceA_ChargeEnrichment_TruthfulZeroStoredAndRead(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(910003)
	cleanupVehicle(t, pool, accountID, teslaID)

	// The vehicle is plugged in but just started charging — energy added is 0,
	// power and current are 0 (charge hasn't ramped yet). These are real readings.
	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    time.Now().UTC().Truncate(time.Microsecond),
		ChargingState: "Charging",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Charging"}}`),
		// Truthful zeros — NOT nil. D12: these must be stored non-NULL and returned
		// as non-nil pointers pointing to 0 (or ""). A nil would falsely mean
		// "pre-migration row that was never backfilled".
		ChargeEnergyAdded:    ptrFloat64(0.0),
		ChargerPower:         ptrInt(0),
		ChargerVoltage:       ptrInt(0),
		ChargerActualCurrent: ptrInt(0),
		UsableBatteryLevel:   ptrInt(0),
		FastChargerType:      ptrString(""),
	}

	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Read back via the direct sqlc query to see raw pgtype values too.
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

	// The pgtype.Float8/Int4/Text columns must be Valid (non-NULL in the DB).
	if !row.ChargeEnergyAdded.Valid {
		t.Error("ChargeEnergyAdded: want Valid=true (non-NULL), got Valid=false — zero was wrongly stored as NULL")
	}
	if row.ChargeEnergyAdded.Float64 != 0.0 {
		t.Errorf("ChargeEnergyAdded: want 0.0, got %v", row.ChargeEnergyAdded.Float64)
	}
	if !row.ChargerPower.Valid {
		t.Error("ChargerPower: want Valid=true (non-NULL), got Valid=false")
	}
	if row.ChargerPower.Int32 != 0 {
		t.Errorf("ChargerPower: want 0, got %v", row.ChargerPower.Int32)
	}
	if !row.ChargerVoltage.Valid {
		t.Error("ChargerVoltage: want Valid=true (non-NULL), got Valid=false")
	}
	if !row.ChargerActualCurrent.Valid {
		t.Error("ChargerActualCurrent: want Valid=true (non-NULL), got Valid=false")
	}
	if !row.UsableBatteryLevel.Valid {
		t.Error("UsableBatteryLevel: want Valid=true (non-NULL), got Valid=false")
	}
	if !row.FastChargerType.Valid {
		t.Error("FastChargerType: want Valid=true (non-NULL), got Valid=false")
	}
	if row.FastChargerType.String != "" {
		t.Errorf("FastChargerType: want empty string, got %q", row.FastChargerType.String)
	}

	// Also verify the domain read path (rowToSnapshot / latestSnapshotsByAccount).
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot from latestSnapshotsByAccount, got %d", len(got))
	}
	s := got[0]

	// D12: each field must be non-nil (pointing to 0 / ""), never nil.
	if s.ChargeEnergyAdded == nil {
		t.Error("ChargeEnergyAdded: want non-nil *0.0, got nil — D12 violated: zero stored as nil")
	} else if *s.ChargeEnergyAdded != 0.0 {
		t.Errorf("ChargeEnergyAdded: want *0.0, got *%v", *s.ChargeEnergyAdded)
	}
	if s.ChargerPower == nil {
		t.Error("ChargerPower: want non-nil *0, got nil — D12 violated")
	}
	if s.ChargerVoltage == nil {
		t.Error("ChargerVoltage: want non-nil *0, got nil — D12 violated")
	}
	if s.ChargerActualCurrent == nil {
		t.Error("ChargerActualCurrent: want non-nil *0, got nil — D12 violated")
	}
	if s.UsableBatteryLevel == nil {
		t.Error("UsableBatteryLevel: want non-nil *0, got nil — D12 violated")
	}
	if s.FastChargerType == nil {
		t.Error("FastChargerType: want non-nil *\"\", got nil — D12 violated")
	} else if *s.FastChargerType != "" {
		t.Errorf("FastChargerType: want *\"\", got *%q", *s.FastChargerType)
	}
}
