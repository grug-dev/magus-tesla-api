// Package charging_test — database-backed integration tests for
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicle (session_reader.go) and
// its ListSessionsByVehicle query (db/query.sql), covering design.md's Test
// Contract T8–T14 and T-Order2 (RM31-charging-add-session-read-ports, tasks.md
// task 3.2).
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has). Assertions are ONLY against charging.Session domain fields — pgtype
// NEVER appears in this file (internal/charging/AGENTS.md §Testing Notes).
// session_ids are in the 960001-960099 range, disjoint from RM29 tier 6's
// 920001-920099, RM30 tier 1's 940001-940099, RM31 tier 1's 950001-950099, this
// change's own updated-since file (960001-960007), and the real backfilled
// 734860294.
//
// T-Order2 runs inside a transaction with `SET LOCAL enable_seqscan = off` before
// its EXPLAIN — load-bearing per design.md and tasks.md 3.2, NOT a workaround: the
// four-row fixture is small enough that the planner would otherwise prefer a Seq
// Scan, which would fail a correct design. The Sort-absence half of the assertion
// keeps full force under the setting; do not drop it and do not delete the Sort
// assertion instead.
//
// Test → Test Contract case mapping:
//
//	T8   TestListSessionsByVehicle_T8_LimitReturnsNewestFirst
//	T9   TestListSessionsByVehicle_T9_LimitLargerThanAvailableReturnsAllNewestFirst
//	T10  TestListSessionsByVehicle_T10_ZeroLimitUsesServerDefault
//	T11  TestListSessionsByVehicle_T11_NegativeLimitUsesServerDefault
//	T12  TestListSessionsByVehicle_T12_NoMatchReturnsEmptyNonNilSlice
//	T13  TestListSessionsByVehicle_T13_NullTeslaIDNeverReturned
//	T14  TestListSessionsByVehicle_T14_MultiTenantAndCrossVehicleIsolation
//	T-Order2  TestListSessionsByVehicle_TOrder2_ExplainConfirmsBackwardIndexScanNoSort
package charging_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- by-vehicle-specific test helpers ---

// fetchSessionsByVehicle wraps
// SuperchargerSessionAnalyticsReader.ListSessionsByVehicle for this file's tests,
// failing the test on any error from the port itself. Constructed via
// NewSuperchargerSessionAnalyticsReader — the new interface RM31 design.md D8
// introduced — not NewSessionReader, which does not expose this method.
func fetchSessionsByVehicle(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, limit int) []charging.Session {
	t.Helper()
	r := charging.NewSuperchargerSessionAnalyticsReader(pool)
	sessions, err := r.ListSessionsByVehicle(context.Background(), accountID, teslaID, limit)
	if err != nil {
		t.Fatalf("ListSessionsByVehicle: %v", err)
	}
	return sessions
}

// seedL1 seeds design.md's baseline fixture L1: four sessions under
// accountID/teslaID, session_ids 960011-960014, with strictly increasing
// ChargeStopDateTime, via SessionWriter.MirrorSessions.
func seedL1(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	stops := []struct {
		sessionID int64
		stop      time.Time
	}{
		{960011, time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)},
		{960012, time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)},
		{960013, time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)},
		{960014, time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)},
	}
	sessions := make([]charging.SessionMirror, 0, len(stops))
	for _, s := range stops {
		sessions = append(sessions, charging.SessionMirror{
			AccountID:           accountID,
			VIN:                 "V960010L1",
			TeslaID:             ptrInt64(teslaID),
			SessionID:           s.sessionID,
			ChargeStartDateTime: s.stop.Add(-time.Hour),
			ChargeStopDateTime:  s.stop,
			SiteLocationName:    "L1 Site",
		})
	}
	if err := w.MirrorSessions(ctx, accountID, sessions); err != nil {
		t.Fatalf("seedL1: MirrorSessions: %v", err)
	}
}

// --- T8 ---

// TestListSessionsByVehicle_T8_LimitReturnsNewestFirst: limit=2 returns the two
// newest sessions by ChargeStopDateTime, descending — not the two oldest, which a
// mistaken ASC LIMIT would return.
func TestListSessionsByVehicle_T8_LimitReturnsNewestFirst(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960010)
	seedL1(t, pool, acctA, teslaID)

	sessions := fetchSessionsByVehicle(t, pool, acctA, teslaID, 2)
	if len(sessions) != 2 {
		t.Fatalf("expected 2 sessions, got %d: %+v", len(sessions), sessions)
	}
	if sessions[0].SessionID != 960014 || sessions[1].SessionID != 960013 {
		t.Errorf("got [%d, %d], want [960014, 960013] (two newest, descending)", sessions[0].SessionID, sessions[1].SessionID)
	}
}

// --- T9 ---

// TestListSessionsByVehicle_T9_LimitLargerThanAvailableReturnsAllNewestFirst:
// limit=100 (larger than the 4-row fixture) returns every row, still newest-first.
func TestListSessionsByVehicle_T9_LimitLargerThanAvailableReturnsAllNewestFirst(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960010)
	seedL1(t, pool, acctA, teslaID)

	sessions := fetchSessionsByVehicle(t, pool, acctA, teslaID, 100)
	assertSessionIDOrder(t, sessions, []int64{960014, 960013, 960012, 960011})
}

// --- T10 ---

// TestListSessionsByVehicle_T10_ZeroLimitUsesServerDefault: limit=0 clamps to the
// server default (defaultLimit=100), proven by getting back all four fixture rows
// rather than zero.
func TestListSessionsByVehicle_T10_ZeroLimitUsesServerDefault(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960010)
	seedL1(t, pool, acctA, teslaID)

	sessions := fetchSessionsByVehicle(t, pool, acctA, teslaID, 0)
	assertSessionIDOrder(t, sessions, []int64{960014, 960013, 960012, 960011})
}

