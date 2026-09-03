// Package charging_test — database-backed integration tests for SessionVerifier
// (session_verifier.go) and its VerifySuperchargerSession query (db/query.sql), covering
// design.md's Test Contract T1-T9 (RM31-charging-add-session-verification-port,
// tasks.md task 3.1).
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has), verified through SessionVerifier.VerifySession, and read back either
// through the returned charging.Session or through the existing fetchSuperchargerSession
// direct-SQL helper (db_session_integration_test.go, same package) — never through
// chargingdb.SuperchargerSession. pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes). session_ids are in the 950001-950099
// range, disjoint from RM29 tier 6's 920001-920099, RM30 tier 1's 940001-940099, and
// the real backfilled 734860294.
//
// Test -> Test Contract case mapping:
//
//	T1, T4, T9  TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt
//	T2          TestVerifySession_PartialStartOnlyStillSetsSource
//	T3          TestVerifySession_PartialEndOnlyStillSetsSource (updated by MAG-36,
//	            charging-add-derived-start-battery-pct: the expected StartBatteryPct
//	            is now the derived 41, not nil -- see the test's own doc comment)
//	T5          TestVerifySession_OutOfRangeRejectedBeforeQuery
//	T6          TestVerifySession_WrongAccountIsNoOp
//	T7          TestVerifySession_UnknownIDSameErrorShapeAsWrongAccount
//	T8          TestVerifySession_OnlyTargetColumnsChange
package charging_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- session-verifier-specific test helpers ---

// seedVerifierSession seeds one baseline supercharger_sessions row via
// SessionWriter.MirrorSessions — non-nil SiteLocationName/EnergyKWh/TotalCost/
// Currency/IsPaid, all five battery-percentage columns NULL (design.md's baseline
// fixture V1 shape) — and returns its server-assigned id.
func seedVerifierSession(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, sessionID int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VVERIFY",
		TeslaID:             ptrInt64(sessionID),
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		SiteLocationName:    "Verifier Test Site",
		EnergyKWh:           ptrFloat64(30.5),
		TotalCost:           ptrFloat64(9.99),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(true),
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("seedVerifierSession: MirrorSessions: %v", err)
	}
	return fetchSuperchargerSessionID(t, pool, accountID, sessionID)
}

// fetchSuperchargerSessionID reads back the server-assigned id for one (accountID,
// sessionID) pair by direct SQL — SessionWriter.MirrorSessions is an :exec query
// and returns no row, but every SessionVerifier.VerifySession call needs the id.
func fetchSuperchargerSessionID(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, sessionID int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		"SELECT id FROM charging.supercharger_sessions WHERE account_id = $1 AND session_id = $2",
		accountID, sessionID,
	).Scan(&id); err != nil {
		t.Fatalf("fetchSuperchargerSessionID: %v", err)
	}
	return id
}

// ptrIntV is a local *int helper for battery-percentage arguments/assertions in
// this file — distinct from db_session_integration_test.go's *int64/*float64/*bool
// helpers (ptrInt64/ptrFloat64/ptrBool), which this file also reuses directly.
func ptrIntV(v int) *int { return &v }

// assertVerifierColumnsUnchanged fails the test unless every column
// TestVerifySession_OutOfRangeRejectedBeforeQuery/WrongAccountIsNoOp cares about
// (start_battery_pct, end_battery_pct, battery_pct_source, updated_at) is identical
// between before and after.
func assertVerifierColumnsUnchanged(t *testing.T, before, after superchargerSessionRow, context string) {
	t.Helper()
	if !intPtrEqualV(before.StartBatteryPct, after.StartBatteryPct) {
		t.Errorf("%s: StartBatteryPct changed: before %v, after %v", context, before.StartBatteryPct, after.StartBatteryPct)
	}
	if !intPtrEqualV(before.EndBatteryPct, after.EndBatteryPct) {
		t.Errorf("%s: EndBatteryPct changed: before %v, after %v", context, before.EndBatteryPct, after.EndBatteryPct)
	}
	if !stringPtrEqualV(before.BatteryPctSource, after.BatteryPctSource) {
		t.Errorf("%s: BatteryPctSource changed: before %v, after %v", context, before.BatteryPctSource, after.BatteryPctSource)
	}
	if !before.UpdatedAt.Equal(after.UpdatedAt) {
		t.Errorf("%s: UpdatedAt changed: before %v, after %v", context, before.UpdatedAt, after.UpdatedAt)
	}
}

