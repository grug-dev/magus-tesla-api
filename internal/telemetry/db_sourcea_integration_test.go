package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// These tests exercise Source A of RM2-telemetry-add-charging-stats: the 6 nullable
// charge-enrichment columns on vehicle_snapshots. They require:
//   - TEST_DATABASE_URL set to a running Postgres with goose migrations applied (including
//     migration 20260716000002_enrich_vehicle_snapshots_charge.sql).
//
// They self-skip when TEST_DATABASE_URL is unset, so `go test ./...` stays green without
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

// The two helpers below replace the deleted telemetrydb.ListSnapshotsByVehicle
// sqlc query (RM44-telemetry-add-query-logging D5/D13): each test that used to
// call it now reads its row back with a raw SQL SELECT via the pool directly,
// independent of this module's own reader queries, narrowed to the columns
// that test actually asserts.

// sourceAChargeRow carries the columns TestSourceA_ChargeEnrichment_TruthfulZeroStoredAndRead
// asserts.
type sourceAChargeRow struct {
	ChargeEnergyAddedKwh  pgtype.Float8
	ChargerPowerKw        pgtype.Int4
	ChargerVoltageV       pgtype.Int4
	ChargerActualCurrentA pgtype.Int4
	UsableBatteryLevelPct pgtype.Int4
}

