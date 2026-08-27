// Package charging_test — database-backed integration tests for SessionReader
// (session_reader.go) and its ListSessionsByVehicleBetween query (db/query.sql),
// covering design.md's Test Contract T1–T11 (RM30-charging-add-session-read-port,
// tasks.md task 3.1).
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has) and, for the human-owned battery-percentage columns, by direct SQL —
// mirroring db_session_integration_test.go's existing pattern. Assertions are ONLY
// against charging.Session domain fields — pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes).
//
// Test → Test Contract case mapping:
//
//	T1, T2, T3, T4, T9, T10  TestListSessionsByVehicleBetween_S1_BoundariesOrderingAndPercentages
//	T5                       TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice
//	T6                       TestListSessionsByVehicleBetween_MultiTenantIsolation
//	T7                       TestListSessionsByVehicleBetween_DifferentVehicleSameAccountIsolation
//	T8                       TestListSessionsByVehicleBetween_NullTeslaIDNeverReturned
//	T11                      TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- session-reader-specific test helpers ---

// fetchSessionsByVehicleBetween wraps SessionReader.ListSessionsByVehicleBetween for
// this file's tests, failing the test on any error from the port itself.
func fetchSessionsByVehicleBetween(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, from, to time.Time) []charging.Session {
	t.Helper()
	r := charging.NewSessionReader(pool)
	sessions, err := r.ListSessionsByVehicleBetween(context.Background(), accountID, teslaID, from, to)
	if err != nil {
		t.Fatalf("ListSessionsByVehicleBetween: %v", err)
	}
	return sessions
}

