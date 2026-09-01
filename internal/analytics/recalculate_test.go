package analytics

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// These tests pin design.md D8b (the widened charge-source fetch) and D7 (a
// SnapshotPrecedingDay failure aborts Recalculate rather than being swallowed
// into a predecessor-less row), fully OFFLINE against the same hand-written
// port fakes reader_test.go already defines (fakeTelemetryReader,
// fakeSuperchargerReader, fakeManualReader) -- no DB, no *pgxpool.Pool.
//
// Recalculate's write half (the UPSERT/DELETE transaction) needs a real
// database and is exercised by db_integration_test.go (Wave 6b, DB-gated,
// not this worker's task). What CAN be tested offline is everything BEFORE
// that transaction begins: the three fetches (snapshots, the
// SnapshotPrecedingDay lookup, and the two charge-source fetches) all go
// through this recalculator's telemetry/supercharger/manual port fields,
// which are plain interfaces. Each test below forces the LAST of those
// fetches (charging.Reader.ListEntriesByVehicleBetween, or, for the D7 test,
// SnapshotPrecedingDay itself) to fail, so Recalculate returns with a wrapped
// error before ever reaching r.pool.Begin(ctx) -- pool and q are left nil
// below on purpose; a bug that let execution reach the transaction would
// panic on the nil pool rather than silently pass, which is an acceptable
// (loud) failure mode for a worker task that must never touch a real
// database.
//
// The fakes record their "got" bind values BEFORE checking their own
// err field (reader_test.go's existing convention), so the forced failure
// does not prevent asserting on the bounds each fetch actually received.
//
// Per the Test-Execution-Policy, these tests are written but NOT run by the
// worker; go vet ./... compiles them as a signature-drift signal. The owner
// runs `go test ./internal/analytics/...` and reports the result.

var errRecalculateFakeBoom = errors.New("recalculate_test: forced fake failure")

// TestRecalculate_FetchWindow_NoGap_WidensChargeStartByOneDayPastLookback pins
// design.md D8b's "Correction (leader, wave-2 reconcile)": in the steady
// state -- preceding is "the ordinary one-day lookback row", CapturedDate ==
// start-1 day -- effectiveDay(preceding) == start-2 days, which IS
// Before(lookbackStart == start-1 day), so chargeStart drops to start-2 days
// on every call. An implementation still carrying the withdrawn
// "chargeStart == lookbackStart in the steady state" claim fails this test.
func TestRecalculate_FetchWindow_NoGap_WidensChargeStartByOneDayPastLookback(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	start := day(2026, 8, 20)
	end := day(2026, 8, 20)
	lookbackStart := start.AddDate(0, 0, -1) // 2026-08-19

	preceding := telemetry.Snapshot{CapturedDate: lookbackStart} // "the ordinary one-day lookback row"
	telemetryFake := &fakeTelemetryReader{
		snapshots: []telemetry.Snapshot{{CapturedDate: start}}, // non-empty: only its presence matters here
		preceding: &preceding,
	}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{err: errRecalculateFakeBoom} // forces an early return before r.pool.Begin

	r := &recalculator{telemetry: telemetryFake, supercharger: superchargerFake, manual: manualFake}

	err := r.Recalculate(context.Background(), accountID, teslaID, start, end)
	if !errors.Is(err, errRecalculateFakeBoom) {
		t.Fatalf("Recalculate error: want wrapped errRecalculateFakeBoom, got %v", err)
	}

	wantChargeStart := start.AddDate(0, 0, -2) // start-2d, NOT lookbackStart (start-1d)
	if !superchargerFake.gotBetweenStart.Equal(wantChargeStart) {
		t.Errorf("supercharger fetch start: want %v (start-2d), got %v", wantChargeStart, superchargerFake.gotBetweenStart)
	}
	if !manualFake.gotBetweenStart.Equal(wantChargeStart) {
		t.Errorf("manual entries fetch start: want %v (start-2d), got %v", wantChargeStart, manualFake.gotBetweenStart)
	}
	if superchargerFake.gotBetweenStart.Equal(lookbackStart) {
		t.Error("supercharger fetch start equals lookbackStart (start-1d) -- the withdrawn design.md D8b claim, not the corrected one")
	}

	wantSuperchargerEnd := end.AddDate(0, 0, 2)
	if !superchargerFake.gotBetweenEnd.Equal(wantSuperchargerEnd) {
		t.Errorf("supercharger fetch end: want %v (end+2d, unchanged), got %v", wantSuperchargerEnd, superchargerFake.gotBetweenEnd)
	}
	if !manualFake.gotBetweenEnd.Equal(end) {
		t.Errorf("manual entries fetch end: want %v (end, unchanged), got %v", end, manualFake.gotBetweenEnd)
	}
}