func querySourceACharge(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]sourceAChargeRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
		        charger_actual_current_a, usable_battery_level_pct
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []sourceAChargeRow
	for rows.Next() {
		var r sourceAChargeRow
		if err := rows.Scan(&r.ChargeEnergyAddedKwh, &r.ChargerPowerKw, &r.ChargerVoltageV,
			&r.ChargerActualCurrentA, &r.UsableBatteryLevelPct); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// maxRangeChargeCounterRow carries the column the two
// TestMaxRangeChargeCounter_* tests below assert.
type maxRangeChargeCounterRow struct {
	MaxRangeChargeCounter pgtype.Int4
}

func queryMaxRangeChargeCounter(ctx context.Context, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) ([]maxRangeChargeCounterRow, error) {
	rows, err := pool.Query(ctx,
		`SELECT max_range_charge_counter
		   FROM telemetry.vehicle_snapshots
		  WHERE account_id = $1 AND tesla_id = $2
		  ORDER BY captured_at DESC`,
		accountID, teslaID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var got []maxRangeChargeCounterRow
	for rows.Next() {
		var r maxRangeChargeCounterRow
		if err := rows.Scan(&r.MaxRangeChargeCounter); err != nil {
			return nil, err
		}
		got = append(got, r)
	}
	return got, rows.Err()
}

// TestSourceA_ChargeEnrichment_NonNilRoundTrip verifies that all 6 charge-enrichment
// fields round-trip faithfully when they carry non-nil, non-zero values (A5.1a).
// Demonstrates D12: a truthful value stored at write time is returned faithfully at read.
func TestSourceA_ChargeEnrichment_NonNilRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(910001)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Charging",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Charging"}}`),
		// All 5 Source A fields — non-nil, non-zero values.
		// fast_charger_type was dropped in 20260801000001; lossless in raw_data JSONB.
		ChargeEnergyAddedKWh:  ptrFloat64(12.5), // kWh
		ChargerPowerKW:        ptrInt(11),       // kW
		ChargerVoltageV:       ptrInt(240),      // V
		ChargerActualCurrentA: ptrInt(48),       // A
		UsableBatteryLevelPct: ptrInt(73),       // %
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
	if s.ChargeEnergyAddedKWh == nil {
		t.Fatal("ChargeEnergyAddedKWh: want non-nil, got nil")
	} else if *s.ChargeEnergyAddedKWh != 12.5 {
		t.Errorf("ChargeEnergyAddedKWh: want 12.5, got %v", *s.ChargeEnergyAddedKWh)
	}

	if s.ChargerPowerKW == nil {
		t.Fatal("ChargerPowerKW: want non-nil, got nil")
	} else if *s.ChargerPowerKW != 11 {
		t.Errorf("ChargerPowerKW: want 11, got %v", *s.ChargerPowerKW)
	}

	if s.ChargerVoltageV == nil {
		t.Fatal("ChargerVoltageV: want non-nil, got nil")
	} else if *s.ChargerVoltageV != 240 {
		t.Errorf("ChargerVoltageV: want 240, got %v", *s.ChargerVoltageV)
	}

	if s.ChargerActualCurrentA == nil {
		t.Fatal("ChargerActualCurrentA: want non-nil, got nil")
	} else if *s.ChargerActualCurrentA != 48 {
		t.Errorf("ChargerActualCurrentA: want 48, got %v", *s.ChargerActualCurrentA)
	}

	if s.UsableBatteryLevelPct == nil {
		t.Fatal("UsableBatteryLevelPct: want non-nil, got nil")
	} else if *s.UsableBatteryLevelPct != 73 {
		t.Errorf("UsableBatteryLevelPct: want 73, got %v", *s.UsableBatteryLevelPct)
	}
	// fast_charger_type dropped in 20260801000001 — not asserted here;
	// value remains recoverable from raw_data->'charge_state'->'fast_charger_type'.
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

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Disconnected",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Disconnected"}}`),
		// All 5 Source A fields are nil — simulates inserting NULL params (pre-enrichment row).
		// fast_charger_type was dropped in 20260801000001; lossless in raw_data JSONB.
		ChargeEnergyAddedKWh:  nil,
		ChargerPowerKW:        nil,
		ChargerVoltageV:       nil,
		ChargerActualCurrentA: nil,
		UsableBatteryLevelPct: nil,
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
	if s.ChargeEnergyAddedKWh != nil {
		t.Errorf("ChargeEnergyAddedKWh: want nil, got %v", *s.ChargeEnergyAddedKWh)
	}
	if s.ChargerPowerKW != nil {
		t.Errorf("ChargerPowerKW: want nil, got %v", *s.ChargerPowerKW)
	}
	if s.ChargerVoltageV != nil {
		t.Errorf("ChargerVoltageV: want nil, got %v", *s.ChargerVoltageV)
	}
	if s.ChargerActualCurrentA != nil {
		t.Errorf("ChargerActualCurrentA: want nil, got %v", *s.ChargerActualCurrentA)
	}
	if s.UsableBatteryLevelPct != nil {
		t.Errorf("UsableBatteryLevelPct: want nil, got %v", *s.UsableBatteryLevelPct)
	}
	// fast_charger_type dropped in 20260801000001 — not asserted here.
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
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:     accountID,
		TeslaID:       teslaID,
		CapturedAt:    captured,
		CapturedDate:  clock.CalendarDay(captured, time.UTC),
		ChargingState: "Charging",
		CarVersion:    "2026.20.1",
		RawData:       []byte(`{"charge_state":{"charging_state":"Charging"}}`),
		// Truthful zeros — NOT nil. D12: these must be stored non-NULL and returned
		// as non-nil pointers pointing to 0. A nil would falsely mean
		// "pre-migration row that was never backfilled".
		// fast_charger_type dropped in 20260801000001; not included here.
		ChargeEnergyAddedKWh:  ptrFloat64(0.0),
		ChargerPowerKW:        ptrInt(0),
		ChargerVoltageV:       ptrInt(0),
		ChargerActualCurrentA: ptrInt(0),
		UsableBatteryLevelPct: ptrInt(0),
	}

	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Read back via a direct SQL SELECT to see raw pgtype values too.
	rows, err := querySourceACharge(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]

	// The pgtype.Float8/Int4/Text columns must be Valid (non-NULL in the DB).
	if !row.ChargeEnergyAddedKwh.Valid {
		t.Error("ChargeEnergyAddedKWh: want Valid=true (non-NULL), got Valid=false — zero was wrongly stored as NULL")
	}
	if row.ChargeEnergyAddedKwh.Float64 != 0.0 {
		t.Errorf("ChargeEnergyAddedKWh: want 0.0, got %v", row.ChargeEnergyAddedKwh.Float64)
	}
	if !row.ChargerPowerKw.Valid {
		t.Error("ChargerPowerKW: want Valid=true (non-NULL), got Valid=false")
	}
	if row.ChargerPowerKw.Int32 != 0 {
		t.Errorf("ChargerPowerKW: want 0, got %v", row.ChargerPowerKw.Int32)
	}
	if !row.ChargerVoltageV.Valid {
		t.Error("ChargerVoltageV: want Valid=true (non-NULL), got Valid=false")
	}
	if !row.ChargerActualCurrentA.Valid {
		t.Error("ChargerActualCurrentA: want Valid=true (non-NULL), got Valid=false")
	}
	if !row.UsableBatteryLevelPct.Valid {
		t.Error("UsableBatteryLevelPct: want Valid=true (non-NULL), got Valid=false")
	}
	// fast_charger_type dropped in 20260801000001 — not asserted here.
	// MaxRangeChargeCounter nil round-trip is tested separately in TestMaxRangeChargeCounter_NilAndNonNilFidelity.

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
	if s.ChargeEnergyAddedKWh == nil {
		t.Error("ChargeEnergyAddedKWh: want non-nil *0.0, got nil — D12 violated: zero stored as nil")
	} else if *s.ChargeEnergyAddedKWh != 0.0 {
		t.Errorf("ChargeEnergyAddedKWh: want *0.0, got *%v", *s.ChargeEnergyAddedKWh)
	}
	if s.ChargerPowerKW == nil {
		t.Error("ChargerPowerKW: want non-nil *0, got nil — D12 violated")
	}
	if s.ChargerVoltageV == nil {
		t.Error("ChargerVoltageV: want non-nil *0, got nil — D12 violated")
	}
	if s.ChargerActualCurrentA == nil {
		t.Error("ChargerActualCurrentA: want non-nil *0, got nil — D12 violated")
	}
	if s.UsableBatteryLevelPct == nil {
		t.Error("UsableBatteryLevelPct: want non-nil *0, got nil — D12 violated")
	}
	// fast_charger_type dropped in 20260801000001 — not asserted here.
}

