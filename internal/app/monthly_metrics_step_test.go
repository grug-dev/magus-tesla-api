package app

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// This file covers design.md's Test Contract Group A (monthlyMetricsPeriods,
// pure, no fakes, no DB), Group B (callMonthlySyncer, fake port, no clock)
// and part of Group C (runMonthlyMetricsStep's own vehicle enumeration and
// per-(vehicle, period) isolation). The expected values below are copied
// from design.md's Test Contract, not derived from the implementation.

// --- Group B fake: fakeMonthlySyncer ---

// syncCall is one recorded (teslaID, period) call to fakeMonthlySyncer.
type syncCall struct {
	teslaID int64
	period  time.Time
}

// fakeMonthlySyncer satisfies analytics.MonthlySyncer. It records every call
// it receives, as a slice — unlike most fakes in this package, this port is
// called multiple times per invocation (two periods per vehicle) — and looks
// up its (result, error) for a call from a map keyed by (teslaID, period),
// falling back to a zero-value success when no override is configured.
type fakeMonthlySyncer struct {
	calls []syncCall
	errs  map[syncCall]error
}

func (f *fakeMonthlySyncer) SyncMonth(_ context.Context, teslaID int64, period time.Time) (analytics.VehicleMonthlyMetrics, error) {
	call := syncCall{teslaID: teslaID, period: period}
	f.calls = append(f.calls, call)
	if err, ok := f.errs[call]; ok {
		return analytics.VehicleMonthlyMetrics{}, err
	}
	return analytics.VehicleMonthlyMetrics{}, nil
}

var _ analytics.MonthlySyncer = (*fakeMonthlySyncer)(nil)

// failOn configures the fake to return err for exactly this (teslaID, period)
// call, leaving every other call to succeed.
func (f *fakeMonthlySyncer) failOn(teslaID int64, period time.Time, err error) {
	if f.errs == nil {
		f.errs = make(map[syncCall]error)
	}
	f.errs[syncCall{teslaID: teslaID, period: period}] = err
}

// --- Group A — monthlyMetricsPeriods (pure, no fakes, no DB) ---

// TestMonthlyMetricsPeriods covers design.md Test Contract A1-A6. loc is
// clock.Zone() (America/Bogota) for every case except A6, which proves loc
// is a real parameter, not hardcoded.
func TestMonthlyMetricsPeriods(t *testing.T) {
	bogota := clock.Zone()

	cases := []struct {
		id           string
		now          time.Time
		loc          *time.Location
		wantCurrent  time.Time
		wantPrevious time.Time
	}{
		{
			id:           "A1 plain case: today is the 1st",
			now:          time.Date(2026, 9, 1, 0, 30, 0, 0, bogota),
			loc:          bogota,
			wantCurrent:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			id:           "A2 mid-month still normalizes to the 1st of the same month",
			now:          time.Date(2026, 9, 15, 12, 0, 0, 0, bogota),
			loc:          bogota,
			wantCurrent:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			id:           "A3 year boundary: previous month of January is December of the prior year",
			now:          time.Date(2027, 1, 1, 10, 0, 0, 0, bogota),
			loc:          bogota,
			wantCurrent:  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// Load-bearing case: the UTC calendar day is the 1st of September,
			// but the Bogota calendar day is still the 31st of August (UTC-5).
			// A naive UTC-day check would wrongly report September as current.
			id:           "A4 UTC day is the 1st but Bogota day is still the 31st",
			now:          time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC),
			loc:          bogota,
			wantCurrent:  time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			// Zone-aware counterpart to A4: this UTC instant is 2026-09-01
			// 01:00 in Bogota, so September is correctly current — because it
			// IS the 1st in Bogota, not by coincidence.
			id:           "A5 UTC instant is 2026-09-01 01:00 in Bogota, correctly current",
			now:          time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC),
			loc:          bogota,
			wantCurrent:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
		},
		{
			id:           "A6 loc is a real parameter: time.UTC works too, not hardcoded to Bogota",
			now:          time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC),
			loc:          time.UTC,
			wantCurrent:  time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			wantPrevious: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			current, previous := monthlyMetricsPeriods(tc.now, tc.loc)

			if !current.Equal(tc.wantCurrent) {
				t.Errorf("current = %v, want %v", current, tc.wantCurrent)
			}
			if !previous.Equal(tc.wantPrevious) {
				t.Errorf("previous = %v, want %v", previous, tc.wantPrevious)
			}
		})
	}
}

