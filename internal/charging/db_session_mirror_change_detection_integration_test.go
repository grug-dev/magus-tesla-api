// Package charging_test — database-backed integration tests for the
// change-detecting ON CONFLICT DO UPDATE SET clause in MirrorSuperchargerSession
// (db/query.sql). Re-keyed on tesla_id, not account_id.
//
// updated_at advances only when one of the five refreshed columns (energy_kwh,
// total_cost, currency, is_paid, tesla_id) actually differs from what is already
// stored. These tests assert the values Test Contract T-9 states.
//
// T7 (a genuine SessionVerifier.VerifySession edit still advances updated_at) is
// NOT here — VerifySession is unchanged by this design, and an existing test
// already covers it (see db_session_verifier_integration_test.go's
// TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt).
//
// Fixtures are seeded through SessionWriter.MirrorSessions and, for the
// human-owned-columns cases, also through SessionVerifier.VerifySession (the only
// two writers this table has). Rows are read back through the existing
// fetchSuperchargerSession direct-SQL helper (db_session_integration_test.go,
// same package) plus one small local helper for the two columns that helper does
// not select (status, inferred_capacity_kwh_calc) — never through
// chargingdb.SuperchargerSession. pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes). session_ids use the
// 970001-970099 range, disjoint from every other integration test file in this
// package.
//
// Test -> Test Contract case mapping:
//
//	T-9  TestMirrorSessions_T1_UnchangedRemirrorDoesNotAdvanceUpdatedAt
//	     TestMirrorSessions_T2_EnergyKWhChangeAdvancesUpdatedAt
//	     TestMirrorSessions_T3_IsPaidChangeAdvancesUpdatedAt
//	     TestMirrorSessions_T4_TeslaIDValueChangeAdvancesUpdatedAt
//	     TestMirrorSessions_T5_HumanOwnedColumnsSurviveUnchangedRemirror
//	     TestMirrorSessions_T6_InferredCapacityPopulatedUnchangedRemirror
//	     TestMirrorSessions_T8_WriteOnceColumnMismatchNeverResolvesAcrossTwoCalls
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// changeDetectionM1 is a fresh baseline SessionMirror for teslaID/sessionID, all
// fee fields populated — the starting point for every case in this file.
func changeDetectionM1(teslaID, sessionID int64) charging.SessionMirror {
	return charging.SessionMirror{
		VIN:                 "VCHANGE",
		TeslaID:             teslaID,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 10, 8, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 10, 8, 45, 0, 0, time.UTC),
		SiteLocationName:    "Change Detection Test Site",
		EnergyKWh:           ptrFloat64(40.0),
		TotalCost:           ptrFloat64(15.0),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(false),
	}
}

// fetchStatusAndCapacity reads the two columns fetchSuperchargerSession does not
// select — status and inferred_capacity_kwh_calc — for one session_id. Plain Go
// types only (string, *float64), never pgtype.
func fetchStatusAndCapacity(t *testing.T, pool *pgxpool.Pool, sessionID int64) (status string, inferredCapacityKWhCalc *float64) {
	t.Helper()
	if err := pool.QueryRow(context.Background(),
		`SELECT status, inferred_capacity_kwh_calc
		 FROM charging.supercharger_sessions
		 WHERE session_id = $1`,
		sessionID,
	).Scan(&status, &inferredCapacityKWhCalc); err != nil {
		t.Fatalf("fetchStatusAndCapacity: %v", err)
	}
	return status, inferredCapacityKWhCalc
}

