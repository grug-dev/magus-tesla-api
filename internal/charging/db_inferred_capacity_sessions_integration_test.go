// Package charging_test — database-backed integration tests for
// supercharger_sessions.inferred_capacity_kwh_calc (MAG-25, charging-add-inferred-capacity),
// covering design.md's Test Contract **Group B (T13-T23)** (tasks.md task 3.2).
//
// This is a GENERATED ALWAYS AS (...) STORED column (design.md D2): nothing in Go
// computes it, and the database itself recomputes it on every write path that
// touches energy_kwh / start_battery_pct / end_battery_pct. Every expected value
// below is copied VERBATIM from design.md §Test Contract, fixed BEFORE this file
// was written (ai/go-conventions.md §Testing authoring order) — not derived by
// running this code. Do not "correct" a value to match the ticket's 2-decimal
// prose; design.md §"What must NOT change" explicitly forbids it.
//
// SessionMirror carries NO percentage fields (RM29 design.md D6), so every fixture
// here is seeded in two steps: SessionWriter.MirrorSessions for the mirrored facts
// (identity, window, energy), then SessionVerifier.VerifySession for the
// percentages — which is itself the mechanism T20/T21 test directly. Reads go
// through SessionReader.ListSessionsByVehicleBetween (fetchSessionsByVehicleBetween,
// db_session_reader_integration_test.go) except T20, which asserts on
// VerifySession's own RETURNING * result to prove freshness, and T22, which needs
// direct SQL since no port can express a write to a generated column. Assertions
// are ONLY against charging.Session domain fields and, for T22, a *pgconn.PgError's
// SQLSTATE — pgtype NEVER appears in this file (internal/charging/AGENTS.md
// §Testing Notes).
//
// session_ids are in the 960001-960099 range, disjoint from RM29's 920001-920099,
// RM30's 940001-940099, RM31's 950001-950099, and the real backfilled 734860294.
//
// Test -> Test Contract case mapping:
//
//	T13-T19  TestMirrorAndVerify_InferredCapacity_TableCases (table-driven, one subtest each)
//	T20, T21 TestVerifyThenRemirror_InferredCapacity_RecomputesBothWays
//	T22      TestInferredCapacity_Sessions_ColumnUnwritable
//	T23      proven by the T13 and T17 subtests of
//	         TestMirrorAndVerify_InferredCapacity_TableCases, which already read
//	         back through SessionReader.ListSessionsByVehicleBetween and assert the
//	         non-nil/nil InferredCapacityKWhCalc mapping — no separate test needed.
package charging_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
)

// sessionCapacityCase is one row of design.md §Test Contract Group B (T13-T19).
type sessionCapacityCase struct {
	id        string // Test Contract case ID, e.g. "T13"
	energyKWh *float64
	startPct  *int
	endPct    *int
	want      *float64 // nil means the expected InferredCapacityKWhCalc is NULL
}

