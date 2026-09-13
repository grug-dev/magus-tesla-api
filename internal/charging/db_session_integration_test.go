// Package charging_test — database-backed integration tests for SessionWriter
// (session_writer.go) and its MirrorSuperchargerSession query (db/query.sql).
// Re-keyed on tesla_id, not account_id (RM57-charging-rekey-supercharger-
// sessions-on-tesla-id, MAG-67): the table has no account_id column left, and
// MirrorSessions no longer takes a scoping argument — the mirror's own upsert
// key is now session_id alone, with no vehicle-scoped check in Go at all.
//
// There is no reader port exercising these fixtures — every assertion here
// reads rows back with direct SQL over the shared testPool. Assertions are
// against plain Go domain values only (charging.SessionMirror fields and raw
// SQL column values); pgtype NEVER appears in this file
// (internal/charging/AGENTS.md §Testing Notes).
//
// Test → Test Contract case mapping (design.md §"Test contract"):
//
//	T-1 TestMirrorSessions_UpsertsOnSessionIDAlone
//	T-2 TestMirrorSessions_SessionIDUniqueAcrossVehicles
//	    TestMirrorSessions_NewSessionInsertsTenColumns
//	    TestMirrorSessions_ReMirrorUnchanged_NoDuplicate_LeavesUpdatedAtUntouched
//	    TestMirrorSessions_SettledFeesRefreshed
//	    TestMirrorSessions_WriteOnceColumnsNotRefreshed
//	    TestMirrorSessions_TeslaIDRefreshed
//	    TestMirrorSessions_NeverTouchesVerifiedPercentage
//	    TestMirrorSessions_EmptySliceIsNoOp
//	    TestMirrorSessions_NullableFeeFieldsRoundTripAsNull
//	    TestMirrorSessions_TwoVehiclesAreIndependent
//	    TestConstraint_ProvenanceRequired
//	    TestConstraint_NoPercentagesAllowsNullSource
//	    TestConstraint_PercentageRange
//	    TestConstraint_ProvenanceEnum
//	    TestConstraint_SessionIDUniqueness
//	    TestConstraint_SiteLocationNameRequired_FeeColumnsNullable
//	    TestConstraint_NoStricterThanSource
package charging_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// mirrorGap is slept between two mirror passes (or a direct-SQL write and a mirror
// pass) whenever a test must observe updated_at strictly advance. 15ms comfortably
// clears the "≥10 ms" requirement and any server clock/driver rounding.
const mirrorGap = 15 * time.Millisecond

// --- session-specific test helpers ---

// cleanupChargingSuperchargerSessionsBySessionIDs registers a cleanup that deletes
// supercharger_sessions rows for the given session ids so a shared DB stays tidy
// across test runs. session_id is now the table's own global identity — the table
// carries no account_id to scope a cleanup by (design.md D1/D2).
func cleanupChargingSuperchargerSessionsBySessionIDs(t *testing.T, pool *pgxpool.Pool, sessionIDs ...int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = pool.Exec(ctx, "DELETE FROM charging.supercharger_sessions WHERE session_id = ANY($1)", sessionIDs)
	})
}

// countSuperchargerSessionsBySessionIDs returns how many of the given session ids
// have a stored row.
func countSuperchargerSessionsBySessionIDs(t *testing.T, pool *pgxpool.Pool, sessionIDs ...int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM charging.supercharger_sessions WHERE session_id = ANY($1)", sessionIDs,
	).Scan(&n); err != nil {
		t.Fatalf("counting supercharger_sessions rows: %v", err)
	}
	return n
}

