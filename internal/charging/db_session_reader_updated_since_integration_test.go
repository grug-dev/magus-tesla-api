// Package charging_test — database-backed integration tests for
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince
// (session_reader.go) and its ListSessionsByVehicleUpdatedSince query
// (db/query.sql), covering design.md's Test Contract T1–T7
// (RM31-charging-add-session-read-ports, tasks.md task 3.1).
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has) and, for updated_at (a column MirrorSessions itself sets to "now"),
// pinned by direct SQL — mirroring db_session_reader_integration_test.go's existing
// pattern of direct-SQL writes for columns the public ports don't set directly. T1
// additionally calls SessionVerifier.VerifySession to reproduce the exact RM31
// mechanism design.md D1 describes. Assertions are ONLY against charging.Session
// domain fields — pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes). session_ids are in the
// 960001-960099 range, disjoint from RM29 tier 6's 920001-920099, RM30 tier 1's
// 940001-940099, RM31 tier 1's 950001-950099, and the real backfilled 734860294.
//
// Test → Test Contract case mapping:
//
//	T1  TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible
//	T2, T3  TestListSessionsByVehicleUpdatedSince_U1_BoundaryInclusiveExclusive
//	T4  TestListSessionsByVehicleUpdatedSince_NoMatchReturnsEmptyNonNilSlice
//	T5  TestListSessionsByVehicleUpdatedSince_NullTeslaIDNeverReturned
//	T6  TestListSessionsByVehicleUpdatedSince_MultiTenantIsolation
//	T7  TestListSessionsByVehicleUpdatedSince_U1_OrderingAscendingByChargeStopDateTime
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- updated-since-specific test helpers ---

// fetchSessionsByVehicleUpdatedSince wraps
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince for this
// file's tests, failing the test on any error from the port itself. Constructed via
// NewSuperchargerSessionAnalyticsReader — the new interface RM31 design.md D8
// introduced — not NewSessionReader, which does not expose this method.
func fetchSessionsByVehicleUpdatedSince(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, since time.Time) []charging.Session {
	t.Helper()
	r := charging.NewSuperchargerSessionAnalyticsReader(pool)
	sessions, err := r.ListSessionsByVehicleUpdatedSince(context.Background(), accountID, teslaID, since)
	if err != nil {
		t.Fatalf("ListSessionsByVehicleUpdatedSince: %v", err)
	}
	return sessions
}

