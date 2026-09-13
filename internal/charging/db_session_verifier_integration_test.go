// Package charging_test — database-backed integration tests for SessionVerifier
// (session_verifier.go) and its VerifySuperchargerSession query (db/query.sql).
// Re-keyed on tesla_id, not account_id (RM57-charging-rekey-supercharger-
// sessions-on-tesla-id, MAG-67): VerifySession's tenant boundary is now the
// vehicle, not the account (design.md D3).
//
// Fixtures are seeded through SessionWriter.MirrorSessions (the only writer this
// table has), verified through SessionVerifier.VerifySession, and read back either
// through the returned charging.Session or through the existing fetchSuperchargerSession
// direct-SQL helper (db_session_integration_test.go, same package) — never through
// chargingdb.SuperchargerSession. pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes). session_ids are in the 950001-950099
// range, disjoint from RM29 tier 6's 920001-920099, RM30/RM31 tier 1's 940001-960099,
// and the real backfilled 734860294. This change's own T-6/T-7 fixtures use 9401/9501,
// disjoint from every range above.
//
// Test -> Test Contract case mapping:
//
//	T-6  TestVerifySession_T6_WrongVehicleIsNoOp
//	T-7  TestVerifySession_T7_DerivedStartNoNilVehicleBranch
//	     TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt
//	     TestVerifySession_PartialStartOnlyStillSetsSource
//	     TestVerifySession_PartialEndOnlyStillSetsSource (updated by MAG-36,
//	     charging-add-derived-start-battery-pct: the expected StartBatteryPct
//	     is now the derived 41, not nil -- see the test's own doc comment)
//	     TestVerifySession_OutOfRangeRejectedBeforeQuery
//	     TestVerifySession_UnknownIDSameErrorShapeAsWrongVehicle
//	     TestVerifySession_OnlyTargetColumnsChange
//	S1   TestVerifySession_S1_FreshMirrorIsInProgress
//	S2   TestVerifySession_S2_StartOnlyIsInProgress
//	S3   TestVerifySession_S3_EndOnlyNoEnergyStaysInProgress
//	S4   TestVerifySession_S4_EndOnlyDerivedIsDoneCalculated
//	S5   TestVerifySession_S5_BothSuppliedIsDone
//	S6   TestVerifySession_S6_ReverificationFlipsCalculatedToDone
//	S7   TestVerifySession_S7_ClearingBothResetsToInProgress
//	S8   TestVerifySession_S8_DerivedOutOfRangeStaysInProgress
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
// Currency/IsPaid, all three battery-percentage/provenance columns NULL — and
// returns its server-assigned id.
func seedVerifierSession(t *testing.T, pool *pgxpool.Pool, teslaID, sessionID int64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	m := charging.SessionMirror{
		VIN:                 "VVERIFY",
		TeslaID:             teslaID,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		SiteLocationName:    "Verifier Test Site",
		EnergyKWh:           ptrFloat64(30.5),
		TotalCost:           ptrFloat64(9.99),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(true),
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("seedVerifierSession: MirrorSessions: %v", err)
	}
	return fetchSuperchargerSessionID(t, pool, sessionID)
}

// fetchSuperchargerSessionID reads back the server-assigned id for one session_id
// by direct SQL — SessionWriter.MirrorSessions is an :exec query and returns no
// row, but every SessionVerifier.VerifySession call needs the id.
func fetchSuperchargerSessionID(t *testing.T, pool *pgxpool.Pool, sessionID int64) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := pool.QueryRow(context.Background(),
		"SELECT id FROM charging.supercharger_sessions WHERE session_id = $1",
		sessionID,
	).Scan(&id); err != nil {
		t.Fatalf("fetchSuperchargerSessionID: %v", err)
	}
	return id
}

// ptrIntV is a local *int helper for battery-percentage arguments/assertions in
// this file — distinct from db_session_integration_test.go's *float64/*bool
// helpers (ptrFloat64/ptrBool), which this file also reuses directly.
func ptrIntV(v int) *int { return &v }

// assertVerifierColumnsUnchanged fails the test unless every column
// TestVerifySession_OutOfRangeRejectedBeforeQuery/T6 cares about (start_battery_pct,
// end_battery_pct, battery_pct_source, updated_at) is identical between before and
// after.
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

