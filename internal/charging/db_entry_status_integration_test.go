// Package charging_test — database-backed integration tests for MAG-18/RM33
// tier 1 (RM33-charging-add-entry-status): manual_charge_entries' new lifecycle
// status, energy provenance, odometer reading, and the now-optional
// energy_added_kwh. Covers design.md's Test Contract Group B (B1-B8, direct SQL
// against the schema itself) and Group C (C1-C16, through Writer/Reader).
//
// Group B asserts the DATABASE's own behavior — defaults, CHECK constraints —
// via direct SQL, mirroring db_inferred_capacity_entries_integration_test.go's
// style. Group C asserts the write-path derivation/validation rules (design.md
// D3/D4/D5/D8) through the public Writer/Reader ports. Assertions are ONLY
// against charging.Entry domain fields, raw SQL column values, and
// *pgconn.PgError's SQLSTATE (via the shared assertPgErrorCode helper) — never
// pgtype, in this file or any other (internal/charging/AGENTS.md §Testing
// Notes). Float comparisons use assertFloatPtrApprox (shared, 1e-9 tolerance),
// never ==.
//
// Test -> Test Contract case mapping:
//
//	B1  TestEntryStatus_B1_PreMigrationInsertTakesDefaults
//	B2  TestEntryStatus_B2_EnergyZeroRejected
//	B3  TestEntryStatus_B3_EnergyNegativeRejected
//	B4  TestEntryStatus_B4_EnergyNullAccepted
//	B5  TestEntryStatus_B5_StatusCheckRejectsUnknownValue
//	B6  TestEntryStatus_B6_EnergySourceCheckRejectsUnknownValue
//	B7  TestEntryStatus_B7_OdometerKmCheckBothDirections
//	B8  TestEntryStatus_B8_NullEnergyWithValidPercentagesYieldsNullInferredCapacity
//	C1  TestEntryStatus_C1_InProgressWithoutEnergy
//	C2  TestEntryStatus_C2_DerivationAndItsD4Consequence
//	C3  TestEntryStatus_C3_SuppliedEnergySkipsDerivation
//	C4  TestEntryStatus_C4_CallerSuppliedProvenanceIgnored
//	C5  TestEntryStatus_C5_NoDerivationOnNonPositiveDelta
//	C6  TestEntryStatus_C6_NoDerivationOnMissingPercentage
//	C7  TestEntryStatus_C7_DoneRejectsMissingEndBatteryPct
//	C8  TestEntryStatus_C8_DoneRejectsMissingEndedAt
//	C9  TestEntryStatus_C9_CompleteDoneEntryAccepted
//	C10 TestEntryStatus_C10_EmptyStatusNormalizesToInProgress
//	C11 TestEntryStatus_C11_UnknownStatusRejectedInGo
//	C12 TestEntryStatus_C12_OdometerKmRoundTrips
//	C13 TestEntryStatus_C13_UpdateDerivesIdenticallyToCreate
//	C14 TestEntryStatus_C14_UpdateProvenanceRecomputedNeverSticky
//	C15 TestEntryStatus_C15_UpdateEnforcesRequiredFieldsAndWritesNothing
//	C16 TestEntryStatus_C16_DoneToInProgressIsAllowed
package charging_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// --- Group B helpers: direct SQL against manual_charge_entries ---

// insertEntryColumns INSERTs a manual_charge_entries row directly via SQL,
// binding exactly the named columns (comma-separated) to args in order, and
// returns the new row's id (or an error, e.g. a CHECK violation). Used only by
// Group B, which asserts the database's own behavior — not the Writer port.
func insertEntryColumns(ctx context.Context, pool *pgxpool.Pool, columns string, args ...any) (uuid.UUID, error) {
	placeholders := make([]string, len(args))
	for i := range args {
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	query := "INSERT INTO manual_charge_entries (" + columns + ") VALUES (" +
		strings.Join(placeholders, ", ") + ") RETURNING id"
	var id uuid.UUID
	err := pool.QueryRow(ctx, query, args...).Scan(&id)
	return id, err
}

// minColumns/minArgs are the pre-migration required column set (Context fact 5
// / B1's own subject) and a set of valid values for it, reused by every Group B
// case that does not specifically exercise one of these columns.
const minColumns = "account_id, tesla_id, vin, charged_on, energy_added_kwh, price, currency, location_kind"

func minArgs(accountID uuid.UUID, teslaID int64) []any {
	return []any{
		accountID, teslaID, "5YJ3E1EA0NF000001",
		time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC),
		20.5, 45000.00, "COP", "HOME",
	}
}

