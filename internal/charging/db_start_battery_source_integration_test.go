// Package charging_test — database-backed integration tests for the
// manual_charge_entries.start_battery_source column and its write-path
// derivation. Mirrors db_promotion_price_source_integration_test.go's style
// and reuses this package's shared helpers (newTestPool, cleanupAccount,
// minEntry, insertEntryColumns, minColumns, minArgs, assertPgErrorCode,
// assertFloatPtrApprox, cleanupMonthlyEffectiveCapacity,
// fetchMonthlyEffectiveCapacity, refFor) -- same package, no duplication.
//
// Assertions are ONLY against charging.Entry domain fields, raw SQL column
// values, and *pgconn.PgError's SQLSTATE (via assertPgErrorCode) -- never
// pgtype, in this file or any other (internal/charging/AGENTS.md §Testing
// Notes). Every expected value below was fixed before this file existed
// (ai/go-conventions.md §Testing authoring order) -- this file asserts what
// the change must do, not whatever the implementation happens to produce.
//
// Test Contract case mapping (row -> test function):
//
//	B1  TestStartBatterySource_NoDefaultOnPreMigrationInsert
//	B2  TestStartBatterySource_CheckRejectsUnknownValue
//	B3  TestStartBatterySource_DatabaseDoesNotEnforceNullnessPairing
//	C1  TestStartBatterySource_NothingSuppliedNothingDerived
//	C2  TestStartBatterySource_DerivesFromEnergyAndEndPercentage
//	C3  TestStartBatterySource_NoPrecedenceConflictWhenEnergyAlsoUnresolvable
//	C4  TestStartBatterySource_TwoDerivationsNeverCollide
//	C5  TestStartBatterySource_EnergySourceDoesNotAffectStartDerivation
//	C6  TestStartBatterySource_ClearingTypedValueTriggersDerivationOnUpdate
//	C7  TestStartBatterySource_CallerCannotSetProvenanceDirectly
//	D1  TestStartBatterySource_CapacityQueryExcludesDerivedStartPercentage
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- Group B: schema, defaults and constraints (direct SQL) ---

// TestStartBatterySource_NoDefaultOnPreMigrationInsert proves that an
// INSERT naming only pre-migration columns (no
// start_battery_source), with start_battery_pct set, gets NULL for the new
// column -- unlike energy_source/price_source, there is no truthful DEFAULT
// for a column with no percentage to record provenance for.
func TestStartBatterySource_NoDefaultOnPreMigrationInsert(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", start_battery_pct"
	args := append(minArgs(accountID, 350001), 50)
	id, err := insertEntryColumns(ctx, pool, columns, args...)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var sourceNull bool
	if err := pool.QueryRow(ctx,
		"SELECT start_battery_source IS NULL FROM charging.manual_charge_entries WHERE id = $1", id,
	).Scan(&sourceNull); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !sourceNull {
		t.Errorf("start_battery_source expected NULL, no DEFAULT exists for this column")
	}
}

// TestStartBatterySource_CheckRejectsUnknownValue proves the
// start_battery_source CHECK rejects a value outside the two allowed ones.
// Asserts SQLSTATE 23514, not message text.
func TestStartBatterySource_CheckRejectsUnknownValue(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", start_battery_source"
	args := append(minArgs(accountID, 350002), "DERIVED")
	_, err := insertEntryColumns(ctx, pool, columns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestStartBatterySource_DatabaseDoesNotEnforceNullnessPairing proves a row
// with start_battery_pct NULL (omitted --
// minColumns never names it) and start_battery_source = 'USER' inserts with
// no error. The database allows this inconsistent pairing on purpose -- only
// resolveStartBatteryPct (Group C below) keeps the two columns in step.
func TestStartBatterySource_DatabaseDoesNotEnforceNullnessPairing(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", start_battery_source"
	args := append(minArgs(accountID, 350003), "USER")
	if _, err := insertEntryColumns(ctx, pool, columns, args...); err != nil {
		t.Fatalf("insert: expected success, got error: %v", err)
	}
}

// --- Group C: write-path behaviour through Writer/Reader ---

// TestStartBatterySource_NothingSuppliedNothingDerived proves the base case:
// nothing supplied, nothing derived, through the real write path.
func TestStartBatterySource_NothingSuppliedNothingDerived(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)
	const teslaID = int64(350011)

	e := minEntry(accountID, teslaID)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = nil
	e.EndBatteryPct = nil

	if _, err := w.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := onlyEntry(t, r, teslaID)
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil", *got.StartBatteryPct)
	}
	if got.StartBatterySource != nil {
		t.Errorf("StartBatterySource = %v, want nil", *got.StartBatterySource)
	}
}

