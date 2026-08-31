package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// These tests exercise Reader.SnapshotPrecedingDay (RM29-telemetry-drop-derived-
// columns tier 4, design D2), the public port that replaced the module's
// private previousSnapshot store seam and the five derived-consumption columns
// it used to feed for internal/analytics — all deleted by migration
// 20260822000001 (design D8/D9). Formerly
// db_derived_consumption_integration_test.go; renamed to reflect what this
// file now covers, per design.md's "Characterization parity contract" table.
// They require the same testcontainers-provisioned Postgres as every other
// db_*_integration_test.go file in this package (see testdb_test.go) — no
// manual DB setup, no live Tesla API call.

// TestReader_SnapshotPrecedingDay_RoundTrips is the direct re-home of
// TestStore_PreviousSnapshot_RoundTrips from the deleted
// db_derived_consumption_integration_test.go (design.md "Characterization
// parity contract"), rewritten against the public Reader.SnapshotPrecedingDay
// port instead of the deleted private previousSnapshot seam. Renamed (task
// 6a.1) because the old Store_/PreviousSnapshot name described a seam that no
// longer exists — this test only ever exercised the public port. All three
// original cases are kept: a predecessor exists; a single-row vehicle queried
// with a `day` after its only row still returns that row; a vehicle with no
// stored snapshots at all returns (nil, nil), never an error (design D2/D8/D10).
func TestReader_SnapshotPrecedingDay_RoundTrips(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	reader := NewReader(pool)

	accountID := uuid.New()
	const teslaID = int64(940001)
	cleanupVehicle(t, pool, accountID, teslaID)

	day0 := time.Date(2026, 2, 1, 4, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)

	first := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day0,
		CapturedDate:    clock.CalendarDay(day0, time.UTC),
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
	second.CapturedDate = clock.CalendarDay(day1, time.UTC)
	second.OdometerKm = 1040
	second.BatteryLevelPct = 68
	second.RawData = []byte(`{"pass":2}`)
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("insert day-1 snapshot: %v", err)
	}

	// SnapshotPrecedingDay(day1's CapturedDate) must return the day-0 row —
	// the bound is day1's calendar day itself (design D2), not an instant.
	prev, err := reader.SnapshotPrecedingDay(ctx, accountID, teslaID, second.CapturedDate)
	if err != nil {
		t.Fatalf("SnapshotPrecedingDay: %v", err)
	}
	if prev == nil {
		t.Fatal("SnapshotPrecedingDay: want non-nil (day-0 predecessor), got nil")
	}
	if !prev.CapturedAt.Equal(day0) {
		t.Errorf("SnapshotPrecedingDay returned wrong row: want captured_at=%v, got %v", day0, prev.CapturedAt)
	}
	if prev.OdometerKm != 1000 {
		t.Errorf("SnapshotPrecedingDay OdometerKm: want 1000, got %v", prev.OdometerKm)
	}

	// A vehicle with only ONE stored snapshot: a `day` well after that
	// snapshot's own captured_date still returns it (not "no predecessor").
	accountIDSingle := uuid.New()
	const teslaIDSingle = int64(940002)
	cleanupVehicle(t, pool, accountIDSingle, teslaIDSingle)
	only := Snapshot{
		AccountID:       accountIDSingle,
		TeslaID:         teslaIDSingle,
		CapturedAt:      day0,
		CapturedDate:    clock.CalendarDay(day0, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      500,
		BatteryLevelPct: 90,
		RawData:         []byte(`{}`),
	}
	if err := st.insertSnapshot(ctx, only); err != nil {
		t.Fatalf("insert single snapshot: %v", err)
	}
	dayAfter := clock.CalendarDay(day0.AddDate(0, 0, 2), time.UTC) // well after the sole snapshot's own captured_date
	got, err := reader.SnapshotPrecedingDay(ctx, accountIDSingle, teslaIDSingle, dayAfter)
	if err != nil {
		t.Fatalf("SnapshotPrecedingDay (single-row vehicle): %v", err)
	}
	if got == nil {
		t.Fatal("SnapshotPrecedingDay (single-row vehicle): want non-nil, got nil")
	}
	if got.OdometerKm != 500 {
		t.Errorf("SnapshotPrecedingDay (single-row vehicle) OdometerKm: want 500, got %v", got.OdometerKm)
	}

	// A vehicle with NO stored snapshots at all: (nil, nil) — not an error
	// (design D2/D8/D10, the first-ever-snapshot case).
	noneAccountID := uuid.New()
	const noneTeslaID = int64(940003)
	none, err := reader.SnapshotPrecedingDay(ctx, noneAccountID, noneTeslaID, dayAfter)
	if err != nil {
		t.Fatalf("SnapshotPrecedingDay (no snapshots): want nil error, got %v", err)
	}
	if none != nil {
		t.Errorf("SnapshotPrecedingDay (no snapshots): want nil, got %+v", *none)
	}
}

// TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan verifies design.md's
// Index Plan (RM29-telemetry-drop-derived-columns): the query reuses
// idx_vehicle_snapshots_vehicle_time (account_id, tesla_id, captured_at) by
// walking it BACKWARD to satisfy ORDER BY captured_at DESC, rather than adding
// a new index or falling back to a sequential scan. Mirrors the same-shaped
// EXPLAIN assertion design.md documents for SnapshotsByVehicleUpdatedSince
// (telemetry.go's doc comment on that method) — no such Go test currently
// exists in the repo to literally mirror line-for-line, so this test is
// authored directly from design.md's "EXPLAIN assertion" section instead.
// The SELECT below is copied verbatim from the generated
// internal/telemetry/db/query.sql.go's snapshotPrecedingDay constant (query.sql
// task 1.1), prefixed with EXPLAIN (FORMAT TEXT), so a drift between this test
// and the real query would only ever make the test fail, never pass on a stale
// copy silently.
func TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const teslaID = int64(940004)
	cleanupVehicle(t, pool, accountID, teslaID)

	day0 := time.Date(2026, 3, 1, 4, 0, 0, 0, time.UTC)
	day1 := day0.AddDate(0, 0, 1)

	older := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      day0,
		CapturedDate:    clock.CalendarDay(day0, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      1500,
		BatteryLevelPct: 75,
		RawData:         []byte(`{"explain":"day0"}`),
	}
	if err := st.insertSnapshot(ctx, older); err != nil {
		t.Fatalf("insert day-0 snapshot: %v", err)
	}
	newer := older
	newer.CapturedAt = day1
	newer.CapturedDate = clock.CalendarDay(day1, time.UTC)
	newer.OdometerKm = 1560
	newer.BatteryLevelPct = 70
	newer.RawData = []byte(`{"explain":"day1"}`)
	if err := st.insertSnapshot(ctx, newer); err != nil {
		t.Fatalf("insert day-1 snapshot: %v", err)
	}

	// The query dateFrom(day) binds against — same shape reader.go's
	// SnapshotPrecedingDay implementation uses (design D2).
	explainDay := clock.CalendarDay(day1.AddDate(0, 0, 1), time.UTC)

	rows, err := pool.Query(ctx, `EXPLAIN (FORMAT TEXT)
SELECT
    id, account_id, tesla_id, captured_at, raw_data,
    battery_level_pct, battery_range_km, charging_state, charge_limit_soc_pct,
    odometer_km, inside_temp_c, outside_temp_c, locked, sentry_mode,
    car_version,
    charge_energy_added_kwh, charger_power_kw, charger_voltage_v,
    charger_actual_current_a, usable_battery_level_pct,
    max_range_charge_counter,
    tpms_pressure_fl_psi, tpms_pressure_fr_psi, tpms_pressure_rl_psi, tpms_pressure_rr_psi,
    captured_date, updated_at
FROM vehicle_snapshots
WHERE account_id   = $1
  AND tesla_id     = $2
  AND captured_date < $3
ORDER BY captured_at DESC
LIMIT 1`, accountID, teslaID, dateFrom(explainDay))
	if err != nil {
		t.Fatalf("EXPLAIN query: %v", err)
	}
	defer rows.Close()

	var plan strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatalf("scanning EXPLAIN row: %v", err)
		}
		plan.WriteString(line)
		plan.WriteString("\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("EXPLAIN rows: %v", err)
	}

	planText := plan.String()
	if !strings.Contains(planText, "Index Scan Backward") {
		t.Errorf("expected plan to contain %q, got:\n%s", "Index Scan Backward", planText)
	}
	if !strings.Contains(planText, "idx_vehicle_snapshots_vehicle_time") {
		t.Errorf("expected plan to contain %q, got:\n%s", "idx_vehicle_snapshots_vehicle_time", planText)
	}
	if strings.Contains(planText, "Seq Scan") {
		t.Errorf("expected plan to NOT contain %q — design.md's \"deliberately not added\" index reasoning depends on this plan; report to the leader instead of adding an index, got:\n%s", "Seq Scan", planText)
	}
}