// --- MaxRangeChargeCounter nil↔NULL fidelity (telemetry-vehicle-snapshots-maxrange-drop-location) ---
//
// These tests verify the D12/DSA3 pointer-wrap semantics for max_range_charge_counter:
//   - A reported non-zero value round-trips as non-nil.
//   - A reported 0 (new vehicle, never charged to max-range) is stored as non-NULL *0
//     (truthful zero, NOT collapsed into nil).
//   - A nil in the domain Snapshot (pre-extraction / pre-migration row) stores as SQL NULL
//     and comes back as nil.
// They also verify the migration backfill: a row whose raw_data contains
// charge_state.max_range_charge_counter as a number gets its typed column populated.
// Requires migration 20260801000001_add_maxrange_drop_location_fastchargertype.sql applied.

// TestMaxRangeChargeCounter_NonNilNonZeroRoundTrip verifies that a non-nil, non-zero
// MaxRangeChargeCounter (e.g. 3 = charged to max-range three times) round-trips
// faithfully through the store → DB → read path.
func TestMaxRangeChargeCounter_NonNilNonZeroRoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(920001)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:             accountID,
		TeslaID:               teslaID,
		CapturedAt:            captured,
		CapturedDate:          clock.CalendarDay(captured, time.UTC),
		ChargingState:         "Disconnected",
		CarVersion:            "2026.20.1",
		RawData:               []byte(`{"charge_state":{"max_range_charge_counter":3}}`),
		MaxRangeChargeCounter: ptrInt(3),
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
	if s.MaxRangeChargeCounter == nil {
		t.Fatal("MaxRangeChargeCounter: want non-nil *3, got nil")
	}
	if *s.MaxRangeChargeCounter != 3 {
		t.Errorf("MaxRangeChargeCounter: want *3, got *%d", *s.MaxRangeChargeCounter)
	}
}

