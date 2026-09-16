// Package charging_test — database-backed integration tests for
// manual_charge_entries.inferred_capacity_kwh_calc (MAG-25,
// charging-add-inferred-capacity), covering design.md's Test Contract
// **Group A (T1-T12) and T24** (tasks.md task 3.1).
//
// This is a GENERATED ALWAYS AS (...) STORED column (design.md D2): nothing in Go
// computes it, and the database itself recomputes it on every INSERT/UPDATE and
// rejects any direct write. Every expected value below is copied VERBATIM from
// design.md §Test Contract, which fixed them BEFORE this file was written
// (ai/go-conventions.md §Testing authoring order) — they are not derived by running
// this code. Do not "correct" a value here to match the ticket's 2-decimal prose;
// design.md §"What must NOT change" explicitly forbids it.
//
// T4 is the ONE case whose expectation has since changed, and deliberately. The
// module now fills in a missing starting percentage on write, so that row's
// battery delta is complete and the generated column is no longer NULL. The
// original rule — NULL delta gives NULL column — is unchanged and still covered
// by T5 through T8.
//
// Fixtures are seeded through Writer.Create (and Writer.Update for T11) — the
// module's only writers for this table — and read back through
// Reader.ListEntriesByVehicle, except T12, which needs direct SQL because no port
// can express a write to a generated column. Assertions are ONLY against
// charging.Entry domain fields and, for T12, a *pgconn.PgError's SQLSTATE — pgtype
// NEVER appears in this file (internal/charging/AGENTS.md §Testing Notes).
//
// Float comparisons use assertFloatPtrApprox (1e-9 tolerance), never ==, per
// design.md §Test Contract "Float comparison". Every NULL case asserts nil
// explicitly.
//
// Test -> Test Contract case mapping:
//
//	T1-T10  TestCreate_InferredCapacity_TableCases (table-driven, one subtest each)
//	T11     TestUpdate_InferredCapacity_RecomputesOnEndBatteryPctChange
//	T12     TestInferredCapacity_Entries_ColumnUnwritable
//	T24     proven by the T1 and T7 subtests of TestCreate_InferredCapacity_TableCases,
//	        which already read back through Reader.ListEntriesByVehicle and assert
//	        the non-nil/nil InferredCapacityKWhCalc mapping — no separate test needed.
package charging_test

import (
	"context"
	"math"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// assertFloatPtrApprox fails the test unless got and want are both nil, or both
// non-nil and within 1e-9 of each other. The domain field is *float64 converted
// from NUMERIC, so it is never compared with == (design.md §Test Contract "Float
// comparison"). Shared by both db_inferred_capacity_*_integration_test.go files
// (same package).
func assertFloatPtrApprox(t *testing.T, label string, got, want *float64) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("%s = %v, want nil", label, *got)
		}
		return
	}
	if got == nil {
		t.Fatalf("%s = nil, want %v", label, *want)
	}
	if diff := math.Abs(*got - *want); diff >= 1e-9 {
		t.Errorf("%s = %v, want %v (diff %v >= 1e-9)", label, *got, *want, diff)
	}
}

// entryCapacityCase is one row of design.md §Test Contract Group A (T1-T10).
type entryCapacityCase struct {
	id             string // Test Contract case ID, e.g. "T1"
	energyAddedKWh float64
	startPct       *int
	endPct         *int
	want           *float64 // nil means the expected InferredCapacityKWhCalc is NULL
	// wantDerivedStart marks a case where the module fills the starting
	// percentage in on write. The battery delta is then complete, so the
	// generated column has a value. Its exact figure follows the pack capacity,
	// which is not this file's subject, so the case asserts presence only.
	wantDerivedStart bool
}

