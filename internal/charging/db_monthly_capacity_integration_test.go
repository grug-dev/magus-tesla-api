// Package charging_test — database-backed integration tests for
// charging.monthly_effective_capacity (RM52-charging-add-monthly-effective-capacity,
// MAG-32), covering design.md's Test Contract Group B (B1-B2, schema/constraints)
// and Group C (C1-C13, the MonthlyCapacityCalculator job and both updated
// packCapacityKWh seams), tasks.md task 3.2.
//
// Fixtures for Group C are seeded through the real public ports
// (charging.NewWriter, charging.NewSessionWriter, charging.NewSessionVerifier)
// wherever those ports can produce the needed combination -- e.g. an
// energy_source=USER row is just Writer.Create with EnergyAddedKWh set
// directly, and a status=DONE_CALCULATED session is SessionVerifier.VerifySession
// deriving a start percentage. Design.md's own Group C preamble mentions seeding
// via `chargingdb.New(pool)` for shapes the public port cannot produce, but this
// module's AGENTS.md §Allowed Imports states plainly that chargingdb "is
// module-private by convention; no other module imports it, and no _test.go file
// does either" -- a rule every other integration test file in this package
// already honors by seeding through the public ports or, for schema/constraint
// checks, plain SQL. Every fixture below needed by this file's Test Contract
// cases IS reachable through the public ports, so this file follows AGENTS.md and
// never imports chargingdb. See this file's final report for the explicit
// design.md-vs-AGENTS.md note.
//
// monthly_effective_capacity has NO account_id (RD5 -- it describes a battery
// pack, not user data) and is keyed by tesla_id alone, so every test below uses
// a tesla_id reserved to this file, in the 990001-990014/990101-990102 range,
// disjoint from every other integration test file's ranges (checked by grep
// before this file was written) -- this is what keeps one test's
// monthly_effective_capacity row invisible to another test's assertions even
// though the table itself is shared, unscoped, package-wide state.
//
// Assertions read the new table back with direct SQL into plain Go fields
// (never chargingdb.MonthlyEffectiveCapacity, which is all pgtype) and, for
// Group C, through charging.Entry/charging.Session's own exported fields.
// pgtype NEVER appears in this file (internal/charging/AGENTS.md §Testing Notes).
//
// Test -> Test Contract case mapping:
//
//	B1  TestMonthlyEffectiveCapacity_PeriodMustBeMonthStart
//	B2  TestMonthlyEffectiveCapacity_UniqueTeslaIDPeriod
//	C1  TestCalculate_C1_EstimatedEntriesExcluded
//	C2  TestCalculate_C2_DoneCalculatedSessionsExcluded
//	C3  TestCalculate_C3_InProgressSessionsExcluded
//	C4  TestCalculate_C4_TwoValidRowsBelowMinSamples
//	C5  TestCalculate_C5_EvenValidCountAveragesMiddleTwo
//	C6  TestCalculate_C6_PoolsAcrossAccounts
//	C7  TestCalculate_C7_NullTeslaIDSessionSkipped
//	C8  TestPackCapacityKWh_C8_NoMeasuredRowUsesDefault
//	C9  TestPackCapacityKWh_C9_ThinCurrentMonthSkipsToEarlierMeasured
//	C10 TestPackCapacityKWh_C10_UnchangedUntilFirstMonthComputed
//	C11 TestVerifySession_C11_NullTeslaIDShortCircuitsToDefault
//	C12 TestCalculate_C12_UpsertIsIdempotent
//	C13 TestCalculate_C13_CandidateCountExistsForAllGated
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- monthly-capacity-specific test helpers ---

// monthlyEffectiveCapacityRow is the subset of monthly_effective_capacity
// columns these tests read back directly. EffectiveCapacityKWh is a plain Go
// *float64 (pgx v5 supports NULL-into-pointer-to-pointer scanning natively) --
// never pgtype.
type monthlyEffectiveCapacityRow struct {
	EffectiveCapacityKWh *float64
	CandidateCount       int
	SampleCount          int
}