// superchargerSessionRow is the subset of supercharger_sessions columns these tests read back
// directly (no Reader port exercises these fixtures). TeslaID is now a plain int64 — the
// column is NOT NULL (design.md D1). Every other nullable column stays a plain Go pointer
// (**T at Scan time), never pgtype — pgx v5 natively supports NULL-into-pointer-to-pointer
// scanning.
type superchargerSessionRow struct {
	VIN                 string
	TeslaID             int64
	ChargeStartDateTime time.Time
	ChargeStopDateTime  time.Time
	SiteLocationName    string
	EnergyKWh           *float64
	TotalCost           *float64
	Currency            *string
	IsPaid              *bool
	StartBatteryPct     *int
	EndBatteryPct       *int
	BatteryPctSource    *string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// fetchSuperchargerSession reads one supercharger_sessions row by its session_id — the
// table's own UNIQUE constraint (supercharger_sessions_session_id_unique). ok is false
// when no row exists for that id (not an error).
func fetchSuperchargerSession(t *testing.T, pool *pgxpool.Pool, sessionID int64) (row superchargerSessionRow, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT vin, tesla_id, charge_start_date_time, charge_stop_date_time,
		       site_location_name, energy_kwh, total_cost, currency, is_paid,
		       start_battery_pct, end_battery_pct, battery_pct_source,
		       created_at, updated_at
		FROM charging.supercharger_sessions
		WHERE session_id = $1`,
		sessionID,
	).Scan(&row.VIN, &row.TeslaID, &row.ChargeStartDateTime, &row.ChargeStopDateTime,
		&row.SiteLocationName, &row.EnergyKWh, &row.TotalCost, &row.Currency, &row.IsPaid,
		&row.StartBatteryPct, &row.EndBatteryPct, &row.BatteryPctSource,
		&row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return superchargerSessionRow{}, false
		}
		t.Fatalf("fetching supercharger_sessions row: %v", err)
	}
	return row, true
}

// assertPgErrorCode fails the test unless err is a *pgconn.PgError carrying the
// given SQLSTATE code (23514 check_violation, 23502 not_null_violation, 23505
// unique_violation — https://www.postgresql.org/docs/current/errcodes-appendix.html).
func assertPgErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error (SQLSTATE %s), got nil", code)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected *pgconn.PgError, got %T: %v", err, err)
	}
	if pgErr.Code != code {
		t.Errorf("expected SQLSTATE %s, got %q: %v", code, pgErr.Code, err)
	}
}

func ptrFloat64(v float64) *float64 { return &v }
func ptrBool(v bool) *bool          { return &v }

// baselineM1 is a fresh session for SessionID 920001 / tesla_id 920001, with every
// mirrored field populated. Used by the tests that need every field non-nil.
func baselineM1(teslaID int64) charging.SessionMirror {
	return charging.SessionMirror{
		VIN:                 "V920001",
		TeslaID:             teslaID,
		SessionID:           920001,
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 10, 40, 0, 0, time.UTC),
		SiteLocationName:    "Medellín, Colombia",
		EnergyKWh:           ptrFloat64(35.5),
		TotalCost:           ptrFloat64(48250.0),
		Currency:            ptrString("COP"),
		IsPaid:              ptrBool(false),
	}
}

// minSessionMirror is a minimal valid SessionMirror for sessionID/teslaID, all
// optional fee fields nil — used where the exact fee figures are irrelevant to what
// is being asserted.
func minSessionMirror(sessionID, teslaID int64) charging.SessionMirror {
	return charging.SessionMirror{
		VIN:                 "VTEST",
		TeslaID:             teslaID,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 5, 9, 30, 0, 0, time.UTC),
		SiteLocationName:    "Test Site",
	}
}

// --- Group B — MirrorSessions ---

// T-1: the mirror upserts on the session id alone. Re-calling with identical values
// leaves updated_at unchanged; calling again with a changed EnergyKWh stores it and
// advances updated_at.
func TestMirrorSessions_UpsertsOnSessionIDAlone(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(9001)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m := charging.SessionMirror{
		TeslaID:             111,
		SessionID:           sessionID,
		VIN:                 "VIN_A",
		SiteLocationName:    "Site A",
		ChargeStartDateTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 1, 10, 30, 0, 0, time.UTC),
		EnergyKWh:           ptrFloat64(30.0),
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, sessionID); n != 1 {
		t.Fatalf("expected 1 row for session %d, got %d", sessionID, n)
	}
	first, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row for session %d, found none", sessionID)
	}
	if first.TeslaID != 111 {
		t.Errorf("TeslaID: got %d, want 111", first.TeslaID)
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("MirrorSessions (re-mirror, unchanged): %v", err)
	}
	second, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row to survive unchanged re-mirror")
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("UpdatedAt: want unchanged %v, got %v", first.UpdatedAt, second.UpdatedAt)
	}

	time.Sleep(mirrorGap)
	m.EnergyKWh = ptrFloat64(31.0)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("MirrorSessions (energy change): %v", err)
	}
	third, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row to survive changed re-mirror")
	}
	if third.EnergyKWh == nil || *third.EnergyKWh != 31.0 {
		t.Errorf("EnergyKWh: got %v, want 31.0", third.EnergyKWh)
	}
	if !third.UpdatedAt.After(second.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly later than %v, got %v", second.UpdatedAt, third.UpdatedAt)
	}
}

// T-2: one session id can exist only once, whatever the vehicle. Before this change
// the same call inserted a second row, one per account; this test is the behaviour
// change design.md D2 introduces.
func TestMirrorSessions_SessionIDUniqueAcrossVehicles(t *testing.T) {
	pool := newTestPool(t)
	const sessionID = int64(9001)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, sessionID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	first := minSessionMirror(sessionID, 111)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{first}); err != nil {
		t.Fatalf("MirrorSessions (tesla_id 111): %v", err)
	}

	reassigned := minSessionMirror(sessionID, 222)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{reassigned}); err != nil {
		t.Fatalf("MirrorSessions (tesla_id 222): %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, sessionID); n != 1 {
		t.Fatalf("expected exactly 1 row for session %d, got %d", sessionID, n)
	}
	row, ok := fetchSuperchargerSession(t, pool, sessionID)
	if !ok {
		t.Fatalf("expected row for session %d, found none", sessionID)
	}
	if row.TeslaID != 222 {
		t.Errorf("TeslaID: want 222 (the upsert conflicted on session_id and refreshed it), got %d", row.TeslaID)
	}
}

// B1: a new session is inserted with exactly the ten mirrored columns; all three
// percentage/provenance columns are NULL; created_at and updated_at are both
// non-zero.
func TestMirrorSessions_NewSessionInsertsTenColumns(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, m1.SessionID); n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
	row, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row for session %d, found none", m1.SessionID)
	}

	if row.VIN != m1.VIN {
		t.Errorf("VIN: got %q, want %q", row.VIN, m1.VIN)
	}
	if row.TeslaID != m1.TeslaID {
		t.Errorf("TeslaID: got %v, want %v", row.TeslaID, m1.TeslaID)
	}
	if !row.ChargeStartDateTime.Equal(m1.ChargeStartDateTime) {
		t.Errorf("ChargeStartDateTime: got %v, want %v", row.ChargeStartDateTime, m1.ChargeStartDateTime)
	}
	if !row.ChargeStopDateTime.Equal(m1.ChargeStopDateTime) {
		t.Errorf("ChargeStopDateTime: got %v, want %v", row.ChargeStopDateTime, m1.ChargeStopDateTime)
	}
	if row.SiteLocationName != m1.SiteLocationName {
		t.Errorf("SiteLocationName: got %q, want %q (non-ASCII must round-trip)", row.SiteLocationName, m1.SiteLocationName)
	}
	if row.EnergyKWh == nil || *row.EnergyKWh != *m1.EnergyKWh {
		t.Errorf("EnergyKWh: got %v, want %v", row.EnergyKWh, *m1.EnergyKWh)
	}
	if row.TotalCost == nil || *row.TotalCost != *m1.TotalCost {
		t.Errorf("TotalCost: got %v, want %v", row.TotalCost, *m1.TotalCost)
	}
	if row.Currency == nil || *row.Currency != *m1.Currency {
		t.Errorf("Currency: got %v, want %v", row.Currency, *m1.Currency)
	}
	if row.IsPaid == nil || *row.IsPaid != *m1.IsPaid {
		t.Errorf("IsPaid: got %v, want %v (false)", row.IsPaid, *m1.IsPaid)
	}

	if row.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct: want nil, got %v", *row.StartBatteryPct)
	}
	if row.EndBatteryPct != nil {
		t.Errorf("EndBatteryPct: want nil, got %v", *row.EndBatteryPct)
	}
	if row.BatteryPctSource != nil {
		t.Errorf("BatteryPctSource: want nil, got %v", *row.BatteryPctSource)
	}

	if row.CreatedAt.IsZero() {
		t.Errorf("CreatedAt: want non-zero")
	}
	if row.UpdatedAt.IsZero() {
		t.Errorf("UpdatedAt: want non-zero")
	}
}

// B2: re-mirroring an unchanged session does not duplicate it, and LEAVES
// updated_at untouched (RM44-charging-add-change-detecting-mirror, MAG-48).
func TestMirrorSessions_ReMirrorUnchanged_NoDuplicate_LeavesUpdatedAtUntouched(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	first, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)

	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (second, unchanged): %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, m1.SessionID); n != 1 {
		t.Fatalf("expected still 1 row, got %d", n)
	}
	second, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}

	if second.SiteLocationName != first.SiteLocationName ||
		!second.ChargeStartDateTime.Equal(first.ChargeStartDateTime) ||
		!second.ChargeStopDateTime.Equal(first.ChargeStopDateTime) ||
		*second.EnergyKWh != *first.EnergyKWh || *second.TotalCost != *first.TotalCost ||
		*second.Currency != *first.Currency || *second.IsPaid != *first.IsPaid {
		t.Errorf("mirrored values changed on an unchanged re-mirror: first=%+v second=%+v", first, second)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want unchanged %v", second.CreatedAt, first.CreatedAt)
	}
	if !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("UpdatedAt: want unchanged %v, got %v — an unchanged re-mirror must not advance updated_at", first.UpdatedAt, second.UpdatedAt)
	}
}

// B3: settled fees (energy_kwh, total_cost, currency, is_paid) are refreshed on a
// re-mirror; created_at is unchanged and updated_at advances.
func TestMirrorSessions_SettledFeesRefreshed(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	before, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)

	settled := m1
	settled.EnergyKWh = ptrFloat64(36.1)
	settled.TotalCost = ptrFloat64(49100.0)
	settled.Currency = ptrString("COP")
	settled.IsPaid = ptrBool(true)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{settled}); err != nil {
		t.Fatalf("MirrorSessions (settled fees): %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, m1.SessionID); n != 1 {
		t.Fatalf("expected still 1 row, got %d", n)
	}
	after, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if after.EnergyKWh == nil || *after.EnergyKWh != 36.1 {
		t.Errorf("EnergyKWh: got %v, want 36.1", after.EnergyKWh)
	}
	if after.TotalCost == nil || *after.TotalCost != 49100.0 {
		t.Errorf("TotalCost: got %v, want 49100", after.TotalCost)
	}
	if after.IsPaid == nil || !*after.IsPaid {
		t.Errorf("IsPaid: got %v, want true", after.IsPaid)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want unchanged %v", after.CreatedAt, before.CreatedAt)
	}
	if !after.UpdatedAt.After(before.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly later than %v, got %v", before.UpdatedAt, after.UpdatedAt)
	}
}

// B4: write-once mirrored columns (site_location_name, charge_start_date_time,
// charge_stop_date_time) are NOT refreshed on a re-mirror — they stay absent from
// the conflict clause because they are absent from telemetry's (design.md D1's rule).
func TestMirrorSessions_WriteOnceColumnsNotRefreshed(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}

	changed := m1
	changed.SiteLocationName = "Bogotá, Colombia"
	changed.ChargeStartDateTime = m1.ChargeStartDateTime.Add(time.Hour)
	changed.ChargeStopDateTime = m1.ChargeStopDateTime.Add(time.Hour)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{changed}); err != nil {
		t.Fatalf("MirrorSessions (attempted write-once change): %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, m1.SessionID); n != 1 {
		t.Fatalf("expected still 1 row, got %d", n)
	}
	row, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if row.SiteLocationName != m1.SiteLocationName {
		t.Errorf("SiteLocationName: got %q, want unchanged original %q", row.SiteLocationName, m1.SiteLocationName)
	}
	if !row.ChargeStartDateTime.Equal(m1.ChargeStartDateTime) {
		t.Errorf("ChargeStartDateTime: got %v, want unchanged original %v", row.ChargeStartDateTime, m1.ChargeStartDateTime)
	}
	if !row.ChargeStopDateTime.Equal(m1.ChargeStopDateTime) {
		t.Errorf("ChargeStopDateTime: got %v, want unchanged original %v", row.ChargeStopDateTime, m1.ChargeStopDateTime)
	}
}

// B5: tesla_id is refreshed to a different vehicle; vin is unaffected; created_at
// stays unchanged and updated_at advances. Unlike before this change, tesla_id can
// no longer be reassigned to NULL — the column is NOT NULL (design.md D1) and
// SessionMirror.TeslaID is a plain int64 with no nil state left to express.
func TestMirrorSessions_TeslaIDRefreshed(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}
	first, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after first mirror")
	}

	time.Sleep(mirrorGap)
	reassigned := m1
	reassigned.TeslaID = 920099
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{reassigned}); err != nil {
		t.Fatalf("MirrorSessions (reassigned tesla_id): %v", err)
	}
	second, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after second mirror")
	}
	if second.TeslaID != 920099 {
		t.Errorf("TeslaID after reassignment: got %v, want 920099", second.TeslaID)
	}
	if second.VIN != m1.VIN {
		t.Errorf("VIN: got %q, want unchanged %q", second.VIN, m1.VIN)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt: got %v, want unchanged %v", second.CreatedAt, first.CreatedAt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt: want strictly later than %v, got %v", first.UpdatedAt, second.UpdatedAt)
	}
}

// B6: the sync never touches a verified percentage, even across a re-mirror that
// changes tesla_id and the fee figures in the same call.
func TestMirrorSessions_NeverTouchesVerifiedPercentage(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920001)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	m1 := baselineM1(920001)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (first): %v", err)
	}

	tag, err := pool.Exec(ctx, `
		UPDATE charging.supercharger_sessions
		SET start_battery_pct = 41, end_battery_pct = 88, battery_pct_source = 'user_verified'
		WHERE session_id = $1`,
		m1.SessionID)
	if err != nil {
		t.Fatalf("direct-SQL set verified percentages: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("direct-SQL set verified percentages: expected 1 row affected, got %d", tag.RowsAffected())
	}

	// Re-mirror #1: unchanged.
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{m1}); err != nil {
		t.Fatalf("MirrorSessions (unchanged re-mirror): %v", err)
	}
	// Re-mirror #2: different TeslaID AND different fee figures in the same call.
	changed := m1
	changed.TeslaID = 920098
	changed.EnergyKWh = ptrFloat64(99.9)
	changed.TotalCost = ptrFloat64(1.0)
	changed.IsPaid = ptrBool(true)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{changed}); err != nil {
		t.Fatalf("MirrorSessions (changed re-mirror): %v", err)
	}

	row, ok := fetchSuperchargerSession(t, pool, m1.SessionID)
	if !ok {
		t.Fatalf("expected row after both re-mirrors")
	}
	if row.StartBatteryPct == nil || *row.StartBatteryPct != 41 {
		t.Errorf("StartBatteryPct: want 41 (untouched by sync), got %v", row.StartBatteryPct)
	}
	if row.EndBatteryPct == nil || *row.EndBatteryPct != 88 {
		t.Errorf("EndBatteryPct: want 88 (untouched by sync), got %v", row.EndBatteryPct)
	}
	if row.BatteryPctSource == nil || *row.BatteryPctSource != "user_verified" {
		t.Errorf("BatteryPctSource: want user_verified (untouched by sync), got %v", row.BatteryPctSource)
	}
}

// B8: an empty (nil or zero-length) slice is a successful no-op — no error, no
// rows written, and no existing row altered.
func TestMirrorSessions_EmptySliceIsNoOp(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920012)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	// Seed one pre-existing row so "no existing row altered" is a real assertion.
	existing := minSessionMirror(920012, 920012)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{existing}); err != nil {
		t.Fatalf("MirrorSessions (seed): %v", err)
	}
	before, ok := fetchSuperchargerSession(t, pool, existing.SessionID)
	if !ok {
		t.Fatalf("expected seeded row")
	}

	if err := w.MirrorSessions(ctx, nil); err != nil {
		t.Errorf("MirrorSessions(nil): expected nil error, got %v", err)
	}
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{}); err != nil {
		t.Errorf("MirrorSessions(empty slice): expected nil error, got %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, existing.SessionID); n != 1 {
		t.Errorf("expected still 1 row, got %d", n)
	}
	after, ok := fetchSuperchargerSession(t, pool, existing.SessionID)
	if !ok {
		t.Fatalf("expected seeded row to survive")
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt: expected unchanged (%v), got %v — an empty call must not touch existing rows", before.UpdatedAt, after.UpdatedAt)
	}
}

// B9: nullable fee fields (EnergyKWh, TotalCost, Currency, IsPaid all nil) round-trip
// as NULL, and site_location_name is still NOT NULL.
func TestMirrorSessions_NullableFeeFieldsRoundTripAsNull(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920013)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	noFees := minSessionMirror(920013, 920013)
	// minSessionMirror already leaves EnergyKWh/TotalCost/Currency/IsPaid nil.
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{noFees}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}

	if n := countSuperchargerSessionsBySessionIDs(t, pool, noFees.SessionID); n != 1 {
		t.Fatalf("expected 1 row, got %d", n)
	}
	row, ok := fetchSuperchargerSession(t, pool, noFees.SessionID)
	if !ok {
		t.Fatalf("expected row")
	}
	if row.EnergyKWh != nil {
		t.Errorf("EnergyKWh: want nil, got %v", *row.EnergyKWh)
	}
	if row.TotalCost != nil {
		t.Errorf("TotalCost: want nil, got %v", *row.TotalCost)
	}
	if row.Currency != nil {
		t.Errorf("Currency: want nil, got %v", *row.Currency)
	}
	if row.IsPaid != nil {
		t.Errorf("IsPaid: want nil, got %v", *row.IsPaid)
	}
	if row.SiteLocationName == "" {
		t.Errorf("SiteLocationName: want non-empty (NOT NULL column), got empty")
	}
}

// B11: two vehicles are independent — re-mirroring only the first leaves the
// second's updated_at byte-for-byte unchanged.
func TestMirrorSessions_TwoVehiclesAreIndependent(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920030, 920031)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	v1 := minSessionMirror(920030, 920030)
	v2 := minSessionMirror(920031, 920031)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{v1, v2}); err != nil {
		t.Fatalf("MirrorSessions (both vehicles): %v", err)
	}
	if n := countSuperchargerSessionsBySessionIDs(t, pool, v1.SessionID, v2.SessionID); n != 2 {
		t.Fatalf("expected 2 rows, got %d", n)
	}
	v2Before, ok := fetchSuperchargerSession(t, pool, v2.SessionID)
	if !ok {
		t.Fatalf("expected v2's row")
	}

	time.Sleep(mirrorGap)
	if err := w.MirrorSessions(ctx, []charging.SessionMirror{v1}); err != nil {
		t.Fatalf("MirrorSessions (only v1): %v", err)
	}

	v2After, ok := fetchSuperchargerSession(t, pool, v2.SessionID)
	if !ok {
		t.Fatalf("expected v2's row to survive")
	}
	if !v2After.UpdatedAt.Equal(v2Before.UpdatedAt) {
		t.Errorf("v2 UpdatedAt: want byte-for-byte unchanged (%v), got %v — v2 was not in the second call", v2Before.UpdatedAt, v2After.UpdatedAt)
	}
}

// --- Group C — constraints (direct SQL INSERTs; no Go writer exercises these
// deliberately-invalid shapes) ---

// C1: a recorded percentage requires battery_pct_source to be set —
// supercharger_sessions_pct_source_required.
func TestConstraint_ProvenanceRequired(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920040, 920041, 920042)
	ctx := context.Background()

	cases := []struct {
		name string
		sql  string
	}{
		{
			"start_battery_pct set, source NULL",
			`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct)
			 VALUES ($1, 'VC1', 920040, now(), now(), 'Test Site', 50)`,
		},
		{
			"end_battery_pct set, source NULL",
			`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, end_battery_pct)
			 VALUES ($1, 'VC1', 920041, now(), now(), 'Test Site', 50)`,
		},
		{
			"both set, source NULL",
			`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct, end_battery_pct)
			 VALUES ($1, 'VC1', 920042, now(), now(), 'Test Site', 50, 80)`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.sql, int64(920040))
			assertPgErrorCode(t, err, "23514")
		})
	}
}

// C2: a row with no percentages may have a NULL source — the complement of C1.
func TestConstraint_NoPercentagesAllowsNullSource(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920043)
	ctx := context.Background()

	_, err := pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name)
		 VALUES ($1, 'VC2', 920043, now(), now(), 'Test Site')`,
		int64(920043))
	if err != nil {
		t.Fatalf("insert with no percentages and NULL source: expected success, got %v", err)
	}
}