// TestCreate_InferredCapacity_TableCases implements design.md §Test Contract Group
// A, T1-T10, verbatim. Every case must INSERT successfully — T7, T8 and T10 are the
// D3/D4 regression tests explicitly required to assert a successful Create, not
// just the resulting value (tasks.md 3.1): T7/T8 prove the strict end>start guard
// prevents a division_by_zero / a stored negative, and T10 proves the unconstrained
// NUMERIC type does not reject a legal max-energy/min-delta row that NUMERIC(8,3)
// would have rejected. Each subtest gets a fresh uuid.New() account id (Test
// Contract "Fixture hygiene").
func TestCreate_InferredCapacity_TableCases(t *testing.T) {
	cases := []entryCapacityCase{
		// T1: ticket worked example 2 (its "70.40").
		{id: "T1", energyAddedKWh: 7.04, startPct: ptrInt(64), endPct: ptrInt(74), want: ptrFloat64(70.400)},
		// T2: ticket worked example 3. Ticket prose says "66.63" — its own 2-decimal
		// restatement of 66.6290...; at scale 3 the value is 66.629. Do NOT "correct"
		// this to 66.63 (design.md §"What must NOT change").
		{id: "T2", energyAddedKWh: 41.31, startPct: ptrInt(18), endPct: ptrInt(80), want: ptrFloat64(66.629)},
		// T3: ticket worked example 1 AS THIS TABLE CAN STORE IT. energy_added_kwh is
		// NUMERIC(6,2), so the ticket's 52.273 is not representable here and rounds to
		// 52.27 on insert, giving 73.620 — NOT 73.624 (that figure belongs to T13 on
		// supercharger_sessions, whose energy_kwh is full DOUBLE PRECISION).
		{id: "T3", energyAddedKWh: 52.27, startPct: ptrInt(29), endPct: ptrInt(100), want: ptrFloat64(73.620)},
		// T4: a missing start no longer stays missing. The module derives it from
		// the energy and the ending percentage, so the delta is complete and the
		// column has a value. The NULL-on-missing-start rule itself is unchanged
		// and still covered by T6, where no derivation is possible.
		{id: "T4", energyAddedKWh: 7.04, startPct: nil, endPct: ptrInt(74), wantDerivedStart: true},
		// T5: missing end (D3).
		{id: "T5", energyAddedKWh: 7.04, startPct: ptrInt(64), endPct: nil, want: nil},
		// T6: both missing (D3).
		{id: "T6", energyAddedKWh: 7.04, startPct: nil, endPct: nil, want: nil},
		// T7: zero delta (D3). MUST insert successfully — must NOT raise division_by_zero.
		{id: "T7", energyAddedKWh: 7.04, startPct: ptrInt(74), endPct: ptrInt(74), want: nil},
		// T8: negative delta (D3). Row inserts; no negative value is stored.
		{id: "T8", energyAddedKWh: 7.04, startPct: ptrInt(74), endPct: ptrInt(64), want: nil},
		// T9: minimum legal energy, maximum delta — smallest producible value, proves
		// scale 3 survives a sub-unit result.
		{id: "T9", energyAddedKWh: 0.01, startPct: ptrInt(0), endPct: ptrInt(100), want: ptrFloat64(0.010)},
		// T10: maximum legal energy, minimum positive delta. MUST insert successfully —
		// the D4 regression test: under the originally proposed NUMERIC(8,3) this
		// insert fails with "numeric field overflow". If this ever starts failing, the
		// column's type was narrowed.
		{id: "T10", energyAddedKWh: 9999.99, startPct: ptrInt(0), endPct: ptrInt(1), want: ptrFloat64(999999.000)},
	}

	pool := newTestPool(t)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	r := charging.NewReader(pool)

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			accountID := uuid.New()
			cleanupAccount(t, pool, accountID)
			const teslaID = int64(250001)

			e := minEntry(accountID, teslaID)
			e.EnergyAddedKWh = ptrFloat64(tc.energyAddedKWh)
			e.StartBatteryPct = tc.startPct
			e.EndBatteryPct = tc.endPct

			created, err := w.Create(ctx, e)
			if err != nil {
				t.Fatalf("%s: Create: expected success, got error: %v", tc.id, err)
			}

			got, err := r.ListEntriesByVehicle(ctx, teslaID, 10)
			if err != nil {
				t.Fatalf("%s: ListEntriesByVehicle: %v", tc.id, err)
			}
			if len(got) != 1 {
				t.Fatalf("%s: expected 1 entry, got %d", tc.id, len(got))
			}
			if got[0].ID != created.ID {
				t.Fatalf("%s: round-trip ID mismatch: got %v, want %v", tc.id, got[0].ID, created.ID)
			}

			if tc.wantDerivedStart {
				if got[0].StartBatteryPct == nil {
					t.Fatalf("%s: StartBatteryPct = nil, want a derived value", tc.id)
				}
				if got[0].StartBatterySource == nil || *got[0].StartBatterySource != charging.StartBatterySourceEstimated {
					t.Errorf("%s: StartBatterySource = %v, want ESTIMATED", tc.id, got[0].StartBatterySource)
				}
				if got[0].InferredCapacityKWhCalc == nil {
					t.Errorf("%s: InferredCapacityKWhCalc = nil, want a value: a derived starting percentage completes the delta", tc.id)
				}
				return
			}

			// T24 (for T1/T7): the domain-mapping proof is this same read-back —
			// Reader.ListEntriesByVehicle already routes the row through rowToEntry's
			// pgNumericToFloat64Ptr mapping (design.md D7/D8).
			assertFloatPtrApprox(t, tc.id+": InferredCapacityKWhCalc", got[0].InferredCapacityKWhCalc, tc.want)
		})
	}
}