// TestMirrorAndVerify_InferredCapacity_TableCases implements design.md §Test
// Contract Group B, T13-T19, verbatim. Every case must MIRROR successfully — T19
// is the D4 regression test explicitly required to assert MirrorSessions succeeds
// with an unbounded DOUBLE PRECISION input: under any fixed NUMERIC precision this
// row would abort the WHOLE nightly mirror transaction (RM29: one bad entry
// rejects the entire call). T17 is the equivalent zero-delta regression: it must
// mirror successfully with no aborted transaction.
//
// SessionMirror has no percentage fields, so every case is seeded in two steps:
// MirrorSessions for the mirrored facts, then SessionVerifier.VerifySession for
// whichever percentages the case needs (nil for the side each of T15/T16 omits).
// T14 seeds BOTH percentages (29, 100) via VerifySession even though it also
// expects NULL — the case exists specifically to isolate D4's "no kWh fee" guard
// from D3's "missing percentage" guards, so the fixture must not conflate the two
// by leaving the percentages unset. Each subtest gets a fresh uuid.New() account id
// (Test Contract "Fixture hygiene").
func TestMirrorAndVerify_InferredCapacity_TableCases(t *testing.T) {
	cases := []sessionCapacityCase{
		// T13: ticket worked example 1, the Supercharger row, at full DOUBLE PRECISION
		// input. Ticket prose says "73.63" — its own imprecise 2-decimal rounding of
		// 73.6239...; at scale 3 the value is 73.624. Do NOT "correct" this to 73.63
		// (design.md §"What must NOT change"). Also proves the float8->numeric cast
		// takes the shortest round-trip decimal (52.273, not 52.27299999...).
		{id: "T13", energyKWh: ptrFloat64(52.273), startPct: ptrIntV(29), endPct: ptrIntV(100), want: ptrFloat64(73.624)},
		// T14: a session with no kWh fee (D4). Percentages ARE set (see doc comment
		// above) so this case tests the energy-null guard in isolation.
		{id: "T14", energyKWh: nil, startPct: ptrIntV(29), endPct: ptrIntV(100), want: nil},
		// T15: missing start (D3). VerifySession called with one nil argument.
		// Corrected by MAG-36 (charging-add-derived-start-battery-pct design.md
		// D12/C2): energyKWh changed from 52.273 to 124.0 (exactly 2 x the 62.0 kWh
		// capacity constant, chosen so the arithmetic is legible by inspection). At
		// the old 52.273 value, VerifySession's new derivation would compute a start
		// percentage of 16 (in range) instead of leaving it absent, breaking this
		// case's precondition through a cascading mechanism T15 never anticipated.
		// At 124.0, derivedStartBatteryPct(62.0, 124.0, 100) computes
		// 100 - 124.0/62.0*100 = -100.0, out of range -- so start_battery_pct stays
		// genuinely NULL and want: nil is reached via design.md D3's out-of-range
		// guard rather than "the start percentage was never touched."
		{id: "T15", energyKWh: ptrFloat64(124.0), startPct: nil, endPct: ptrIntV(100), want: nil},
		// T16: missing end (D3). VerifySession called with one nil argument.
		{id: "T16", energyKWh: ptrFloat64(52.273), startPct: ptrIntV(29), endPct: nil, want: nil},
		// T17: zero delta (D3). MUST mirror successfully — no aborted transaction.
		{id: "T17", energyKWh: ptrFloat64(52.273), startPct: ptrIntV(50), endPct: ptrIntV(50), want: nil},
		// T18: negative delta (D3).
		{id: "T18", energyKWh: ptrFloat64(52.273), startPct: ptrIntV(80), endPct: ptrIntV(18), want: nil},
		// T19: unbounded DOUBLE PRECISION source. MUST mirror successfully. The D4
		// regression test for this table: any fixed precision would abort the whole
		// mirror transaction here (design.md Context 6).
		{id: "T19", energyKWh: ptrFloat64(1000000000), startPct: ptrIntV(0), endPct: ptrIntV(1), want: ptrFloat64(100000000000.000)},
	}

	pool := newTestPool(t)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	v := charging.NewSessionVerifier(pool)

	baseStop := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	for i, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			accountID := uuid.New()
			cleanupChargingSuperchargerSessions(t, pool, accountID)
			sessionID := int64(960013 + i) // T13->960013 ... T19->960019
			teslaID := sessionID

			m := charging.SessionMirror{
				AccountID:           accountID,
				VIN:                 "VCAP",
				TeslaID:             ptrInt64(teslaID),
				SessionID:           sessionID,
				ChargeStartDateTime: baseStop.Add(-time.Hour),
				ChargeStopDateTime:  baseStop,
				SiteLocationName:    "Capacity Test Site",
				EnergyKWh:           tc.energyKWh,
			}
			if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
				t.Fatalf("%s: MirrorSessions: expected success, got error: %v", tc.id, err)
			}

			if tc.startPct != nil || tc.endPct != nil {
				id := fetchSuperchargerSessionID(t, pool, accountID, sessionID)
				if _, err := v.VerifySession(ctx, accountID, id, tc.startPct, tc.endPct); err != nil {
					t.Fatalf("%s: VerifySession: %v", tc.id, err)
				}
			}

			sessions := fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)
			if len(sessions) != 1 {
				t.Fatalf("%s: expected 1 session, got %d", tc.id, len(sessions))
			}

			// T23 (for T13/T17): the domain-mapping proof is this same read-back —
			// ListSessionsByVehicleBetween already routes the row through
			// rowToSession's pgNumericToFloat64Ptr mapping (design.md D7/D8).
			assertFloatPtrApprox(t, tc.id+": InferredCapacityKWhCalc", sessions[0].InferredCapacityKWhCalc, tc.want)
		})
	}
}

