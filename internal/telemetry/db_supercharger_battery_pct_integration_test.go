package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the five battery-% verification/override columns added to
// supercharger_sessions by RM27-telemetry-add-supercharger-battery-pct (MAG-14):
// start_battery_pct, end_battery_pct, battery_pct_source (the human-owned trio, D1/D2)
// and start_battery_pct_est, end_battery_pct_est (the frozen, write-once verification
// snapshot pair, design D6). They implement the test contract authored in this
// change's design.md BEFORE the mapping code existed (§"Test Contract").
//
// No Go writer exists anywhere in this repository for any of the five columns (R7),
// so tests set them via direct SQL against the test pool — legitimate here because
// these tests verify a DATABASE constraint and the absence of a Go write path, not an
// application-level API. Session IDs mirror design.md's own numbering (900001-900003)
// for direct traceability; each test seeds and cleans up its own fixture so tests run
// independently of order (design.md's (d) reuses (b)/(b2)'s *shape*, not their literal
// runtime state, since Go tests do not share state across functions).

// insertBaseSuperchargerSession upserts an ordinary session (no battery-% columns —
// design.md scenario (a)'s starting state) and registers cleanup.
func insertBaseSuperchargerSession(t *testing.T, st *dbStore, pool *pgxpool.Pool, accountID uuid.UUID, sessionID int64, energyKWh, totalCost float64, currency string, isPaid bool, rawData []byte) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM supercharger_sessions WHERE session_id = $1", sessionID)
	})

	start := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	sess := SuperchargerSession{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_BATPCT",
		SiteLocationName:    "Test Supercharger",
		CountryCode:         "US",
		ChargeStartDateTime: start,
		ChargeStopDateTime:  start.Add(30 * time.Minute),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           &energyKWh,
		TotalCost:           &totalCost,
		Currency:            &currency,
		IsPaid:              &isPaid,
		RawData:             rawData,
	}
	if err := st.upsertSuperchargerSession(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerSession(%d): %v", sessionID, err)
	}
}

// TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull implements
// design.md test-contract scenario (a): a fresh UpsertSuperchargerSession leaves all
// five battery-% columns NULL (T6.1).
func TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(900001)
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionID, 45.2, 12000, "COP", false, []byte(`{"sessionId":900001}`))

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]
	if s.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct: want nil on fresh insert, got %v", *s.StartBatteryPct)
	}
	if s.EndBatteryPct != nil {
		t.Errorf("EndBatteryPct: want nil on fresh insert, got %v", *s.EndBatteryPct)
	}
	if s.BatteryPctSource != nil {
		t.Errorf("BatteryPctSource: want nil on fresh insert, got %v", *s.BatteryPctSource)
	}
	if s.StartBatteryPctEst != nil {
		t.Errorf("StartBatteryPctEst: want nil on fresh insert, got %v", *s.StartBatteryPctEst)
	}
	if s.EndBatteryPctEst != nil {
		t.Errorf("EndBatteryPctEst: want nil on fresh insert, got %v", *s.EndBatteryPctEst)
	}
}

// TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched implements design.md
// test-contract scenario (b) — the most important test in this change (T6.2). It MUST
// fail if start_battery_pct/end_battery_pct/battery_pct_source are ever added to
// UpsertSuperchargerSession's ON CONFLICT DO UPDATE SET clause: the regression guard
// for R3.
func TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(900002)
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionID, 30.0, 9000, "USD", false, []byte(`{"sessionId":900002,"v":1}`))

	// Simulate the (future, out-of-scope) verification UI's write via direct SQL —
	// no Go writer exists for the trio in this tier (R7).
	if _, err := pool.Exec(ctx,
		`UPDATE supercharger_sessions SET start_battery_pct = 18, end_battery_pct = 76, battery_pct_source = 'user_verified' WHERE session_id = $1`,
		sessionID); err != nil {
		t.Fatalf("direct-SQL set trio: %v", err)
	}

	var updatedAtBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM supercharger_sessions WHERE session_id = $1`, sessionID).Scan(&updatedAtBefore); err != nil {
		t.Fatalf("read updated_at before re-upsert: %v", err)
	}

	// Simulate the nightly poller's re-fetch: billing state mutates post-session.
	paid := true
	sess2 := SuperchargerSession{
		SessionID:           sessionID,
		AccountID:           accountID,
		VIN:                 "VIN_BATPCT",
		SiteLocationName:    "Test Supercharger",
		CountryCode:         "US",
		ChargeStartDateTime: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 6, 28, 10, 30, 0, 0, time.UTC),
		BillingType:         "PAYMENT",
		VehicleMakeType:     "MODEL_3",
		EnergyKWh:           floatPtr(30.0),
		TotalCost:           floatPtr(9000),
		Currency:            strPtr("USD"),
		IsPaid:              &paid,
		RawData:             []byte(`{"sessionId":900002,"v":2,"invoiceStatus":"finalized"}`),
	}
	if err := st.upsertSuperchargerSession(ctx, sess2); err != nil {
		t.Fatalf("re-upsertSuperchargerSession: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)
	got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 session, got %d", len(got))
	}
	s := got[0]

	// Mutable column DID refresh.
	if s.IsPaid == nil || !*s.IsPaid {
		t.Errorf("IsPaid: want *true after re-upsert, got %v", s.IsPaid)
	}
	if !s.UpdatedAt.After(updatedAtBefore) {
		t.Errorf("UpdatedAt: want advanced after re-upsert, got %v (was %v)", s.UpdatedAt, updatedAtBefore)
	}

	// The trio is BYTE-FOR-BYTE unchanged — the R3 regression guard.
	if s.StartBatteryPct == nil || *s.StartBatteryPct != 18 {
		t.Errorf("StartBatteryPct: want *18 (untouched by re-upsert), got %v", s.StartBatteryPct)
	}
	if s.EndBatteryPct == nil || *s.EndBatteryPct != 76 {
		t.Errorf("EndBatteryPct: want *76 (untouched by re-upsert), got %v", s.EndBatteryPct)
	}
	if s.BatteryPctSource == nil || *s.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource: want *user_verified (untouched by re-upsert), got %v", s.BatteryPctSource)
	}
}

// TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched implements
// design.md test-contract scenario (b2) — the same R3 regression shape as T6.2,
// applied to the frozen snapshot pair (design D6). It MUST fail if
// start_battery_pct_est/end_battery_pct_est are ever added to
// UpsertSuperchargerSession's ON CONFLICT DO UPDATE SET clause, or turned into a
// nightly-refreshed pair — the regression guard for D6.
func TestStore_SuperchargerUpsert_LeavesVerificationSnapshotUntouched(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(900003)
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionID, 22.5, 7000, "USD", false, []byte(`{"sessionId":900003,"v":1}`))

	// Simulate the future verification UI's single write of the trio AND the frozen
	// snapshot pair together — snapshot deliberately differs from the verified value
	// ("model said X, human said Y").
	if _, err := pool.Exec(ctx,
		`UPDATE supercharger_sessions SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified', start_battery_pct_est = 22, end_battery_pct_est = 78 WHERE session_id = $1`,
		sessionID); err != nil {
		t.Fatalf("direct-SQL set trio + snapshot: %v", err)
	}

	paid := true
	for i, rawData := range [][]byte{
		[]byte(`{"sessionId":900003,"v":2,"invoiceStatus":"pending"}`),
		[]byte(`{"sessionId":900003,"v":3,"invoiceStatus":"finalized"}`),
	} {
		var updatedAtBefore time.Time
		if err := pool.QueryRow(ctx, `SELECT updated_at FROM supercharger_sessions WHERE session_id = $1`, sessionID).Scan(&updatedAtBefore); err != nil {
			t.Fatalf("read updated_at before re-upsert #%d: %v", i+1, err)
		}

		sess := SuperchargerSession{
			SessionID:           sessionID,
			AccountID:           accountID,
			VIN:                 "VIN_BATPCT",
			SiteLocationName:    "Test Supercharger",
			CountryCode:         "US",
			ChargeStartDateTime: time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC),
			ChargeStopDateTime:  time.Date(2026, 6, 28, 10, 30, 0, 0, time.UTC),
			BillingType:         "PAYMENT",
			VehicleMakeType:     "MODEL_3",
			EnergyKWh:           floatPtr(22.5),
			TotalCost:           floatPtr(7000),
			Currency:            strPtr("USD"),
			IsPaid:              &paid,
			RawData:             rawData,
		}
		if err := st.upsertSuperchargerSession(ctx, sess); err != nil {
			t.Fatalf("re-upsertSuperchargerSession #%d: %v", i+1, err)
		}

		r := newSuperchargerReaderImpl(pool)
		got, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
		if err != nil {
			t.Fatalf("SuperchargerSessionsByAccount (after re-upsert #%d): %v", i+1, err)
		}
		if len(got) != 1 {
			t.Fatalf("want 1 session, got %d", len(got))
		}
		s := got[0]

		if s.IsPaid == nil || !*s.IsPaid {
			t.Errorf("re-upsert #%d: IsPaid: want *true, got %v", i+1, s.IsPaid)
		}
		if !s.UpdatedAt.After(updatedAtBefore) {
			t.Errorf("re-upsert #%d: UpdatedAt: want advanced, got %v (was %v)", i+1, s.UpdatedAt, updatedAtBefore)
		}

		// The frozen snapshot pair is BYTE-FOR-BYTE unchanged across every re-upsert —
		// the D6 regression guard.
		if s.StartBatteryPctEst == nil || *s.StartBatteryPctEst != 22 {
			t.Errorf("re-upsert #%d: StartBatteryPctEst: want *22 (untouched), got %v", i+1, s.StartBatteryPctEst)
		}
		if s.EndBatteryPctEst == nil || *s.EndBatteryPctEst != 78 {
			t.Errorf("re-upsert #%d: EndBatteryPctEst: want *78 (untouched), got %v", i+1, s.EndBatteryPctEst)
		}
	}
}

// TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues
// implements design.md test-contract scenario (c): every out-of-range percentage and
// unrecognized battery_pct_source value is rejected by the DB's CHECK constraints
// (Postgres 23514), and the boundary/'polled' sanity check succeeds (T6.3).
func TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	const sessionID = int64(900001)
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionID, 10.0, 3000, "USD", false, []byte(`{"sessionId":900001,"checks":true}`))

	rejected := []struct {
		name string
		sql  string
	}{
		{"start_battery_pct > 100", `UPDATE supercharger_sessions SET start_battery_pct = 101 WHERE session_id = $1`},
		{"start_battery_pct < 0", `UPDATE supercharger_sessions SET start_battery_pct = -1 WHERE session_id = $1`},
		{"end_battery_pct > 100", `UPDATE supercharger_sessions SET end_battery_pct = 101 WHERE session_id = $1`},
		{"start_battery_pct_est > 100", `UPDATE supercharger_sessions SET start_battery_pct_est = 101 WHERE session_id = $1`},
		{"start_battery_pct_est < 0", `UPDATE supercharger_sessions SET start_battery_pct_est = -1 WHERE session_id = $1`},
		{"end_battery_pct_est > 100", `UPDATE supercharger_sessions SET end_battery_pct_est = 101 WHERE session_id = $1`},
		{"battery_pct_source = 'estimated'", `UPDATE supercharger_sessions SET battery_pct_source = 'estimated' WHERE session_id = $1`},
		{"battery_pct_source = 'bogus'", `UPDATE supercharger_sessions SET battery_pct_source = 'bogus' WHERE session_id = $1`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.sql, sessionID)
			if err == nil {
				t.Fatalf("%s: want CHECK violation, got no error", tc.name)
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) {
				t.Fatalf("%s: want *pgconn.PgError, got %T: %v", tc.name, err, err)
			}
			if pgErr.Code != "23514" {
				t.Errorf("%s: want SQLSTATE 23514 (check_violation), got %q: %v", tc.name, pgErr.Code, err)
			}
		})
	}

	// Sanity check: 0 and 100 are inclusive boundaries and must be accepted on all
	// four SMALLINT columns; 'polled' must be accepted even though this tier never
	// writes it in application code.
	if _, err := pool.Exec(ctx,
		`UPDATE supercharger_sessions SET start_battery_pct = 0, end_battery_pct = 100, battery_pct_source = 'polled', start_battery_pct_est = 0, end_battery_pct_est = 100 WHERE session_id = $1`,
		sessionID); err != nil {
		t.Fatalf("boundary/'polled' sanity-check UPDATE: want success, got %v", err)
	}
}