// TestStartBatterySource_DerivesFromEnergyAndEndPercentage proves the
// headline derivation through Writer.Create. It is the end-to-end form of
// the offline derives_from_energy_and_end_percentage case.
func TestStartBatterySource_DerivesFromEnergyAndEndPercentage(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)
	const teslaID = int64(350012)

	e := minEntry(accountID, teslaID)
	e.StartBatteryPct = nil
	e.EndBatteryPct = ptrInt(74)
	e.EnergyAddedKWh = ptrFloat64(6.20)

	if _, err := w.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := onlyEntry(t, r, teslaID)
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 64 {
		t.Errorf("StartBatteryPct = %v, want 64", got.StartBatteryPct)
	}
	if got.StartBatterySource == nil || *got.StartBatterySource != charging.StartBatterySourceEstimated {
		t.Errorf("StartBatterySource = %v, want ESTIMATED", got.StartBatterySource)
	}
}

// TestStartBatterySource_NoPrecedenceConflictWhenEnergyAlsoUnresolvable
// proves that when resolveEnergy also cannot
// derive an energy (only one percentage present), nothing downstream can
// derive a start percentage from it either -- proven end-to-end, not just at
// the unit level.
func TestStartBatterySource_NoPrecedenceConflictWhenEnergyAlsoUnresolvable(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)
	const teslaID = int64(350013)

	e := minEntry(accountID, teslaID)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = nil
	e.EndBatteryPct = ptrInt(74)

	if _, err := w.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := onlyEntry(t, r, teslaID)
	if got.EnergyAddedKWh != nil {
		t.Errorf("EnergyAddedKWh = %v, want nil (only one percentage present)", *got.EnergyAddedKWh)
	}
	if got.StartBatteryPct != nil {
		t.Errorf("StartBatteryPct = %v, want nil", *got.StartBatteryPct)
	}
	if got.StartBatterySource != nil {
		t.Errorf("StartBatterySource = %v, want nil", *got.StartBatterySource)
	}
}

// TestStartBatterySource_TwoDerivationsNeverCollide proves the case where
// resolveEnergy derives the energy from the same
// percentage pair resolveStartBatteryPct would otherwise divide by, but the
// caller's own start percentage is never touched.
func TestStartBatterySource_TwoDerivationsNeverCollide(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)
	const teslaID = int64(350014)

	e := minEntry(accountID, teslaID)
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(90)
	e.EnergyAddedKWh = nil

	if _, err := w.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := onlyEntry(t, r, teslaID)
	assertFloatPtrApprox(t, "EnergyAddedKWh", got.EnergyAddedKWh, ptrFloat64(24.80))
	if got.EnergySource != charging.EnergySourceEstimated {
		t.Errorf("EnergySource = %v, want ESTIMATED", got.EnergySource)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 50 {
		t.Errorf("StartBatteryPct = %v, want 50 (the caller's own value, untouched)", got.StartBatteryPct)
	}
	if got.StartBatterySource == nil || *got.StartBatterySource != charging.StartBatterySourceUser {
		t.Errorf("StartBatterySource = %v, want USER", got.StartBatterySource)
	}
}