// fetchMonthlyEffectiveCapacity reads one monthly_effective_capacity row by its
// (teslaID, period) key -- the table's own UNIQUE (tesla_id, effective_period)
// constraint. ok is false when no row exists for that key (not an error).
func fetchMonthlyEffectiveCapacity(t *testing.T, pool *pgxpool.Pool, teslaID int64, period time.Time) (row monthlyEffectiveCapacityRow, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `
		SELECT effective_capacity_kwh, candidate_count, sample_count
		  FROM charging.monthly_effective_capacity
		 WHERE tesla_id = $1 AND effective_period = $2`,
		teslaID, period,
	).Scan(&row.EffectiveCapacityKWh, &row.CandidateCount, &row.SampleCount)
	if err != nil {
		return monthlyEffectiveCapacityRow{}, false
	}
	return row, true
}

// countMonthlyEffectiveCapacityRows returns how many monthly_effective_capacity
// rows exist for one tesla_id, across every period.
func countMonthlyEffectiveCapacityRows(t *testing.T, pool *pgxpool.Pool, teslaID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM charging.monthly_effective_capacity WHERE tesla_id = $1", teslaID,
	).Scan(&n); err != nil {
		t.Fatalf("counting monthly_effective_capacity rows: %v", err)
	}
	return n
}

// cleanupMonthlyEffectiveCapacity registers a cleanup that deletes
// monthly_effective_capacity rows for the given tesla_ids, so the shared,
// account-less table stays tidy across test runs. Called BEFORE seeding too,
// in every test below, so a re-run against a managed (non-disposable) database
// starts from a clean slate for these reserved tesla_ids.
func cleanupMonthlyEffectiveCapacity(t *testing.T, pool *pgxpool.Pool, teslaIDs ...int64) {
	t.Helper()
	del := func() {
		ctx := context.Background()
		for _, id := range teslaIDs {
			_, _ = pool.Exec(ctx, "DELETE FROM charging.monthly_effective_capacity WHERE tesla_id = $1", id)
		}
	}
	del()
	t.Cleanup(del)
}

// monthlyEntryFixture builds a USER-provenance manual_charge_entries Entry
// whose InferredCapacityKWhCalc equals exactly capacityKWh: energy is set
// directly (not derived) to capacityKWh*deltaPct/100, so
// energy_added_kwh / ((end-start)/100) == capacityKWh. Reused across every
// Group C case that needs a manual entry with a controlled implied capacity.
func monthlyEntryFixture(accountID uuid.UUID, teslaID int64, chargedOn time.Time, startPct int, capacityKWh float64, deltaPct int) charging.Entry {
	e := minEntry(accountID, teslaID)
	e.ChargedOn = chargedOn
	energy := capacityKWh * float64(deltaPct) / 100
	e.EnergyAddedKWh = ptrFloat64(energy)
	e.StartBatteryPct = ptrInt(startPct)
	e.EndBatteryPct = ptrInt(startPct + deltaPct)
	return e
}

// floatsAlmostEqual compares two plain (non-pointer) float64 values with a
// tight tolerance, for the derived-energy assertions below (Group C's fixture
// values are chosen to round to an exact 2-decimal figure, so this is mostly
// guarding against binary floating-point representation, not real tolerance).
func floatsAlmostEqual(got, want float64) bool {
	d := got - want
	if d < 0 {
		d = -d
	}
	return d < 1e-6
}

// --- Group B: schema and constraints (direct SQL) ---

// TestMonthlyEffectiveCapacity_PeriodMustBeMonthStart implements design.md
// Test Contract B1: monthly_effective_capacity_period_is_month_start rejects a
// non-1st-of-month date, SQLSTATE 23514 (not message text).
func TestMonthlyEffectiveCapacity_PeriodMustBeMonthStart(t *testing.T) {
	pool := newTestPool(t)
	teslaID := int64(990101)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	_, err := pool.Exec(context.Background(), `
		INSERT INTO charging.monthly_effective_capacity (tesla_id, effective_period, sample_count)
		VALUES ($1, '2026-08-15', 0)`,
		teslaID,
	)
	assertPgErrorCode(t, err, "23514")
}