// --- T-6 ---

// T-6: VerifySession is scoped by vehicle, and a wrong vehicle writes nothing.
// Seed session 9401 for tesla_id 111 with both percentages NULL. A call naming
// tesla_id 222 errors, wrapping pgx.ErrNoRows, and leaves the row untouched
// (proven by a direct SELECT, not only by the returned error). A call naming the
// right vehicle then succeeds.
func TestVerifySession_T6_WrongVehicleIsNoOp(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(9401)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 111, sessionID)
	before, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected baseline row for session %d", sessionID)
	}

	_, err := v.VerifySession(ctx, 222, id, ptrIntV(20), ptrIntV(80))
	if err == nil {
		t.Fatal("VerifySession with wrong vehicle: expected error, got nil")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected error wrapping pgx.ErrNoRows, got %v", err)
	}

	after, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row for session %d still present", sessionID)
	}
	assertVerifierColumnsUnchanged(t, before, after, "after wrong-vehicle VerifySession call")
	if after.StartBatteryPct != nil || after.EndBatteryPct != nil {
		t.Errorf("percentages must still be NULL: start=%v end=%v", after.StartBatteryPct, after.EndBatteryPct)
	}

	got, err := v.VerifySession(ctx, 111, id, ptrIntV(20), ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession with the right vehicle: expected success, got %v", err)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 20 {
		t.Errorf("StartBatteryPct = %v, want 20", got.StartBatteryPct)
	}
	if got.EndBatteryPct == nil || *got.EndBatteryPct != 80 {
		t.Errorf("EndBatteryPct = %v, want 80", got.EndBatteryPct)
	}
	if got.BatteryPctSource == nil || *got.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource = %v, want user_verified", got.BatteryPctSource)
	}
	if got.Status != charging.SessionStatusDone {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDone)
	}
	if got.TeslaID != 111 {
		t.Errorf("TeslaID = %d, want 111", got.TeslaID)
	}
}

// --- T-7 ---

// T-7: the derived-start path no longer has a nil-vehicle branch. Session 9501,
// tesla_id 111, energy_kwh=31.0, no monthly_effective_capacity row for that
// vehicle, so packCapacityKWh falls back to the default 62.0.
// 80 − 31.0/62.0*100 = 30.
func TestVerifySession_T7_DerivedStartNoNilVehicleBranch(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(9501)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSessionEnergy(t, pool, 111, sessionID, ptrFloat64(31.0))

	got, err := v.VerifySession(ctx, 111, id, nil, ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 30 {
		t.Errorf("StartBatteryPct = %v, want 30 (derived)", got.StartBatteryPct)
	}
	if got.BatteryPctSource == nil || *got.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource = %v, want user_verified", got.BatteryPctSource)
	}
	if got.Status != charging.SessionStatusDoneCalculated {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDoneCalculated)
	}
}

// --- T1, T4, T9 ---

// TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt seeds baseline session
// 950001, then makes two sequential VerifySession calls on the SAME session: T1
// reuses T1's fixture (the session T1 just verified), and T9 compares the two
// calls' UpdatedAt.
func TestVerifySession_T1_T4_T9_SetThenClearAdvancesUpdatedAt(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950001)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v1ID := seedVerifierSession(t, pool, 950001, 950001)
	baseline, ok := fetchSuperchargerSession(t, pool, 950001)
	if !ok {
		t.Fatalf("expected baseline row for session 950001")
	}

	time.Sleep(mirrorGap)

	// T1: a normal verify sets both percentages and the source, and returns them.
	s1, err := v.VerifySession(ctx, 950001, v1ID, ptrIntV(20), ptrIntV(80))
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
	s4, err := v.VerifySession(ctx, 950001, v1ID, nil, nil)
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
// leaves endBatteryPct NULL and still writes the source (a non-nil source is
// written even though only one percentage is set).
func TestVerifySession_PartialStartOnlyStillSetsSource(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950002)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v2ID := seedVerifierSession(t, pool, 950002, 950002)

	s2, err := v.VerifySession(ctx, 950002, v2ID, ptrIntV(35), nil)
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
// that is non-nil.
//
// seedVerifierSession's fixture carries EnergyKWh = 30.5, so leaving
// startBatteryPct nil while supplying endBatteryPct = 90 derives StartBatteryPct
// instead of leaving it absent. At the 62.0 kWh capacity constant,
// derivedStartBatteryPct(62.0, 30.5, 90) computes 90 - 30.5/62.0*100 = 40.806...,
// which math.Round (half away from zero) rounds to 41 -- in [0, 100], so it is
// stored rather than discarded.
func TestVerifySession_PartialEndOnlyStillSetsSource(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950003)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v3ID := seedVerifierSession(t, pool, 950003, 950003)

	s3, err := v.VerifySession(ctx, 950003, v3ID, nil, ptrIntV(90))
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
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950005)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v5ID := seedVerifierSession(t, pool, 950005, 950005)
	before, ok := fetchSuperchargerSession(t, pool, 950005)
	if !ok {
		t.Fatalf("expected baseline row for session 950005")
	}

	// Sub-case 1: start_battery_pct out of range (101 > 100).
	_, err := v.VerifySession(ctx, 950005, v5ID, ptrIntV(101), ptrIntV(50))
	if err == nil {
		t.Fatal("expected error for start_battery_pct=101, got nil")
	}
	if !strings.Contains(err.Error(), "start_battery_pct") || !strings.Contains(err.Error(), "101") {
		t.Errorf("error = %q, want it to contain \"start_battery_pct\" and \"101\"", err.Error())
	}
	after1, ok := fetchSuperchargerSession(t, pool, 950005)
	if !ok {
		t.Fatalf("expected row for session 950005 still present")
	}
	assertVerifierColumnsUnchanged(t, before, after1, "after start_battery_pct=101 rejection")

	// Sub-case 2: end_battery_pct out of range (-1 < 0).
	_, err = v.VerifySession(ctx, 950005, v5ID, ptrIntV(50), ptrIntV(-1))
	if err == nil {
		t.Fatal("expected error for end_battery_pct=-1, got nil")
	}
	if !strings.Contains(err.Error(), "end_battery_pct") || !strings.Contains(err.Error(), "-1") {
		t.Errorf("error = %q, want it to contain \"end_battery_pct\" and \"-1\"", err.Error())
	}
	after2, ok := fetchSuperchargerSession(t, pool, 950005)
	if !ok {
		t.Fatalf("expected row for session 950005 still present")
	}
	assertVerifierColumnsUnchanged(t, before, after2, "after end_battery_pct=-1 rejection")
}

// --- T7 (old numbering; not to be confused with this change's own T-7) ---

// TestVerifySession_UnknownIDSameErrorShapeAsWrongVehicle: an id belonging to no
// session at all produces the identical error shape as a wrong-vehicle call —
// not-found and wrong-vehicle are indistinguishable (design.md D3).
func TestVerifySession_UnknownIDSameErrorShapeAsWrongVehicle(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	_, err := v.VerifySession(ctx, 950007, uuid.New(), ptrIntV(10), ptrIntV(20))
	if err == nil {
		t.Fatal("VerifySession with unknown id: expected error, got nil")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("expected error wrapping pgx.ErrNoRows (same sentinel as a wrong vehicle), got %v", err)
	}
}

// --- T8 ---