// TestStartBatterySource_EnergySourceDoesNotAffectStartDerivation proves the
// source of the energy does not matter to
// the start-percentage derivation, only that it is non-nil.
func TestStartBatterySource_EnergySourceDoesNotAffectStartDerivation(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)
	const teslaID = int64(350015)

	e := minEntry(accountID, teslaID)
	e.StartBatteryPct = nil
	e.EndBatteryPct = ptrInt(90)
	e.EnergyAddedKWh = ptrFloat64(20.0)

	if _, err := w.Create(ctx, e); err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := onlyEntry(t, r, teslaID)
	if got.EnergySource != charging.EnergySourceUser {
		t.Errorf("EnergySource = %v, want USER (energy was supplied, not derived)", got.EnergySource)
	}
	if got.StartBatteryPct == nil || *got.StartBatteryPct != 58 {
		t.Errorf("StartBatteryPct = %v, want 58", got.StartBatteryPct)
	}
	if got.StartBatterySource == nil || *got.StartBatterySource != charging.StartBatterySourceEstimated {
		t.Errorf("StartBatterySource = %v, want ESTIMATED", got.StartBatterySource)
	}
}

// TestStartBatterySource_ClearingTypedValueTriggersDerivationOnUpdate
// proves that clearing a typed value is how a
// caller asks for it to be calculated, and the derivation re-runs on Update
// exactly as on Create.
func TestStartBatterySource_ClearingTypedValueTriggersDerivationOnUpdate(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	const teslaID = int64(350016)

	e := minEntry(accountID, teslaID)
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(90)
	e.EnergyAddedKWh = ptrFloat64(20.0)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("setup Create: %v", err)
	}
	if created.StartBatteryPct == nil || *created.StartBatteryPct != 50 {
		t.Fatalf("setup: StartBatteryPct = %v, want 50", created.StartBatteryPct)
	}

	updated := created
	updated.StartBatteryPct = nil
	updated.EndBatteryPct = ptrInt(90)
	updated.EnergyAddedKWh = ptrFloat64(20.0)

	result, err := w.Update(ctx, refFor(teslaID), updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if result.StartBatteryPct == nil || *result.StartBatteryPct != 58 {
		t.Errorf("StartBatteryPct = %v, want 58", result.StartBatteryPct)
	}
	if result.StartBatterySource == nil || *result.StartBatterySource != charging.StartBatterySourceEstimated {
		t.Errorf("StartBatterySource = %v, want ESTIMATED", result.StartBatterySource)
	}
}

// TestStartBatterySource_CallerCannotSetProvenanceDirectly proves a caller
// cannot set the provenance directly --
// matches EnergySource's and PriceSource's identical precedent.
func TestStartBatterySource_CallerCannotSetProvenanceDirectly(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	const teslaID = int64(350017)

	forced := charging.StartBatterySourceUser
	e := minEntry(accountID, teslaID)
	e.StartBatteryPct = nil
	e.EndBatteryPct = ptrInt(74)
	e.EnergyAddedKWh = ptrFloat64(6.20)
	e.StartBatterySource = &forced // must be ignored

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.StartBatteryPct == nil || *created.StartBatteryPct != 64 {
		t.Errorf("StartBatteryPct = %v, want 64", created.StartBatteryPct)
	}
	if created.StartBatterySource == nil || *created.StartBatterySource != charging.StartBatterySourceEstimated {
		t.Errorf("StartBatterySource = %v, want ESTIMATED (caller's USER must be ignored)", created.StartBatterySource)
	}
}

