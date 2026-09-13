// Package charging_test — database-backed integration tests for
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince
// (session_reader.go) and its ListSessionsByVehicleUpdatedSince query
// (db/query.sql). Re-keyed on tesla_id, not account_id: the query has
// no account_id predicate left, and there is no tenant to isolate.
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has) and, for updated_at (a column MirrorSessions itself sets to "now"),
// pinned by direct SQL. Every fixture UPDATE asserts RowsAffected() == 1 — a write
// that matches no row does not error, and the assertions after it would then pass
// or fail for an unrelated reason. Assertions are ONLY against charging.Session
// domain fields — pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes).
//
// Test → Test Contract case mapping:
//
//	T-5  TestListSessionsByVehicleUpdatedSince_T5_OrderingAndBoundary
//	     TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- updated-since-specific test helpers ---

// fetchSessionsByVehicleUpdatedSince wraps
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince for this
// file's tests, failing the test on any error from the port itself.
func fetchSessionsByVehicleUpdatedSince(t *testing.T, pool *pgxpool.Pool, teslaID int64, since time.Time) []charging.Session {
	t.Helper()
	r := charging.NewSuperchargerSessionAnalyticsReader(pool)
	sessions, err := r.ListSessionsByVehicleUpdatedSince(context.Background(), teslaID, since)
	if err != nil {
		t.Fatalf("ListSessionsByVehicleUpdatedSince: %v", err)
	}
	return sessions
}

// pinSessionUpdatedAt overwrites one session's updated_at by direct SQL — the
// only way to set this column to an exact value, since MirrorSessions always
// writes "now". Asserts RowsAffected() == 1: a fixture write that matches no row
// does not error, and the assertions after it would then pass or fail for an
// unrelated reason.
func pinSessionUpdatedAt(t *testing.T, pool *pgxpool.Pool, sessionID int64, updatedAt time.Time) {
	t.Helper()
	tag, err := pool.Exec(context.Background(),
		`UPDATE charging.supercharger_sessions SET updated_at = $1 WHERE session_id = $2`,
		updatedAt, sessionID,
	)
	if err != nil {
		t.Fatalf("pinSessionUpdatedAt: session %d: %v", sessionID, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("pinSessionUpdatedAt: session %d: expected 1 row affected, got %d", sessionID, tag.RowsAffected())
	}
}

// T-5: ListSessionsByVehicleUpdatedSince ordering and boundary. Seed for
// tesla_id 111: 9301/9302/9303; plus 9304 for tesla_id 222. Pin updated_at to
// t1/t2/t3 on 9301/9302/9303 and t2 on 9304. since = t2 returns [9302, 9303];
// since = t3 returns [9303]; since = t3 + 1µs returns an empty, non-nil slice.
// 9304 never appears.
func TestListSessionsByVehicleUpdatedSince_T5_OrderingAndBoundary(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9301, 9302, 9303, 9304)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	sessions := []charging.SessionMirror{
		{VIN: "V9301", TeslaID: 111, SessionID: 9301,
			ChargeStartDateTime: time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9301"},
		{VIN: "V9302", TeslaID: 111, SessionID: 9302,
			ChargeStartDateTime: time.Date(2026, 8, 1, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9302"},
		{VIN: "V9303", TeslaID: 111, SessionID: 9303,
			ChargeStartDateTime: time.Date(2026, 8, 2, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9303"},
		{VIN: "V9304", TeslaID: 222, SessionID: 9304,
			ChargeStartDateTime: time.Date(2026, 8, 3, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9304"},
	}
	if err := w.MirrorSessions(ctx, sessions); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}

	t1 := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	pinSessionUpdatedAt(t, pool, 9301, t1)
	pinSessionUpdatedAt(t, pool, 9302, t2)
	pinSessionUpdatedAt(t, pool, 9303, t3)
	pinSessionUpdatedAt(t, pool, 9304, t2)

	got := fetchSessionsByVehicleUpdatedSince(t, pool, 111, t2)
	assertSessionIDOrder(t, got, []int64{9302, 9303})

	got = fetchSessionsByVehicleUpdatedSince(t, pool, 111, t3)
	assertSessionIDOrder(t, got, []int64{9303})

	got = fetchSessionsByVehicleUpdatedSince(t, pool, 111, t3.Add(time.Microsecond))
	if got == nil {
		t.Error("since = t3 + 1µs: expected non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Errorf("since = t3 + 1µs: expected 0 sessions, got %d: %+v", len(got), got)
	}
}

// TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible
// reproduces the end-to-end mechanism this query exists for: a mirror pass sets
// updated_at, then a later SessionVerifier.VerifySession call — which sets
// updated_at = now() and touches no other timestamp column — makes the session
// visible to a `since` cursor taken after the mirror pass but before the
// verification's own timestamp.
func TestListSessionsByVehicleUpdatedSince_T1_VerifySessionEditBecomesVisible(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(960001)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()

	const teslaID = int64(960001)
	mirrorPass := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	stopTime := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)

	w := charging.NewSessionWriter(pool)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{
		{
			VIN:                 "V960001T1",
			TeslaID:             teslaID,
			SessionID:           sessionID,
			ChargeStartDateTime: stopTime.Add(-time.Hour),
			ChargeStopDateTime:  stopTime,
			SiteLocationName:    "T1 Site",
		},
	}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}
	pinSessionUpdatedAt(t, pool, sessionID, mirrorPass)

	id := fetchSuperchargerSessionID(t, pool, sessionID)
	v := charging.NewSessionVerifier(pool)
	if _, err := v.VerifySession(ctx, teslaID, id, ptrIntV(50), ptrIntV(90)); err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	// One second after the mirror pass, before the verification's own now().
	since := mirrorPass.Add(time.Second)
	sessions := fetchSessionsByVehicleUpdatedSince(t, pool, teslaID, since)

	if len(sessions) != 1 {
		t.Fatalf("expected 1 session (verified after mirror pass, visible to since), got %d: %+v", len(sessions), sessions)
	}
	s := sessions[0]
	if s.SessionID != sessionID {
		t.Fatalf("expected SessionID %d, got %d", sessionID, s.SessionID)
	}
	if s.StartBatteryPct == nil || *s.StartBatteryPct != 50 {
		t.Errorf("StartBatteryPct = %v, want 50", s.StartBatteryPct)
	}
	if s.EndBatteryPct == nil || *s.EndBatteryPct != 90 {
		t.Errorf("EndBatteryPct = %v, want 90", s.EndBatteryPct)
	}
}