// TestMonthlyEffectiveCapacity_UniqueTeslaIDPeriod implements design.md Test
// Contract B2: the UNIQUE (tesla_id, effective_period) constraint exists and
// is the upsert's own conflict target -- a second plain INSERT (no ON
// CONFLICT) with the same key errors SQLSTATE 23505 (not message text).
func TestMonthlyEffectiveCapacity_UniqueTeslaIDPeriod(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	teslaID := int64(990102)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	insert := func() error {
		_, err := pool.Exec(ctx, `
			INSERT INTO charging.monthly_effective_capacity (tesla_id, effective_period, sample_count)
			VALUES ($1, '2026-08-01', 0)`,
			teslaID,
		)
		return err
	}
	if err := insert(); err != nil {
		t.Fatalf("first insert: unexpected error: %v", err)
	}
	assertPgErrorCode(t, insert(), "23505")
}

// --- Group C: the job and both updated seams (through the real ports) ---

// TestCalculate_C1_EstimatedEntriesExcluded implements design.md Test
// Contract C1: an ESTIMATED manual_charge_entries row (energy derived from the
// 62.0 constant) is excluded (RD2) -- its own energy was itself derived by
// dividing by packCapacityKWh, so counting it would feed the constant back
// into itself.
func TestCalculate_C1_EstimatedEntriesExcluded(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990001)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	w := charging.NewWriter(pool)
	e := minEntry(accountID, teslaID)
	e.ChargedOn = time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	e.EnergyAddedKWh = nil // derived on write -> EnergySourceEstimated
	e.StartBatteryPct = ptrInt(10)
	e.EndBatteryPct = ptrInt(30) // delta 20, >= minDeltaPct
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C1 setup: Create: %v", err)
	}
	if created.EnergySource != charging.EnergySourceEstimated {
		t.Fatalf("C1 setup precondition: EnergySource = %v, want ESTIMATED", created.EnergySource)
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C1: Calculate: %v", err)
	}

	if n := countMonthlyEffectiveCapacityRows(t, pool, teslaID); n != 0 {
		t.Errorf("C1: expected no monthly_effective_capacity row for tesla_id %d, got %d", teslaID, n)
	}
}

// TestCalculate_C2_DoneCalculatedSessionsExcluded implements design.md Test
// Contract C2: a DONE_CALCULATED supercharger_sessions row is excluded (RD2)
// -- its start_battery_pct was itself derived via derivedStartBatteryPct
// dividing by 62.0, so its inferred_capacity_kwh_calc is exactly 62.0 by
// construction, not a real measurement.
func TestCalculate_C2_DoneCalculatedSessionsExcluded(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990002)
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	sw := charging.NewSessionWriter(pool)
	sv := charging.NewSessionVerifier(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VC02CAP",
		TeslaID:             ptrInt64(teslaID),
		SessionID:           teslaID,
		ChargeStartDateTime: time.Date(2026, 8, 5, 11, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC),
		SiteLocationName:    "C2 Site",
		EnergyKWh:           ptrFloat64(31.0),
	}
	if err := sw.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C2 setup: MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountID, teslaID)
	// start nil, end 100 -> derives using 62.0: 100 - 31.0/62.0*100 = 50 (in range).
	verified, err := sv.VerifySession(ctx, accountID, id, nil, ptrIntV(100))
	if err != nil {
		t.Fatalf("C2 setup: VerifySession: %v", err)
	}
	if verified.Status != charging.SessionStatusDoneCalculated {
		t.Fatalf("C2 setup precondition: Status = %v, want DONE_CALCULATED", verified.Status)
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C2: Calculate: %v", err)
	}

	if n := countMonthlyEffectiveCapacityRows(t, pool, teslaID); n != 0 {
		t.Errorf("C2: expected no monthly_effective_capacity row for tesla_id %d, got %d", teslaID, n)
	}
}