// TestMaxRangeChargeCounter_TruthfulZeroStoredAsNonNil verifies the D12/DSA3 invariant:
// a reported counter of 0 (new vehicle, never charged to max-range) is stored as non-NULL
// and comes back as a non-nil pointer to 0 — never as nil. Collapsing *0 into nil would
// lose the distinction between "new vehicle" and "row predates this extraction".
func TestMaxRangeChargeCounter_TruthfulZeroStoredAsNonNil(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(920002)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:             accountID,
		TeslaID:               teslaID,
		CapturedAt:            captured,
		CapturedDate:          clock.CalendarDay(captured, time.UTC),
		ChargingState:         "Disconnected",
		CarVersion:            "2026.20.1",
		RawData:               []byte(`{"charge_state":{"max_range_charge_counter":0}}`),
		MaxRangeChargeCounter: ptrInt(0), // truthful 0: new vehicle, never charged to max-range
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Check the raw pgtype column: it must be Valid=true (non-NULL), Int32=0.
	rows, err := queryMaxRangeChargeCounter(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	row := rows[0]
	if !row.MaxRangeChargeCounter.Valid {
		t.Error("MaxRangeChargeCounter: want Valid=true (non-NULL) for a truthful 0, got Valid=false — D12 violated: zero stored as NULL")
	}
	if row.MaxRangeChargeCounter.Int32 != 0 {
		t.Errorf("MaxRangeChargeCounter: want Int32=0, got %d", row.MaxRangeChargeCounter.Int32)
	}

	// Also verify the domain read path: must come back as non-nil *0.
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]
	if s.MaxRangeChargeCounter == nil {
		t.Error("MaxRangeChargeCounter: want non-nil *0, got nil — D12 violated: *0 collapsed into nil")
	} else if *s.MaxRangeChargeCounter != 0 {
		t.Errorf("MaxRangeChargeCounter: want *0, got *%d", *s.MaxRangeChargeCounter)
	}
}

// TestMaxRangeChargeCounter_NilStoresAsNullAndRoundTripsNil verifies that a nil
// MaxRangeChargeCounter (pre-extraction row simulation) stores as SQL NULL and comes
// back as nil through the full read path. NULL means "row predates the
// 20260801000001 migration or vehicle did not report the field".
func TestMaxRangeChargeCounter_NilStoresAsNullAndRoundTripsNil(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(920003)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:             accountID,
		TeslaID:               teslaID,
		CapturedAt:            captured,
		CapturedDate:          clock.CalendarDay(captured, time.UTC),
		ChargingState:         "Disconnected",
		CarVersion:            "2026.20.1",
		RawData:               []byte(`{}`),
		MaxRangeChargeCounter: nil, // pre-extraction row: no counter → SQL NULL
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Raw pgtype: must be Valid=false (SQL NULL).
	rows, err := queryMaxRangeChargeCounter(ctx, pool, accountID, teslaID)
	if err != nil {
		t.Fatalf("querying vehicle_snapshots: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if rows[0].MaxRangeChargeCounter.Valid {
		t.Errorf("MaxRangeChargeCounter: want Valid=false (SQL NULL) for nil domain value, got Valid=true (value=%d)", rows[0].MaxRangeChargeCounter.Int32)
	}

	// Domain read path: must come back as nil.
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].MaxRangeChargeCounter != nil {
		t.Errorf("MaxRangeChargeCounter: want nil (SQL NULL round-trips to nil), got *%d", *got[0].MaxRangeChargeCounter)
	}
}