// TestReader_SnapshotPrecedingDay_SameDayRecaptureNotItsOwnPredecessor pins
// the guarantee the deleted dayStart used to provide (tier
// telemetry-add-derived-consumption-columns design D7), re-expressed as
// SnapshotPrecedingDay's captured_date < @day predicate (design.md D2) — the
// replacement for the deleted TestDayStart_* tests (task 4.7). A same-day
// re-capture REPLACES the existing row via the dedupe UPSERT
// (vehicle_snapshots_account_tesla_date_unique); SnapshotPrecedingDay must
// still return the day N−1 row, never either the first or the replacing day-N
// capture.
func TestReader_SnapshotPrecedingDay_SameDayRecaptureNotItsOwnPredecessor(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()
	reader := NewReader(pool)

	accountID := uuid.New()
	const teslaID = int64(940005)
	cleanupVehicle(t, pool, accountID, teslaID)

	dayNMinus1 := time.Date(2026, 4, 10, 3, 30, 0, 0, time.UTC)
	dayN := dayNMinus1.AddDate(0, 0, 1)

	predecessor := Snapshot{
		AccountID:       accountID,
		TeslaID:         teslaID,
		CapturedAt:      dayNMinus1,
		CapturedDate:    clock.CalendarDay(dayNMinus1, time.UTC),
		ChargingState:   "Disconnected",
		CarVersion:      "v1",
		OdometerKm:      2000,
		BatteryLevelPct: 70,
		RawData:         []byte(`{"day":"n-1"}`),
	}
	if err := st.insertSnapshot(ctx, predecessor); err != nil {
		t.Fatalf("insert day N-1 snapshot: %v", err)
	}

	firstCaptureOnDayN := dayN.Add(1 * time.Hour)
	first := predecessor
	first.CapturedAt = firstCaptureOnDayN
	first.CapturedDate = clock.CalendarDay(firstCaptureOnDayN, time.UTC)
	first.OdometerKm = 2050
	first.BatteryLevelPct = 60
	first.RawData = []byte(`{"day":"n","pass":1}`)
	if err := st.insertSnapshot(ctx, first); err != nil {
		t.Fatalf("insert day N first capture: %v", err)
	}

	// A second capture on the SAME calendar day (e.g. a repeat nightly run
	// within the poller's timeout window) REPLACES the first via the dedupe
	// UPSERT — this is exactly the scenario dayStart used to guard against.
	secondCaptureOnDayN := firstCaptureOnDayN.Add(2 * time.Hour)
	second := first
	second.CapturedAt = secondCaptureOnDayN
	second.CapturedDate = clock.CalendarDay(secondCaptureOnDayN, time.UTC)
	second.OdometerKm = 2055
	second.BatteryLevelPct = 59
	second.RawData = []byte(`{"day":"n","pass":2}`)
	if err := st.insertSnapshot(ctx, second); err != nil {
		t.Fatalf("insert day N second (replacing) capture: %v", err)
	}

	got, err := reader.SnapshotPrecedingDay(ctx, accountID, teslaID, second.CapturedDate)
	if err != nil {
		t.Fatalf("SnapshotPrecedingDay: %v", err)
	}
	if got == nil {
		t.Fatal("SnapshotPrecedingDay: want the day N-1 row, got nil")
	}
	if !got.CapturedDate.Equal(predecessor.CapturedDate) {
		t.Errorf("SnapshotPrecedingDay returned wrong day: want CapturedDate=%v (day N-1), got %v", predecessor.CapturedDate, got.CapturedDate)
	}
	if got.OdometerKm != 2000 {
		t.Errorf("SnapshotPrecedingDay: want day N-1's OdometerKm 2000 (never a day-N row), got %v", got.OdometerKm)
	}
	if got.CapturedAt.Equal(first.CapturedAt) || got.CapturedAt.Equal(second.CapturedAt) {
		t.Errorf("SnapshotPrecedingDay must never return a day-N row (its own about-to-be-replaced capture); got CapturedAt=%v", got.CapturedAt)
	}
}
