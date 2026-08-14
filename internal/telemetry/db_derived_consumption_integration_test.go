package telemetry

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	telemetrydb "github.com/cristianpena/magus-tesla-api/internal/telemetry/db"
)

// These tests exercise the five derived-consumption columns added by migration
// 20260814000001 (telemetry-add-derived-consumption-columns, MAG-10): the
// previousSnapshot store method (design D7/D8), the same-day-recapture
// staleness fix (design D6/D7, the direct regression for the leader's dispatch
// warning), and the rowToSnapshot mapping round-trip (design D9). They require
// the same testcontainers-provisioned Postgres as every other
// db_*_integration_test.go file in this package (see testdb_test.go) — no
// manual DB setup, no live Tesla API call.

const derivedConsumptionFloatTol = 1e-9

// TestStore_PreviousSnapshot_RoundTrips covers tasks.md T7.1: previousSnapshot
// returns the correct predecessor row for a two-snapshot vehicle, still
// returns the sole stored snapshot for a vehicle with only one row queried
// with a `before` after its own captured_at, and returns (nil, nil) — NOT an
// error — for a vehicle with no stored snapshots at all (design D8/D10).
func TestStore_PreviousSnapshot_RoundTrips(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(940001)
	cleanupVehicle(t, pool, accountID, teslaID)

	day0 := time.Date(2026, 2, 1, 4, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)

	first := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day0,
		CapturedDate:    dateOnly(day0, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      1000,
		BatteryLevelPct: 80,
		RawData:         []byte(`{"pass":1}`),
	}
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("insert day-0 snapshot: %v", err)
	}

	second := first
	second.CapturedAt = day1
	second.CapturedDate = dateOnly(day1, time.UTC)
	second.OdometerKm = 1040
	second.BatteryLevelPct = 68
	second.RawData = []byte(`{"pass":2}`)
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("insert day-1 snapshot: %v", err)
	}

	// previousSnapshot for day-1's own capture, bounded by dayStart(day1, UTC)
	// (design D7 — the LOCAL-day-start boundary, not day1's own captured_at),
	// must return the day-0 row.
	prev, err := st.previousSnapshot(ctx, accountID, teslaID, dayStart(day1, time.UTC))
	if err != nil {
		t.Fatalf("previousSnapshot: %v", err)
	}
	if prev == nil {
		t.Fatal("previousSnapshot: want non-nil (day-0 predecessor), got nil")
	}
	if !prev.CapturedAt.Equal(day0) {
		t.Errorf("previousSnapshot returned wrong row: want captured_at=%v, got %v", day0, prev.CapturedAt)
	}
	if prev.OdometerKm != 1000 {
		t.Errorf("previousSnapshot OdometerKm: want 1000, got %v", prev.OdometerKm)
	}

	// A vehicle with only ONE stored snapshot: a `before` well after that
	// snapshot's own captured_at still returns it (not "no predecessor").
	accountIDSingle := uuid.New()
	const teslaIDSingle = int64(940002)
	cleanupVehicle(t, pool, accountIDSingle, teslaIDSingle)
	only := Snapshot{
		AccountID:       accountIDSingle,
		TeslaID:         teslaIDSingle,
		CapturedAt:      day0,
		CapturedDate:    dateOnly(day0, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      500,
		BatteryLevelPct: 90,
		RawData:         []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, only); err != nil {
		t.Fatalf("insert single snapshot: %v", err)
	}
	before := day0.Add(48 * time.Hour) // well after the sole snapshot's own captured_at
	got, err := st.previousSnapshot(ctx, accountIDSingle, teslaIDSingle, before)
	if err != nil {
		t.Fatalf("previousSnapshot (single-row vehicle): %v", err)
	}
	if got == nil {
		t.Fatal("previousSnapshot (single-row vehicle): want non-nil, got nil")
	}
	if got.OdometerKm != 500 {
		t.Errorf("previousSnapshot (single-row vehicle) OdometerKm: want 500, got %v", got.OdometerKm)
	}

	// A vehicle with NO stored snapshots at all: (nil, nil) — not an error
	// (design D8/D10, the first-ever-snapshot case).
	noneAccountID := uuid.New()
	const noneTeslaID = int64(940003)
	none, err := st.previousSnapshot(ctx, noneAccountID, noneTeslaID, before)
	if err != nil {
		t.Fatalf("previousSnapshot (no snapshots): want nil error, got %v", err)
	}
	if none != nil {
		t.Errorf("previousSnapshot (no snapshots): want nil, got %+v", *none)
	}
}

// TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns is the direct
// regression test for design.md's D7 same-day-recapture refinement and the
// D6/L1 staleness bug the leader's dispatch warned about (tasks.md T7.2).
//
// Sequence: a day-0 predecessor row exists; a first day-1 capture (A) is
// derived against day-0 and stored; a SECOND, same-day capture (B) with
// different odometer/battery readings arrives (the same-day re-capture,
// ON CONFLICT ... DO UPDATE). The crucial assertion is that B's own
// previousSnapshot lookup — bounded by dayStart(B's captured_at), design D7 —
// still resolves to the DAY-0 predecessor, NOT to A (which is already stored
// with an earlier captured_at on the SAME captured_date and is about to be
// replaced). If the lookup instead selected A, the derived columns would
// reflect a near-zero same-day delta instead of the correct day-over-day
// delta against day-0 — exactly the bug D7 exists to prevent.
func TestStore_SnapshotUpsert_RecapturesRecomputeDerivedColumns(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(940010)
	cleanupVehicle(t, pool, accountID, teslaID)

	day0CapturedAt := time.Date(2026, 4, 1, 3, 30, 0, 0, time.UTC)
	day1CapturedAtA := time.Date(2026, 4, 2, 3, 30, 0, 0, time.UTC) // first nightly run
	day1CapturedAtB := time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC) // same day, later recapture

	// Day-0 predecessor: no prior row, so it is inserted directly with no
	// derived columns (it is itself a first-ever snapshot from the fixture's
	// point of view).
	day0Snapshot := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day0CapturedAt,
		CapturedDate:    dateOnly(day0CapturedAt, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      5000,
		BatteryLevelPct: 80,
		RawData:         []byte(`{"pass":"day0"}`),
	}
	if err := st.insertSnapshot(ctx, day0Snapshot); err != nil {
		t.Fatalf("insert day-0 predecessor: %v", err)
	}

	// First day-1 capture (A): previousSnapshot must resolve to day-0.
	prevA, err := st.previousSnapshot(ctx, accountID, teslaID, dayStart(day1CapturedAtA, time.UTC))
	if err != nil {
		t.Fatalf("previousSnapshot for capture A: %v", err)
	}
	if prevA == nil || prevA.OdometerKm != 5000 || prevA.BatteryLevelPct != 80 {
		t.Fatalf("previousSnapshot for capture A: want day-0 (odo=5000, battery=80), got %+v", prevA)
	}
	curA := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day1CapturedAtA,
		CapturedDate:    dateOnly(day1CapturedAtA, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      5040,
		BatteryLevelPct: 68,
		RawData:         []byte(`{"pass":"day1-A"}`),
	}
	snapA := deriveConsumption(prevA, curA)
	if err := st.insertSnapshot(ctx, snapA); err != nil {
		t.Fatalf("insert day-1 capture A: %v", err)
	}

	// Second, same-day capture (B): the critical assertion. previousSnapshot,
	// bounded by dayStart(B's captured_at) — the START of day-1's LOCAL
	// calendar day, NOT B's own captured_at — must STILL resolve to day-0,
	// even though capture A (5040/68) is already stored with an earlier
	// captured_at on the same captured_date.
	prevB, err := st.previousSnapshot(ctx, accountID, teslaID, dayStart(day1CapturedAtB, time.UTC))
	if err != nil {
		t.Fatalf("previousSnapshot for capture B: %v", err)
	}
	if prevB == nil {
		t.Fatal("previousSnapshot for capture B: want non-nil (day-0 predecessor), got nil")
	}
	if prevB.OdometerKm != 5000 || prevB.BatteryLevelPct != 80 {
		t.Fatalf("previousSnapshot for capture B: want DAY-0 predecessor (odo=5000, battery=80) — NOT capture A (odo=5040, battery=68). got odo=%v battery=%v (D7 staleness bug if this is capture A's values)",
			prevB.OdometerKm, prevB.BatteryLevelPct)
	}

	curB := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day1CapturedAtB,
		CapturedDate:    dateOnly(day1CapturedAtB, time.UTC), // SAME captured_date as A — triggers ON CONFLICT DO UPDATE
		ChargingState:   "Disconnected",
		CarVersion:      "v2",
		OdometerKm:      5060,
		BatteryLevelPct: 62,
		RawData:         []byte(`{"pass":"day1-B"}`),
	}
	snapB := deriveConsumption(prevB, curB)
	if err := st.insertSnapshot(ctx, snapB); err != nil {
		t.Fatalf("insert day-1 capture B (same-day recapture): %v", err)
	}

	rows, err := q.ListSnapshotsByVehicle(ctx, telemetrydb.ListSnapshotsByVehicleParams{
		AccountID: accountID,
		TeslaID:   teslaID,
	})
	if err != nil {
		t.Fatalf("ListSnapshotsByVehicle: %v", err)
	}
	// Exactly two rows total: day-0 and the single surviving (upserted) day-1
	// row — capture A was replaced in place, never a third row.
	if len(rows) != 2 {
		t.Fatalf("want 2 rows (day-0 + one upserted day-1 row), got %d", len(rows))
	}

	wantDay1Date := dateOnly(day1CapturedAtB, time.UTC)
	var day1Row *telemetrydb.VehicleSnapshot
	for i := range rows {
		if rows[i].CapturedDate.Time.Equal(wantDay1Date) {
			day1Row = &rows[i]
		}
	}
	if day1Row == nil {
		t.Fatal("could not find the day-1 row among the 2 stored rows")
	}

	// The surviving row must carry capture B's readings (upsert replaced A).
	if day1Row.OdometerKm != 5060 || day1Row.BatteryLevelPct != 62 {
		t.Fatalf("day-1 row was not replaced by capture B: want odo=5060 battery=62, got odo=%v battery=%v", day1Row.OdometerKm, day1Row.BatteryLevelPct)
	}
	if day1Row.CarVersion != "v2" {
		t.Errorf("day-1 row CarVersion: want v2 (capture B), got %q", day1Row.CarVersion)
	}

	got := rowToSnapshot(*day1Row)

	// The five derived columns must reflect a recompute against the DAY-0
	// predecessor (odo=5000, battery=80) using capture B's readings
	// (odo=5060, battery=62) — NOT against capture A (odo=5040, battery=68),
	// which was itself replaced. This is the direct D7/D6 regression check:
	// distance = 60 (not 20, which A-as-predecessor would give), battery
	// used = 18 (not 6).
	wantDistance := 60.0  // 5060 - 5000
	wantBatteryUsed := 18 // 80 - 62
	wantDays := 1
	wantKmPerPct := wantDistance / float64(wantBatteryUsed)
	wantEstRange := wantKmPerPct * 100

	if got.DistanceTraveledKmCalc == nil || math.Abs(*got.DistanceTraveledKmCalc-wantDistance) > derivedConsumptionFloatTol {
		t.Errorf("DistanceTraveledKmCalc: want ~%v (against day-0), got %v", wantDistance, got.DistanceTraveledKmCalc)
	}
	if got.BatteryUsedPctCalc == nil || *got.BatteryUsedPctCalc != wantBatteryUsed {
		t.Errorf("BatteryUsedPctCalc: want %v (against day-0), got %v", wantBatteryUsed, got.BatteryUsedPctCalc)
	}
	if got.DaysSpannedCalc == nil || *got.DaysSpannedCalc != wantDays {
		t.Errorf("DaysSpannedCalc: want %v, got %v", wantDays, got.DaysSpannedCalc)
	}
	if got.KmPerPctCalc == nil || math.Abs(*got.KmPerPctCalc-wantKmPerPct) > derivedConsumptionFloatTol {
		t.Errorf("KmPerPctCalc: want ~%v (against day-0), got %v", wantKmPerPct, got.KmPerPctCalc)
	}
	if got.EstimatedRangeKmCalc == nil || math.Abs(*got.EstimatedRangeKmCalc-wantEstRange) > derivedConsumptionFloatTol {
		t.Errorf("EstimatedRangeKmCalc: want ~%v (against day-0), got %v", wantEstRange, got.EstimatedRangeKmCalc)
	}
}