// --- T11 ---

// TestListSessionsByVehicle_T11_NegativeLimitUsesServerDefault: a negative limit
// also clamps to the server default, exactly like limit==0 — the documented guard
// is `limit <= 0`, not `limit == 0` alone (design.md D3).
func TestListSessionsByVehicle_T11_NegativeLimitUsesServerDefault(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	const teslaID = int64(960010)
	seedL1(t, pool, acctA, teslaID)

	sessions := fetchSessionsByVehicle(t, pool, acctA, teslaID, -5)
	assertSessionIDOrder(t, sessions, []int64{960014, 960013, 960012, 960011})
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

// --- T12 ---

// TestListSessionsByVehicle_T12_NoMatchReturnsEmptyNonNilSlice: a vehicle with no
// charge session records returns a non-nil, zero-length slice.
func TestListSessionsByVehicle_T12_NoMatchReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)

	sessions := fetchSessionsByVehicle(t, pool, acctA, 960010, 100)
	if sessions == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// --- T13 ---

// TestListSessionsByVehicle_T13_NullTeslaIDNeverReturned: a session mirrored with
// TeslaID: nil (a deregistered vehicle) is never returned by any teslaID value —
// design.md D4's documented consequence, not a bug.
func TestListSessionsByVehicle_T13_NullTeslaIDNeverReturned(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	stopTime := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	orphaned := charging.SessionMirror{
		AccountID: acctA, VIN: "VORPHANL1", TeslaID: nil, SessionID: 960015,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "Orphaned Site",
	}
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{orphaned}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	for _, teslaID := range []int64{960010, 960015, 0} {
		sessions := fetchSessionsByVehicle(t, pool, acctA, teslaID, 100)
		for _, s := range sessions {
			if s.SessionID == 960015 {
				t.Errorf("teslaID=%d: expected orphaned session 960015 (NULL tesla_id) never returned, but it was", teslaID)
			}
		}
	}
}

// --- T14 ---

// TestListSessionsByVehicle_T14_MultiTenantAndCrossVehicleIsolation: a same-teslaID
// session under a distinct account, and a different-teslaID session under the same
// account, are both absent from a read scoped to (acctA, primaryTeslaID).
func TestListSessionsByVehicle_T14_MultiTenantAndCrossVehicleIsolation(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	acctB := uuid.New()
	cleanupChargeSessions(t, pool, acctA, acctB)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const primaryTeslaID = int64(960010)
	const otherTeslaID = int64(960099)
	stopTime := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	primary := charging.SessionMirror{
		AccountID: acctA, VIN: "VPRIMARYL1", TeslaID: ptrInt64(primaryTeslaID), SessionID: 960016,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "Primary Site",
	}
	sameTeslaOtherAccount := charging.SessionMirror{
		AccountID: acctB, VIN: "VOTHERACCTL1", TeslaID: ptrInt64(primaryTeslaID), SessionID: 960017,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "Other Account Site",
	}
	otherVehicleSameAccount := charging.SessionMirror{
		AccountID: acctA, VIN: "VOTHERVEHICLEL1", TeslaID: ptrInt64(otherTeslaID), SessionID: 960018,
		ChargeStartDateTime: stopTime.Add(-time.Hour), ChargeStopDateTime: stopTime,
		SiteLocationName: "Other Vehicle Site",
	}
	if err := w.MirrorSessions(ctx, acctA, []charging.SessionMirror{primary, otherVehicleSameAccount}); err != nil {
		t.Fatalf("MirrorSessions acctA: %v", err)
	}
	if err := w.MirrorSessions(ctx, acctB, []charging.SessionMirror{sameTeslaOtherAccount}); err != nil {
		t.Fatalf("MirrorSessions acctB: %v", err)
	}

	sessions := fetchSessionsByVehicle(t, pool, acctA, primaryTeslaID, 100)
	if len(sessions) != 1 || sessions[0].SessionID != 960016 {
		t.Fatalf("expected only session 960016, got %+v", sessions)
	}
}

// --- T-Order2 ---

// TestListSessionsByVehicle_TOrder2_ExplainConfirmsBackwardIndexScanNoSort is the
// concrete verification for design.md D3's index proof: inside a transaction with
// `SET LOCAL enable_seqscan = off` (load-bearing — see file header), EXPLAIN the
// literal ListSessionsByVehicle SQL with the L1 fixture's real parameter values and
// assert the plan uses "Index Scan Backward using idx_charge_sessions_vehicle_stop"
// with no "Sort" node — proving the ASC-built index serves the DESC query by
// walking backward, not by a sequential scan plus an in-memory sort.
func TestListSessionsByVehicle_TOrder2_ExplainConfirmsBackwardIndexScanNoSort(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargeSessions(t, pool, acctA)
	ctx := context.Background()
	const teslaID = int64(960010)
	seedL1(t, pool, acctA, teslaID)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
	}

	rows, err := tx.Query(ctx,
		`EXPLAIN (FORMAT TEXT) SELECT * FROM charge_sessions WHERE account_id = $1 AND tesla_id = $2 ORDER BY charge_stop_date_time DESC LIMIT $3`,
		acctA, teslaID, 2,
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
	if !strings.Contains(planText, "Index Scan Backward using idx_charge_sessions_vehicle_stop") {
		t.Errorf("expected plan to contain \"Index Scan Backward using idx_charge_sessions_vehicle_stop\", got:\n%s", planText)
	}
	if strings.Contains(planText, "Sort") {
		t.Errorf("expected plan to NOT contain a Sort node (the ASC-built index must serve DESC via backward scan with no sort step), got:\n%s", planText)
	}
}