func intPtrEqualV(a, b *int) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func int64PtrEqualV(a, b *int64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func float64PtrEqualV(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func boolPtrEqualV(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func stringPtrEqualV(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// --- T1, T4, T9 ---

// TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt seeds baseline session
// 950001 under a fresh account, then makes two sequential VerifySession calls on the
// SAME session, matching design.md's dependency chain: T4 reuses T1's fixture (the
// session T1 just verified), and T9 compares the two calls' UpdatedAt.
func TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v1ID := seedVerifierSession(t, pool, acctA, 950001)
	baseline, ok := fetchSuperchargerSession(t, pool, acctA, 950001)
	if !ok {
		t.Fatalf("expected baseline row for session 950001")
	}

	time.Sleep(mirrorGap)

	// T1: a normal verify sets both percentages and the source, and returns them.
	s1, err := v.VerifySession(ctx, acctA, v1ID, ptrIntV(20), ptrIntV(80))
	if err != nil {
		t.Fatalf("T1: VerifySession: %v", err)
	}
	if s1.StartBatteryPct == nil || *s1.StartBatteryPct != 20 {
		t.Errorf("T1: StartBatteryPct = %v, want 20", s1.StartBatteryPct)
	}
	if s1.EndBatteryPct == nil || *s1.EndBatteryPct != 80 {
		t.Errorf("T1: EndBatteryPct = %v, want 80", s1.EndBatteryPct)
	}
	if s1.BatteryPctSource == nil || *s1.BatteryPctSource != "user_verified" {
		t.Errorf("T1: BatteryPctSource = %v, want user_verified", s1.BatteryPctSource)
	}
	if !s1.UpdatedAt.After(baseline.UpdatedAt) {
		t.Errorf("T1: UpdatedAt = %v, want strictly after baseline UpdatedAt %v", s1.UpdatedAt, baseline.UpdatedAt)
	}

	time.Sleep(mirrorGap)

	// T4: clearing both previously-set percentages also clears the source.
	s4, err := v.VerifySession(ctx, acctA, v1ID, nil, nil)
	if err != nil {
		t.Fatalf("T4: VerifySession: %v", err)
	}
	if s4.StartBatteryPct != nil {
		t.Errorf("T4: StartBatteryPct = %v, want nil", s4.StartBatteryPct)
	}
	if s4.EndBatteryPct != nil {
		t.Errorf("T4: EndBatteryPct = %v, want nil", s4.EndBatteryPct)
	}
	if s4.BatteryPctSource != nil {
		t.Errorf("T4: BatteryPctSource = %v, want nil (cleared in the same call, D7)", s4.BatteryPctSource)
	}

	// T9: updated_at strictly advances on every successful call, including the
	// clear-to-NULL call that changes no visible percentage value.
	if !s4.UpdatedAt.After(s1.UpdatedAt) {
		t.Errorf("T9: T4's UpdatedAt = %v, want strictly after T1's UpdatedAt %v", s4.UpdatedAt, s1.UpdatedAt)
	}
}

// --- T2 ---

// TestVerifySession_PartialStartOnlyStillSetsSource: setting only startBatteryPct
// leaves endBatteryPct NULL and still writes the source (D6 — a non-nil source is
// written even though only one percentage is set).
func TestVerifySession_PartialStartOnlyStillSetsSource(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v2ID := seedVerifierSession(t, pool, acctA, 950002)

	s2, err := v.VerifySession(ctx, acctA, v2ID, ptrIntV(35), nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if s2.StartBatteryPct == nil || *s2.StartBatteryPct != 35 {
		t.Errorf("StartBatteryPct = %v, want 35", s2.StartBatteryPct)
	}
	if s2.EndBatteryPct != nil {
		t.Errorf("EndBatteryPct = %v, want nil", s2.EndBatteryPct)
	}
	if s2.BatteryPctSource == nil || *s2.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource = %v, want user_verified", s2.BatteryPctSource)
	}
}

// --- T3 ---

// TestVerifySession_PartialEndOnlyStillSetsSource is T2's symmetric case: setting
// only endBatteryPct still writes the source correctly, proving the source
// computation is not conditioned specifically on startBatteryPct being the one
// that is non-nil (RM31's original point, unchanged by this change).
//
// Since MAG-36 (charging-add-derived-start-battery-pct design.md D12/C1), this is
// also the natural end-to-end regression proof that VerifySession's new derivation
// fires: seedVerifierSession's fixture carries EnergyKWh = 30.5 (unchanged), so
// leaving startBatteryPct nil while supplying endBatteryPct = 90 now derives
// StartBatteryPct instead of leaving it absent. At the 62.0 kWh capacity constant,
// derivedStartBatteryPct(62.0, 30.5, 90) computes 90 - 30.5/62.0*100 = 40.806...,
// which math.Round (half away from zero) rounds to 41 -- in [0, 100], so it is
// stored rather than discarded.
func TestVerifySession_PartialEndOnlyStillSetsSource(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v3ID := seedVerifierSession(t, pool, acctA, 950003)

	s3, err := v.VerifySession(ctx, acctA, v3ID, nil, ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if s3.StartBatteryPct == nil || *s3.StartBatteryPct != 41 {
		t.Errorf("StartBatteryPct = %v, want 41 (derived)", s3.StartBatteryPct)
	}
	if s3.EndBatteryPct == nil || *s3.EndBatteryPct != 90 {
		t.Errorf("EndBatteryPct = %v, want 90", s3.EndBatteryPct)
	}
	if s3.BatteryPctSource == nil || *s3.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource = %v, want user_verified", s3.BatteryPctSource)
	}
}

// --- T5 ---

// TestVerifySession_OutOfRangeRejectedBeforeQuery: an out-of-range percentage on
// either side is rejected before the query runs, naming the offending field and
// value, and the row is left completely unchanged both times.
func TestVerifySession_OutOfRangeRejectedBeforeQuery(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v5ID := seedVerifierSession(t, pool, acctA, 950005)
	before, ok := fetchSuperchargerSession(t, pool, acctA, 950005)
	if !ok {
		t.Fatalf("expected baseline row for session 950005")
	}

	// Sub-case 1: start_battery_pct out of range (101 > 100).
	_, err := v.VerifySession(ctx, acctA, v5ID, ptrIntV(101), ptrIntV(50))
	if err == nil {
		t.Fatal("expected error for start_battery_pct=101, got nil")
	}
	if !strings.Contains(err.Error(), "start_battery_pct") || !strings.Contains(err.Error(), "101") {
		t.Errorf("error = %q, want it to contain \"start_battery_pct\" and \"101\"", err.Error())
	}
	after1, ok := fetchSuperchargerSession(t, pool, acctA, 950005)
	if !ok {
		t.Fatalf("expected row for session 950005 still present")
	}
	assertVerifierColumnsUnchanged(t, before, after1, "after start_battery_pct=101 rejection")

	// Sub-case 2: end_battery_pct out of range (-1 < 0).
	_, err = v.VerifySession(ctx, acctA, v5ID, ptrIntV(50), ptrIntV(-1))
	if err == nil {
		t.Fatal("expected error for end_battery_pct=-1, got nil")
	}
	if !strings.Contains(err.Error(), "end_battery_pct") || !strings.Contains(err.Error(), "-1") {
		t.Errorf("error = %q, want it to contain \"end_battery_pct\" and \"-1\"", err.Error())
	}
	after2, ok := fetchSuperchargerSession(t, pool, acctA, 950005)
	if !ok {
		t.Fatalf("expected row for session 950005 still present")
	}
	assertVerifierColumnsUnchanged(t, before, after2, "after end_battery_pct=-1 rejection")
}

// --- T6 ---

// TestVerifySession_WrongAccountIsNoOp mirrors TestUpdate_CrossAccountIsNoOp
// (db_integration_test.go) for supercharger_sessions: calling VerifySession with a
// session id that belongs to a different account has zero effect and returns an
// error wrapping pgx.ErrNoRows.
func TestVerifySession_WrongAccountIsNoOp(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	acctB := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA, acctB)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v6ID := seedVerifierSession(t, pool, acctA, 950006)
	before, ok := fetchSuperchargerSession(t, pool, acctA, 950006)
	if !ok {
		t.Fatalf("expected baseline row for session 950006")
	}

	_, err := v.VerifySession(ctx, acctB, v6ID, ptrIntV(10), ptrIntV(20))
	if err == nil {
		t.Fatal("VerifySession with wrong account_id: expected error, got nil")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected error wrapping pgx.ErrNoRows, got %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, acctA, 950006)
	if !ok {
		t.Fatalf("expected row for session 950006 still present under acctA")
	}
	assertVerifierColumnsUnchanged(t, before, after, "after wrong-account VerifySession call")
}

// --- T7 ---

// TestVerifySession_UnknownIDSameErrorShapeAsWrongAccount: an id belonging to no
// session at all produces the identical error shape as T6 — not-found and
// wrong-account are indistinguishable (design.md D5).
func TestVerifySession_UnknownIDSameErrorShapeAsWrongAccount(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	_, err := v.VerifySession(ctx, acctA, uuid.New(), ptrIntV(10), ptrIntV(20))
	if err == nil {
		t.Fatal("VerifySession with unknown id: expected error, got nil")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected error wrapping pgx.ErrNoRows (same sentinel as T6), got %v", err)
	}
}

// --- T8 ---

// TestVerifySession_OnlyTargetColumnsChange is the structural proof of design.md
// D1: a successful verify changes ONLY start_battery_pct, end_battery_pct,
// battery_pct_source, and updated_at. Every other column is captured via direct SQL
// both before and after the call and asserted bit-identical. id, account_id, and
// session_id are implicitly unchanged — fetchSuperchargerSession is keyed on
// (account_id, session_id), so a changed id or session_id would make the "after"
// re-fetch return ok=false or a different row outright.
func TestVerifySession_OnlyTargetColumnsChange(t *testing.T) {
	pool := newTestPool(t)
	acctA := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, acctA)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v8ID := seedVerifierSession(t, pool, acctA, 950008)
	before, ok := fetchSuperchargerSession(t, pool, acctA, 950008)
	if !ok {
		t.Fatalf("expected baseline row for session 950008")
	}

	if _, err := v.VerifySession(ctx, acctA, v8ID, ptrIntV(15), ptrIntV(95)); err != nil {
		t.Fatalf("VerifySession: %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, acctA, 950008)
	if !ok {
		t.Fatalf("expected row for session 950008 still present")
	}

	// id must be bit-identical too (design.md T8 lists it). superchargerSessionRow carries
	// no ID field and belongs to RM29 tier 6's test file, which this change must not
	// touch — so re-read the key directly. account_id and session_id are already
	// pinned by fetchSuperchargerSession's own WHERE clause returning ok above.
	if got := fetchSuperchargerSessionID(t, pool, acctA, 950008); got != v8ID {
		t.Errorf("id changed: before %v, after %v", v8ID, got)
	}

	// Columns that must be BIT-IDENTICAL before and after.
	if before.VIN != after.VIN {
		t.Errorf("VIN changed: before %q, after %q", before.VIN, after.VIN)
	}
	if !int64PtrEqualV(before.TeslaID, after.TeslaID) {
		t.Errorf("TeslaID changed: before %v, after %v", before.TeslaID, after.TeslaID)
	}
	if !before.ChargeStartDateTime.Equal(after.ChargeStartDateTime) {
		t.Errorf("ChargeStartDateTime changed: before %v, after %v", before.ChargeStartDateTime, after.ChargeStartDateTime)
	}
	if !before.ChargeStopDateTime.Equal(after.ChargeStopDateTime) {
		t.Errorf("ChargeStopDateTime changed: before %v, after %v", before.ChargeStopDateTime, after.ChargeStopDateTime)
	}
	if before.SiteLocationName != after.SiteLocationName {
		t.Errorf("SiteLocationName changed: before %q, after %q", before.SiteLocationName, after.SiteLocationName)
	}
	if !float64PtrEqualV(before.EnergyKWh, after.EnergyKWh) {
		t.Errorf("EnergyKWh changed: before %v, after %v", before.EnergyKWh, after.EnergyKWh)
	}
	if !float64PtrEqualV(before.TotalCost, after.TotalCost) {
		t.Errorf("TotalCost changed: before %v, after %v", before.TotalCost, after.TotalCost)
	}
	if !stringPtrEqualV(before.Currency, after.Currency) {
		t.Errorf("Currency changed: before %v, after %v", before.Currency, after.Currency)
	}
	if !boolPtrEqualV(before.IsPaid, after.IsPaid) {
		t.Errorf("IsPaid changed: before %v, after %v", before.IsPaid, after.IsPaid)
	}
	if !before.CreatedAt.Equal(after.CreatedAt) {
		t.Errorf("CreatedAt changed: before %v, after %v", before.CreatedAt, after.CreatedAt)
	}

	// The four columns that ARE expected to differ.
	if after.StartBatteryPct == nil || *after.StartBatteryPct != 15 {
		t.Errorf("StartBatteryPct after = %v, want 15", after.StartBatteryPct)
	}
	if after.EndBatteryPct == nil || *after.EndBatteryPct != 95 {
		t.Errorf("EndBatteryPct after = %v, want 95", after.EndBatteryPct)
	}
	if after.BatteryPctSource == nil || *after.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource after = %v, want user_verified", after.BatteryPctSource)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: before %v, after %v", before.UpdatedAt, after.UpdatedAt)
	}
}
