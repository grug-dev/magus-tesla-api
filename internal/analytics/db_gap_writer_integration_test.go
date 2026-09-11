package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// These tests exercise the real analyticsdb-backed GapWriter (gap_writer.go) against
// the test Postgres this package's TestMain provisions -- TEST_DATABASE_URL when set,
// otherwise a Docker container -- and self-skip when neither is available, so
// `go test ./...` stays green without a database (ai/go-conventions.md §persistence,
// AGENTS.md §Testing notes). They require the charge_gaps table created by migration
// 20260815000002, which moved to this module with the ledger in RM29 tier 5.
//
// They implement the test contract authored in RM28-telemetry-add-charge-gap-storage's
// design.md BEFORE the implementation existed (§"Test Contract"), per
// ai/go-conventions.md's "author expected values up front" convention:
//
//   - (a) TestGapWriter_ReconcileWindow_UpsertIdempotent_PreservesCreatedAtAdvancesUpdatedAt
//   - (b) TestGapWriter_ReconcileWindow_DeletesResolvedDay_LeavesOtherFlaggedDayUntouched
//     and the adjacent empty-flagged-set case,
//     TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow
//   - (e)-i TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere
//   - (e)-ii TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing —
//     the direct regression guard for the validation loop in gap_writer.go's
//     ReconcileWindow: it MUST fail if that validation is ever removed or weakened
//     (tasks.md T7.5's acceptance criterion).
//
// tasks.md's T9 (appended after review round 1, finding R1-1) adds two more scenarios
// that specs/telemetry/spec.md states as ADDED requirements but design.md's (a)-(e)
// contract above never transcribed, so no test covered them until now:
//
//   - T9.1 "Reconciliation only affects the reconciled window" —
//     TestGapWriter_ReconcileWindow_OutsideWindowRowUnaffected
//   - T9.2 "Ledger rows for different vehicles are independent" —
//     TestGapWriter_ReconcileWindow_DifferentVehiclesIndependent — distinct from the
//     (e)-i test above, which seeds only one flagged day per vehicle; this one seeds
//     multiple days per vehicle, one shared, to check per-day independence too.
//
// No public Reader exists for charge_gaps in this tier (GapWriter is write-only; the
// future notification read port is out of scope here — AGENTS.md), so these tests read
// the table back via direct SQL against the shared test pool, exactly as
// db_supercharger_battery_pct_integration_test.go does for the columns that have no Go
// writer of their own.

// cleanupChargeGaps registers a cleanup that deletes charge_gaps rows for one
// teslaID so a shared DB stays tidy across test runs.
func cleanupChargeGaps(t *testing.T, pool *pgxpool.Pool, teslaID int64) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM analytics.charge_gaps WHERE tesla_id = $1", teslaID)
	})
}

// countChargeGaps returns the number of stored charge_gaps rows for one teslaID.
func countChargeGaps(t *testing.T, pool *pgxpool.Pool, teslaID int64) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		"SELECT COUNT(*) FROM analytics.charge_gaps WHERE tesla_id = $1",
		teslaID,
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

