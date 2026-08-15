package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real telemetrydb-backed GapWriter (gap_writer.go) against a
// live Postgres from DATABASE_URL and self-skip when it is unset, so `go test ./...`
// stays green without a database (ai/go-conventions.md §persistence, AGENTS.md §Testing
// notes). They require the charge_gaps table created by migration 20260815000002.
//
// They implement the test contract authored in RM28-telemetry-add-charge-gap-storage's
// design.md BEFORE the implementation existed (§"Test Contract"), per
// ai/go-conventions.md's "author expected values up front" convention:
//
//   - (a) TestGapWriter_ReconcileWindow_UpsertIdempotent_PreservesCreatedAtAdvancesUpdatedAt
//   - (b) TestGapWriter_ReconcileWindow_DeletesResolvedDay_LeavesOtherFlaggedDayUntouched
//     and the adjacent empty-flagged-set case,
//     TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow
//   - (e)-i TestGapWriter_ReconcileWindow_TenantIsolation_NeverTouchesOtherAccountVehicle
//   - (e)-ii TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing —
//     the direct regression guard for the validation loop in gap_writer.go's
//     ReconcileWindow: it MUST fail if that validation is ever removed or weakened
//     (tasks.md T7.5's acceptance criterion).
//
// No public Reader exists for charge_gaps in this tier (GapWriter is write-only; the
// future notification read port is out of scope here — AGENTS.md), so these tests read
// the table back via direct SQL against the shared test pool, exactly as
// db_supercharger_battery_pct_integration_test.go does for the columns that have no Go
// writer of their own.

// cleanupChargeGaps registers a cleanup that deletes charge_gaps rows for one
// (accountID, teslaID) so a shared DB stays tidy across test runs.
func cleanupChargeGaps(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM charge_gaps WHERE account_id = $1 AND tesla_id = $2", accountID, teslaID)
	})
}

// cleanupChargeGapsByAccount registers a cleanup that deletes every charge_gaps row for
// one account, regardless of tesla_id. Used by the mis-scoped-entry test, where a bug in
// the validation loop under test could plausibly write under an unexpected tesla_id.
func cleanupChargeGapsByAccount(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM charge_gaps WHERE account_id = $1", accountID)
	})
}

// countChargeGaps returns the number of stored charge_gaps rows for one
// (accountID, teslaID).
func countChargeGaps(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM charge_gaps WHERE account_id = $1 AND tesla_id = $2",
		accountID, teslaID,
	).Scan(&n); err != nil {
		t.Fatalf("counting charge_gaps rows: %v", err)
	}
	return n
}