// --- Group B: schema, defaults and constraints ---

// TestEntryStatus_B1_PreMigrationInsertTakesDefaults implements design.md B1:
// an INSERT naming only the pre-migration columns takes exactly the path a
// pre-migration row took — the column DEFAULT (roadmap D1's backfill,
// mechanically proven).
func TestEntryStatus_B1_PreMigrationInsertTakesDefaults(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	id, err := insertEntryColumns(ctx, pool, minColumns, minArgs(accountID, 310001)...)
	if err != nil {
		t.Fatalf("B1: insert: %v", err)
	}

	var status, energySource string
	var odometerNull bool
	err = pool.QueryRow(ctx,
		"SELECT status, energy_source, odometer_km IS NULL FROM manual_charge_entries WHERE id = $1", id,
	).Scan(&status, &energySource, &odometerNull)
	if err != nil {
		t.Fatalf("B1: read back: %v", err)
	}
	if status != "IN_PROGRESS" {
		t.Errorf("B1: status = %q, want IN_PROGRESS", status)
	}
	if energySource != "USER" {
		t.Errorf("B1: energy_source = %q, want USER", energySource)
	}
	if !odometerNull {
		t.Errorf("B1: odometer_km expected NULL")
	}
}

// TestEntryStatus_B2_EnergyZeroRejected implements design.md B2: the retained
// CHECK (energy_added_kwh > 0) still rejects 0 after DROP NOT NULL (D2).
func TestEntryStatus_B2_EnergyZeroRejected(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	args := minArgs(accountID, 310002)
	args[4] = 0 // energy_added_kwh
	_, err := insertEntryColumns(ctx, pool, minColumns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestEntryStatus_B3_EnergyNegativeRejected implements design.md B3: the
// retained CHECK still rejects a negative value.
func TestEntryStatus_B3_EnergyNegativeRejected(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	args := minArgs(accountID, 310003)
	args[4] = -5.0 // energy_added_kwh
	_, err := insertEntryColumns(ctx, pool, minColumns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestEntryStatus_B4_EnergyNullAccepted implements design.md B4: the core D2
// proof — a CHECK evaluates NULL, not false, on a NULL input, so the retained
// CHECK now accepts NULL and the row round-trips it.
func TestEntryStatus_B4_EnergyNullAccepted(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	args := minArgs(accountID, 310004)
	args[4] = nil // energy_added_kwh
	id, err := insertEntryColumns(ctx, pool, minColumns, args...)
	if err != nil {
		t.Fatalf("B4: insert with NULL energy: %v", err)
	}

	var energyNull bool
	if err := pool.QueryRow(ctx,
		"SELECT energy_added_kwh IS NULL FROM manual_charge_entries WHERE id = $1", id,
	).Scan(&energyNull); err != nil {
		t.Fatalf("B4: read back: %v", err)
	}
	if !energyNull {
		t.Errorf("B4: expected energy_added_kwh IS NULL")
	}
}

// TestEntryStatus_B5_StatusCheckRejectsUnknownValue implements design.md B5:
// the status CHECK (D1).
func TestEntryStatus_B5_StatusCheckRejectsUnknownValue(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", status"
	args := append(minArgs(accountID, 310005), "PAUSED")
	_, err := insertEntryColumns(ctx, pool, columns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestEntryStatus_B6_EnergySourceCheckRejectsUnknownValue implements design.md
// B6: the energy_source CHECK (D4).
func TestEntryStatus_B6_EnergySourceCheckRejectsUnknownValue(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", energy_source"
	args := append(minArgs(accountID, 310006), "GUESSED")
	_, err := insertEntryColumns(ctx, pool, columns, args...)
	assertPgErrorCode(t, err, "23514")
}

// TestEntryStatus_B7_OdometerKmCheckBothDirections implements design.md B7:
// the odometer_km CHECK, both directions (D6).
func TestEntryStatus_B7_OdometerKmCheckBothDirections(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", odometer_km"

	negArgs := append(minArgs(accountID, 310007), -1)
	_, err := insertEntryColumns(ctx, pool, columns, negArgs...)
	assertPgErrorCode(t, err, "23514")

	for _, v := range []int{0, 999999} {
		v := v
		t.Run(strconv.Itoa(v), func(t *testing.T) {
			okArgs := append(minArgs(accountID, 310007), v)
			if _, err := insertEntryColumns(ctx, pool, columns, okArgs...); err != nil {
				t.Fatalf("odometer_km=%d: expected success, got %v", v, err)
			}
		})
	}
}

// TestEntryStatus_B8_NullEnergyWithValidPercentagesYieldsNullInferredCapacity
// implements design.md B8: the D4 consequence for a NULL-energy row proven
// rather than argued — the generated CASE's WHEN tests only the percentages
// (it is true here), the THEN branch evaluates NULL / 0.5, and SQL yields NULL
// — not an error, not a division by zero. Also proves DROP NOT NULL did not
// invalidate the generation expression.
func TestEntryStatus_B8_NullEnergyWithValidPercentagesYieldsNullInferredCapacity(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()

	columns := minColumns + ", start_battery_pct, end_battery_pct"
	args := minArgs(accountID, 310008)
	args[4] = nil // energy_added_kwh
	args = append(args, 50, 100)

	id, err := insertEntryColumns(ctx, pool, columns, args...)
	if err != nil {
		t.Fatalf("B8: insert: %v", err)
	}

	var calcNull bool
	if err := pool.QueryRow(ctx,
		"SELECT inferred_capacity_kwh_calc IS NULL FROM manual_charge_entries WHERE id = $1", id,
	).Scan(&calcNull); err != nil {
		t.Fatalf("B8: read back: %v", err)
	}
	if !calcNull {
		t.Errorf("B8: expected inferred_capacity_kwh_calc IS NULL")
	}
}

// --- Group C helpers ---

// assertErrorNamesField fails the test unless err is non-nil and its message
// contains field (a Field's string value, e.g. "end_battery_pct") — the plain,
// untyped error missingFieldsError builds (design.md D5).
func assertErrorNamesField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error naming %q, got nil", field)
	}
	if !strings.Contains(err.Error(), field) {
		t.Errorf("expected error to name %q, got: %v", field, err)
	}
}

// --- Group C: write-path behaviour through Writer/Reader ---

// TestEntryStatus_C1_InProgressWithoutEnergy implements design.md C1 — the
// headline feature: a charge can be logged at plug-in time with nothing
// fabricated (D2/D3).
func TestEntryStatus_C1_InProgressWithoutEnergy(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320001)
	e.Status = charging.StatusInProgress
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(40)
	e.EndBatteryPct = nil
	e.EndedAt = nil

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C1: Create: unexpected error: %v", err)
	}
	if created.EnergyAddedKWh != nil {
		t.Errorf("C1: EnergyAddedKWh = %v, want nil", *created.EnergyAddedKWh)
	}
	if created.EnergySource != charging.EnergySourceUser {
		t.Errorf("C1: EnergySource = %v, want USER", created.EnergySource)
	}
	if created.InferredCapacityKWhCalc != nil {
		t.Errorf("C1: InferredCapacityKWhCalc = %v, want nil", *created.InferredCapacityKWhCalc)
	}
	if created.Status != charging.StatusInProgress {
		t.Errorf("C1: Status = %v, want IN_PROGRESS", created.Status)
	}
}

// TestEntryStatus_C2_DerivationAndItsD4Consequence implements design.md C2:
// derivation (D3) and its D4 consequence together — 62.000 is exactly the
// capacity constant, by algebra, which is the whole reason energy_source
// exists.
func TestEntryStatus_C2_DerivationAndItsD4Consequence(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320002)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(100)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C2: Create: unexpected error: %v", err)
	}
	assertFloatPtrApprox(t, "C2: EnergyAddedKWh", created.EnergyAddedKWh, ptrFloat64(31.00))
	if created.EnergySource != charging.EnergySourceEstimated {
		t.Errorf("C2: EnergySource = %v, want ESTIMATED", created.EnergySource)
	}
	assertFloatPtrApprox(t, "C2: InferredCapacityKWhCalc", created.InferredCapacityKWhCalc, ptrFloat64(62.000))
}

