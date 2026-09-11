package telemetry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise SuperchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince
// (RM44-platform-add-mirror-watermark, MAG-48, roadmap D20) against a real Postgres from
// TEST_DATABASE_URL and self-skip when it is unset, so `go test ./...` stays green without a
// database (ai/go-conventions.md §persistence, AGENTS.md §Testing notes). They reuse
// upsertSuperchargerHistory (service.go) and newSuperchargerHistoryReaderImpl (reader.go),
// the same helpers db_supercharger_integration_test.go already exercises, plus the itoa
// helper defined there (same package, no re-declaration needed).
//
// updated_at is not a writable parameter of upsertSuperchargerHistory (it is computed by
// the change-detecting CASE expression, RM44-telemetry-add-change-detecting-upsert), so
// every test below inserts a row first, then overwrites updated_at directly with a raw
// SQL UPDATE to get an exact, known value to assert against — the same technique
// db_change_detection_integration_test.go and db_supercharger_battery_pct_integration_test.go
// already use for other columns this port does not expose a setter for.
//
// They implement the Test Contract authored in design.md BEFORE the implementation
// existed: T-tel-1 through T-tel-7.

// setUpdatedAt overwrites session sessionID's updated_at column directly, since
// upsertSuperchargerHistory computes it and exposes no setter for it.
func setUpdatedAt(t *testing.T, pool *pgxpool.Pool, sessionID int64, at time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE telemetry.supercharger_history SET updated_at = $1 WHERE session_id = $2", at, sessionID); err != nil {
		t.Fatalf("setUpdatedAt(session_id=%d): %v", sessionID, err)
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_AcrossVehicles implements T-tel-1: two
// vehicles in one account, each with one session updated at or after `since`, one
// session updated before `since` — only the two at-or-after sessions are returned.
func TestSuperchargerHistoryByAccountUpdatedSince_AcrossVehicles(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	vehicleA := int64(981001)
	vehicleB := int64(981002)
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	sessionAfterA := int64(991001) // vehicle A, updated at-or-after since
	sessionAfterB := int64(991002) // vehicle B, updated at-or-after since
	sessionBefore := int64(991003) // vehicle A, updated before since — must be excluded
	ids := []int64{sessionAfterA, sessionAfterB, sessionBefore}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	insert := func(sessionID, teslaID int64, stop time.Time) {
		tid := teslaID
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			AccountID:           accountID,
			VIN:                 "VIN_ACCT_UPD",
			TeslaID:             &tid,
			SiteLocationName:    "Site",
			CountryCode:         "US",
			ChargeStartDateTime: stop.Add(-30 * time.Minute),
			ChargeStopDateTime:  stop,
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			RawData:             []byte(`{}`),
		}); err != nil {
			t.Fatalf("upsertSuperchargerHistory(session_id=%d): %v", sessionID, err)
		}
	}

	insert(sessionAfterA, vehicleA, time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC))
	insert(sessionAfterB, vehicleB, time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC))
	insert(sessionBefore, vehicleA, time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC))

	setUpdatedAt(t, pool, sessionAfterA, since.Add(1*time.Hour))
	setUpdatedAt(t, pool, sessionAfterB, since.Add(2*time.Hour))
	setUpdatedAt(t, pool, sessionBefore, since.Add(-1*time.Hour))

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByAccountUpdatedSince: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 sessions, got %d: %+v", len(got), got)
	}
	gotIDs := map[int64]bool{got[0].SessionID: true, got[1].SessionID: true}
	if !gotIDs[sessionAfterA] || !gotIDs[sessionAfterB] {
		t.Errorf("want sessions %d and %d, got %v", sessionAfterA, sessionAfterB, gotIDs)
	}
	if gotIDs[sessionBefore] {
		t.Errorf("session %d updated before `since` must be excluded, got %v", sessionBefore, gotIDs)
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_IncludesOrphanedVehicle implements
// T-tel-2: a session whose tesla_id is NULL (vehicle no longer registered) is
// included — the whole reason this port takes no tesla_id param (roadmap D3/D20,
// design.md D6).
func TestSuperchargerHistoryByAccountUpdatedSince_IncludesOrphanedVehicle(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sessionID := int64(991010)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	// No TeslaID set at all — the session's VIN belongs to no currently
	// registered vehicle, so tesla_id is NULL.
	if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_ORPHAN",
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{}`),
	}); err != nil {
		t.Fatalf("upsertSuperchargerHistory: %v", err)
	}
	setUpdatedAt(t, pool, sessionID, since.Add(1*time.Hour))

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByAccountUpdatedSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 orphaned session included, got %d: %+v", len(got), got)
	}
	if got[0].TeslaID != nil {
		t.Errorf("want TeslaID nil (orphaned session), got %v", *got[0].TeslaID)
	}
	if got[0].SessionID != sessionID {
		t.Errorf("SessionID: want %d, got %d", sessionID, got[0].SessionID)
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_TenantIsolation implements T-tel-3:
// two accounts, each with a qualifying session — only the requested account's
// session is returned.
func TestSuperchargerHistoryByAccountUpdatedSince_TenantIsolation(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountA := uuid.New()
	accountB := uuid.New()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	sessionA := int64(991020)
	sessionB := int64(991021)
	ids := []int64{sessionA, sessionB}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	insert := func(sessionID int64, accountID uuid.UUID) {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			AccountID:           accountID,
			VIN:                 "VIN_TENANT",
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
		setUpdatedAt(t, pool, sessionID, since.Add(1*time.Hour))
	}
	insert(sessionA, accountA)
	insert(sessionB, accountB)

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountA, since)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByAccountUpdatedSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session (account A only), got %d: %+v", len(got), got)
	}
	if got[0].SessionID != sessionA {
		t.Errorf("SessionID: want %d (account A's), got %d", sessionA, got[0].SessionID)
	}
	if got[0].AccountID != accountA {
		t.Errorf("AccountID: want %v, got %v", accountA, got[0].AccountID)
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_OrderedOldestFirst implements
// T-tel-4: three qualifying sessions with different updated_at, inserted out of
// order, are returned ascending by updated_at.
func TestSuperchargerHistoryByAccountUpdatedSince_OrderedOldestFirst(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	sessionLatest := int64(991030) // inserted first, updated latest
	sessionMiddle := int64(991031)
	sessionEarliest := int64(991032) // inserted last, updated earliest (but still >= since)
	ids := []int64{sessionLatest, sessionMiddle, sessionEarliest}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = ANY($1::bigint[])", ids)
	})

	insert := func(sessionID int64) {
		if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
			SessionID:           sessionID,
			AccountID:           accountID,
			VIN:                 "VIN_ORDER",
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
	// Insert in an order different from the expected result order.
	insert(sessionLatest)
	insert(sessionMiddle)
	insert(sessionEarliest)

	setUpdatedAt(t, pool, sessionLatest, since.Add(3*time.Hour))
	setUpdatedAt(t, pool, sessionMiddle, since.Add(2*time.Hour))
	setUpdatedAt(t, pool, sessionEarliest, since.Add(1*time.Hour))

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByAccountUpdatedSince: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 sessions, got %d: %+v", len(got), got)
	}
	wantOrder := []int64{sessionEarliest, sessionMiddle, sessionLatest}
	for i, want := range wantOrder {
		if got[i].SessionID != want {
			t.Errorf("position %d: want session %d, got %d (full order: %v)", i, want, got[i].SessionID, sessionIDs(got))
		}
	}
}

func sessionIDs(sessions []SuperchargerHistory) []int64 {
	ids := make([]int64, len(sessions))
	for i, s := range sessions {
		ids[i] = s.SessionID
	}
	return ids
}

// TestSuperchargerHistoryByAccountUpdatedSince_EmptyResultNoError implements
// T-tel-5: an account with no session updated at or after `since` returns a
// non-nil empty slice and a nil error.
func TestSuperchargerHistoryByAccountUpdatedSince_EmptyResultNoError(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New() // never seeded — no rows at all for this account
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if got == nil {
		t.Fatal("want non-nil empty slice, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %d sessions", len(got))
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_BoundaryInclusive implements
// T-tel-6: a session whose updated_at equals `since` exactly is included.
func TestSuperchargerHistoryByAccountUpdatedSince_BoundaryInclusive(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	since := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	sessionID := int64(991040)

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	if err := st.upsertSuperchargerHistory(ctx, SuperchargerHistory{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_BOUNDARY",
		SiteLocationName:    "Site",
		CountryCode:         "US",
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		RawData:             []byte(`{}`),
	}); err != nil {
		t.Fatalf("upsertSuperchargerHistory: %v", err)
	}
	setUpdatedAt(t, pool, sessionID, since) // exactly equal to since

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByAccountUpdatedSince(ctx, accountID, since)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByAccountUpdatedSince: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session (exact boundary inclusive), got %d: %+v", len(got), got)
	}
	if got[0].SessionID != sessionID {
		t.Errorf("SessionID: want %d, got %d", sessionID, got[0].SessionID)
	}
}

// TestSuperchargerHistoryByAccountUpdatedSince_UsesIndexNoSort implements
// T-tel-7: the query uses idx_supercharger_history_account_updated and needs no
// sort step. The SELECT below is copied verbatim from the generated
// internal/telemetry/db/query.sql.go's superchargerHistoryByAccountUpdatedSince
// constant, prefixed with EXPLAIN (FORMAT TEXT), so a drift between this test
// and the real query would only ever make the test fail, never pass on a stale
// copy silently — the same technique
// TestReader_SnapshotPrecedingDay_UsesIndexBackwardScan already established in
// db_preceding_snapshot_integration_test.go.
//
// Runs inside a transaction with `SET LOCAL enable_seqscan = off` — load-bearing,
// not a workaround: the two-row fixture is small enough that the planner would
// otherwise prefer a Seq Scan + Sort, which would fail a correct design. Mirrors
// internal/charging's TestListSessionsByVehicle_TOrder2_ExplainConfirmsBackwardIndexScanNoSort
// precedent exactly.
func TestSuperchargerHistoryByAccountUpdatedSince_UsesIndexNoSort(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
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
			AccountID:           accountID,
			VIN:                 "VIN_EXPLAIN",
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
SELECT id, session_id, account_id, vin, tesla_id, site_location_name, country_code,
    charge_start_date_time, charge_stop_date_time, unlatch_date_time, billing_type,
    vehicle_make_type, energy_kwh, total_cost, currency, is_paid, raw_data,
    created_at, updated_at, start_battery_pct, end_battery_pct, battery_pct_source
FROM telemetry.supercharger_history
WHERE account_id = $1
  AND updated_at >= $2
ORDER BY updated_at ASC`, accountID, since)
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
	if !strings.Contains(planText, "idx_supercharger_history_account_updated") {
		t.Errorf("expected plan to use idx_supercharger_history_account_updated, got:\n%s", planText)
	}
	if strings.Contains(planText, "Sort") {
		t.Errorf("expected plan to contain NO Sort node (index already returns updated_at ASC order), got:\n%s", planText)
	}
	if strings.Contains(planText, "Seq Scan") {
		t.Errorf("expected plan to NOT contain Seq Scan, got:\n%s", planText)
	}
}