// TestCalculate_C3_InProgressSessionsExcluded implements design.md Test
// Contract C3: an IN_PROGRESS supercharger_sessions row is excluded -- it has
// no complete percentage pair, so inferred_capacity_kwh_calc is already NULL
// and the query's own WHERE drops it.
func TestCalculate_C3_InProgressSessionsExcluded(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990003)
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	sw := charging.NewSessionWriter(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VC03CAP",
		TeslaID:             ptrInt64(teslaID),
		SessionID:           teslaID,
		ChargeStartDateTime: time.Date(2026, 8, 6, 11, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		SiteLocationName:    "C3 Site",
		EnergyKWh:           ptrFloat64(30.0),
	}
	if err := sw.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C3 setup: MirrorSessions: %v", err)
	}
	// No VerifySession call -- status stays the column DEFAULT, IN_PROGRESS.

	calc := charging.NewMonthlyCapacityCalculator(pool)
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C3: Calculate: %v", err)
	}

	if n := countMonthlyEffectiveCapacityRows(t, pool, teslaID); n != 0 {
		t.Errorf("C3: expected no monthly_effective_capacity row for tesla_id %d, got %d", teslaID, n)
	}
}

// TestCalculate_C4_TwoValidRowsBelowMinSamples implements design.md Test
// Contract C4: exactly 2 valid rows -> NULL capacity, sample_count = 2 (RD4),
// through the real job, not just the pure function (A1).
func TestCalculate_C4_TwoValidRowsBelowMinSamples(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990004)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)
	for i, capacity := range []float64{60.0, 64.0} {
		e := monthlyEntryFixture(accountID, teslaID, period.AddDate(0, 0, 4+i), 10, capacity, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C4 setup entry %d: %v", i, err)
		}
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C4: Calculate: %v", err)
	}

	row, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("C4: expected a row")
	}
	if row.EffectiveCapacityKWh != nil {
		t.Errorf("C4: EffectiveCapacityKWh = %v, want nil", *row.EffectiveCapacityKWh)
	}
	if row.CandidateCount != 2 {
		t.Errorf("C4: CandidateCount = %d, want 2", row.CandidateCount)
	}
	if row.SampleCount != 2 {
		t.Errorf("C4: SampleCount = %d, want 2", row.SampleCount)
	}
}

// TestCalculate_C5_EvenValidCountAveragesMiddleTwo implements design.md Test
// Contract C5: even valid-sample count -> average of the two middle values
// (RD3), end-to-end.
func TestCalculate_C5_EvenValidCountAveragesMiddleTwo(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990005)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)
	for i, capacity := range []float64{58.0, 60.0, 64.0, 70.0} {
		e := monthlyEntryFixture(accountID, teslaID, period.AddDate(0, 0, 4+i), 10, capacity, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C5 setup entry %d: %v", i, err)
		}
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C5: Calculate: %v", err)
	}

	row, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("C5: expected a row")
	}
	if row.EffectiveCapacityKWh == nil || !floatsAlmostEqual(*row.EffectiveCapacityKWh, 62.0) {
		t.Errorf("C5: EffectiveCapacityKWh = %v, want 62.0", row.EffectiveCapacityKWh)
	}
	if row.CandidateCount != 4 {
		t.Errorf("C5: CandidateCount = %d, want 4", row.CandidateCount)
	}
	if row.SampleCount != 4 {
		t.Errorf("C5: SampleCount = %d, want 4", row.SampleCount)
	}
}