// TestStore_DerivedConsumptionColumns_RoundTrip covers tasks.md T7.3: a
// snapshot inserted with all five derived fields set — including a NEGATIVE
// BatteryUsedPctCalc (net overnight charge, DU2) and nil efficiency fields —
// comes back identical through ListSnapshotsByVehicle/rowToSnapshot (T5.1's
// mapping), mirroring the module's existing sentry/TPMS/Source-A round-trip
// test precedent (db_tpms_integration_test.go, db_sourcea_integration_test.go).
func TestStore_DerivedConsumptionColumns_RoundTrip(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	q := telemetrydb.New(pool)

	accountID := uuid.New()
	const teslaID = int64(940020)
	cleanupVehicle(t, pool, accountID, teslaID)

	captured := time.Now().UTC().Truncate(time.Microsecond)

	distance := 5.0
	batteryUsed := -22 // negative: net charge overnight (DU2) — truthful, not clamped
	days := 1

	snap := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      captured,
		CapturedDate:    dateOnly(captured, time.UTC),
		ChargingState:   "Charging",
		CarVersion:      "2026.20.1",
		OdometerKm:      5045,
		BatteryLevelPct: 90,
		RawData:         []byte(`{"charge_state":{"charging_state":"Charging"}}`),

		DistanceTraveledKmCalc: &distance,
		BatteryUsedPctCalc:     &batteryUsed,
		KmPerPctCalc:           nil, // nil: non-positive divisor (D2)
		EstimatedRangeKmCalc:   nil, // nil: same reason (D2)
		DaysSpannedCalc:        &days,
	}
	if err := st.insertSnapshot(ctx, snap); err != nil {
		t.Fatalf("insertSnapshot: %v", err)
	}

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

	got := rowToSnapshot(rows[0])

	if got.DistanceTraveledKmCalc == nil || *got.DistanceTraveledKmCalc != distance {
		t.Errorf("DistanceTraveledKmCalc: want *%v, got %v", distance, got.DistanceTraveledKmCalc)
	}
	if got.BatteryUsedPctCalc == nil || *got.BatteryUsedPctCalc != batteryUsed {
		t.Errorf("BatteryUsedPctCalc: want *%v (negative, truthful), got %v", batteryUsed, got.BatteryUsedPctCalc)
	}
	if got.KmPerPctCalc != nil {
		t.Errorf("KmPerPctCalc: want nil (non-positive divisor), got *%v", *got.KmPerPctCalc)
	}
	if got.EstimatedRangeKmCalc != nil {
		t.Errorf("EstimatedRangeKmCalc: want nil (non-positive divisor), got *%v", *got.EstimatedRangeKmCalc)
	}
	if got.DaysSpannedCalc == nil || *got.DaysSpannedCalc != days {
		t.Errorf("DaysSpannedCalc: want *%v, got %v", days, got.DaysSpannedCalc)
	}

	// Also verify at the raw pgtype boundary: the two nil efficiency fields
	// must be SQL NULL (Valid=false), and the negative BatteryUsedPctCalc must
	// be Valid=true with the negative Int32 preserved (not coerced/clamped).
	row := rows[0]
	if !row.BatteryUsedPctCalc.Valid || row.BatteryUsedPctCalc.Int32 != int32(batteryUsed) {
		t.Errorf("raw BatteryUsedPctCalc: want Valid=true Int32=%d, got Valid=%t Int32=%d", batteryUsed, row.BatteryUsedPctCalc.Valid, row.BatteryUsedPctCalc.Int32)
	}
	if row.KmPerPctCalc.Valid {
		t.Errorf("raw KmPerPctCalc: want Valid=false (SQL NULL), got Valid=true value=%v", row.KmPerPctCalc.Float64)
	}
	if row.EstimatedRangeKmCalc.Valid {
		t.Errorf("raw EstimatedRangeKmCalc: want Valid=false (SQL NULL), got Valid=true value=%v", row.EstimatedRangeKmCalc.Float64)
	}
}
