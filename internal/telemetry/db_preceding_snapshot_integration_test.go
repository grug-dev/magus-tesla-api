package telemetry

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
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

// TestStore_PreviousSnapshot_RoundTrips is the direct re-home of the same-named
// test from the deleted db_derived_consumption_integration_test.go (design.md
// "Characterization parity contract"), rewritten against the public
// Reader.SnapshotPrecedingDay port instead of the deleted private
// previousSnapshot seam. All three original cases are kept: a predecessor
// exists; a single-row vehicle queried with a `day` after its only row still
// returns that row; a vehicle with no stored snapshots at all returns
// (nil, nil), never an error (design D2/D8/D10).
func TestStore_PreviousSnapshot_RoundTrips(t *testing.T) {
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
	dayAfter := dateOnly(day0.AddDate(0, 0, 2), time.UTC) // well after the sole snapshot's own captured_date
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