// TestCalculate_C6_PoolsAcrossAccounts implements design.md Test Contract C6:
// two accounts with the same tesla_id are pooled into one row (RD5) -- no
// account_id on the table, GROUP BY tesla_id (in Go) pools automatically.
func TestCalculate_C6_PoolsAcrossAccounts(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountA := uuid.New()
	accountB := uuid.New()
	teslaID := int64(990006)
	cleanupAccount(t, pool, accountA)
	cleanupChargingSuperchargerSessions(t, pool, accountB)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)
	for i, capacity := range []float64{60.0, 64.0} {
		e := monthlyEntryFixture(accountA, teslaID, period.AddDate(0, 0, 4+i), 10, capacity, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C6 setup entry %d: %v", i, err)
		}
	}

	sw := charging.NewSessionWriter(pool)
	sv := charging.NewSessionVerifier(pool)
	m := charging.SessionMirror{
		AccountID:           accountB,
		VIN:                 "VC06CAP",
		TeslaID:             ptrInt64(teslaID),
		SessionID:           teslaID,
		ChargeStartDateTime: time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC),
		SiteLocationName:    "C6 Site",
		EnergyKWh:           ptrFloat64(12.40), // implied capacity 62.0 at delta 20
	}
	if err := sw.MirrorSessions(ctx, accountB, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C6 setup: MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountB, teslaID)
	// Both percentages supplied directly -> status DONE (not DONE_CALCULATED).
	verified, err := sv.VerifySession(ctx, accountB, id, ptrIntV(10), ptrIntV(30))
	if err != nil {
		t.Fatalf("C6 setup: VerifySession: %v", err)
	}
	if verified.Status != charging.SessionStatusDone {
		t.Fatalf("C6 setup precondition: Status = %v, want DONE", verified.Status)
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C6: Calculate: %v", err)
	}

	row, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("C6: expected exactly one row")
	}
	if row.CandidateCount != 3 {
		t.Errorf("C6: CandidateCount = %d, want 3", row.CandidateCount)
	}
	if row.SampleCount != 3 {
		t.Errorf("C6: SampleCount = %d, want 3", row.SampleCount)
	}
	if n := countMonthlyEffectiveCapacityRows(t, pool, teslaID); n != 1 {
		t.Errorf("C6: expected exactly 1 row pooling both accounts, got %d", n)
	}
}

// TestCalculate_C7_NullTeslaIDSessionSkipped implements design.md Test
// Contract C7: a session row with tesla_id IS NULL is skipped (RD5) -- a
// capacity cannot be attributed to a car nobody has registered. There is no
// tesla_id group for a NULL-tesla_id session to appear under at all; this
// test additionally guards against a hypothetical bug that fell back to using
// session_id as a stand-in tesla_id.
func TestCalculate_C7_NullTeslaIDSessionSkipped(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	sessionID := int64(990007)
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, sessionID)

	sw := charging.NewSessionWriter(pool)
	sv := charging.NewSessionVerifier(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VNOTREG1",
		TeslaID:             nil, // VIN not currently registered to any vehicle
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC),
		SiteLocationName:    "C7 Site",
		EnergyKWh:           ptrFloat64(12.40),
	}
	if err := sw.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C7 setup: MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountID, sessionID)
	if _, err := sv.VerifySession(ctx, accountID, id, ptrIntV(10), ptrIntV(30)); err != nil {
		t.Fatalf("C7 setup: VerifySession: %v", err)
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C7: Calculate: %v", err)
	}

	if n := countMonthlyEffectiveCapacityRows(t, pool, sessionID); n != 0 {
		t.Errorf("C7: expected no monthly_effective_capacity row keyed by this session's id, got %d", n)
	}
}

// TestPackCapacityKWh_C8_NoMeasuredRowUsesDefault implements design.md Test
// Contract C8: packCapacityKWh with no measured row -> returns
// defaultPackCapacityKWh (62.0), through the real seam (resolveEnergy via
// Writer.Create), not the fake (A6).
func TestPackCapacityKWh_C8_NoMeasuredRowUsesDefault(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990008)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	w := charging.NewWriter(pool)
	e := minEntry(accountID, teslaID)
	e.ChargedOn = time.Date(2026, 8, 9, 0, 0, 0, 0, time.UTC)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(20)
	e.EndBatteryPct = ptrInt(60) // delta 40
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C8: Create: %v", err)
	}

	wantEnergy := 62.0 * 40 / 100 // 24.80, what defaultPackCapacityKWh would produce
	if created.EnergyAddedKWh == nil || !floatsAlmostEqual(*created.EnergyAddedKWh, wantEnergy) {
		t.Errorf("C8: EnergyAddedKWh = %v, want %v (defaultPackCapacityKWh-derived)", created.EnergyAddedKWh, wantEnergy)
	}
	if created.EnergySource != charging.EnergySourceEstimated {
		t.Errorf("C8: EnergySource = %v, want ESTIMATED", created.EnergySource)
	}
}