// TestRecalculate_FetchWindow_RealGap_WidensToPrecedingsEffectiveDay pins
// design.md D8b's actual gap-widening behaviour, using the same dates as
// design.md's Test Contract Fixture D: a seven-day capture gap. chargeStart
// must drop all the way to effectiveDay(preceding) = 2026-07-31, not merely
// to start-2d.
func TestRecalculate_FetchWindow_RealGap_WidensToPrecedingsEffectiveDay(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	start := day(2026, 8, 7)
	end := day(2026, 8, 7)

	preceding := telemetry.Snapshot{CapturedDate: day(2026, 8, 1)} // seven days before the fetch window
	telemetryFake := &fakeTelemetryReader{
		snapshots: []telemetry.Snapshot{{CapturedDate: day(2026, 8, 8)}},
		preceding: &preceding,
	}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{err: errRecalculateFakeBoom}

	r := &recalculator{telemetry: telemetryFake, supercharger: superchargerFake, manual: manualFake}

	err := r.Recalculate(context.Background(), accountID, teslaID, start, end)
	if !errors.Is(err, errRecalculateFakeBoom) {
		t.Fatalf("Recalculate error: want wrapped errRecalculateFakeBoom, got %v", err)
	}

	wantChargeStart := day(2026, 7, 31) // effectiveDay(2026-08-01) = clock.CalendarDay(2026-08-01, time.UTC) - 1
	if !superchargerFake.gotBetweenStart.Equal(wantChargeStart) {
		t.Errorf("supercharger fetch start: want %v, got %v", wantChargeStart, superchargerFake.gotBetweenStart)
	}
	if !manualFake.gotBetweenStart.Equal(wantChargeStart) {
		t.Errorf("manual entries fetch start: want %v, got %v", wantChargeStart, manualFake.gotBetweenStart)
	}
}

// TestRecalculate_FetchWindow_NoPredecessor_UsesLookbackStart pins the guard
// clause `if preceding != nil` in Recalculate: when the vehicle has no
// predecessor at all (SnapshotPrecedingDay returns (nil, nil), the fake's
// zero value), chargeStart stays at the ordinary lookbackStart (start-1d) --
// there is nothing to widen against.
func TestRecalculate_FetchWindow_NoPredecessor_UsesLookbackStart(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	start := day(2026, 8, 4)
	end := day(2026, 8, 4)
	lookbackStart := start.AddDate(0, 0, -1)

	telemetryFake := &fakeTelemetryReader{
		snapshots: []telemetry.Snapshot{{CapturedDate: start}},
		// preceding left at its zero value: nil, nil -- no predecessor anywhere.
	}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{err: errRecalculateFakeBoom}

	r := &recalculator{telemetry: telemetryFake, supercharger: superchargerFake, manual: manualFake}

	err := r.Recalculate(context.Background(), accountID, teslaID, start, end)
	if !errors.Is(err, errRecalculateFakeBoom) {
		t.Fatalf("Recalculate error: want wrapped errRecalculateFakeBoom, got %v", err)
	}

	if !superchargerFake.gotBetweenStart.Equal(lookbackStart) {
		t.Errorf("supercharger fetch start: want %v (lookbackStart, unwidened), got %v", lookbackStart, superchargerFake.gotBetweenStart)
	}
	if !manualFake.gotBetweenStart.Equal(lookbackStart) {
		t.Errorf("manual entries fetch start: want %v (lookbackStart, unwidened), got %v", lookbackStart, manualFake.gotBetweenStart)
	}
}

// TestRecalculate_SnapshotPrecedingDayError_AbortsBeforeChargeFetches pins
// design.md D7: a SnapshotPrecedingDay failure must abort Recalculate with a
// wrapped error, and must NEVER be degraded into "no predecessor" (which
// would silently NULL out a real vehicle's figures on a transient DB
// hiccup). Asserted two ways: the returned error wraps the fake's forced
// error, AND neither charge-source fetch ever ran -- their fakes' recorded
// bind times stay at the zero time.Time, proving Recalculate returned before
// reaching them.
func TestRecalculate_SnapshotPrecedingDayError_AbortsBeforeChargeFetches(t *testing.T) {
	accountID := uuid.New()
	const teslaID = int64(42)

	start := day(2026, 8, 4)
	end := day(2026, 8, 4)

	telemetryFake := &fakeTelemetryReader{
		snapshots:    []telemetry.Snapshot{{CapturedDate: start}},
		precedingErr: errRecalculateFakeBoom,
	}
	superchargerFake := &fakeSuperchargerReader{}
	manualFake := &fakeManualReader{}

	r := &recalculator{telemetry: telemetryFake, supercharger: superchargerFake, manual: manualFake}

	err := r.Recalculate(context.Background(), accountID, teslaID, start, end)
	if !errors.Is(err, errRecalculateFakeBoom) {
		t.Fatalf("Recalculate error: want wrapped errRecalculateFakeBoom, got %v", err)
	}

	if !superchargerFake.gotBetweenStart.IsZero() {
		t.Errorf("supercharger fetch was called (gotBetweenStart=%v) -- a SnapshotPrecedingDay error must abort BEFORE the charge-source fetches", superchargerFake.gotBetweenStart)
	}
	if !manualFake.gotBetweenStart.IsZero() {
		t.Errorf("manual entries fetch was called (gotBetweenStart=%v) -- a SnapshotPrecedingDay error must abort BEFORE the charge-source fetches", manualFake.gotBetweenStart)
	}
}
