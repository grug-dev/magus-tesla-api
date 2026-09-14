package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the three battery-% verification/override columns on
// telemetry.supercharger_history: start_battery_pct, end_battery_pct,
// battery_pct_source (the human-owned trio). A frozen, write-once verification
// snapshot pair existed alongside the trio until RM41-telemetry-drop-estimate-columns
// dropped both — the reservation they existed for (a future SOC estimator) turned
// out unnecessary once the estimator that shipped wrote the real start_battery_pct
// column instead.
//
// No Go writer exists anywhere in this repository for any of the three columns, so
// tests set them via direct SQL against the test pool — legitimate here because
// these tests verify a DATABASE constraint and the absence of a Go write path, not
// an application-level API. Every session now carries a real, non-null tesla_id — a
// session for an unregistered VIN is never stored — so every fixture below sets one.

// insertBaseSuperchargerSession upserts an ordinary session (no battery-% columns)
// and registers cleanup.
func insertBaseSuperchargerSession(t *testing.T, st *dbStore, pool *pgxpool.Pool, teslaID, sessionID int64, energyKWh, totalCost float64, currency string, isPaid bool, rawData []byte) {
	t.Helper()
	ctx := context.Background()
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM telemetry.supercharger_history WHERE session_id = $1", sessionID)
	})

	start := time.Date(2026, 6, 28, 10, 0, 0, 0, time.UTC)
	sess := SuperchargerHistory{
		SessionID:           sessionID,
		VIN:                 "VIN_BATPCT",
		TeslaID:             teslaID,
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
	if err := st.upsertSuperchargerHistory(ctx, sess); err != nil {
		t.Fatalf("upsertSuperchargerHistory(%d): %v", sessionID, err)
	}
}

// TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull verifies
// that a fresh upsert leaves all three battery-% columns NULL.
func TestStore_SuperchargerUpsert_FreshInsertSeedsBatteryPctColumnsNull(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700099)
	const sessionID = int64(900001)
	insertBaseSuperchargerSession(t, st, pool, teslaID, sessionID, 45.2, 12000, "COP", false, []byte(`{"sessionId":900001}`))

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
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
}

// TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched is the most
// important test in this file. It MUST fail if start_battery_pct/end_battery_pct/
// battery_pct_source are ever added to UpsertSuperchargerHistory's ON CONFLICT DO
// UPDATE SET clause: the regression guard for the human-owned trio.
func TestStore_SuperchargerUpsert_LeavesVerifiedBatteryPctUntouched(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700100)
	const sessionID = int64(900002)
	insertBaseSuperchargerSession(t, st, pool, teslaID, sessionID, 30.0, 9000, "USD", false, []byte(`{"sessionId":900002,"v":1}`))

	// Simulate the (future, out-of-scope) verification UI's write via direct SQL —
	// no Go writer exists for the trio.
	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history SET start_battery_pct = 18, end_battery_pct = 76, battery_pct_source = 'user_verified' WHERE session_id = $1`,
		sessionID); err != nil {
		t.Fatalf("direct-SQL set trio: %v", err)
	}

	var updatedAtBefore time.Time
	if err := pool.QueryRow(ctx, `SELECT updated_at FROM telemetry.supercharger_history WHERE session_id = $1`, sessionID).Scan(&updatedAtBefore); err != nil {
		t.Fatalf("read updated_at before re-upsert: %v", err)
	}

	// Simulate the nightly poller's re-fetch: billing state mutates post-session.
	paid := true
	sess2 := SuperchargerHistory{
		SessionID:           sessionID,
		VIN:                 "VIN_BATPCT",
		TeslaID:             teslaID,
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
	if err := st.upsertSuperchargerHistory(ctx, sess2); err != nil {
		t.Fatalf("re-upsertSuperchargerHistory: %v", err)
	}

	r := newSuperchargerHistoryReaderImpl(pool)
	got, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
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

	// The trio is BYTE-FOR-BYTE unchanged.
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

// TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues
// verifies that every out-of-range percentage and unrecognized battery_pct_source
// value is rejected by the DB's CHECK constraints (Postgres 23514), and that the
// boundary/'polled' values are accepted.
func TestStore_SuperchargerBatteryPctChecks_RejectOutOfRangeAndUnrecognizedValues(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700101)
	const sessionID = int64(900001)
	insertBaseSuperchargerSession(t, st, pool, teslaID, sessionID, 10.0, 3000, "USD", false, []byte(`{"sessionId":900001,"checks":true}`))

	rejected := []struct {
		name string
		sql  string
	}{
		{"start_battery_pct > 100", `UPDATE telemetry.supercharger_history SET start_battery_pct = 101 WHERE session_id = $1`},
		{"start_battery_pct < 0", `UPDATE telemetry.supercharger_history SET start_battery_pct = -1 WHERE session_id = $1`},
		{"end_battery_pct > 100", `UPDATE telemetry.supercharger_history SET end_battery_pct = 101 WHERE session_id = $1`},
		{"battery_pct_source = 'estimated'", `UPDATE telemetry.supercharger_history SET battery_pct_source = 'estimated' WHERE session_id = $1`},
		{"battery_pct_source = 'bogus'", `UPDATE telemetry.supercharger_history SET battery_pct_source = 'bogus' WHERE session_id = $1`},
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

	// Sanity check: 0 and 100 are inclusive boundaries and must be accepted on both
	// remaining SMALLINT columns; 'polled' must be accepted even though this tier
	// never writes it in application code.
	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history SET start_battery_pct = 0, end_battery_pct = 100, battery_pct_source = 'polled' WHERE session_id = $1`,
		sessionID); err != nil {
		t.Fatalf("boundary/'polled' sanity-check UPDATE: want success, got %v", err)
	}
}

// TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrio verifies that
// SuperchargerHistoryByVehicle surfaces the trio, round-trips an untouched (NULL)
// session correctly, and (T-11) returns TeslaID as a plain int64.
func TestStore_SuperchargerHistoryReader_ReturnsBatteryPctTrio(t *testing.T) {
	st, pool := newTestStore(t)
	ctx := context.Background()

	teslaID := int64(700099)
	otherTeslaID := int64(700098)
	const sessionVerified = int64(900002)
	const sessionSnapshot = int64(900003)
	const sessionUntouched = int64(900001)

	insertBaseSuperchargerSession(t, st, pool, teslaID, sessionVerified, 30.0, 9000, "USD", false, []byte(`{"sessionId":900002}`))
	insertBaseSuperchargerSession(t, st, pool, otherTeslaID, sessionSnapshot, 22.5, 7000, "USD", false, []byte(`{"sessionId":900003}`))
	insertBaseSuperchargerSession(t, st, pool, otherTeslaID, sessionUntouched, 10.0, 3000, "USD", false, []byte(`{"sessionId":900001}`))

	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history SET start_battery_pct = 18, end_battery_pct = 76, battery_pct_source = 'user_verified' WHERE session_id = $1`,
		sessionVerified); err != nil {
		t.Fatalf("direct-SQL set trio: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`UPDATE telemetry.supercharger_history SET start_battery_pct = 20, end_battery_pct = 80, battery_pct_source = 'user_verified' WHERE session_id = $1`,
		sessionSnapshot); err != nil {
		t.Fatalf("direct-SQL set trio: %v", err)
	}

	r := newSuperchargerHistoryReaderImpl(pool)

	byVehicle, err := r.SuperchargerHistoryByVehicle(ctx, teslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle: %v", err)
	}
	if len(byVehicle) != 1 || byVehicle[0].SessionID != sessionVerified {
		t.Fatalf("SuperchargerHistoryByVehicle: want 1 session (%d), got %+v", sessionVerified, byVehicle)
	}
	// T-11: the round-tripped TeslaID is a plain int64, not a pointer — a
	// compile-time property of the SuperchargerHistory struct, checked here at
	// runtime by simply reading the field.
	if byVehicle[0].TeslaID != teslaID {
		t.Errorf("TeslaID: want %d, got %d", teslaID, byVehicle[0].TeslaID)
	}
	if byVehicle[0].StartBatteryPct == nil || *byVehicle[0].StartBatteryPct != 18 {
		t.Errorf("StartBatteryPct: want *18, got %v", byVehicle[0].StartBatteryPct)
	}
	if byVehicle[0].EndBatteryPct == nil || *byVehicle[0].EndBatteryPct != 76 {
		t.Errorf("EndBatteryPct: want *76, got %v", byVehicle[0].EndBatteryPct)
	}
	if byVehicle[0].BatteryPctSource == nil || *byVehicle[0].BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource: want *user_verified, got %v", byVehicle[0].BatteryPctSource)
	}

	byOtherVehicle, err := r.SuperchargerHistoryByVehicle(ctx, otherTeslaID, 10)
	if err != nil {
		t.Fatalf("SuperchargerHistoryByVehicle (other vehicle): %v", err)
	}
	var foundSnapshot, foundUntouched bool
	for _, s := range byOtherVehicle {
		switch s.SessionID {
		case sessionSnapshot:
			foundSnapshot = true
			if s.StartBatteryPct == nil || *s.StartBatteryPct != 20 {
				t.Errorf("StartBatteryPct: want *20, got %v", s.StartBatteryPct)
			}
			if s.EndBatteryPct == nil || *s.EndBatteryPct != 80 {
				t.Errorf("EndBatteryPct: want *80, got %v", s.EndBatteryPct)
			}
			if s.BatteryPctSource == nil || *s.BatteryPctSource != "user_verified" {
				t.Errorf("BatteryPctSource: want *user_verified, got %v", s.BatteryPctSource)
			}
		case sessionUntouched:
			foundUntouched = true
			if s.StartBatteryPct != nil || s.EndBatteryPct != nil || s.BatteryPctSource != nil {
				t.Errorf("want all three battery-percentage columns nil for an untouched session, got %+v", s)
			}
		}
	}
	if !foundSnapshot {
		t.Errorf("SuperchargerHistoryByVehicle(otherTeslaID): missing session %d", sessionSnapshot)
	}
	if !foundUntouched {
		t.Errorf("SuperchargerHistoryByVehicle(otherTeslaID): missing session %d", sessionUntouched)
	}
}

func floatPtr(f float64) *float64 { return &f }
func strPtr(s string) *string     { return &s }