// T1: re-mirroring identical values leaves updated_at at exactly T_prev.
func TestMirrorSessions_T1_UnchangedRemirrorDoesNotAdvanceUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := changeDetectionM1(970001, 970001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (second, unchanged): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if !after.UpdatedAt.Equal(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want unchanged at %v, got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
}

// T2: a changed EnergyKWh advances updated_at past T_prev.
func TestMirrorSessions_T2_EnergyKWhChangeAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970002)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := changeDetectionM1(970002, 970002)
	m1.EnergyKWh = ptrFloat64(44.64)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)
	changed := m1
	changed.EnergyKWh = ptrFloat64(46.10)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{changed}); err != nil {
		t.Fatalf("MirrorSessions (energy_kwh changed): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if after.EnergyKWh == nil || *after.EnergyKWh != 46.10 {
		t.Errorf("EnergyKWh: got %v, want 46.10", after.EnergyKWh)
	}
	if !after.UpdatedAt.After(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly after %v, got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
}

// T3: is_paid false -> true advances updated_at past T_prev.
func TestMirrorSessions_T3_IsPaidChangeAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970003)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := changeDetectionM1(970003, 970003)
	m1.IsPaid = ptrBool(false)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)
	changed := m1
	changed.IsPaid = ptrBool(true)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{changed}); err != nil {
		t.Fatalf("MirrorSessions (is_paid changed): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if after.IsPaid == nil || !*after.IsPaid {
		t.Errorf("IsPaid: got %v, want true", after.IsPaid)
	}
	if !after.UpdatedAt.After(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly after %v, got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
}

// T4: tesla_id changing to a different value advances updated_at past T_prev.
// tesla_id stays inside the change comparison because telemetry refreshes it too,
// and a mirrored column takes exactly its source column's write semantics — the
// old orphan-recovery reason (a NULL tesla_id recovering to a value) no longer
// exists, since tesla_id is NOT NULL now.
func TestMirrorSessions_T4_TeslaIDValueChangeAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970004)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := changeDetectionM1(970004, 970004)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}
	if tPrev.TeslaID != 970004 {
		t.Fatalf("setup: expected TeslaID 970004, got %v", tPrev.TeslaID)
	}

	time.Sleep(mirrorGap)
	reassigned := m1
	reassigned.TeslaID = 970099
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{reassigned}); err != nil {
		t.Fatalf("MirrorSessions (tesla_id reassigned): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if after.TeslaID != 970099 {
		t.Errorf("TeslaID: got %v, want 970099", after.TeslaID)
	}
	if !after.UpdatedAt.After(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly after %v, got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
}

// T5: the four human-owned columns (start_battery_pct, end_battery_pct,
// battery_pct_source, status), set earlier via SessionVerifier.VerifySession and
// never via the mirror, survive an otherwise-unchanged re-mirror byte-for-byte —
// and updated_at does not advance either. Both directions matter: a mirror pass
// must not look like a human edit, and this test is the mirror side.
func TestMirrorSessions_T5_HumanOwnedColumnsSurviveUnchangedRemirror(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970005)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	v := charging.NewSessionVerifier(pool)

	m1 := changeDetectionM1(970005, 970005)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, m1.SessionID)

	// Human edit: both percentages supplied directly -> status DONE (not derived).
	if _, err := v.VerifySession(ctx, refFor(m1.TeslaID), id, ptrIntV(20), ptrIntV(80)); err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after VerifySession")
	}
	statusBefore, _ := fetchStatusAndCapacity(t, pool, m1.SessionID)
	if statusBefore != string(charging.SessionStatusDone) {
		t.Fatalf("setup: expected status DONE, got %q", statusBefore)
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (unchanged re-mirror after human edit): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after re-mirror")
	}
	statusAfter, _ := fetchStatusAndCapacity(t, pool, m1.SessionID)

	if !after.UpdatedAt.Equal(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want unchanged at %v (mirror must not look like a human edit), got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
	if after.StartBatteryPct == nil || *after.StartBatteryPct != 20 {
		t.Errorf("StartBatteryPct: want unchanged 20, got %v", after.StartBatteryPct)
	}
	if after.EndBatteryPct == nil || *after.EndBatteryPct != 80 {
		t.Errorf("EndBatteryPct: want unchanged 80, got %v", after.EndBatteryPct)
	}
	if after.BatteryPctSource == nil || *after.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource: want unchanged user_verified, got %v", after.BatteryPctSource)
	}
	if statusAfter != statusBefore {
		t.Errorf("Status: want unchanged %q, got %q", statusBefore, statusAfter)
	}
}