// C3: start_battery_pct outside [0, 100] is rejected; 0 and 100 succeed (source set
// in every case, so only the range CHECK is under test).
func TestConstraint_PercentageRange(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920044, 920045, 920046, 920047)
	ctx := context.Background()
	const teslaID = int64(920044)

	rejected := []struct {
		name string
		sql  string
	}{
		{"101", `INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct, battery_pct_source) VALUES ($1, 'VC3', 920044, now(), now(), 'Test Site', 101, 'user_verified')`},
		{"-1", `INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct, battery_pct_source) VALUES ($1, 'VC3', 920045, now(), now(), 'Test Site', -1, 'user_verified')`},
	}
	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, tc.sql, teslaID)
			assertPgErrorCode(t, err, "23514")
		})
	}

	accepted := []struct {
		name string
		sql  string
	}{
		{"0", `INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct, battery_pct_source) VALUES ($1, 'VC3', 920046, now(), now(), 'Test Site', 0, 'user_verified')`},
		{"100", `INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, start_battery_pct, battery_pct_source) VALUES ($1, 'VC3', 920047, now(), now(), 'Test Site', 100, 'user_verified')`},
	}
	for _, tc := range accepted {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, tc.sql, teslaID); err != nil {
				t.Errorf("insert with start_battery_pct=%s: expected success, got %v", tc.name, err)
			}
		})
	}
}