// TestEntryStatus_C3_SuppliedEnergySkipsDerivation implements design.md C3:
// the derivation does not fire when the caller supplied a value, even though
// the percentages would have allowed it (D3).
func TestEntryStatus_C3_SuppliedEnergySkipsDerivation(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320003)
	e.EnergyAddedKWh = ptrFloat64(20.5)
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(100)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C3: Create: unexpected error: %v", err)
	}
	assertFloatPtrApprox(t, "C3: EnergyAddedKWh", created.EnergyAddedKWh, ptrFloat64(20.50))
	if created.EnergySource != charging.EnergySourceUser {
		t.Errorf("C3: EnergySource = %v, want USER", created.EnergySource)
	}
	assertFloatPtrApprox(t, "C3: InferredCapacityKWhCalc", created.InferredCapacityKWhCalc, ptrFloat64(41.000))
}

// TestEntryStatus_C4_CallerSuppliedProvenanceIgnored implements design.md C4:
// a caller cannot set provenance — it is always computed (D4), matching
// battery_pct_source's precedent.
func TestEntryStatus_C4_CallerSuppliedProvenanceIgnored(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320004)
	e.EnergySource = charging.EnergySourceEstimated
	e.EnergyAddedKWh = ptrFloat64(20.5)
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(100)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C4: Create: unexpected error: %v", err)
	}
	if created.EnergySource != charging.EnergySourceUser {
		t.Errorf("C4: EnergySource = %v, want USER (caller's ESTIMATED must be ignored)", created.EnergySource)
	}
}

