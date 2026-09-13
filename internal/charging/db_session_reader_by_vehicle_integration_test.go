// Package charging_test — database-backed integration tests for
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicle (session_reader.go) and
// its ListSessionsByVehicle query (db/query.sql). Re-keyed on tesla_id, not
// account_id (RM57-charging-rekey-supercharger-sessions-on-tesla-id, MAG-67): the
// query has no account_id predicate left, and there is no tenant to isolate — a
// read is scoped by vehicle alone.
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has). Assertions are ONLY against charging.Session domain fields — pgtype
// NEVER appears in this file (internal/charging/AGENTS.md §Testing Notes).
//
// T-11's EXPLAIN runs inside a transaction with `SET LOCAL enable_seqscan = off` —
// load-bearing, not a workaround: the small fixture is otherwise cheap enough that
// the planner would prefer a Seq Scan, which would fail a correct design. The
// Sort-absence half of the assertion keeps full force under the setting; do not
// drop it and do not delete the Sort assertion instead.
//
// Test → Test Contract case mapping (design.md §"Test contract"):
//
//	T-4   TestListSessionsByVehicle_T4_NewestFirstLimited
//	T-11  TestListSessionsByVehicle_T11_ExplainConfirmsBackwardIndexScanNoSort
//	      TestListSessionsByVehicle_LimitLargerThanAvailableReturnsAllNewestFirst
//	      TestListSessionsByVehicle_ZeroLimitUsesServerDefault
//	      TestListSessionsByVehicle_NegativeLimitUsesServerDefault
//	      TestListSessionsByVehicle_NoMatchReturnsEmptyNonNilSlice
package charging_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- by-vehicle-specific test helpers ---

// fetchSessionsByVehicle wraps
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicle for this file's tests,
// failing the test on any error from the port itself.
func fetchSessionsByVehicle(t *testing.T, pool *pgxpool.Pool, teslaID int64, limit int) []charging.Session {
	t.Helper()
	r := charging.NewSuperchargerSessionAnalyticsReader(pool)
	sessions, err := r.ListSessionsByVehicle(context.Background(), teslaID, limit)
	if err != nil {
		t.Fatalf("ListSessionsByVehicle: %v", err)
	}
	return sessions
}

// seedT4 seeds design.md's T-4 fixture: three sessions for tesla_id 111
// (9201/9202/9203, strictly increasing ChargeStopDateTime), plus a decoy session
// 9204 for a different vehicle (tesla_id 222), stopping later than all three.
func seedT4(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	sessions := []charging.SessionMirror{
		{VIN: "V9201", TeslaID: 111, SessionID: 9201,
			ChargeStartDateTime: time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9201"},
		{VIN: "V9202", TeslaID: 111, SessionID: 9202,
			ChargeStartDateTime: time.Date(2026, 8, 1, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9202"},
		{VIN: "V9203", TeslaID: 111, SessionID: 9203,
			ChargeStartDateTime: time.Date(2026, 8, 2, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9203"},
		{VIN: "V9204", TeslaID: 222, SessionID: 9204,
			ChargeStartDateTime: time.Date(2026, 8, 3, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9204"},
	}
	if err := w.MirrorSessions(ctx, sessions); err != nil {
		t.Fatalf("seedT4: MirrorSessions: %v", err)
	}
}

// T-4: ListSessionsByVehicle returns newest-first, limited, and never returns
// another vehicle's row — even one that is newest in the whole table.
func TestListSessionsByVehicle_T4_NewestFirstLimited(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9201, 9202, 9203, 9204)
	seedT4(t, pool)

	sessions := fetchSessionsByVehicle(t, pool, 111, 2)
	assertSessionIDOrder(t, sessions, []int64{9203, 9202})
}

// TestListSessionsByVehicle_LimitLargerThanAvailableReturnsAllNewestFirst:
// limit=100 (larger than the 3-row fixture for tesla_id 111) returns every row for
// that vehicle, still newest-first, and never the decoy vehicle's row.
func TestListSessionsByVehicle_LimitLargerThanAvailableReturnsAllNewestFirst(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9201, 9202, 9203, 9204)
	seedT4(t, pool)

	sessions := fetchSessionsByVehicle(t, pool, 111, 100)
	assertSessionIDOrder(t, sessions, []int64{9203, 9202, 9201})
}

// TestListSessionsByVehicle_ZeroLimitUsesServerDefault: limit=0 clamps to the
// server default (defaultLimit=100), proven by getting back all three fixture rows
// rather than zero.
func TestListSessionsByVehicle_ZeroLimitUsesServerDefault(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9201, 9202, 9203, 9204)
	seedT4(t, pool)

	sessions := fetchSessionsByVehicle(t, pool, 111, 0)
	assertSessionIDOrder(t, sessions, []int64{9203, 9202, 9201})
}

// TestListSessionsByVehicle_NegativeLimitUsesServerDefault: a negative limit also
// clamps to the server default, exactly like limit==0 — the documented guard is
// `limit <= 0`, not `limit == 0` alone.
func TestListSessionsByVehicle_NegativeLimitUsesServerDefault(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9201, 9202, 9203, 9204)
	seedT4(t, pool)

	sessions := fetchSessionsByVehicle(t, pool, 111, -5)
	assertSessionIDOrder(t, sessions, []int64{9203, 9202, 9201})
}

// assertSessionIDOrder fails the test unless sessions' SessionIDs, in order, match
// wantIDs exactly.
func assertSessionIDOrder(t *testing.T, sessions []charging.Session, wantIDs []int64) {
	t.Helper()
	if len(sessions) != len(wantIDs) {
		t.Fatalf("expected %d sessions, got %d: %+v", len(wantIDs), len(sessions), sessions)
	}
	for i, s := range sessions {
		if s.SessionID != wantIDs[i] {
			t.Errorf("sessions[%d].SessionID = %d, want %d", i, s.SessionID, wantIDs[i])
		}
	}
}

// TestListSessionsByVehicle_NoMatchReturnsEmptyNonNilSlice: a vehicle with no
// charge session records returns a non-nil, zero-length slice.
func TestListSessionsByVehicle_NoMatchReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)

	sessions := fetchSessionsByVehicle(t, pool, 940998, 100)
	if sessions == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// T-11: EXPLAIN the newest-first read. The plan names
// idx_supercharger_sessions_vehicle_stop, reports a backward scan, and contains no
// Sort node — proving the ASC-built index serves the DESC query by walking
// backward, not by a sequential scan plus an in-memory sort.
func TestListSessionsByVehicle_T11_ExplainConfirmsBackwardIndexScanNoSort(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9201, 9202, 9203, 9204)
	ctx := context.Background()
	seedT4(t, pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
	}

	rows, err := tx.Query(ctx,
		`EXPLAIN (FORMAT TEXT) SELECT * FROM charging.supercharger_sessions WHERE tesla_id = $1 ORDER BY charge_stop_date_time DESC LIMIT $2`,
		int64(111), 2,
	)
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
	if !strings.Contains(planText, "Index Scan Backward using idx_supercharger_sessions_vehicle_stop") {
		t.Errorf("expected plan to contain \"Index Scan Backward using idx_supercharger_sessions_vehicle_stop\", got:\n%s", planText)
	}
	if strings.Contains(planText, "Sort") {
		t.Errorf("expected plan to NOT contain a Sort node (the ASC-built index must serve DESC via backward scan with no sort step), got:\n%s", planText)
	}
}