// C4: battery_pct_source is restricted to the enum ('user_verified', 'polled');
// anything else is rejected.
func TestConstraint_ProvenanceEnum(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920048, 920049, 920050)
	ctx := context.Background()
	const teslaID = int64(920048)

	_, err := pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, battery_pct_source)
		 VALUES ($1, 'VC4', 920048, now(), now(), 'Test Site', 'estimated')`,
		teslaID)
	assertPgErrorCode(t, err, "23514")

	accepted := []struct {
		source string
		sessID int64
	}{
		{"user_verified", 920049},
		{"polled", 920050},
	}
	for _, tc := range accepted {
		t.Run(tc.source, func(t *testing.T) {
			_, err := pool.Exec(ctx,
				`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, battery_pct_source)
				 VALUES ($1, 'VC4', $2, now(), now(), 'Test Site', $3)`,
				teslaID, tc.sessID, tc.source)
			if err != nil {
				t.Errorf("insert with battery_pct_source=%q: expected success, got %v", tc.source, err)
			}
		})
	}
}

// C5: session_id uniqueness is global, and enforced —
// supercharger_sessions_session_id_unique. Replaces the old account-scoped
// uniqueness test: the table has no account_id left to scope by (design.md D2).
func TestConstraint_SessionIDUniqueness(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920051)
	ctx := context.Background()

	insert := `INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name)
	           VALUES ($1, 'VC5', 920051, now(), now(), 'Test Site')`
	if _, err := pool.Exec(ctx, insert, int64(920051)); err != nil {
		t.Fatalf("first insert: expected success, got %v", err)
	}
	// A second insert for the SAME session_id under a DIFFERENT vehicle must still
	// collide — the constraint no longer has a vehicle column to distinguish on.
	_, err := pool.Exec(ctx, insert, int64(920052))
	assertPgErrorCode(t, err, "23505")
}

// C6: site_location_name is NOT NULL; the four fee columns are nullable.
func TestConstraint_SiteLocationNameRequired_FeeColumnsNullable(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920052, 920053)
	ctx := context.Background()
	const teslaID = int64(920052)

	_, err := pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time)
		 VALUES ($1, 'VC6', 920052, now(), now())`,
		teslaID)
	assertPgErrorCode(t, err, "23502")

	_, err = pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name)
		 VALUES ($1, 'VC6', 920053, now(), now(), 'Test Site')`,
		teslaID)
	if err != nil {
		t.Errorf("insert omitting energy_kwh/total_cost/currency/is_paid: expected success, got %v", err)
	}
}

// C7: no constraint here is stricter than its source — a reversed time window and
// a negative total_cost both succeed, pinning the table's own deliberate omissions.
func TestConstraint_NoStricterThanSource(t *testing.T) {
	pool := newTestPool(t)
	cleanupChargingSuperchargerSessionsBySessionIDs(t, pool, 920054, 920055)
	ctx := context.Background()
	const teslaID = int64(920054)

	_, err := pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name)
		 VALUES ($1, 'VC7', 920054, '2026-08-05T10:00:00Z', '2026-08-05T09:00:00Z', 'Test Site')`,
		teslaID)
	if err != nil {
		t.Errorf("insert with charge_stop_date_time before charge_start_date_time: expected success, got %v", err)
	}

	_, err = pool.Exec(ctx,
		`INSERT INTO charging.supercharger_sessions (tesla_id, vin, session_id, charge_start_date_time, charge_stop_date_time, site_location_name, total_cost)
		 VALUES ($1, 'VC7', 920055, now(), now(), 'Test Site', -100.0)`,
		teslaID)
	if err != nil {
		t.Errorf("insert with negative total_cost: expected success, got %v", err)
	}
}