// TestEntryStatus_C5_NoDerivationOnNonPositiveDelta implements design.md C5:
// no derivation on a zero or negative delta; the row is still created, not
// rejected (D3).
func TestEntryStatus_C5_NoDerivationOnNonPositiveDelta(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	cases := []struct {
		name       string
		teslaID    int64
		start, end int
	}{
		{"zero delta", 320005, 60, 60},
		{"negative delta", 320006, 60, 40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := minEntry(accountID, tc.teslaID)
			e.EnergyAddedKWh = nil
			e.StartBatteryPct = ptrInt(tc.start)
			e.EndBatteryPct = ptrInt(tc.end)

			created, err := w.Create(ctx, e)
			if err != nil {
				t.Fatalf("C5 (%s): Create: unexpected error: %v", tc.name, err)
			}
			if created.EnergyAddedKWh != nil {
				t.Errorf("C5 (%s): EnergyAddedKWh = %v, want nil", tc.name, *created.EnergyAddedKWh)
			}
			if created.EnergySource != charging.EnergySourceUser {
				t.Errorf("C5 (%s): EnergySource = %v, want USER", tc.name, created.EnergySource)
			}
			if created.InferredCapacityKWhCalc != nil {
				t.Errorf("C5 (%s): InferredCapacityKWhCalc = %v, want nil", tc.name, *created.InferredCapacityKWhCalc)
			}
		})
	}
}

// TestEntryStatus_C6_NoDerivationOnMissingPercentage implements design.md C6:
// no derivation with a percentage missing (D3).
func TestEntryStatus_C6_NoDerivationOnMissingPercentage(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320007)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = nil
	e.EndBatteryPct = ptrInt(80)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C6: Create: unexpected error: %v", err)
	}
	if created.EnergyAddedKWh != nil {
		t.Errorf("C6: EnergyAddedKWh = %v, want nil", *created.EnergyAddedKWh)
	}
	if created.EnergySource != charging.EnergySourceUser {
		t.Errorf("C6: EnergySource = %v, want USER", created.EnergySource)
	}
}