// TestVerifyThenRemirror_InferredCapacity_RecomputesBothWays implements T20 and
// T21, chained on the same session as design.md's dependency requires.
//
// T20 is the load-bearing case of the whole change: MirrorSessions a session with
// EnergyKWh = 41.31 and no percentages -> InferredCapacityKWhCalc is nil. Then
// SessionVerifier.VerifySession(ctx, accountID, id, ptr(18), ptr(80)) -> expect
// 66.629, asserted on VerifySession's OWN returned charging.Session so the
// RETURNING * freshness is proven too. VerifySuperchargerSession's SET clause names only
// start_battery_pct, end_battery_pct, battery_pct_source and updated_at — no Go
// code anywhere computes capacity, so the value appears purely because the engine
// recomputed it (direct proof of design.md D2).
//
// T21 re-mirrors the SAME (account_id, session_id) with EnergyKWh = 44.64 (the
// nightly ON CONFLICT DO UPDATE SET refresh path). Expect 72.000 (44.64 / 0.62).
// Proves the value tracks the nightly fee-settlement refresh, and — together with
// T20 — that both write paths touching the three inputs from opposite directions
// keep the column correct.
func TestVerifyThenRemirror_InferredCapacity_RecomputesBothWays(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)
	verifier := charging.NewSessionVerifier(pool)

	const sessionID = int64(960020)
	const teslaID = sessionID
	stop := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	from := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VVERIFYCAP",
		TeslaID:             ptrInt64(teslaID),
		SessionID:           sessionID,
		ChargeStartDateTime: stop.Add(-time.Hour),
		ChargeStopDateTime:  stop,
		SiteLocationName:    "T20/T21 Site",
		EnergyKWh:           ptrFloat64(41.31),
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("initial MirrorSessions: %v", err)
	}

	// T20, first half: no percentages yet -> nil.
	sessions := fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)
	if len(sessions) != 1 {
		t.Fatalf("T20: expected 1 session, got %d", len(sessions))
	}
	assertFloatPtrApprox(t, "T20: InferredCapacityKWhCalc before verification", sessions[0].InferredCapacityKWhCalc, nil)

	id := fetchSuperchargerSessionID(t, pool, accountID, sessionID)
	time.Sleep(mirrorGap)

	// T20, second half: verify sets the percentages; the value the engine computes
	// must appear on VerifySession's OWN returned Session (RETURNING * freshness).
	verified, err := verifier.VerifySession(ctx, accountID, id, ptrIntV(18), ptrIntV(80))
	if err != nil {
		t.Fatalf("T20: VerifySession: %v", err)
	}
	assertFloatPtrApprox(t, "T20: InferredCapacityKWhCalc on VerifySession's returned Session", verified.InferredCapacityKWhCalc, ptrFloat64(66.629))

	time.Sleep(mirrorGap)

	// T21: a nightly re-mirror with a settled fee refreshes energy_kwh, which
	// recomputes the column again — the opposite write path from T20.
	remirror := m
	remirror.EnergyKWh = ptrFloat64(44.64)
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{remirror}); err != nil {
		t.Fatalf("T21: re-mirror: %v", err)
	}

	sessions = fetchSessionsByVehicleBetween(t, pool, accountID, teslaID, from, to)
	if len(sessions) != 1 {
		t.Fatalf("T21: expected 1 session, got %d", len(sessions))
	}
	assertFloatPtrApprox(t, "T21: InferredCapacityKWhCalc after re-mirror", sessions[0].InferredCapacityKWhCalc, ptrFloat64(72.000))
}

// TestInferredCapacity_Sessions_ColumnUnwritable implements T22: a direct SQL
// write to the generated column is rejected by the database itself with SQLSTATE
// 428C9 (design.md D2, "Write-protection, reproduced") — same shape as T12 on
// manual_charge_entries. No port can express this write: MirrorSuperchargerSession and
// VerifySuperchargerSession name their columns explicitly and neither includes this one
// (design.md D8).
func TestInferredCapacity_Sessions_ColumnUnwritable(t *testing.T) {
	pool := newTestPool(t)
	accountID := uuid.New()
	cleanupChargingSuperchargerSessions(t, pool, accountID)
	ctx := context.Background()
	w := charging.NewSessionWriter(pool)

	const sessionID = int64(960022)
	stop := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	m := charging.SessionMirror{
		AccountID:           accountID,
		VIN:                 "VUNWRITABLE",
		TeslaID:             ptrInt64(sessionID),
		SessionID:           sessionID,
		ChargeStartDateTime: stop.Add(-time.Hour),
		ChargeStopDateTime:  stop,
		SiteLocationName:    "T22 Site",
		EnergyKWh:           ptrFloat64(10.0),
	}
	if err := w.MirrorSessions(ctx, accountID, []charging.SessionMirror{m}); err != nil {
		t.Fatalf("MirrorSessions: %v", err)
	}
	id := fetchSuperchargerSessionID(t, pool, accountID, sessionID)

	_, err := pool.Exec(ctx,
		"UPDATE charging.supercharger_sessions SET inferred_capacity_kwh_calc = 1 WHERE id = $1",
		id)
	assertPgErrorCode(t, err, "428C9")
}