// TestPackCapacityKWh_C9_ThinCurrentMonthSkipsToEarlierMeasured implements
// design.md Test Contract C9: packCapacityKWh with a NULL current month and a
// measured earlier month -> returns the earlier one (RD4) --
// LatestMeasuredCapacity's own WHERE ... IS NOT NULL skips the thin row
// unconditionally.
func TestPackCapacityKWh_C9_ThinCurrentMonthSkipsToEarlierMeasured(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990009)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	w := charging.NewWriter(pool)
	calc := charging.NewMonthlyCapacityCalculator(pool)

	// July: four USER entries, all implying capacity 70.0 -> a real measured row.
	julPeriod := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i, day := range []int{5, 10, 15, 20} {
		e := monthlyEntryFixture(accountID, teslaID, julPeriod.AddDate(0, 0, day-1), 10, 70.0, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C9 setup July entry %d: %v", i, err)
		}
	}
	if _, err := calc.Calculate(ctx, julPeriod, &teslaID); err != nil {
		t.Fatalf("C9 setup: Calculate(July): %v", err)
	}
	julRow, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, julPeriod)
	if !ok || julRow.EffectiveCapacityKWh == nil || !floatsAlmostEqual(*julRow.EffectiveCapacityKWh, 70.0) {
		t.Fatalf("C9 setup precondition: July row = %+v, ok=%v, want measured 70.0", julRow, ok)
	}

	// August: two USER entries -- below minSamples -> a thin (NULL) row.
	augPeriod := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	for i, day := range []int{5, 10} {
		e := monthlyEntryFixture(accountID, teslaID, augPeriod.AddDate(0, 0, day-1), 10, 60.0, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C9 setup August entry %d: %v", i, err)
		}
	}
	if _, err := calc.Calculate(ctx, augPeriod, &teslaID); err != nil {
		t.Fatalf("C9 setup: Calculate(August): %v", err)
	}
	augRow, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, augPeriod)
	if !ok || augRow.EffectiveCapacityKWh != nil {
		t.Fatalf("C9 setup precondition: August row = %+v, ok=%v, want thin (NULL)", augRow, ok)
	}

	// A third August entry, EnergyAddedKWh nil -- exercises packCapacityKWh
	// through the real seam. Expect the July measurement (70.0), not the
	// thin August row and not defaultPackCapacityKWh (62.0).
	measuring := minEntry(accountID, teslaID)
	measuring.ChargedOn = augPeriod.AddDate(0, 0, 14)
	measuring.EnergyAddedKWh = nil
	measuring.StartBatteryPct = ptrInt(10)
	measuring.EndBatteryPct = ptrInt(50) // delta 40
	created, err := w.Create(ctx, measuring)
	if err != nil {
		t.Fatalf("C9: Create (measuring call): %v", err)
	}
	wantEnergy := 70.0 * 40 / 100 // 28.00, using the July measurement, not 62.0
	if created.EnergyAddedKWh == nil || !floatsAlmostEqual(*created.EnergyAddedKWh, wantEnergy) {
		t.Errorf("C9: EnergyAddedKWh = %v, want %v (derived from the earlier measured 70.0)", created.EnergyAddedKWh, wantEnergy)
	}
	if created.EnergySource != charging.EnergySourceEstimated {
		t.Errorf("C9: EnergySource = %v, want ESTIMATED", created.EnergySource)
	}
}