// TestEntryStatus_C7_DoneRejectsMissingEndBatteryPct implements design.md C7:
// RequiredFieldsFor(DONE) is enforced by Writer, and rejection happens before
// any DB write (D5).
func TestEntryStatus_C7_DoneRejectsMissingEndBatteryPct(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	end := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 320008)
	e.Status = charging.StatusDone
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = nil

	_, err := w.Create(ctx, e)
	assertErrorNamesField(t, err, string(charging.FieldEndBatteryPct))

	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("C7: ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("C7: expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestEntryStatus_C8_DoneRejectsMissingEndedAt implements design.md C8: the
// other half of the DONE set — the newly-required field (D5).
func TestEntryStatus_C8_DoneRejectsMissingEndedAt(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	e := minEntry(accountID, 320009)
	e.Status = charging.StatusDone
	e.EndBatteryPct = ptrInt(90)
	e.EndedAt = nil

	_, err := w.Create(ctx, e)
	assertErrorNamesField(t, err, string(charging.FieldEndedAt))

	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("C8: ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("C8: expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestEntryStatus_C9_CompleteDoneEntryAccepted implements design.md C9: a
// complete DONE entry is accepted (D5).
func TestEntryStatus_C9_CompleteDoneEntryAccepted(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	end := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 320010)
	e.Status = charging.StatusDone
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = ptrInt(90)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C9: Create: unexpected error: %v", err)
	}
	if created.Status != charging.StatusDone {
		t.Errorf("C9: Status = %v, want DONE", created.Status)
	}
}

// TestEntryStatus_C10_EmptyStatusNormalizesToInProgress implements design.md
// C10 — what makes tier 1 shippable before tier 2 (D8). If this test fails,
// entry creation is broken for the un-updated gateway.
func TestEntryStatus_C10_EmptyStatusNormalizesToInProgress(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320011)
	e.Status = "" // zero value — parseChargeForm builds an Entry with no Status today

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C10: Create: unexpected error: %v", err)
	}
	if created.Status != charging.StatusInProgress {
		t.Errorf("C10: Status = %v, want IN_PROGRESS", created.Status)
	}
}

// TestEntryStatus_C11_UnknownStatusRejectedInGo implements design.md C11: an
// unknown status is rejected in Go, before the DB (D8); B5 covers the DB
// backstop separately.
func TestEntryStatus_C11_UnknownStatusRejectedInGo(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	e := minEntry(accountID, 320012)
	e.Status = charging.Status("PAUSED")

	_, err := w.Create(ctx, e)
	if err == nil {
		t.Fatal("C11: Create with status=PAUSED: expected error, got nil")
	}

	entries, listErr := r.ListEntriesByAccount(ctx, accountID, 10)
	if listErr != nil {
		t.Fatalf("C11: ListEntriesByAccount: %v", listErr)
	}
	if len(entries) != 0 {
		t.Errorf("C11: expected 0 rows after rejection, got %d", len(entries))
	}
}

// TestEntryStatus_C12_OdometerKmRoundTrips implements design.md C12:
// odometer_km round-trips through the port in both states (D6).
func TestEntryStatus_C12_OdometerKmRoundTrips(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	t.Run("non-nil", func(t *testing.T) {
		e := minEntry(accountID, 320013)
		e.OdometerKm = ptrInt(123456)

		created, err := w.Create(ctx, e)
		if err != nil {
			t.Fatalf("Create: unexpected error: %v", err)
		}
		if created.OdometerKm == nil || *created.OdometerKm != 123456 {
			t.Errorf("OdometerKm = %v, want 123456", created.OdometerKm)
		}
	})

	t.Run("nil", func(t *testing.T) {
		e := minEntry(accountID, 320014)
		e.OdometerKm = nil

		created, err := w.Create(ctx, e)
		if err != nil {
			t.Fatalf("Create: unexpected error: %v", err)
		}
		if created.OdometerKm != nil {
			t.Errorf("OdometerKm = %v, want nil", *created.OdometerKm)
		}
	})
}

// TestEntryStatus_C13_UpdateDerivesIdenticallyToCreate implements design.md
// C13: derivation runs on Update identically to Create, and provenance flips
// USER -> ESTIMATED (D3/D4). Setup mirrors C3 (20.50, USER, 50->100).
func TestEntryStatus_C13_UpdateDerivesIdenticallyToCreate(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320015)
	e.EnergyAddedKWh = ptrFloat64(20.5)
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(100)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C13: setup Create: %v", err)
	}

	updated := created
	updated.EnergyAddedKWh = nil // percentages (50/100) carry over from created

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C13: Update: unexpected error: %v", err)
	}
	assertFloatPtrApprox(t, "C13: EnergyAddedKWh", result.EnergyAddedKWh, ptrFloat64(31.00))
	if result.EnergySource != charging.EnergySourceEstimated {
		t.Errorf("C13: EnergySource = %v, want ESTIMATED", result.EnergySource)
	}
	assertFloatPtrApprox(t, "C13: InferredCapacityKWhCalc", result.InferredCapacityKWhCalc, ptrFloat64(62.000))
}