// TestVerifySession_OnlyTargetColumnsChange is the structural proof that a
// successful verify changes ONLY start_battery_pct, end_battery_pct,
// battery_pct_source, status and updated_at. Every other column is captured via
// direct SQL both before and after the call and asserted bit-identical. id and
// session_id are implicitly unchanged — fetchSuperchargerSession is keyed on
// session_id, so a changed id or session_id would make the "after" re-fetch
// return ok=false or a different row outright.
func TestVerifySession_OnlyTargetColumnsChange(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950008)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	v8ID := seedVerifierSession(t, pool, 950008, 950008)
	before, ok := fetchSuperchargerSession(t, pool, 950008)
	if !ok {
		t.Fatalf("expected baseline row for session 950008")
	}

	got, err := v.VerifySession(ctx, 950008, v8ID, ptrIntV(15), ptrIntV(95))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDone {
		t.Errorf("Status = %q, want %q (both percentages supplied directly)", got.Status, charging.SessionStatusDone)
	}

	after, ok := fetchSuperchargerSession(t, pool, 950008)
	if !ok {
		t.Fatalf("expected row for session 950008 still present")
	}

	// id must be bit-identical too. superchargerSessionRow carries no ID field, so
	// re-read the key directly. session_id is already pinned by
	// fetchSuperchargerSession's own WHERE clause returning ok above.
	if got := fetchSuperchargerSessionID(t, pool, 950008); got != v8ID {
		t.Errorf("id changed: before %v, after %v", v8ID, got)
	}

	// Columns that must be BIT-IDENTICAL before and after.
	if before.VIN != after.VIN {
		t.Errorf("VIN changed: before %q, after %q", before.VIN, after.VIN)
	}
	if before.TeslaID != after.TeslaID {
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

// --- Group S: sessionStatusFor state truth table (RM41 tier 4, MAG-45) ---

// seedVerifierSessionEnergy seeds one baseline supercharger_sessions row via
// SessionWriter.MirrorSessions with a caller-chosen EnergyKWh (nil included) and
// all battery-percentage/status columns at their defaults -- generalizes
// seedVerifierSession for Group S and T-7, which need an energy_kwh SQL NULL
// fixture (S3) and specific non-default values (T-7, S8) seedVerifierSession's
// hardcoded 30.5 cannot produce.
func seedVerifierSessionEnergy(t *testing.T, pool *pgxpool.Pool, teslaID, sessionID int64, energyKWh *float64) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	m := charging.SessionMirror{
		VIN:                 "VVERIFY",
		TeslaID:             teslaID,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 11, 0, 0, 0, time.UTC),
		SiteLocationName:    "Verifier Test Site",
		EnergyKWh:           energyKWh,
		TotalCost:           ptrFloat64(9.99),
		Currency:            ptrString("USD"),
		IsPaid:              ptrBool(true),
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("seedVerifierSessionEnergy: MirrorSessions: %v", err)
	}
	return fetchSuperchargerSessionID(t, pool, sessionID)
}

// TestVerifySession_S1_FreshMirrorIsInProgress: a fresh mirror, never verified,
// reads IN_PROGRESS -- the column DEFAULT, exercised through the real INSERT path.
func TestVerifySession_S1_FreshMirrorIsInProgress(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950010)

	id := seedVerifierSession(t, pool, 950010, 950010)

	var status string
	if err := pool.QueryRow(context.Background(),
		"SELECT status FROM charging.supercharger_sessions WHERE id = $1", id,
	).Scan(&status); err != nil {
		t.Fatalf("reading status: %v", err)
	}
	if status != string(charging.SessionStatusInProgress) {
		t.Errorf("status = %q, want %q", status, charging.SessionStatusInProgress)
	}
}

// TestVerifySession_S2_StartOnlyIsInProgress: start only, end nil -> IN_PROGRESS
// (truth table row 3) -- deliberately not a fourth state.
func TestVerifySession_S2_StartOnlyIsInProgress(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950011)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 950011, 950011)

	got, err := v.VerifySession(ctx, 950011, id, ptrIntV(30), nil)
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 30 {
		t.Errorf("StartBatteryPct = %v, want 30", got.StartBatteryPct)
	}
	if got.EndBatteryPct != nil {
		t.Errorf("EndBatteryPct = %v, want nil", got.EndBatteryPct)
	}
}

// TestVerifySession_S3_EndOnlyNoEnergyStaysInProgress: end only, energy_kwh SQL
// NULL -> derivation impossible, IN_PROGRESS (truth table row 2, no-energy sub-case).
func TestVerifySession_S3_EndOnlyNoEnergyStaysInProgress(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950012)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSessionEnergy(t, pool, 950012, 950012, nil)

	got, err := v.VerifySession(ctx, 950012, id, nil, ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil (no energy to derive from)", got.StartBatteryPct)
	}
	if got.EndBatteryPct == nil || *got.EndBatteryPct != 80 {
		t.Errorf("EndBatteryPct = %v, want 80", got.EndBatteryPct)
	}
}