// TestPackCapacityKWh_C10_UnchangedUntilFirstMonthComputed implements
// design.md Test Contract C10: with no monthly_effective_capacity row
// anywhere, both callers -- Writer.Create (via resolveEnergy) and
// SessionVerifier.VerifySession (via derivedStartBatteryPct) -- derive using
// defaultPackCapacityKWh (62.0), matching pre-change behaviour bit-for-bit.
func TestPackCapacityKWh_C10_UnchangedUntilFirstMonthComputed(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	entryTeslaID := int64(990010)
	sessionTeslaID := int64(990011)
	cleanupAccount(t, pool, accountID)
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, entryTeslaID, sessionTeslaID)

	if n := countMonthlyEffectiveCapacityRows(t, pool, entryTeslaID); n != 0 {
		t.Fatalf("C10 precondition: entry tesla_id already has a monthly_effective_capacity row")
	}
	if n := countMonthlyEffectiveCapacityRows(t, pool, sessionTeslaID); n != 0 {
		t.Fatalf("C10 precondition: session tesla_id already has a monthly_effective_capacity row")
	}

	// Caller 1: Writer.Create, EnergyAddedKWh nil, a valid delta.
	w := charging.NewWriter(pool)
	e := minEntry(accountID, entryTeslaID)
	e.ChargedOn = time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(10)
	e.EndBatteryPct = ptrInt(40) // delta 30
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C10: Create: %v", err)
	}
	wantEntryEnergy := 62.0 * 30 / 100 // 18.60
	if created.EnergyAddedKWh == nil || !floatsAlmostEqual(*created.EnergyAddedKWh, wantEntryEnergy) {
		t.Errorf("C10: EnergyAddedKWh = %v, want %v (defaultPackCapacityKWh-derived)", created.EnergyAddedKWh, wantEntryEnergy)
	}

	// Caller 2: SessionVerifier.VerifySession deriving a start percentage on
	// a session with a non-nil tesla_id.
	sw := charging.NewSessionWriter(pool)
	sv := charging.NewSessionVerifier(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VC10CAP",
		TeslaID:             ptrInt64(sessionTeslaID),
		SessionID:           sessionTeslaID,
		ChargeStartDateTime: time.Date(2026, 8, 13, 10, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 13, 11, 0, 0, 0, time.UTC),
		SiteLocationName:    "C10 Site",
		EnergyKWh:           ptrFloat64(31.0),
	}
	if err := sw.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C10: MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountID, sessionTeslaID)
	verified, err := sv.VerifySession(ctx, accountID, id, nil, ptrIntV(80))
	if err != nil {
		t.Fatalf("C10: VerifySession: %v", err)
	}
	// defaultPackCapacityKWh(62.0): 80 - 31.0/62.0*100 = 80 - 50 = 30.
	if verified.StartBatteryPct == nil || *verified.StartBatteryPct != 30 {
		t.Errorf("C10: StartBatteryPct = %v, want 30 (defaultPackCapacityKWh-derived)", verified.StartBatteryPct)
	}
}

// TestVerifySession_C11_NullTeslaIDShortCircuitsToDefault implements
// design.md Test Contract C11: a session whose tesla_id is NULL never reaches
// packCapacityKWh's DB read at all -- derivation succeeds using
// defaultPackCapacityKWh directly. No monthly_effective_capacity row is
// seeded for this id anywhere, so a hypothetical bug that DID reach the read
// would observe the identical "no row" outcome -- the weaker assertion the
// Test Contract itself allows for this case ("or by a call-count fake
// substituted only for this one case").
func TestVerifySession_C11_NullTeslaIDShortCircuitsToDefault(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	sessionID := int64(990012)
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, sessionID)

	sw := charging.NewSessionWriter(pool)
	sv := charging.NewSessionVerifier(pool)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VNOTREG2",
		TeslaID:             nil,
		SessionID:           sessionID,
		ChargeStartDateTime: time.Date(2026, 8, 12, 11, 0, 0, 0, time.UTC),
		ChargeStopDateTime:  time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC),
		SiteLocationName:    "C11 Site",
		EnergyKWh:           ptrFloat64(31.0),
	}
	if err := sw.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("C11: MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountID, sessionID)

	verified, err := sv.VerifySession(ctx, accountID, id, nil, ptrIntV(100))
	if err != nil {
		t.Fatalf("C11: VerifySession: %v", err)
	}
	// defaultPackCapacityKWh(62.0): 100 - 31.0/62.0*100 = 50.
	if verified.StartBatteryPct == nil || *verified.StartBatteryPct != 50 {
		t.Errorf("C11: StartBatteryPct = %v, want 50 (derived using defaultPackCapacityKWh directly)", verified.StartBatteryPct)
	}
	if verified.Status != charging.SessionStatusDoneCalculated {
		t.Errorf("C11: Status = %v, want DONE_CALCULATED", verified.Status)
	}
}