// onlyEntry reads back exactly one entry for teslaID via Reader.ListEntriesByVehicle
// and fails the test unless exactly one row comes back. Shared by every Group C
// case above that seeds a single entry and reads it back through the port,
// rather than trusting Writer.Create's own return value alone.
func onlyEntry(t *testing.T, r charging.Reader, teslaID int64) charging.Entry {
	t.Helper()
	entries, err := r.ListEntriesByVehicle(context.Background(), teslaID, 10)
	if err != nil {
		t.Fatalf("ListEntriesByVehicle: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	return entries[0]
}

// --- Group D: the capacity-query exclusion (the change's central invariant) ---

// TestStartBatterySource_CapacityQueryExcludesDerivedStartPercentage is the
// proof this change exists for.
// Two entries in the same period, both with EnergySource USER (energy
// supplied directly) and the identical implied capacity (62.0 kWh): one with
// a typed starting percentage (StartBatterySource USER), one with a derived
// one (StartBatterySource ESTIMATED). Only the USER row may count as
// capacity evidence.
//
// This calls MonthlyCapacityCalculator.Calculate -- the real port backing
// ListValidManualEntryCapacitiesForPeriod -- rather than asserting on
// resolveStartBatteryPct's return value. Both entries carry the same
// energy_source and the same inferred_capacity_kwh_calc, so CandidateCount
// is the only signal that can tell them apart: 1 means the ESTIMATED row was
// excluded, 2 would mean it leaked into the evidence pool.
func TestStartBatterySource_CapacityQueryExcludesDerivedStartPercentage(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	accountID := uuid.New()
	teslaID := int64(350021)
	cleanupAccount(t, pool, accountID)
	cleanupMonthlyEffectiveCapacity(t, pool, teslaID)

	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	w := charging.NewWriter(pool)

	// Entry X: a typed starting percentage -- StartBatterySource USER.
	x := minEntry(accountID, teslaID)
	x.ChargedOn = period.AddDate(0, 0, 4)
	x.StartBatteryPct = ptrInt(50)
	x.EndBatteryPct = ptrInt(90)
	x.EnergyAddedKWh = ptrFloat64(24.80)
	createdX, err := w.Create(ctx, x)
	if err != nil {
		t.Fatalf("Create (typed start): %v", err)
	}
	if createdX.StartBatterySource == nil || *createdX.StartBatterySource != charging.StartBatterySourceUser {
		t.Fatalf("setup: entry X StartBatterySource = %v, want USER", createdX.StartBatterySource)
	}
	assertFloatPtrApprox(t, "setup: entry X InferredCapacityKWhCalc", createdX.InferredCapacityKWhCalc, ptrFloat64(62.0))

	// Entry Y: same energy and end percentage, but no typed start -- the
	// module derives it, so StartBatterySource is ESTIMATED. It lands on the
	// same implied capacity as entry X.
	y := minEntry(accountID, teslaID)
	y.ChargedOn = period.AddDate(0, 0, 5)
	y.StartBatteryPct = nil
	y.EndBatteryPct = ptrInt(90)
	y.EnergyAddedKWh = ptrFloat64(24.80)
	createdY, err := w.Create(ctx, y)
	if err != nil {
		t.Fatalf("Create (derived start): %v", err)
	}
	if createdY.StartBatterySource == nil || *createdY.StartBatterySource != charging.StartBatterySourceEstimated {
		t.Fatalf("setup: entry Y StartBatterySource = %v, want ESTIMATED", createdY.StartBatterySource)
	}
	assertFloatPtrApprox(t, "setup: entry Y InferredCapacityKWhCalc", createdY.InferredCapacityKWhCalc, ptrFloat64(62.0))

	calc := charging.NewMonthlyCapacityCalculator(pool)
	if _, err := calc.Calculate(ctx, period, &teslaID); err != nil {
		t.Fatalf("Calculate: %v", err)
	}

	row, ok := fetchMonthlyEffectiveCapacity(t, pool, teslaID, period)
	if !ok {
		t.Fatalf("expected a monthly_effective_capacity row")
	}
	if row.CandidateCount != 1 {
		t.Errorf("CandidateCount = %d, want 1 (the ESTIMATED entry must not count as evidence)", row.CandidateCount)
	}
}