// TestUpdate_InferredCapacity_RecomputesOnEndBatteryPctChange implements T11:
// creates T1's row (7.04, 64->74, value 70.400), then Writer.Update changes
// EndBatteryPct to 84, energy unchanged. Expect 35.200 (7.04 / 0.20). Proves the
// value tracks an UPDATE even though UpdateEntry's SET clause never names the
// column and no Go code computes it (design.md D2).
func TestUpdate_InferredCapacity_RecomputesOnEndBatteryPctChange(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)
	const teslaID = int64(250011)

	e := minEntry(accountID, teslaID)
	e.EnergyAddedKWh = ptrFloat64(7.04)
	e.StartBatteryPct = ptrInt(64)
	e.EndBatteryPct = ptrInt(74)

	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	assertFloatPtrApprox(t, "T11: pre-update InferredCapacityKWhCalc", created.InferredCapacityKWhCalc, ptrFloat64(70.400))

	updated := created
	updated.EndBatteryPct = ptrInt(84)

	result, err := w.Update(ctx, refFor(teslaID), updated)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	assertFloatPtrApprox(t, "T11: post-update InferredCapacityKWhCalc", result.InferredCapacityKWhCalc, ptrFloat64(35.200))
}

// TestInferredCapacity_Entries_ColumnUnwritable implements T12: a direct SQL write
// to the generated column is rejected by the database itself with SQLSTATE 428C9
// (design.md D2, "Write-protection, reproduced"). No port can express this write —
// CreateEntry/UpdateEntry name their columns explicitly and neither includes it
// (design.md D8) — so this is the one case in this file needing direct SQL.
func TestInferredCapacity_Entries_ColumnUnwritable(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupAccount(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewWriter(pool)

	e := minEntry(accountID, 250012)
	e.EnergyAddedKWh = ptrFloat64(7.04)
	e.StartBatteryPct = ptrInt(64)
	e.EndBatteryPct = ptrInt(74)
	created, err := w.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	_, err = pool.Exec(ctx,
		"UPDATE charging.manual_charge_entries SET inferred_capacity_kwh_calc = 1 WHERE id = $1",
		created.ID)
	assertPgErrorCode(t, err, "428C9")
}