// TestCalculate_C12_UpsertIsIdempotent implements design.md Test Contract
// C12: the upsert is idempotent (RD9's re-run path) -- ON CONFLICT (tesla_id,
// effective_period) DO UPDATE is exercised, not just declared. The row
// updates in place; no second row is created.
//
// design.md's own C12 cell says "state as in C4" but also says both counts
// "become 5" after a fifth entry -- only consistent starting from 4 rows
// (C5's shape), not C4's 2. This test starts from C5's 4-row shape so the
// stated "become 5" outcome is reachable; see this file's final report for
// the note on the mismatched cross-reference.
func TestCalculate_C12_UpsertIsIdempotent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990013)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)
	calc := charging.NewMonthlyCapacityCalculator(pool)

	for i, capacity := range []float64{58.0, 60.0, 64.0, 70.0} {
		e := monthlyEntryFixture(accountID, teslaID, period.AddDate(0, 0, 4+i), 10, capacity, 20)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C12 setup entry %d: %v", i, err)
		}
	}
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C12: first Calculate: %v", err)
	}
	first, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok || first.CandidateCount != 4 || first.SampleCount != 4 {
		t.Fatalf("C12 setup precondition: row = %+v, ok=%v, want candidate/sample 4", first, ok)
	}

	fifth := monthlyEntryFixture(accountID, teslaID, period.AddDate(0, 0, 20), 10, 66.0, 20)
	if _, err := w.Create(ctx, fifth); err != nil {
		t.Fatalf("C12: fifth entry: %v", err)
	}
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C12: second Calculate: %v", err)
	}

	second, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("C12: row disappeared after re-run")
	}
	if second.CandidateCount != 5 {
		t.Errorf("C12: CandidateCount = %d, want 5", second.CandidateCount)
	}
	if second.SampleCount != 5 {
		t.Errorf("C12: SampleCount = %d, want 5", second.SampleCount)
	}
	if n := countMonthlyEffectiveCapacityRows(t, pool, teslaID); n != 1 {
		t.Errorf("C12: expected exactly 1 row after re-run (upsert, not duplicate), got %d", n)
	}
}

// TestCalculate_C13_CandidateCountExistsForAllGated implements design.md Test
// Contract C13: five valid records found, none surviving the delta gate.
// Without candidate_count this row would be indistinguishable from a month
// with nothing measurable at all (D1, roadmap RD15).
func TestCalculate_C13_CandidateCountExistsForAllGated(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(990014)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)
	for i := 0; i < 5; i++ {
		e := monthlyEntryFixture(accountID, teslaID, period.AddDate(0, 0, i+1), 10, 60.0, 5) // delta 5, < minDeltaPct(15)
		if _, err := w.Create(ctx, e); err != nil {
			t.Fatalf("C13 setup entry %d: %v", i, err)
		}
	}

	calc := charging.NewMonthlyCapacityCalculator(pool)
	if _, err := calc.Calculate(ctx, period, nil); err != nil {
		t.Fatalf("C13: Calculate: %v", err)
	}

	row, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("C13: expected a row (5 candidates were found this period)")
	}
	if row.EffectiveCapacityKWh != nil {
		t.Errorf("C13: EffectiveCapacityKWh = %v, want nil", *row.EffectiveCapacityKWh)
	}
	if row.CandidateCount != 5 {
		t.Errorf("C13: CandidateCount = %d, want 5", row.CandidateCount)
	}
	if row.SampleCount != 0 {
		t.Errorf("C13: SampleCount = %d, want 0", row.SampleCount)
	}
}