// TestMaxRangeChargeCounter_BackfillFromRawData verifies the migration backfill
// behavior: a row inserted with nil MaxRangeChargeCounter but with a numeric
// charge_state.max_range_charge_counter in its raw_data gets its typed column
// populated when the migration UP backfill UPDATE runs. Since we cannot re-run
// the migration in a test, this test simulates the backfill with a direct UPDATE
// matching the migration's exact WHERE and SET logic — proving the JSONB path and
// the ::INTEGER cast are correct.
func TestMaxRangeChargeCounter_BackfillFromRawData(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(920004)
	cleanupVehicle(t, pool, accountID, teslaID)

	// Insert a "pre-migration" row: typed column is nil (NULL) but raw_data
	// contains the counter value under the correct JSONB path.
	rawWithCounter := []byte(`{"charge_state":{"max_range_charge_counter":5}}`)
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:             accountID,
		TeslaID:               teslaID,
		CapturedAt:            captured,
		CapturedDate:          clock.CalendarDay(captured, time.UTC),
		ChargingState:         "Disconnected",
		CarVersion:            "2026.20.1",
		RawData:               rawWithCounter,
		MaxRangeChargeCounter: nil, // simulates pre-migration row where typed col didn't exist
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Simulate the migration backfill UPDATE using the exact SQL from the migration Up.
	// This tests that the JSONB path, the jsonb_typeof guard, and the ::INTEGER cast work.
	_, err := pool.Exec(ctx, `
		UPDATE telemetry.vehicle_snapshots
		SET max_range_charge_counter =
		        (raw_data -> 'charge_state' ->> 'max_range_charge_counter')::INTEGER
		WHERE account_id = $1
		  AND tesla_id = $2
		  AND jsonb_typeof(raw_data -> 'charge_state' -> 'max_range_charge_counter') = 'number'`,
		accountID, teslaID)
	if err != nil {
		t.Fatalf("backfill UPDATE: %v", err)
	}

	// After backfill, the typed column must be non-NULL and equal to the value in raw_data.
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount after backfill: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	s := got[0]
	if s.MaxRangeChargeCounter == nil {
		t.Fatal("MaxRangeChargeCounter: want non-nil *5 after backfill, got nil")
	}
	if *s.MaxRangeChargeCounter != 5 {
		t.Errorf("MaxRangeChargeCounter: want *5 after backfill, got *%d", *s.MaxRangeChargeCounter)
	}
}

// TestMaxRangeChargeCounter_BackfillSkipsRowsWithoutPath verifies that rows whose
// raw_data lacks the charge_state.max_range_charge_counter path are NOT affected by
// the backfill UPDATE — they remain NULL after the migration runs.
func TestMaxRangeChargeCounter_BackfillSkipsRowsWithoutPath(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(920005)
	cleanupVehicle(t, pool, accountID, teslaID)

	// raw_data has no charge_state.max_range_charge_counter — the JSONB path does not exist.
	rawWithoutCounter := []byte(`{"charge_state":{"charging_state":"Disconnected"}}`)
	captured := time.Now().UTC().Truncate(time.Microsecond)
	snap := Snapshot{
		AccountID:             accountID,
		TeslaID:               teslaID,
		CapturedAt:            captured,
		CapturedDate:          clock.CalendarDay(captured, time.UTC),
		ChargingState:         "Disconnected",
		CarVersion:            "2026.20.1",
		RawData:               rawWithoutCounter,
		MaxRangeChargeCounter: nil,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

	// Run the migration backfill logic — the jsonb_typeof guard must exclude this row.
	_, err := pool.Exec(ctx, `
		UPDATE telemetry.vehicle_snapshots
		SET max_range_charge_counter =
		        (raw_data -> 'charge_state' ->> 'max_range_charge_counter')::INTEGER
		WHERE account_id = $1
		  AND tesla_id = $2
		  AND jsonb_typeof(raw_data -> 'charge_state' -> 'max_range_charge_counter') = 'number'`,
		accountID, teslaID)
	if err != nil {
		t.Fatalf("backfill UPDATE: %v", err)
	}

	// The row must still have NULL after the backfill (the UPDATE must be a no-op for it).
	got, err := st.latestSnapshotsByAccount(ctx, accountID)
	if err != nil {
		t.Fatalf("latestSnapshotsByAccount after backfill: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(got))
	}
	if got[0].MaxRangeChargeCounter != nil {
		t.Errorf("MaxRangeChargeCounter: want nil (row without JSONB path must not be touched), got *%d", *got[0].MaxRangeChargeCounter)
	}
}