// TestVerifySession_S4_EndOnlyDerivedIsDoneCalculated: end only, energy_kwh
// present, derivation succeeds -> DONE_CALCULATED (truth table row 4). Arithmetic:
// 90 - 30.5/62*100 = 40.8 -> rounds to 41, the identical fixture shape
// TestVerifySession_PartialEndOnlyStillSetsSource already pins.
func TestVerifySession_S4_EndOnlyDerivedIsDoneCalculated(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950013)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 950013, 950013)

	got, err := v.VerifySession(ctx, 950013, id, nil, ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDoneCalculated {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDoneCalculated)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 41 {
		t.Errorf("StartBatteryPct = %v, want 41 (derived)", got.StartBatteryPct)
	}
}

// TestVerifySession_S5_BothSuppliedIsDone: both supplied directly -> DONE (truth
// table row 5).
func TestVerifySession_S5_BothSuppliedIsDone(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950014)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 950014, 950014)

	got, err := v.VerifySession(ctx, 950014, id, ptrIntV(20), ptrIntV(80))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusDone {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusDone)
	}
}

// TestVerifySession_S6_ReverificationFlipsCalculatedToDone: a re-verification
// flips a status -- DONE_CALCULATED -> DONE when a later call supplies a
// caller-typed start. This is also the concrete proof that status is recomputed on
// every write, not fixed at first-write.
func TestVerifySession_S6_ReverificationFlipsCalculatedToDone(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950015)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 950015, 950015)

	first, err := v.VerifySession(ctx, 950015, id, nil, ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession (first): %v", err)
	}
	if first.Status != charging.SessionStatusDoneCalculated {
		t.Fatalf("first Status = %q, want %q", first.Status, charging.SessionStatusDoneCalculated)
	}

	second, err := v.VerifySession(ctx, 950015, id, ptrIntV(45), ptrIntV(90))
	if err != nil {
		t.Fatalf("VerifySession (second): %v", err)
	}
	if second.Status != charging.SessionStatusDone {
		t.Errorf("second Status = %q, want %q (a caller-supplied start must flip DONE_CALCULATED to DONE)", second.Status, charging.SessionStatusDone)
	}
	if second.StartBatteryPct == nil || *second.StartBatteryPct != 45 {
		t.Errorf("second StartBatteryPct = %v, want 45 (the caller's own value, never the earlier derived 41)", second.StartBatteryPct)
	}
}

// TestVerifySession_S7_ClearingBothResetsToInProgress: clearing both percentages
// resets a DONE session to IN_PROGRESS.
func TestVerifySession_S7_ClearingBothResetsToInProgress(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950016)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSession(t, pool, 950016, 950016)

	if _, err := v.VerifySession(ctx, 950016, id, ptrIntV(20), ptrIntV(80)); err != nil {
		t.Fatalf("VerifySession (set): %v", err)
	}

	got, err := v.VerifySession(ctx, 950016, id, nil, nil)
	if err != nil {
		t.Fatalf("VerifySession (clear): %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil || got.EndBatteryPct != nil {
		t.Errorf("percentages not cleared: start=%v end=%v", got.StartBatteryPct, got.EndBatteryPct)
	}
}

// TestVerifySession_S8_DerivedOutOfRangeStaysInProgress: end only, energy_kwh
// present, derivation out of range -> IN_PROGRESS (truth table row 2,
// out-of-range sub-case -- a different path to the same status as S3). Arithmetic:
// 10 - 100/62*100 = -151.29 -> rounds to -151, outside [0,100].
func TestVerifySession_S8_DerivedOutOfRangeStaysInProgress(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 950017)
	ctx := context.Background()
	v := charging.NewSessionVerifier(pool)

	id := seedVerifierSessionEnergy(t, pool, 950017, 950017, ptrFloat64(100.0))

	got, err := v.VerifySession(ctx, 950017, id, nil, ptrIntV(10))
	if err != nil {
		t.Fatalf("VerifySession: %v", err)
	}
	if got.Status != charging.SessionStatusInProgress {
		t.Errorf("Status = %q, want %q", got.Status, charging.SessionStatusInProgress)
	}
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil (derivation out of range)", got.StartBatteryPct)
	}
}