// TestStore_SuperchargerReader_ReturnsBatteryPctTrioAndSnapshot implements design.md
// test-contract scenario (d): SuperchargerSessionsByAccount and
// SuperchargerSessionsByVehicle both surface the trio and the frozen snapshot pair,
// and round-trip an untouched (NULL) session correctly (T6.4).
func TestStore_SuperchargerReader_ReturnsBatteryPctTrioAndSnapshot(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(700099)
	const sessionVerified = int64(900002)
	const sessionSnapshot = int64(900003)
	const sessionUntouched = int64(900001)

	insertBaseSuperchargerSession(t, st, pool, accountID, sessionVerified, 30.0, 9000, "USD", false, []byte(`{"sessionId":900002}`))
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionSnapshot, 22.5, 7000, "USD", false, []byte(`{"sessionId":900003}`))
	insertBaseSuperchargerSession(t, st, pool, accountID, sessionUntouched, 10.0, 3000, "USD", false, []byte(`{"sessionId":900001}`))

	// This fixture's vehicle-scoped session needs a TeslaID to exercise
	// SuperchargerSessionsByVehicle — set it via a direct-SQL update since
	// insertBaseSuperchargerSession's shared fixture doesn't take one.
	if _, err := pool.Exec(ctx, `UPDATE supercharger_sessions SET tesla_id = $1 WHERE session_id = $2`, teslaID, sessionVerified); err != nil {
		t.Fatalf("set tesla_id on verified session: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`UPDATE supercharger_sessions SET start_battery_pct = 18, end_battery_pct = 76, battery_pct_source = 'user_verified' WHERE session_id = $1`,
		sessionVerified); err != nil {
		t.Fatalf("direct-SQL set trio: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE supercharger_sessions SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified', start_battery_pct_est = 22, end_battery_pct_est = 78 WHERE session_id = $1`,
		sessionSnapshot); err != nil {
		t.Fatalf("direct-SQL set trio + snapshot: %v", err)
	}

	r := newSuperchargerReaderImpl(pool)

	byAccount, err := r.SuperchargerSessionsByAccount(ctx, accountID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByAccount: %v", err)
	}
	byVehicle, err := r.SuperchargerSessionsByVehicle(ctx, accountID, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerSessionsByVehicle: %v", err)
	}

	assertVerifiedTrio := func(t *testing.T, s SuperchargerSession) {
		t.Helper()
		if s.StartBatteryPct == nil || *s.StartBatteryPct != 18 {
			t.Errorf("StartBatteryPct: want *18, got %v", s.StartBatteryPct)
		}
		if s.EndBatteryPct == nil || *s.EndBatteryPct != 76 {
			t.Errorf("EndBatteryPct: want *76, got %v", s.EndBatteryPct)
		}
		if s.BatteryPctSource == nil || *s.BatteryPctSource != "user_verified" {
			t.Errorf("BatteryPctSource: want *user_verified, got %v", s.BatteryPctSource)
		}
	}
	assertSnapshot := func(t *testing.T, s SuperchargerSession) {
		t.Helper()
		if s.StartBatteryPctEst == nil || *s.StartBatteryPctEst != 22 {
			t.Errorf("StartBatteryPctEst: want *22, got %v", s.StartBatteryPctEst)
		}
		if s.EndBatteryPctEst == nil || *s.EndBatteryPctEst != 78 {
			t.Errorf("EndBatteryPctEst: want *78, got %v", s.EndBatteryPctEst)
		}
	}
	assertUntouched := func(t *testing.T, s SuperchargerSession) {
		t.Helper()
		if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil ||
			s.StartBatteryPctEst != nil || s.EndBatteryPctEst != nil {
			t.Errorf("want all five battery-percentage columns nil for an untouched session, got %+v", s)
		}
	}

	var foundVerifiedInAccount, foundSnapshotInAccount, foundUntouchedInAccount bool
	for _, s := range byAccount {
		switch s.SessionID {
		case sessionVerified:
			foundVerifiedInAccount = true
			assertVerifiedTrio(t, s)
		case sessionSnapshot:
			foundSnapshotInAccount = true
			assertSnapshot(t, s)
		case sessionUntouched:
			foundUntouchedInAccount = true
			assertUntouched(t, s)
		}
	}
	if !foundVerifiedInAccount {
		t.Errorf("SuperchargerSessionsByAccount: missing session %d", sessionVerified)
	}
	if !foundSnapshotInAccount {
		t.Errorf("SuperchargerSessionsByAccount: missing session %d", sessionSnapshot)
	}
	if !foundUntouchedInAccount {
		t.Errorf("SuperchargerSessionsByAccount: missing session %d", sessionUntouched)
	}

	if len(byVehicle) != 1 || byVehicle[0].SessionID != sessionVerified {
		t.Fatalf("SuperchargerSessionsByVehicle: want 1 session (%d), got %+v", sessionVerified, byVehicle)
	}
	assertVerifiedTrio(t, byVehicle[0])
}

func floatPtr(f float64) *float64 { return &f }
func strPtr(s string) *string     { return &s }