// chargeGapRow is the subset of charge_gaps columns these tests need to read back
// directly (no Reader port exists for this table in this tier).
type chargeGapRow struct {
	MissingChargingType string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// fetchChargeGap reads one charge_gaps row by its (accountID, teslaID, gap_date) key.
// ok is false when no row exists for that key (not an error — "not flagged").
func fetchChargeGap(t *testing.T, pool *pgxpool.Pool, accountID uuid.UUID, teslaID int64, date time.Time) (row chargeGapRow, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT missing_charging_type, created_at, updated_at FROM charge_gaps
		 WHERE account_id = $1 AND tesla_id = $2 AND gap_date = $3`,
		accountID, teslaID, date,
	).Scan(&row.MissingChargingType, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return chargeGapRow{}, false
		}
		t.Fatalf("fetching charge_gaps row: %v", err)
	}
	return row, true
}

// TestGapWriter_ReconcileWindow_UpsertIdempotent_PreservesCreatedAtAdvancesUpdatedAt
// implements design.md test-contract scenario (a): upserting a newly-flagged day, then
// re-running with the same day still flagged, does not duplicate the row — the UNIQUE
// constraint's ON CONFLICT DO UPDATE is the mechanism, not application-level dedup —
// and created_at is byte-for-byte preserved across the second call while updated_at
// advances (T7.1).
func TestGapWriter_ReconcileWindow_UpsertIdempotent_PreservesCreatedAtAdvancesUpdatedAt(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(910001)
	vin := "V1"
	date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, accountID, teslaID)

	gw := newGapWriter(pool)
	flagged := []ChargeGap{
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: date, MissingChargingType: MissingChargingTypeManual},
	}

	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, flagged); err != nil {
		t.Fatalf("first ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, accountID, teslaID); n != 1 {
		t.Fatalf("want exactly 1 row after the first call, got %d", n)
	}

	first, ok := fetchChargeGap(t, pool, accountID, teslaID, date)
	if !ok {
		t.Fatal("want a charge_gaps row after the first call, found none")
	}
	if first.MissingChargingType != string(MissingChargingTypeManual) {
		t.Errorf("MissingChargingType: want MANUAL, got %q", first.MissingChargingType)
	}
	if first.CreatedAt.IsZero() {
		t.Error("CreatedAt: want set (non-zero) after the first call")
	}

	// Give Postgres's now() room to advance measurably before the second call, so the
	// "updated_at advanced" assertion below cannot pass by timestamp coincidence.
	time.Sleep(10 * time.Millisecond)

	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, flagged); err != nil {
		t.Fatalf("second ReconcileWindow (same day still flagged): %v", err)
	}

	if n := countChargeGaps(t, pool, accountID, teslaID); n != 1 {
		t.Fatalf("want still exactly 1 row after re-flagging the same day (no duplicate row), got %d", n)
	}

	second, ok := fetchChargeGap(t, pool, accountID, teslaID, date)
	if !ok {
		t.Fatal("want a charge_gaps row after the second call, found none")
	}
	if second.MissingChargingType != string(MissingChargingTypeManual) {
		t.Errorf("MissingChargingType: want MANUAL (unchanged), got %q", second.MissingChargingType)
	}
	if !second.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("CreatedAt: want byte-for-byte unchanged across re-flagging — first=%v second=%v", first.CreatedAt, second.CreatedAt)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) {
		t.Errorf("UpdatedAt: want advanced past the first call's value — first=%v second=%v", first.UpdatedAt, second.UpdatedAt)
	}
}

// TestGapWriter_ReconcileWindow_DeletesResolvedDay_LeavesOtherFlaggedDayUntouched
// implements design.md test-contract scenario (b): a day that flags, then stops
// flagging, has its row deleted by the next ReconcileWindow call for the same window —
// while a DIFFERENT, still-flagged day in the same call's flagged set is left
// untouched, proving per-day precision rather than a blunt clear-and-reinsert (T7.2).
func TestGapWriter_ReconcileWindow_DeletesResolvedDay_LeavesOtherFlaggedDayUntouched(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(910002)
	vin := "V1"
	dateKeep := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	dateResolve := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, accountID, teslaID)

	gw := newGapWriter(pool)

	// Seed: both days flagged.
	seed := []ChargeGap{
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: dateKeep, MissingChargingType: MissingChargingTypeManual},
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: dateResolve, MissingChargingType: MissingChargingTypeManual},
	}
	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, seed); err != nil {
		t.Fatalf("seeding ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, accountID, teslaID); n != 2 {
		t.Fatalf("want 2 rows after seeding, got %d", n)
	}

	// dateResolve is no longer flagged; dateKeep still is.
	next := []ChargeGap{
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: dateKeep, MissingChargingType: MissingChargingTypeManual},
	}
	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, next); err != nil {
		t.Fatalf("reconcile after resolution: %v", err)
	}

	if _, ok := fetchChargeGap(t, pool, accountID, teslaID, dateResolve); ok {
		t.Error("dateResolve: want row DELETED (no longer flagged), still present")
	}
	if _, ok := fetchChargeGap(t, pool, accountID, teslaID, dateKeep); !ok {
		t.Error("dateKeep: want row UNTOUCHED (still flagged in this call), was removed")
	}
	if n := countChargeGaps(t, pool, accountID, teslaID); n != 1 {
		t.Fatalf("want exactly 1 row remaining, got %d", n)
	}
}

// TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow implements the
// empty-flagged-set case adjacent to design.md scenario (b): an empty flagged set for a
// window clears every previously-flagged day in that window (T7.2).
func TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(910003)
	vin := "V1"
	dateA := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, accountID, teslaID)

	gw := newGapWriter(pool)
	seed := []ChargeGap{
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: dateA, MissingChargingType: MissingChargingTypeManual},
		{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: dateB, MissingChargingType: MissingChargingTypeSupercharger},
	}
	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, seed); err != nil {
		t.Fatalf("seeding ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, accountID, teslaID); n != 2 {
		t.Fatalf("want 2 rows after seeding, got %d", n)
	}

	if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, []ChargeGap{}); err != nil {
		t.Fatalf("reconcile with an empty flagged set: %v", err)
	}

	if n := countChargeGaps(t, pool, accountID, teslaID); n != 0 {
		t.Fatalf("want 0 rows once every previously-flagged day in the window has resolved, got %d", n)
	}
}

// TestGapWriter_ReconcileWindow_TenantIsolation_NeverTouchesOtherAccountVehicle
// implements design.md test-contract scenario (e)-i: a ReconcileWindow call scoped to
// one account/vehicle never writes or deletes another account/vehicle's rows, even for
// the SAME overlapping gap_date (T7.5).
func TestGapWriter_ReconcileWindow_TenantIsolation_NeverTouchesOtherAccountVehicle(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountA := uuid.New()
	teslaA := int64(910004)
	accountB := uuid.New()
	teslaB := int64(910005)
	date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // deliberately the SAME gap_date for both accounts
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, accountA, teslaA)
	cleanupChargeGaps(t, pool, accountB, teslaB)

	gw := newGapWriter(pool)

	// A's own call: flags only its own vehicle-day.
	if err := gw.ReconcileWindow(ctx, accountA, teslaA, start, end,
		[]ChargeGap{{AccountID: accountA, TeslaID: teslaA, VIN: "VA", Date: date, MissingChargingType: MissingChargingTypeManual}},
	); err != nil {
		t.Fatalf("account A ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, accountB, teslaB); n != 0 {
		t.Errorf("account B: want no row written by A's call, got %d rows", n)
	}

	// B's own, separate call — its own disjoint flagged set, same overlapping gap_date.
	if err := gw.ReconcileWindow(ctx, accountB, teslaB, start, end,
		[]ChargeGap{{AccountID: accountB, TeslaID: teslaB, VIN: "VB", Date: date, MissingChargingType: MissingChargingTypeSupercharger}},
	); err != nil {
		t.Fatalf("account B ReconcileWindow: %v", err)
	}

	aRow, ok := fetchChargeGap(t, pool, accountA, teslaA, date)
	if !ok {
		t.Fatal("account A: want its row still present after B's call, was deleted")
	}
	if aRow.MissingChargingType != string(MissingChargingTypeManual) {
		t.Errorf("account A: MissingChargingType altered by B's call: want MANUAL, got %q", aRow.MissingChargingType)
	}

	bRow, ok := fetchChargeGap(t, pool, accountB, teslaB, date)
	if !ok {
		t.Fatal("account B: want its own row present after its own call")
	}
	if bRow.MissingChargingType != string(MissingChargingTypeSupercharger) {
		t.Errorf("account B: MissingChargingType: want SUPERCHARGER, got %q", bRow.MissingChargingType)
	}

	// A resolves its own day (empty flagged) — must not delete or alter B's row for
	// the same overlapping date.
	if err := gw.ReconcileWindow(ctx, accountA, teslaA, start, end, []ChargeGap{}); err != nil {
		t.Fatalf("account A ReconcileWindow (resolve): %v", err)
	}
	if n := countChargeGaps(t, pool, accountA, teslaA); n != 0 {
		t.Errorf("account A: want its own row deleted after resolving, got %d rows", n)
	}
	if n := countChargeGaps(t, pool, accountB, teslaB); n != 1 {
		t.Errorf("account B: want its row STILL present after A resolves the same overlapping date, got %d rows", n)
	}
}

// TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing implements
// design.md test-contract scenario (e)-ii: a ReconcileWindow call whose flagged set
// contains one mis-scoped entry (wrong AccountID, wrong TeslaID, or a Date outside the
// call's own window) is rejected in FULL — nothing from that call is written, including
// the OTHER, correctly-scoped entries in the same flagged slice (the whole-call
// transaction never even begins, per gap_writer.go's validate-before-tx.Begin shape).
//
// This is the direct regression test for the validation loop added in gap_writer.go's
// ReconcileWindow (tasks.md T7.5's own acceptance criterion): it MUST fail if that
// validation is ever removed or weakened, since a removed check would let the
// correctly-scoped first entry through as a partial write.
func TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing(t *testing.T) {
	_, pool := newTestStore(t)
	ctx := context.Background()

	accountID := uuid.New()
	teslaID := int64(910006)
	otherAccount := uuid.New()
	vin := "V1"
	goodDate := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	outsideDate := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) // outside [start, end] below
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGapsByAccount(t, pool, accountID)
	cleanupChargeGapsByAccount(t, pool, otherAccount)

	gw := newGapWriter(pool)

	cases := []struct {
		name    string
		flagged []ChargeGap
	}{
		{
			name: "wrong AccountID on second entry",
			flagged: []ChargeGap{
				{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
				{AccountID: otherAccount, TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
			},
		},
		{
			name: "wrong TeslaID on second entry",
			flagged: []ChargeGap{
				{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
				{AccountID: accountID, TeslaID: teslaID + 1, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
			},
		},
		{
			name: "Date outside window on second entry",
			flagged: []ChargeGap{
				{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
				{AccountID: accountID, TeslaID: teslaID, VIN: vin, Date: outsideDate, MissingChargingType: MissingChargingTypeManual},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := gw.ReconcileWindow(ctx, accountID, teslaID, start, end, tc.flagged); err == nil {
				t.Fatal("want an error for a mis-scoped flagged entry, got nil")
			}
			if n := countChargeGaps(t, pool, accountID, teslaID); n != 0 {
				t.Errorf("want NOTHING written for the call's own scope (including the correctly-scoped first entry) on a rejected call, got %d rows", n)
			}
			if n := countChargeGaps(t, pool, accountID, teslaID+1); n != 0 {
				t.Errorf("want NOTHING written under the mis-scoped tesla_id either, got %d rows", n)
			}
			if n := countChargeGaps(t, pool, otherAccount, teslaID); n != 0 {
				t.Errorf("want NOTHING written under the mis-scoped account_id either, got %d rows", n)
			}
		})
	}
}
