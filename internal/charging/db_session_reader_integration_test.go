// Package charging_test — database-backed integration tests for SessionReader
// (session_reader.go) and its ListSessionsByVehicleBetween query (db/query.sql).
// Re-keyed on tesla_id, not account_id: the query has no account_id predicate left, and
// there is no tenant to isolate — every read is scoped by vehicle alone.
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has). Assertions are ONLY against charging.Session domain fields — pgtype
// NEVER appears in this file (internal/charging/AGENTS.md §Testing Notes).
//
// T-10's EXPLAIN runs inside a transaction with `SET LOCAL enable_seqscan = off` —
// load-bearing, not a workaround: the small fixture is otherwise cheap enough that
// the planner would prefer a Seq Scan, which would fail a correct design.
//
// Test → Test Contract case mapping:
//
//	T-3   TestListSessionsByVehicleBetween_ScopesByVehicleAlone
//	T-10  TestListSessionsByVehicleBetween_T10_ExplainConfirmsIndexNoSort
//	      TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice
//	      TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil
package charging_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- session-reader-specific test helpers ---

// fetchSessionsByVehicleBetween wraps SessionReader.ListSessionsByVehicleBetween for
// this file's tests, failing the test on any error from the port itself.
func fetchSessionsByVehicleBetween(t *testing.T, pool *pgxpool.Pool, teslaID int64, from, to time.Time) []charging.Session {
	t.Helper()
	r := charging.NewSessionReader(pool)
	sessions, err := r.ListSessionsByVehicleBetween(context.Background(), teslaID, from, to)
	if err != nil {
		t.Fatalf("ListSessionsByVehicleBetween: %v", err)
	}
	return sessions
}

// T-3: ListSessionsByVehicleBetween scopes by vehicle alone. Seed four rows with
// distinct session ids: 9101/9102/9103 for tesla_id 111, 9104 for tesla_id 222.
// Query the half-open window [2026-08-01, 2026-08-03]. Expect exactly [9101, 9102],
// ascending by stop time — 9103 excluded by the half-open end bound, 9104 excluded
// because it is another vehicle.
func TestListSessionsByVehicleBetween_ScopesByVehicleAlone(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9101, 9102, 9103, 9104)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	sessions := []charging.SessionMirror{
		{VIN: "V9101", TeslaID: 111, SessionID: 9101,
			ChargeStartDateTime: time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9101"},
		{VIN: "V9102", TeslaID: 111, SessionID: 9102,
			ChargeStartDateTime: time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 3, 23, 59, 0, 0, time.UTC),
			SiteLocationName:    "Site 9102"},
		{VIN: "V9103", TeslaID: 111, SessionID: 9103,
			ChargeStartDateTime: time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9103"},
		{VIN: "V9104", TeslaID: 222, SessionID: 9104,
			ChargeStartDateTime: time.Date(2026, 8, 1, 23, 30, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9104"},
	}
	if err := w.MirrorSessions(ctx, sessions); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}

	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	got := fetchSessionsByVehicleBetween(t, pool, 111, from, to)

	wantIDs := []int64{9101, 9102}
	if len(got) != len(wantIDs) {
		t.Fatalf("expected %d sessions, got %d: %+v", len(wantIDs), len(got), got)
	}
	for i, s := range got {
		if s.SessionID != wantIDs[i] {
			t.Errorf("sessions[%d].SessionID = %d, want %d (ascending stop time)", i, s.SessionID, wantIDs[i])
		}
	}
}

// TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice: a query against a
// vehicle with no sessions at all returns a non-nil, zero-length slice.
func TestListSessionsByVehicleBetween_NoMatchReturnsEmptyNonNilSlice(t *testing.T) {
	pool := newTestPool(t)

	from := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	sessions := fetchSessionsByVehicleBetween(t, pool, 940999, from, to)

	if sessions == nil {
		t.Error("expected non-nil empty slice, got nil")
	}
	if len(sessions) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(sessions))
	}
}

// TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil: a session mirrored
// with all four fee fields nil round-trips as nil through the reverse pgtype helpers,
// and SiteLocationName (NOT NULL) is still populated.
func TestListSessionsByVehicleBetween_NullableFeeFieldsRoundTripAsNil(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 940040)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const teslaID = int64(940001)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 31, 0, 0, 0, 0, time.UTC)
	inWindowStop := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)

	noFees := charging.SessionMirror{
		VIN: "VNOFEE", TeslaID: teslaID, SessionID: 940040,
		ChargeStartDateTime: inWindowStop.Add(-time.Hour), ChargeStopDateTime: inWindowStop,
		SiteLocationName: "No Fees Site",
		// EnergyKWh, TotalCost, Currency, IsPaid deliberately left nil.
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{noFees}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	sessions := fetchSessionsByVehicleBetween(t, pool, teslaID, from, to)
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

// T-10: EXPLAIN the bounded per-vehicle read. The plan names
// idx_supercharger_sessions_vehicle_stop and contains no Sort node — tesla_id
// prunes to the vehicle and the index's own ASC order satisfies ORDER BY with no
// separate sort step.
func TestListSessionsByVehicleBetween_T10_ExplainConfirmsIndexNoSort(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 9101, 9102, 9103, 9104)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	sessions := []charging.SessionMirror{
		{VIN: "V9101", TeslaID: 111, SessionID: 9101,
			ChargeStartDateTime: time.Date(2026, 7, 31, 23, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			SiteLocationName:    "Site 9101"},
		{VIN: "V9102", TeslaID: 111, SessionID: 9102,
			ChargeStartDateTime: time.Date(2026, 8, 3, 23, 30, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 3, 23, 59, 0, 0, time.UTC),
			SiteLocationName:    "Site 9102"},
	}
	if err := w.MirrorSessions(ctx, sessions); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
	}

	rows, err := tx.Query(ctx,
		`EXPLAIN (FORMAT TEXT) SELECT * FROM charging.supercharger_sessions WHERE tesla_id = $1 AND charge_stop_date_time >= $2 AND charge_stop_date_time < $3 ORDER BY charge_stop_date_time ASC`,
		int64(111), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC),
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
	if !strings.Contains(planText, "idx_supercharger_sessions_vehicle_stop") {
		t.Errorf("expected plan to name idx_supercharger_sessions_vehicle_stop, got:\n%s", planText)
	}
	if strings.Contains(planText, "Sort") {
		t.Errorf("expected plan to NOT contain a Sort node, got:\n%s", planText)
	}
}