// fetchChargeGap reads one charge_gaps row by its (teslaID, gap_date) key.
// ok is false when no row exists for that key (not an error — "not flagged").
func fetchChargeGap(t *testing.T, pool *pgxpool.Pool, teslaID int64, date time.Time) (row chargeGapRow, ok bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT missing_charging_type, created_at, updated_at FROM analytics.charge_gaps
		 WHERE tesla_id = $1 AND gap_date = $2`,
		teslaID, date,
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
	pool := newTestPool(t)
	ctx := context.Background()

	teslaID := int64(910001)
	vin := "V1"
	date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaID)

	gw := newGapWriter(pool)
	flagged := []ChargeGap{
		{TeslaID: teslaID, VIN: vin, Date: date, MissingChargingType: MissingChargingTypeManual},
	}

	if err := gw.ReconcileWindow(ctx, teslaID, start, end, flagged); err != nil {
		t.Fatalf("first ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, teslaID); n != 1 {
		t.Fatalf("want exactly 1 row after the first call, got %d", n)
	}

	first, ok := fetchChargeGap(t, pool, teslaID, date)
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

	if err := gw.ReconcileWindow(ctx, teslaID, start, end, flagged); err != nil {
		t.Fatalf("second ReconcileWindow (same day still flagged): %v", err)
	}

	if n := countChargeGaps(t, pool, teslaID); n != 1 {
		t.Fatalf("want still exactly 1 row after re-flagging the same day (no duplicate row), got %d", n)
	}

	second, ok := fetchChargeGap(t, pool, teslaID, date)
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
	pool := newTestPool(t)
	ctx := context.Background()

	teslaID := int64(910002)
	vin := "V1"
	dateKeep := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	dateResolve := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaID)

	gw := newGapWriter(pool)

	// Seed: both days flagged.
	seed := []ChargeGap{
		{TeslaID: teslaID, VIN: vin, Date: dateKeep, MissingChargingType: MissingChargingTypeManual},
		{TeslaID: teslaID, VIN: vin, Date: dateResolve, MissingChargingType: MissingChargingTypeManual},
	}
	if err := gw.ReconcileWindow(ctx, teslaID, start, end, seed); err != nil {
		t.Fatalf("seeding ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, teslaID); n != 2 {
		t.Fatalf("want 2 rows after seeding, got %d", n)
	}

	// dateResolve is no longer flagged; dateKeep still is.
	next := []ChargeGap{
		{TeslaID: teslaID, VIN: vin, Date: dateKeep, MissingChargingType: MissingChargingTypeManual},
	}
	if err := gw.ReconcileWindow(ctx, teslaID, start, end, next); err != nil {
		t.Fatalf("reconcile after resolution: %v", err)
	}

	if _, ok := fetchChargeGap(t, pool, teslaID, dateResolve); ok {
		t.Error("dateResolve: want row DELETED (no longer flagged), still present")
	}
	if _, ok := fetchChargeGap(t, pool, teslaID, dateKeep); !ok {
		t.Error("dateKeep: want row UNTOUCHED (still flagged in this call), was removed")
	}
	if n := countChargeGaps(t, pool, teslaID); n != 1 {
		t.Fatalf("want exactly 1 row remaining, got %d", n)
	}
}

// TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow implements the
// empty-flagged-set case adjacent to design.md scenario (b): an empty flagged set for a
// window clears every previously-flagged day in that window (T7.2).
func TestGapWriter_ReconcileWindow_EmptyFlaggedSet_ClearsAllInWindow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	teslaID := int64(910003)
	vin := "V1"
	dateA := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	dateB := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaID)

	gw := newGapWriter(pool)
	seed := []ChargeGap{
		{TeslaID: teslaID, VIN: vin, Date: dateA, MissingChargingType: MissingChargingTypeManual},
		{TeslaID: teslaID, VIN: vin, Date: dateB, MissingChargingType: MissingChargingTypeSupercharger},
	}
	if err := gw.ReconcileWindow(ctx, teslaID, start, end, seed); err != nil {
		t.Fatalf("seeding ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, teslaID); n != 2 {
		t.Fatalf("want 2 rows after seeding, got %d", n)
	}

	if err := gw.ReconcileWindow(ctx, teslaID, start, end, []ChargeGap{}); err != nil {
		t.Fatalf("reconcile with an empty flagged set: %v", err)
	}

	if n := countChargeGaps(t, pool, teslaID); n != 0 {
		t.Fatalf("want 0 rows once every previously-flagged day in the window has resolved, got %d", n)
	}
}

// TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere implements design.md
// test-contract scenario (e)-i: a ReconcileWindow call scoped to one vehicle never
// writes or deletes another vehicle's rows, even for the SAME overlapping gap_date
// (T7.5). tesla_id alone identifies a vehicle, so two different tesla_ids are enough
// to prove this regardless of which account owns either vehicle.
func TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	teslaA := int64(910004)
	teslaB := int64(910005)
	date := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // deliberately the SAME gap_date for both vehicles
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaA)
	cleanupChargeGaps(t, pool, teslaB)

	gw := newGapWriter(pool)

	// A's own call: flags only its own vehicle-day.
	if err := gw.ReconcileWindow(ctx, teslaA, start, end,
		[]ChargeGap{{TeslaID: teslaA, VIN: "VA", Date: date, MissingChargingType: MissingChargingTypeManual}},
	); err != nil {
		t.Fatalf("vehicle A ReconcileWindow: %v", err)
	}
	if n := countChargeGaps(t, pool, teslaB); n != 0 {
		t.Errorf("vehicle B: want no row written by A's call, got %d rows", n)
	}

	// B's own, separate call — its own disjoint flagged set, same overlapping gap_date.
	if err := gw.ReconcileWindow(ctx, teslaB, start, end,
		[]ChargeGap{{TeslaID: teslaB, VIN: "VB", Date: date, MissingChargingType: MissingChargingTypeSupercharger}},
	); err != nil {
		t.Fatalf("vehicle B ReconcileWindow: %v", err)
	}

	aRow, ok := fetchChargeGap(t, pool, teslaA, date)
	if !ok {
		t.Fatal("vehicle A: want its row still present after B's call, was deleted")
	}
	if aRow.MissingChargingType != string(MissingChargingTypeManual) {
		t.Errorf("vehicle A: MissingChargingType altered by B's call: want MANUAL, got %q", aRow.MissingChargingType)
	}

	bRow, ok := fetchChargeGap(t, pool, teslaB, date)
	if !ok {
		t.Fatal("vehicle B: want its own row present after its own call")
	}
	if bRow.MissingChargingType != string(MissingChargingTypeSupercharger) {
		t.Errorf("vehicle B: MissingChargingType: want SUPERCHARGER, got %q", bRow.MissingChargingType)
	}

	// A resolves its own day (empty flagged) — must not delete or alter B's row for
	// the same overlapping date.
	if err := gw.ReconcileWindow(ctx, teslaA, start, end, []ChargeGap{}); err != nil {
		t.Fatalf("vehicle A ReconcileWindow (resolve): %v", err)
	}
	if n := countChargeGaps(t, pool, teslaA); n != 0 {
		t.Errorf("vehicle A: want its own row deleted after resolving, got %d rows", n)
	}
	if n := countChargeGaps(t, pool, teslaB); n != 1 {
		t.Errorf("vehicle B: want its row STILL present after A resolves the same overlapping date, got %d rows", n)
	}
}

// TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing: a
// ReconcileWindow call whose flagged set contains one mis-scoped entry (wrong
// TeslaID, or a Date outside the call's own window) is rejected in FULL — nothing
// from that call is written, including the OTHER, correctly-scoped entries in the
// same flagged slice (the whole-call transaction never even begins, per
// gap_writer.go's validate-before-tx.Begin shape).
//
// This is the direct regression test for the validation loop in gap_writer.go's
// ReconcileWindow: it MUST fail if that validation is ever removed or weakened,
// since a removed check would let the correctly-scoped first entry through as a
// partial write.
func TestGapWriter_ReconcileWindow_RejectsMisScopedFlaggedEntry_WritesNothing(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	teslaID := int64(910006)
	vin := "V1"
	goodDate := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)
	outsideDate := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC) // outside [start, end] below
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaID)
	cleanupChargeGaps(t, pool, teslaID+1)

	gw := newGapWriter(pool)

	cases := []struct {
		name    string
		flagged []ChargeGap
	}{
		{
			name: "wrong TeslaID on second entry",
			flagged: []ChargeGap{
				{TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
				{TeslaID: teslaID + 1, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
			},
		},
		{
			name: "Date outside window on second entry",
			flagged: []ChargeGap{
				{TeslaID: teslaID, VIN: vin, Date: goodDate, MissingChargingType: MissingChargingTypeManual},
				{TeslaID: teslaID, VIN: vin, Date: outsideDate, MissingChargingType: MissingChargingTypeManual},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := gw.ReconcileWindow(ctx, teslaID, start, end, tc.flagged); err == nil {
				t.Fatal("want an error for a mis-scoped flagged entry, got nil")
			}
			if n := countChargeGaps(t, pool, teslaID); n != 0 {
				t.Errorf("want NOTHING written for the call's own scope (including the correctly-scoped first entry) on a rejected call, got %d rows", n)
			}
			if n := countChargeGaps(t, pool, teslaID+1); n != 0 {
				t.Errorf("want NOTHING written under the mis-scoped tesla_id either, got %d rows", n)
			}
		})
	}
}

// TestGapWriter_ReconcileWindow_OutsideWindowRowUnaffected implements spec.md's
// scenario "Reconciliation only affects the reconciled window" (T9.1, appended after
// review round 1, finding R1-1): a charge_gaps row for a day OUTSIDE the window about
// to be reconciled is left present and byte-for-byte unchanged — both CreatedAt AND
// UpdatedAt — by a ReconcileWindow call for a window that excludes that day, with a
// flagged set that does not mention it. This is the direct regression guard for the
// window bounds on ChargeGapDatesByVehicleBetween/DeleteChargeGap: it must fail if
// either query's gap_date bounds are ever loosened into a table-wide clear.
func TestGapWriter_ReconcileWindow_OutsideWindowRowUnaffected(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	teslaID := int64(910007)
	vin := "V1"
	outsideDate := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaID)

	gw := newGapWriter(pool)

	// Seed the outside-window row via a call whose OWN window contains it.
	seedStart := time.Date(2026, 6, 25, 0, 0, 0, 0, time.UTC)
	seedEnd := time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	if err := gw.ReconcileWindow(ctx, teslaID, seedStart, seedEnd,
		[]ChargeGap{{TeslaID: teslaID, VIN: vin, Date: outsideDate, MissingChargingType: MissingChargingTypeManual}},
	); err != nil {
		t.Fatalf("seeding outside-window row: %v", err)
	}

	before, ok := fetchChargeGap(t, pool, teslaID, outsideDate)
	if !ok {
		t.Fatal("want the seeded outside-window row present, found none")
	}

	// Give Postgres's now() room to advance measurably, so the "unchanged" assertions
	// below cannot pass by timestamp coincidence if the delete pass wrongly touched it.
	time.Sleep(10 * time.Millisecond)

	// Reconcile a DIFFERENT, disjoint window that excludes outsideDate entirely, with
	// a flagged set that does not mention it.
	windowStart := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	inWindowDate := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	if err := gw.ReconcileWindow(ctx, teslaID, windowStart, windowEnd,
		[]ChargeGap{{TeslaID: teslaID, VIN: vin, Date: inWindowDate, MissingChargingType: MissingChargingTypeManual}},
	); err != nil {
		t.Fatalf("reconciling a disjoint window: %v", err)
	}

	after, ok := fetchChargeGap(t, pool, teslaID, outsideDate)
	if !ok {
		t.Fatal("outside-window row: want it still present after reconciling a disjoint window, was deleted")
	}
	if after.MissingChargingType != before.MissingChargingType {
		t.Errorf("outside-window row: MissingChargingType changed: want %q, got %q", before.MissingChargingType, after.MissingChargingType)
	}
	if !after.CreatedAt.Equal(before.CreatedAt) {
		t.Errorf("outside-window row: CreatedAt changed — want byte-for-byte unchanged: before=%v after=%v", before.CreatedAt, after.CreatedAt)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("outside-window row: UpdatedAt changed — want byte-for-byte unchanged: before=%v after=%v", before.UpdatedAt, after.UpdatedAt)
	}
}

// TestGapWriter_ReconcileWindow_DifferentVehiclesIndependent covers spec.md's
// scenario "Ledger rows for different vehicles are independent": two vehicles'
// charge_gaps rows resolve independently of each other, even for the SAME
// overlapping gap_date. Distinct from
// TestGapWriter_ReconcileWindow_DifferentVehiclesNeverInterfere above, which seeds
// only one flagged day per vehicle — this test seeds multiple days per vehicle, one
// shared, so it also fails if ChargeGapDatesByVehicleBetween, DeleteChargeGap, or
// UpsertChargeGap's ON CONFLICT target ever loses its tesla_id predicate on a
// per-day basis, not just a per-call one.
func TestGapWriter_ReconcileWindow_DifferentVehiclesIndependent(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	teslaA := int64(910008)
	teslaB := int64(910009)
	dateShared := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC) // flagged for BOTH vehicles
	dateOnlyA := time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC)   // flagged only for A
	dateOnlyB := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)  // flagged only for B
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)

	cleanupChargeGaps(t, pool, teslaA)
	cleanupChargeGaps(t, pool, teslaB)

	gw := newGapWriter(pool)

	// Seed both vehicles, overlapping dateShared.
	if err := gw.ReconcileWindow(ctx, teslaA, start, end,
		[]ChargeGap{
			{TeslaID: teslaA, VIN: "VA", Date: dateShared, MissingChargingType: MissingChargingTypeManual},
			{TeslaID: teslaA, VIN: "VA", Date: dateOnlyA, MissingChargingType: MissingChargingTypeManual},
		},
	); err != nil {
		t.Fatalf("seeding vehicle A: %v", err)
	}
	if err := gw.ReconcileWindow(ctx, teslaB, start, end,
		[]ChargeGap{
			{TeslaID: teslaB, VIN: "VB", Date: dateShared, MissingChargingType: MissingChargingTypeSupercharger},
			{TeslaID: teslaB, VIN: "VB", Date: dateOnlyB, MissingChargingType: MissingChargingTypeSupercharger},
		},
	); err != nil {
		t.Fatalf("seeding vehicle B: %v", err)
	}
	if n := countChargeGaps(t, pool, teslaA); n != 2 {
		t.Fatalf("vehicle A: want 2 rows after seeding, got %d", n)
	}
	if n := countChargeGaps(t, pool, teslaB); n != 2 {
		t.Fatalf("vehicle B: want 2 rows after seeding, got %d", n)
	}

	bSharedBefore, ok := fetchChargeGap(t, pool, teslaB, dateShared)
	if !ok {
		t.Fatal("vehicle B: want dateShared row present before touching A, found none")
	}
	bOnlyBefore, ok := fetchChargeGap(t, pool, teslaB, dateOnlyB)
	if !ok {
		t.Fatal("vehicle B: want dateOnlyB row present before touching A, found none")
	}

	// Give Postgres's now() room to advance measurably, so the "B unchanged"
	// assertions below cannot pass by timestamp coincidence.
	time.Sleep(10 * time.Millisecond)

	// A resolves its own dateOnlyA (no longer flagged), keeping dateShared flagged.
	if err := gw.ReconcileWindow(ctx, teslaA, start, end,
		[]ChargeGap{{TeslaID: teslaA, VIN: "VA", Date: dateShared, MissingChargingType: MissingChargingTypeManual}},
	); err != nil {
		t.Fatalf("resolving vehicle A's dateOnlyA: %v", err)
	}

	// Vehicle A: dateOnlyA resolved independently, dateShared remains.
	if _, ok := fetchChargeGap(t, pool, teslaA, dateOnlyA); ok {
		t.Error("vehicle A: want dateOnlyA deleted (resolved), still present")
	}
	if n := countChargeGaps(t, pool, teslaA); n != 1 {
		t.Errorf("vehicle A: want exactly 1 row remaining (dateShared), got %d", n)
	}

	// Vehicle B: neither updated nor deleted by A's call, even for the SAME
	// overlapping dateShared.
	bSharedAfter, ok := fetchChargeGap(t, pool, teslaB, dateShared)
	if !ok {
		t.Fatal("vehicle B: want dateShared row still present after A's call, was deleted")
	}
	if !bSharedAfter.CreatedAt.Equal(bSharedBefore.CreatedAt) || !bSharedAfter.UpdatedAt.Equal(bSharedBefore.UpdatedAt) {
		t.Errorf("vehicle B: dateShared row touched by A's ReconcileWindow call — before=%+v after=%+v", bSharedBefore, bSharedAfter)
	}
	bOnlyAfter, ok := fetchChargeGap(t, pool, teslaB, dateOnlyB)
	if !ok {
		t.Fatal("vehicle B: want dateOnlyB row still present after A's call, was deleted")
	}
	if !bOnlyAfter.CreatedAt.Equal(bOnlyBefore.CreatedAt) || !bOnlyAfter.UpdatedAt.Equal(bOnlyBefore.UpdatedAt) {
		t.Errorf("vehicle B: dateOnlyB row touched by A's ReconcileWindow call — before=%+v after=%+v", bOnlyBefore, bOnlyAfter)
	}
	if n := countChargeGaps(t, pool, teslaB); n != 2 {
		t.Errorf("vehicle B: want still 2 rows (untouched), got %d", n)
	}

	// And vice versa: capture A's remaining row before B resolves its own days, then
	// confirm B's independent resolution does not touch A.
	aSharedBefore, ok := fetchChargeGap(t, pool, teslaA, dateShared)
	if !ok {
		t.Fatal("vehicle A: want dateShared row present before touching B, found none")
	}
	time.Sleep(10 * time.Millisecond)

	if err := gw.ReconcileWindow(ctx, teslaB, start, end, []ChargeGap{}); err != nil {
		t.Fatalf("resolving vehicle B (empty flagged set): %v", err)
	}

	if n := countChargeGaps(t, pool, teslaB); n != 0 {
		t.Errorf("vehicle B: want 0 rows after resolving everything, got %d", n)
	}
	aSharedAfter, ok := fetchChargeGap(t, pool, teslaA, dateShared)
	if !ok {
		t.Fatal("vehicle A: want dateShared row still present after B's independent resolution, was deleted")
	}
	if !aSharedAfter.CreatedAt.Equal(aSharedBefore.CreatedAt) || !aSharedAfter.UpdatedAt.Equal(aSharedBefore.UpdatedAt) {
		t.Errorf("vehicle A: dateShared row touched by B's ReconcileWindow call — before=%+v after=%+v", aSharedBefore, aSharedAfter)
	}
}