// --- Group B — callMonthlySyncer (fake port, no clock) ---

// TestCallMonthlySyncer_Success is Test Contract B1: the happy path. The
// port is called once with the right arguments; the function has no return
// value, so nothing propagates.
func TestCallMonthlySyncer_Success(t *testing.T) {
	syncer := &fakeMonthlySyncer{}
	p := &processor{monthlySyncer: syncer}
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	p.callMonthlySyncer(context.Background(), 42, period)

	if len(syncer.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(syncer.calls))
	}
	if got := syncer.calls[0]; got.teslaID != 42 || !got.period.Equal(period) {
		t.Errorf("call = %+v, want teslaID=42 period=%v", got, period)
	}
}

// TestCallMonthlySyncer_ErrorIsLoggedNotPropagated is Test Contract B2: an
// error from the port is logged and swallowed, never propagated — the
// function's own signature has no error return, so the compiler already
// proves nothing can leak out; this test proves the port was still called
// with the right arguments despite the error.
func TestCallMonthlySyncer_ErrorIsLoggedNotPropagated(t *testing.T) {
	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	syncer := &fakeMonthlySyncer{}
	syncer.failOn(42, period, errors.New("db down"))
	p := &processor{monthlySyncer: syncer}

	p.callMonthlySyncer(context.Background(), 42, period)

	if len(syncer.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(syncer.calls))
	}
	if got := syncer.calls[0]; got.teslaID != 42 || !got.period.Equal(period) {
		t.Errorf("call = %+v, want teslaID=42 period=%v", got, period)
	}
}

// --- Group C — runMonthlyMetricsStep's own vehicle enumeration and
// per-(vehicle, period) isolation (C1-C4 plus the dedup case) ---

// fakeAccountVehicles satisfies account.Service (via the embedded
// fakeAccountEmpty's 12 unreachable methods) and overrides only
// AllRegisteredVehicles to return a configurable vehicle list — including,
// deliberately, one that repeats a TeslaID to exercise the dedup case.
type fakeAccountVehicles struct {
	*fakeAccountEmpty
	vehicles []account.OwnedVehicle
	err      error
}

func (f *fakeAccountVehicles) AllRegisteredVehicles(_ context.Context) ([]account.OwnedVehicle, error) {
	return f.vehicles, f.err
}

var _ account.Service = (*fakeAccountVehicles)(nil)

func ownedVehicle(teslaID int64) account.OwnedVehicle {
	return account.OwnedVehicle{AccountID: uuid.New(), TeslaID: teslaID, VIN: "VIN"}
}

// TestRunMonthlyMetricsStep_TwoVehiclesEachGetTwoCalls is Test Contract C1:
// two distinct vehicles each get exactly two calls, current before previous.
func TestRunMonthlyMetricsStep_TwoVehiclesEachGetTwoCalls(t *testing.T) {
	syncer := &fakeMonthlySyncer{}
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(1), ownedVehicle(2)}}
	p := &processor{acct: acct, monthlySyncer: syncer}

	p.runMonthlyMetricsStep(context.Background())

	current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())
	want := []syncCall{
		{teslaID: 1, period: current},
		{teslaID: 1, period: previous},
		{teslaID: 2, period: current},
		{teslaID: 2, period: previous},
	}
	if !slices.Equal(syncer.calls, want) {
		t.Errorf("calls = %+v, want %+v", syncer.calls, want)
	}
}