// seedS1 seeds design.md's baseline fixture S1: four sessions under accountID/teslaID,
// session_ids 940001-940004, spanning the query window [2026-08-01, 2026-08-31] with
// 940004 deliberately stopping at the first instant of the day AFTER the window (so it
// is excluded). Seeded via SessionWriter.MirrorSessions, then 940002's battery
// percentages are written directly by SQL — mirroring
// db_session_integration_test.go's existing pattern for the human-owned columns.
func seedS1(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	sessions := []charging.SessionMirror{
		{
			AccountID:           accountID,
			VIN:                 "V940001",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           940001,
			ChargeStartDateTime: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "S1 Site",
		},
		{
			AccountID:           accountID,
			VIN:                 "V940001",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           940002,
			ChargeStartDateTime: time.Date(2026, 7, 31, 23, 50, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 10, 0, 0, time.UTC),
			SiteLocationName:    "S1 Site",
		},
		{
			AccountID:           accountID,
			VIN:                 "V940001",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           940003,
			ChargeStartDateTime: time.Date(2026, 8, 31, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 31, 23, 59, 59, 999999000, time.UTC),
			SiteLocationName:    "S1 Site",
		},
		{
			AccountID:           accountID,
			VIN:                 "V940001",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           940004,
			ChargeStartDateTime: time.Date(2026, 8, 31, 23, 50, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "S1 Site",
		},
	}
	if err := w.MirrorSessions(ctx, accountID, sessions); err != nil {
		t.Fatalf("seedS1: MirrorSessions: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE charge_sessions
		SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified',
		    start_battery_pct_est = 22, end_battery_pct_est = 78
		WHERE account_id = $1 AND session_id = 940002`,
		accountID); err != nil {
		t.Fatalf("seedS1: direct-SQL set battery percentages on 940002: %v", err)
	}
}

// --- T1, T2, T3, T4, T9, T10 — the shared S1 fixture and its one call ---

// TestListSessionsByVehicleBetween_S1_BoundariesOrderingAndPercentages seeds S1 and
// makes the one design.md call (from = 2026-08-01, to = 2026-08-31), then asserts
// every expectation the Test Contract derives from that single result set:
//   - T1: 940001 (stops exactly at from_time) is included.
//   - T2: 940003 (stops at 23:59:59.999999Z on the to day) is included.
//   - T3: 940004 (stops exactly at end_bound, the day after to) is excluded.
//   - T4: 940002 (starts before the window, stops inside it) is included, and its
//     ChargeStartDateTime is reported as recorded, not clamped to the window.
//   - T9: 940002 carries the battery percentages written by direct SQL; 940001 and
//     940003 carry all five as nil.
//   - T10: results are ordered ascending by ChargeStopDateTime: 940001, 940002, 940003.
func TestListSessionsByVehicleBetween_S1_BoundariesOrderingAndPercentages(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargeSessions(t, pool, accountID)
	const teslaID = int64(940001)
	seedS1(t, pool, accountID, teslaID)

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)

	// T3: 940004 excluded -> exactly 3 rows.
	if len(sessions) != 3 {
		t.Fatalf("expected 3 sessions (940004 excluded per T3), got %d: %+v", len(sessions), sessions)
	}

	// T10: ascending order by ChargeStopDateTime, not SessionID or insertion order
	// (they happen to coincide here, but the assertion is on stop-time order).
	wantIDs := []int64{940001, 940002, 940003}
	for i, s := range sessions {
		if s.SessionID != wantIDs[i] {
			t.Errorf("T10: sessions[%d].SessionID = %d, want %d (ascending ChargeStopDateTime order)", i, s.SessionID, wantIDs[i])
		}
	}

	byID := make(map[int64]charging.Session, len(sessions))
	for _, s := range sessions {
		byID[s.SessionID] = s
	}

	// T1: lower bound inclusive.
	if _, ok := byID[940001]; !ok {
		t.Error("T1: expected 940001 present (stops exactly at from_time, the lower bound)")
	}

	// T2: last representable instant of the `to` day is included.
	if _, ok := byID[940003]; !ok {
		t.Error("T2: expected 940003 present (stops at 23:59:59.999999Z on the to day)")
	}

	// T4: session that starts before the window but stops inside it.
	s940002, ok := byID[940002]
	if !ok {
		t.Fatal("T4: expected 940002 present (starts before window, stops inside it)")
	}
	wantStart := time.Date(2026, 7, 31, 23, 50, 0, 0, time.UTC)
	if !s940002.ChargeStartDateTime.Equal(wantStart) {
		t.Errorf("T4: 940002.ChargeStartDateTime = %v, want %v (reported as recorded, even though it precedes the window)", s940002.ChargeStartDateTime, wantStart)
	}

	// T9: battery percentages carried on 940002; nil on 940001/940003.
	if s940002.StartBatteryPct == nil || *s940002.StartBatteryPct != 20 {
		t.Errorf("T9: 940002.StartBatteryPct = %v, want 20", s940002.StartBatteryPct)
	}
	if s940002.EndBatteryPct == nil || *s940002.EndBatteryPct != 80 {
		t.Errorf("T9: 940002.EndBatteryPct = %v, want 80", s940002.EndBatteryPct)
	}
	if s940002.BatteryPctSource == nil || *s940002.BatteryPctSource != "user_verified" {
		t.Errorf("T9: 940002.BatteryPctSource = %v, want user_verified", s940002.BatteryPctSource)
	}
	if s940002.StartBatteryPctEst == nil || *s940002.StartBatteryPctEst != 22 {
		t.Errorf("T9: 940002.StartBatteryPctEst = %v, want 22", s940002.StartBatteryPctEst)
	}
	if s940002.EndBatteryPctEst == nil || *s940002.EndBatteryPctEst != 78 {
		t.Errorf("T9: 940002.EndBatteryPctEst = %v, want 78", s940002.EndBatteryPctEst)
	}

	for _, id := range []int64{940001, 940003} {
		s := byID[id]
		if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil ||
			s.StartBatteryPctEst != nil || s.EndBatteryPctEst != nil {
			t.Errorf("T9: session %d: expected all five battery fields nil, got %+v", id, s)
		}
	}
}

// --- T5 ---

// TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice: a query against an
// account/vehicle with no sessions at all returns a non-nil, zero-length slice.
func TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargeSessions(t, pool, accountID)
	// No sessions seeded for this account at all.

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleBetween(t, pool, accountID, 940001, from, to)

	if sessions == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// --- T6 ---

// TestListSessionsByVehicleBetween_MultiTenantIsolation: an identical session under a
// second account, same TeslaID and an overlapping window, never leaks into the first
// account's result.
func TestListSessionsByVehicleBetween_MultiTenantIsolation(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	acctB := uuid.New()
	cleanupChargeSessions(t, pool, acctA, acctB)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const teslaID = int64(940001)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	inWindowStop := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	sessA := charging.SessionMirror{
		AccountID: acctA, VIN: "VA", TeslaID: ptrInt64(teslaID), SessionID: 940010,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "A Site",
	}
	sessB := charging.SessionMirror{
		AccountID: acctB, VIN: "VB", TeslaID: ptrInt64(teslaID), SessionID: 940011,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "B Site",
	}
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{sessA}); err != nil {
		t.Fatalf("MirrorSessions acctA: %v", err)
	}
	if err := w.MirrorSessions(ctx, acctB, []charging.SessionMirror{sessB}); err != nil {
		t.Fatalf("MirrorSessions acctB: %v", err)
	}

	sessions := fetchSessionsByVehicleBetween(t, pool, acctA, teslaID, from, to)
	if len(sessions) != 1 || sessions[0].SessionID != 940010 {
		t.Fatalf("expected only acctA's session (940010), got %+v", sessions)
	}
}

// --- T7 ---

// TestListSessionsByVehicleBetween_DifferentVehicleSameAccountIsolation: a second
// vehicle's session within the SAME account and window does not leak into a
// teslaID-scoped read for a different vehicle.
func TestListSessionsByVehicleBetween_DifferentVehicleSameAccountIsolation(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargeSessions(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	inWindowStop := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	primary := charging.SessionMirror{
		AccountID: accountID, VIN: "VPRIMARY", TeslaID: ptrInt64(940001), SessionID: 940020,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "Primary Site",
	}
	otherVehicle := charging.SessionMirror{
		AccountID: accountID, VIN: "VOTHER", TeslaID: ptrInt64(940099), SessionID: 940021,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "Other Vehicle Site",
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{primary, otherVehicle}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	sessions := fetchSessionsByVehicleBetween(t, pool, accountID, 940001, from, to)
	for _, s := range sessions {
		if s.SessionID == 940021 {
			t.Errorf("expected session 940021 (different vehicle, TeslaID 940099) to be absent, but it was returned")
		}
	}
	if len(sessions) != 1 || sessions[0].SessionID != 940020 {
		t.Fatalf("expected only session 940020, got %+v", sessions)
	}
}

// --- T8 ---

// TestListSessionsByVehicleBetween_NullTeslaIDNeverReturned: a session mirrored with
// TeslaID: nil (a deregistered vehicle) is never returned by any teslaID value — SQL's
// NULL = value is neither true nor false (design.md D6). Not a bug to special-case NULL
// away — an orphaned session is definitionally outside a vehicle-scoped read.
func TestListSessionsByVehicleBetween_NullTeslaIDNeverReturned(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargeSessions(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	inWindowStop := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	orphaned := charging.SessionMirror{
		AccountID: accountID, VIN: "VORPHAN", TeslaID: nil, SessionID: 940030,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "Orphaned Site",
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{orphaned}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	for _, teslaID := range []int64{940001, 940030, 0} {
		sessions := fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)
		for _, s := range sessions {
			if s.SessionID == 940030 {
				t.Errorf("teslaID=%d: expected orphaned session 940030 (NULL tesla_id) never returned, but it was", teslaID)
			}
		}
	}
}

// --- T11 ---

// TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil: a session mirrored
// with all four fee fields nil round-trips as nil through the reverse pgtype helpers
// (D6), and SiteLocationName (NOT NULL) is still populated.
func TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargeSessions(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const teslaID = int64(940001)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	inWindowStop := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	noFees := charging.SessionMirror{
		AccountID: accountID, VIN: "VNOFEE", TeslaID: ptrInt64(teslaID), SessionID: 940040,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "No Fees Site",
		// EnergyKWh, TotalCost, Currency, IsPaid deliberately left nil.
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{noFees}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	sessions := fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.EnergyKWh != nil {
		t.Errorf("EnergyKWh: want nil, got %v", *s.EnergyKWh)
	}
	if s.TotalCost != nil {
		t.Errorf("TotalCost: want nil, got %v", *s.TotalCost)
	}
	if s.Currency != nil {
		t.Errorf("Currency: want nil, got %v", *s.Currency)
	}
	if s.IsPaid != nil {
		t.Errorf("IsPaid: want nil, got %v", *s.IsPaid)
	}
	if s.SiteLocationName == "" {
		t.Errorf("SiteLocationName: want non-empty, got empty")
	}
}