// pinSessionUpdatedAt overwrites one session's updated_at by direct SQL — the same
// column-not-settable-through-a-public-port situation
// db_session_reader_integration_test.go's seedS1 already handles for the
// battery-percentage columns, applied here to updated_at instead.
func pinSessionUpdatedAt(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, sessionID int64, updatedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE charge_sessions SET updated_at = $1 WHERE account_id = $2 AND session_id = $3`,
		updatedAt, accountID, sessionID,
	); err != nil {
		t.Fatalf("pinSessionUpdatedAt: session %d: %v", sessionID, err)
	}
}

// seedU1 seeds design.md's baseline fixture U1: three sessions under
// accountID/teslaID, session_ids 960001-960003, via SessionWriter.MirrorSessions,
// then pins each row's updated_at by direct SQL to the table's specified values.
func seedU1(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	rows := []struct {
		sessionID int64
		updatedAt time.Time
		stop      time.Time
	}{
		{960001, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)},
		{960002, time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 4, 9, 0, 0, 0, time.UTC)},
		{960003, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 9, 18, 0, 0, 0, time.UTC)},
	}

	sessions := make([]charging.SessionMirror, 0, len(rows))
	for _, r := range rows {
		sessions = append(sessions, charging.SessionMirror{
			AccountID:           accountID,
			VIN:                 "V960001U1",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           r.sessionID,
			ChargeStartDateTime: r.stop.Add(-time.Hour),
			ChargeStopDateTime:  r.stop,
			SiteLocationName:    "U1 Site",
		})
	}
	if err := w.MirrorSessions(ctx, accountID, sessions); err != nil {
		t.Fatalf("seedU1: MirrorSessions: %v", err)
	}
	for _, r := range rows {
		pinSessionUpdatedAt(t, pool, accountID, r.sessionID, r.updatedAt)
	}
}

// --- T1 ---

// TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible
// reproduces the exact RM31 end-to-end mechanism design.md D1 describes: a mirror
// pass sets updated_at, then a later SessionVerifier.VerifySession call — which sets
// updated_at = now() and touches no other timestamp column — makes the session
// visible to a `since` cursor taken after the mirror pass but before the
// verification's own timestamp.
func TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	ctx := context.Background()

	const teslaID = int64(960001)
	mirrorPass := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stopTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	w := charging.NewSessionWriter(pool)
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{
		{
			AccountID:           acctA,
			VIN:                 "V960001T1",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           960001,
			ChargeStartDateTime: stopTime.Add(-time.Hour),
			ChargeStopDateTime:  stopTime,
			SiteLocationName:    "T1 Site",
		},
	}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}
	pinSessionUpdatedAt(t, pool, acctA, 960001, mirrorPass)

	sessionID := fetchChargeSessionID(t, pool, acctA, 960001)
	v := charging.NewSessionVerifier(pool)
	if _, err := v.VerifySession(ctx, acctA, sessionID, ptrIntV(50), ptrIntV(90)); err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	// One second after the mirror pass, before the verification's own now().
	since := mirrorPass.Add(time.Second)
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (verified after mirror pass, visible to since), got %d: %+v", len(sessions), sessions)
	}
	s := sessions[0]
	if s.SessionID != 960001 {
		t.Fatalf("expected SessionID 960001, got %d", s.SessionID)
	}
	if s.StartBatteryPct == nil || *s.StartBatteryPct != 50 {
		t.Errorf("StartBatteryPct = %v, want 50", s.StartBatteryPct)
	}
	if s.EndBatteryPct == nil || *s.EndBatteryPct != 90 {
		t.Errorf("EndBatteryPct = %v, want 90", s.EndBatteryPct)
	}
}

// --- T2, T3 ---

// TestListSessionsByVehicleUpdatedSince_U1_BoundaryInclusiveExclusive seeds U1 and
// calls with since = 960002's exact pinned updated_at: 960002 is included
// (inclusive lower bound, T2) and 960001 (updated_at strictly before since) is
// excluded (T3).
func TestListSessionsByVehicleUpdatedSince_U1_BoundaryInclusiveExclusive(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960001)
	seedU1(t, pool, acctA, teslaID)

	since := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)

	byID := make(map[int64]charging.Session, len(sessions))
	for _, s := range sessions {
		byID[s.SessionID] = s
	}

	// T2: inclusive lower bound.
	if _, ok := byID[960002]; !ok {
		t.Error("T2: expected 960002 present (updated_at equals since, inclusive lower bound)")
	}
	// T3: strictly-before is excluded.
	if _, ok := byID[960001]; ok {
		t.Error("T3: expected 960001 absent (updated_at strictly before since)")
	}
	// Sanity: 960003 (updated_at after since) remains present.
	if _, ok := byID[960003]; !ok {
		t.Error("expected 960003 present (updated_at after since)")
	}
}

// --- T4 ---

// TestListSessionsByVehicleUpdatedSince_NoMatchReturnsEmptyNonNilSlice: a since
// later than every fixture's updated_at returns a non-nil, zero-length slice.
func TestListSessionsByVehicleUpdatedSince_NoMatchReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960001)
	seedU1(t, pool, acctA, teslaID)

	since := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)

	if sessions == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// --- T5 ---

// TestListSessionsByVehicleUpdatedSince_NullTeslaIDNeverReturned: a session
// mirrored with TeslaID: nil (a deregistered vehicle) is never returned by any
// teslaID value, even when its updated_at matches the since filter — design.md D4's
// documented consequence, not a bug.
func TestListSessionsByVehicleUpdatedSince_NullTeslaIDNeverReturned(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	stopTime := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	orphaned := charging.SessionMirror{
		AccountID: acctA, VIN: "VORPHANU1", TeslaID: nil, SessionID: 960005,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "Orphaned Site",
	}
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{orphaned}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}
	pinSessionUpdatedAt(t, pool, acctA, 960005, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))

	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, teslaID := range []int64{960001, 960005, 0} {
		sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)
		for _, s := range sessions {
			if s.SessionID == 960005 {
				t.Errorf("teslaID=%d: expected orphaned session 960005 (NULL tesla_id) never returned, but it was", teslaID)
			}
		}
	}
}

// --- T6 ---

// TestListSessionsByVehicleUpdatedSince_MultiTenantIsolation: an identical
// updated_at-matching session under a second account, same TeslaID, never leaks
// into the first account's result.
func TestListSessionsByVehicleUpdatedSince_MultiTenantIsolation(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	acctB := uuid.New()
	cleanupChargeSessions(t, pool, acctA, acctB)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const teslaID = int64(960006)
	stopTime := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	sessA := charging.SessionMirror{
		AccountID: acctA, VIN: "VA-U1", TeslaID: ptrInt64(teslaID), SessionID: 960006,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "A Site",
	}
	sessB := charging.SessionMirror{
		AccountID: acctB, VIN: "VB-U1", TeslaID: ptrInt64(teslaID), SessionID: 960007,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "B Site",
	}
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{sessA}); err != nil {
		t.Fatalf("MirrorSessions acctA: %v", err)
	}
	if err := w.MirrorSessions(ctx, acctB, []charging.SessionMirror{sessB}); err != nil {
		t.Fatalf("MirrorSessions acctB: %v", err)
	}

	since := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)
	if len(sessions) != 1 || sessions[0].SessionID != 960006 {
		t.Fatalf("expected only acctA's session (960006), got %+v", sessions)
	}
}

// --- T7 ---

// TestListSessionsByVehicleUpdatedSince_U1_OrderingAscendingByChargeStopDateTime:
// using U1 with a since that matches all three rows, results are ordered ascending
// by ChargeStopDateTime, not by updated_at and not insertion order.
func TestListSessionsByVehicleUpdatedSince_U1_OrderingAscendingByChargeStopDateTime(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960001)
	seedU1(t, pool, acctA, teslaID)

	since := time.Unix(0, 0).UTC() // epoch: matches all three rows' pinned updated_at
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, acctA, teslaID, since)

	wantIDs := []int64{960001, 960002, 960003}
	if len(sessions) != len(wantIDs) {
		t.Fatalf("expected %d sessions, got %d: %+v", len(wantIDs), len(sessions), sessions)
	}
	for i, s := range sessions {
		if s.SessionID != wantIDs[i] {
			t.Errorf("T7: sessions[%d].SessionID = %d, want %d (ascending ChargeStopDateTime order)", i, s.SessionID, wantIDs[i])
		}
	}
}