// TestRunMonthlyMetricsStep_CurrentMonthFailureDoesNotBlockPreviousMonth is
// Test Contract C2: the load-bearing isolation case. A vehicle's
// current-month failure never blocks its own previous-month call, nor
// another vehicle's calls.
func TestRunMonthlyMetricsStep_CurrentMonthFailureDoesNotBlockPreviousMonth(t *testing.T) {
	current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())
	syncer := &fakeMonthlySyncer{}
	syncer.failOn(1, current, errors.New("boom"))
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(1), ownedVehicle(2)}}
	p := &processor{acct: acct, monthlySyncer: syncer}

	p.runMonthlyMetricsStep(context.Background())

	want := []syncCall{
		{teslaID: 1, period: current},
		{teslaID: 1, period: previous},
		{teslaID: 2, period: current},
		{teslaID: 2, period: previous},
	}
	if !slices.Equal(syncer.calls, want) {
		t.Errorf("calls = %+v, want %+v (all 4 calls still happen despite the (1, current) failure)", syncer.calls, want)
	}
}

// TestRunMonthlyMetricsStep_OneVehicleTotalFailureDoesNotBlockAnother is
// Test Contract C3: one vehicle's total failure (both periods) never blocks
// another vehicle's calls.
func TestRunMonthlyMetricsStep_OneVehicleTotalFailureDoesNotBlockAnother(t *testing.T) {
	current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())
	syncer := &fakeMonthlySyncer{}
	syncer.failOn(1, current, errors.New("boom"))
	syncer.failOn(1, previous, errors.New("boom"))
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(1), ownedVehicle(2)}}
	p := &processor{acct: acct, monthlySyncer: syncer}

	p.runMonthlyMetricsStep(context.Background())

	want := []syncCall{
		{teslaID: 2, period: current},
		{teslaID: 2, period: previous},
	}
	var vehicle2Calls []syncCall
	for _, c := range syncer.calls {
		if c.teslaID == 2 {
			vehicle2Calls = append(vehicle2Calls, c)
		}
	}
	if !slices.Equal(vehicle2Calls, want) {
		t.Errorf("vehicle 2's calls = %+v, want %+v (both still happen despite vehicle 1's total failure)", vehicle2Calls, want)
	}
}

// TestRunMonthlyMetricsStep_EnumerationFailureSkipsEveryCall is Test
// Contract C4: a listing failure is a whole-step failure, mirroring
// processChargingData's/recalculateAnalytics's own enumeration-failure
// shape — not one call happens.
func TestRunMonthlyMetricsStep_EnumerationFailureSkipsEveryCall(t *testing.T) {
	syncer := &fakeMonthlySyncer{}
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, err: errors.New("listing boom")}
	p := &processor{acct: acct, monthlySyncer: syncer}

	p.runMonthlyMetricsStep(context.Background())

	if len(syncer.calls) != 0 {
		t.Errorf("calls = %d, want 0", len(syncer.calls))
	}
}

// TestRunMonthlyMetricsStep_SharedVehicleGetsTwoCallsNotFour covers the
// duplicate-TeslaID dedup case described at the end of design.md's Test
// Contract Group C: one car registered to two accounts must produce exactly
// 2 calls (current + previous), not 4.
func TestRunMonthlyMetricsStep_SharedVehicleGetsTwoCallsNotFour(t *testing.T) {
	syncer := &fakeMonthlySyncer{}
	acct := &fakeAccountVehicles{fakeAccountEmpty: &fakeAccountEmpty{}, vehicles: []account.OwnedVehicle{ownedVehicle(7), ownedVehicle(7)}}
	p := &processor{acct: acct, monthlySyncer: syncer}

	p.runMonthlyMetricsStep(context.Background())

	current, previous := monthlyMetricsPeriods(clock.Now(), clock.Zone())
	want := []syncCall{
		{teslaID: 7, period: current},
		{teslaID: 7, period: previous},
	}
	if !slices.Equal(syncer.calls, want) {
		t.Errorf("calls = %+v, want %+v (one car mirrored once, not once per account)", syncer.calls, want)
	}
}