// TestEntryStatus_C14_UpdateProvenanceRecomputedNeverSticky implements
// design.md C14: provenance is recomputed, never sticky — an estimated row
// corrected by hand becomes USER (D4). Setup mirrors C2 (31.00, ESTIMATED,
// 50->100).
func TestEntryStatus_C14_UpdateProvenanceRecomputedNeverSticky(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 320016)
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(50)
	e.EndBatteryPct = ptrInt(100)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C14: setup Create: %v", err)
	}
	if created.EnergySource != charging.EnergySourceEstimated {
		t.Fatalf("C14: setup: EnergySource = %v, want ESTIMATED", created.EnergySource)
	}

	updated := created
	updated.EnergyAddedKWh = ptrFloat64(25.0)

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C14: Update: unexpected error: %v", err)
	}
	assertFloatPtrApprox(t, "C14: EnergyAddedKWh", result.EnergyAddedKWh, ptrFloat64(25.00))
	if result.EnergySource != charging.EnergySourceUser {
		t.Errorf("C14: EnergySource = %v, want USER", result.EnergySource)
	}
	assertFloatPtrApprox(t, "C14: InferredCapacityKWhCalc", result.InferredCapacityKWhCalc, ptrFloat64(50.000))
}

// TestEntryStatus_C15_UpdateEnforcesRequiredFieldsAndWritesNothing implements
// design.md C15: RequiredFieldsFor is enforced on Update, not only on Create,
// and a rejected update writes nothing (D5). Setup mirrors C1 (IN_PROGRESS, no
// energy).
func TestEntryStatus_C15_UpdateEnforcesRequiredFieldsAndWritesNothing(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	e := minEntry(accountID, 320017)
	e.Status = charging.StatusInProgress
	e.EnergyAddedKWh = nil
	e.StartBatteryPct = ptrInt(40)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C15: setup Create: %v", err)
	}

	tampered := created
	tampered.Status = charging.StatusDone
	tampered.EndedAt = nil // still missing -- and end_battery_pct is also nil

	_, err = w.Update(ctx, tampered)
	if err == nil {
		t.Fatal("C15: Update to DONE without ended_at/end_battery_pct: expected error, got nil")
	}

	entries, listErr := r.ListEntriesByVehicle(ctx, accountID, 320017, 10)
	if listErr != nil {
		t.Fatalf("C15: ListEntriesByVehicle: %v", listErr)
	}
	if len(entries) != 1 {
		t.Fatalf("C15: expected 1 entry (rejected update wrote nothing), got %d", len(entries))
	}
	if entries[0].Status != charging.StatusInProgress {
		t.Errorf("C15: Status = %v, want unchanged IN_PROGRESS", entries[0].Status)
	}
}

// TestEntryStatus_C16_DoneToInProgressIsAllowed implements design.md C16:
// DONE -> IN_PROGRESS is allowed — there is deliberately no transition rule
// (D5). Setup mirrors C9 (complete DONE entry).
func TestEntryStatus_C16_DoneToInProgressIsAllowed(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	end := time.Date(2026, 8, 29, 18, 0, 0, 0, time.UTC)
	e := minEntry(accountID, 320018)
	e.Status = charging.StatusDone
	e.EndedAt = ptrTime(end)
	e.EndBatteryPct = ptrInt(90)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("C16: setup Create: %v", err)
	}

	updated := created
	updated.Status = charging.StatusInProgress
	updated.EndedAt = nil
	updated.EndBatteryPct = nil

	result, err := w.Update(ctx, updated)
	if err != nil {
		t.Fatalf("C16: Update DONE -> IN_PROGRESS: unexpected error: %v", err)
	}
	if result.Status != charging.StatusInProgress {
		t.Errorf("C16: Status = %v, want IN_PROGRESS", result.Status)
	}
	if result.EndedAt != nil {
		t.Errorf("C16: EndedAt = %v, want nil", *result.EndedAt)
	}
}
