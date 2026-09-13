package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise SuperchargerHistoryReader.SuperchargerHistoryByVehicleUpdatedSince
// against a real Postgres from TEST_DATABASE_URL and self-skip when it is unset, so
// `go test ./...` stays green without a database. The query is now scoped to one
// vehicle only — there is no account-wide sibling any more, and no row can have a
// NULL tesla_id (a session for an unregistered VIN is never stored).

// setUpdatedAt overwrites session sessionID's updated_at column directly, since
// upsertSuperchargerHistory computes it and exposes no setter for it.
func setUpdatedAt(t *testing.T, pool *pgxpool.Pool, sessionID int64, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE telemetry.supercharger_history SET updated_at = $1 WHERE session_id = $2", at, sessionID); err != nil {
		t.Fatalf("setUpdatedAt(session_id=%d): %v", sessionID, err)
	}
}

// TestSuperchargerHistoryByVehicleUpdatedSince_OrderingAndBoundary implements
// T-8: three sessions for one vehicle at three updated_at instants, plus a
// decoy session for another vehicle at the middle instant. `since` at each of
// the three instants (and one nanosecond past the last) proves both the
// ascending order and the inclusive lower bound, and that the decoy vehicle
// never appears.
func TestSuperchargerHistoryByVehicleUpdatedSince_OrderingAndBoundary(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(111)
	decoyTeslaID := int64(222)
	t1 := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	t3 := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)

	session7101 := int64(7101)
	session7102 := int64(7102)
	session7103 := int64(7103)
	session7104 := int64(7104) // decoy vehicle, same updated_at as 7102
	ids := []int64{session7101, session7102, session7103, session7104}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	insert := func(sessionID, tid int64, start time.Time) {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			VIN:                 "VIN_UPD_SINCE",
			TeslaID:             tid,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: start,
			ChargeStopDateTime:  start.Add(30 * time.Minute),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{}`),
		}); err != nil {
			t.Fatalf("upsertSuperchargerHistory(session_id=%d): %v", sessionID, err)
		}
	}
	insert(session7101, teslaID, time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC))
	insert(session7102, teslaID, time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC))
	insert(session7103, teslaID, time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC))
	insert(session7104, decoyTeslaID, time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC))

	setUpdatedAt(t, pool, session7101, t1)
	setUpdatedAt(t, pool, session7102, t2)
	setUpdatedAt(t, pool, session7103, t3)
	setUpdatedAt(t, pool, session7104, t2)

	r := newSuperchargerHistoryReaderImpl(pool)

	t.Run("since t2 returns 7102 then 7103", func(t *testing.T) {
		got, err := r.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, t2)
		if err != nil {
			t.Fatalf("SuperchargerHistoryByVehicleUpdatedSince: %v", err)
		}
		want := []int64{session7102, session7103}
		if len(got) != len(want) {
			t.Fatalf("want %v, got %d rows: %+v", want, len(got), got)
		}
		for i, w := range want {
			if got[i].SessionID != w {
				t.Errorf("position %d: want session %d, got %d", i, w, got[i].SessionID)
			}
		}
	})

	t.Run("since t3 returns only 7103", func(t *testing.T) {
		got, err := r.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, t3)
		if err != nil {
			t.Fatalf("SuperchargerHistoryByVehicleUpdatedSince: %v", err)
		}
		if len(got) != 1 || got[0].SessionID != session7103 {
			t.Fatalf("want [%d], got %+v", session7103, got)
		}
	})

	t.Run("since t3 plus 1us returns empty non-nil", func(t *testing.T) {
		got, err := r.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, t3.Add(time.Microsecond))
		if err != nil {
			t.Fatalf("SuperchargerHistoryByVehicleUpdatedSince: %v", err)
		}
		if got == nil {
			t.Fatal("want non-nil empty slice, got nil")
		}
		if len(got) != 0 {
			t.Fatalf("want empty slice, got %d sessions: %+v", len(got), got)
		}
	})

	t.Run("decoy vehicle never appears", func(t *testing.T) {
		got, err := r.SuperchargerHistoryByVehicleUpdatedSince(ctx, teslaID, t1)
		if err != nil {
			t.Fatalf("SuperchargerHistoryByVehicleUpdatedSince: %v", err)
		}
		for _, s := range got {
			if s.SessionID == session7104 {
				t.Error("session 7104 belongs to a different vehicle and must not appear")
			}
		}
	})
}

// TestSuperchargerHistoryByVehicleUpdatedSince_UsesIndexNoSort implements
// T-9: the per-vehicle updated-since query is served entirely by
// idx_supercharger_history_vehicle_updated (tesla_id, updated_at), with no
// separate Sort step. The SELECT below is copied from the generated
// internal/telemetry/db/query.sql.go's SuperchargerHistoryByVehicleUpdatedSince
// constant, prefixed with EXPLAIN, so a drift between this test and the real
// query only ever makes the test fail, never pass on a stale copy silently.
//
// Runs inside a transaction with `SET LOCAL enable_seqscan = off` — the
// two-row fixture is small enough that the planner would otherwise prefer a
// Seq Scan + Sort, which would fail a correct design.
func TestSuperchargerHistoryByVehicleUpdatedSince_UsesIndexNoSort(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(333)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	sessionOne := int64(991050)
	sessionTwo := int64(991051)
	ids := []int64{sessionOne, sessionTwo}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	insert := func(sessionID int64) {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			VIN:                 "VIN_EXPLAIN",
			TeslaID:             teslaID,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{}`),
		}); err != nil {
			t.Fatalf("upsertSuperchargerHistory(session_id=%d): %v", sessionID, err)
		}
	}
	insert(sessionOne)
	insert(sessionTwo)
	setUpdatedAt(t, pool, sessionOne, since.Add(1*time.Hour))
	setUpdatedAt(t, pool, sessionTwo, since.Add(2*time.Hour))

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL enable_seqscan = off"); err != nil {
		t.Fatalf("SET LOCAL enable_seqscan = off: %v", err)
	}

	rows, err := tx.Query(ctx, `EXPLAIN (FORMAT TEXT)
SELECT id, session_id, vin, tesla_id, site_location_name, country_code,
    charge_start_date_time, charge_stop_date_time, unlatch_date_time, billing_type,
    vehicle_make_type, energy_kwh, total_cost, currency, is_paid, raw_data,
    created_at, updated_at, start_battery_pct, end_battery_pct, battery_pct_source
FROM telemetry.supercharger_history
WHERE tesla_id = $1
  AND updated_at >= $2
ORDER BY updated_at ASC`, teslaID, since)
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
	if !strings.Contains(planText, "idx_supercharger_history_vehicle_updated") {
		t.Errorf("expected plan to use idx_supercharger_history_vehicle_updated, got:\n%s", planText)
	}
	if strings.Contains(planText, "Sort") {
		t.Errorf("expected plan to contain NO Sort node (index already returns updated_at ASC order), got:\n%s", planText)
	}
	if strings.Contains(planText, "Seq Scan") {
		t.Errorf("expected plan to NOT contain Seq Scan, got:\n%s", planText)
	}
}
