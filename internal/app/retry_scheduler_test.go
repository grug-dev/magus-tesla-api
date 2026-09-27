package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// forVehiclesCall is one recorded ProcessVehicleDataForVehicles call.
type forVehiclesCall struct {
	triggeredBy telemetry.TriggeredBy
	teslaIDs    []int64
}

// fakeRetryCycle satisfies Processor. ProcessVehicleData is unreachable
// from RetryScheduler and stubs to a zero report. ProcessVehicleDataForVehicles
// records every call it receives — or, when explodes is set, fails the test
// immediately — so a test can prove exactly when, and with what arguments,
// RetryScheduler calls it.
type fakeRetryCycle struct {
	t        *testing.T
	explodes bool
	calls    []forVehiclesCall
	report   telemetry.CycleReport
	err      error
}

func (f *fakeRetryCycle) ProcessVehicleData(context.Context, telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	return telemetry.CycleReport{}, nil
}

func (f *fakeRetryCycle) ProcessVehicleDataForVehicles(_ context.Context, triggeredBy telemetry.TriggeredBy, teslaIDs []int64) (telemetry.CycleReport, error) {
	if f.explodes {
		f.t.Fatal("ProcessVehicleDataForVehicles must not be called")
	}
	f.calls = append(f.calls, forVehiclesCall{triggeredBy: triggeredBy, teslaIDs: teslaIDs})
	return f.report, f.err
}

var _ Processor = (*fakeRetryCycle)(nil)

// fakeNotDoneVehicles satisfies NotDoneVehicles. Records the teslaIDs and
// date it was called with, and returns a configurable (ids, error).
type fakeNotDoneVehicles struct {
	ids []int64
	err error

	calls       int
	gotTeslaIDs []int64
	gotDate     time.Time
}

func (f *fakeNotDoneVehicles) NotDone(_ context.Context, teslaIDs []int64, date time.Time) ([]int64, error) {
	f.calls++
	f.gotTeslaIDs = teslaIDs
	f.gotDate = date
	return f.ids, f.err
}

var _ NotDoneVehicles = (*fakeNotDoneVehicles)(nil)

// TestRetryScheduler_EmptyNotDoneCallsNothing proves an empty NotDone result
// makes no Processor call — the fake fails the test if it is called — so a
// finished night's tick writes nothing.
func TestRetryScheduler_EmptyNotDoneCallsNothing(t *testing.T) {
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(5), ownedVehicle(7), ownedVehicle(9)}}
	notDone := &fakeNotDoneVehicles{}
	proc := &fakeRetryCycle{t: t, explodes: true}
	fixed := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	sched := NewRetryScheduler(proc, notDone, acct, time.UTC, telemetry.Config{Clock: func() time.Time { return fixed }})

	sched.tick(context.Background())

	if notDone.calls != 1 {
		t.Fatalf("NotDone calls = %d, want 1", notDone.calls)
	}
}

// TestRetryScheduler_NonEmptyNotDoneCallsProcessorWithExactSet proves NotDone
// naming one vehicle out of a larger registered set calls
// ProcessVehicleDataForVehicles exactly once, with exactly that vehicle and
// telemetry.TriggeredByRetry.
func TestRetryScheduler_NonEmptyNotDoneCallsProcessorWithExactSet(t *testing.T) {
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(5), ownedVehicle(7), ownedVehicle(9)}}
	notDone := &fakeNotDoneVehicles{ids: []int64{7}}
	proc := &fakeRetryCycle{t: t}
	fixed := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	sched := NewRetryScheduler(proc, notDone, acct, time.UTC, telemetry.Config{Clock: func() time.Time { return fixed }})

	sched.tick(context.Background())

	if !slices.Equal(notDone.gotTeslaIDs, []int64{5, 7, 9}) {
		t.Errorf("NotDone's teslaIDs = %v, want [5 7 9] (every registered vehicle)", notDone.gotTeslaIDs)
	}
	if len(proc.calls) != 1 {
		t.Fatalf("ProcessVehicleDataForVehicles calls = %d, want 1", len(proc.calls))
	}
	got := proc.calls[0]
	if !slices.Equal(got.teslaIDs, []int64{7}) {
		t.Errorf("teslaIDs = %v, want [7]", got.teslaIDs)
	}
	if got.triggeredBy != telemetry.TriggeredByRetry {
		t.Errorf("triggeredBy = %v, want %v", got.triggeredBy, telemetry.TriggeredByRetry)
	}
}

// TestRetryScheduler_NotDoneErrorSkipsTickButNotTheNextOne proves a NotDone
// error is logged and the tick is skipped, without blocking the next tick
// from running normally.
func TestRetryScheduler_NotDoneErrorSkipsTickButNotTheNextOne(t *testing.T) {
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(7)}}
	notDone := &fakeNotDoneVehicles{err: errors.New("db down")}
	proc := &fakeRetryCycle{t: t, explodes: true}
	fixed := time.Date(2026, 9, 15, 4, 0, 0, 0, time.UTC)
	sched := NewRetryScheduler(proc, notDone, acct, time.UTC, telemetry.Config{Clock: func() time.Time { return fixed }})

	sched.tick(context.Background()) // first tick: NotDone fails, must not call the processor

	notDone.err = nil
	notDone.ids = []int64{7}
	proc.explodes = false
	sched.tick(context.Background()) // second tick: succeeds normally

	if len(proc.calls) != 1 {
		t.Errorf("ProcessVehicleDataForVehicles calls after the second tick = %d, want 1 (the first bad tick never blocks the next)", len(proc.calls))
	}
}

// TestNewRetryScheduler_NilLocationDefaultsToClockZone mirrors Scheduler's
// own nil-location test.
func TestNewRetryScheduler_NilLocationDefaultsToClockZone(t *testing.T) {
	sched := NewRetryScheduler(&fakeRetryCycle{}, &fakeNotDoneVehicles{}, &fakeAccountEmpty{}, nil, telemetry.Config{})
	if sched.loc != clock.Zone() {
		t.Errorf("nil location should default to clock.Zone(), got %v", sched.loc)
	}
}