// T6: inferred_capacity_kwh_calc is populated (GENERATED from energy_kwh and the
// two human-owned percentages) after a verified edit, and an otherwise-unchanged
// re-mirror still leaves updated_at alone. This is the regression test for the
// missing-column bug: EXCLUDED's copy of this column is always NULL (it is never
// in the INSERT column list), so a comparison that did not deny-list it would
// find a permanent, unresolvable mismatch on every verified session, every night.
func TestMirrorSessions_T6_InferredCapacityPopulatedUnchangedRemirror(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970006)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	v := charging.NewSessionVerifier(pool)

	m1 := changeDetectionM1(970006, 970006)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, m1.SessionID)

	if _, err := v.VerifySession(ctx, refFor(m1.TeslaID), id, ptrIntV(20), ptrIntV(80)); err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	_, capacityBefore := fetchStatusAndCapacity(t, pool, m1.SessionID)
	if capacityBefore == nil {
		t.Fatalf("setup: expected inferred_capacity_kwh_calc to be non-NULL after verification")
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after VerifySession")
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (unchanged re-mirror): %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after re-mirror")
	}
	_, capacityAfter := fetchStatusAndCapacity(t, pool, m1.SessionID)

	if !after.UpdatedAt.Equal(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt: want unchanged at %v, got %v", tPrev.UpdatedAt, after.UpdatedAt)
	}
	if capacityAfter == nil {
		t.Fatalf("InferredCapacityKWhCalc: want still non-NULL, got nil")
	}
	if *capacityAfter != *capacityBefore {
		t.Errorf("InferredCapacityKWhCalc: want unchanged %v, got %v", *capacityBefore, *capacityAfter)
	}
}

// T8: a renamed site_location_name at the source is a permanent mismatch this
// query must never chase — the write-once bucket. The SAME renamed-site call runs
// TWICE in a row (three MirrorSessions calls total: seed, then two identical
// re-mirrors carrying the renamed site). Both re-mirrors must leave updated_at at
// T_prev and the stored name at its original value. A single-call version of this
// case would not have caught the bug the owner found live: the first mismatch
// alone looked like noise, and only a second consecutive call proves the freeze
// holds rather than resolving on its own.
func TestMirrorSessions_T8_WriteOnceColumnMismatchNeverResolvesAcrossTwoCalls(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 970008)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := changeDetectionM1(970008, 970008)
	m1.SiteLocationName = "Supercharger - Denver"
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}
	tPrev, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after seed mirror")
	}

	renamed := m1
	renamed.SiteLocationName = "Supercharger - Denver West"

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{renamed}); err != nil {
		t.Fatalf("MirrorSessions (renamed site, call 1): %v", err)
	}
	afterFirst, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first renamed re-mirror")
	}
	if !afterFirst.UpdatedAt.Equal(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt after call 1: want unchanged at %v, got %v", tPrev.UpdatedAt, afterFirst.UpdatedAt)
	}
	if afterFirst.SiteLocationName != m1.SiteLocationName {
		t.Errorf("SiteLocationName after call 1: got %q, want original %q", afterFirst.SiteLocationName, m1.SiteLocationName)
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{renamed}); err != nil {
		t.Fatalf("MirrorSessions (renamed site, call 2): %v", err)
	}
	afterSecond, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second renamed re-mirror")
	}
	if !afterSecond.UpdatedAt.Equal(tPrev.UpdatedAt) {
		t.Errorf("UpdatedAt after call 2: want STILL unchanged at %v, got %v (a write-once mismatch must never resolve, and must never keep re-triggering)", tPrev.UpdatedAt, afterSecond.UpdatedAt)
	}
	if afterSecond.SiteLocationName != m1.SiteLocationName {
		t.Errorf("SiteLocationName after call 2: got %q, want original %q", afterSecond.SiteLocationName, m1.SiteLocationName)
	}
}
