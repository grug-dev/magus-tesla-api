package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// This file covers design.md's Test Contract Group A (monthlyCapacityPeriod,
// pure, no fakes, no DB) and Group B (callMonthlyCapacityCalculator, fake
// port, no clock). See design.md §Test Contract — the expected values below
// are copied from there, not derived from the implementation.

// --- Group B fake: fakeMonthlyCapacityCalculator ---

// fakeMonthlyCapacityCalculator satisfies charging.MonthlyCapacityCalculator.
// It records the (ctx, period, teslaID) it was called with, counts calls, and
// returns a configurable (charging.MonthlyCapacityReport, error) — same shape
// as fakeCollector/fakeRunWriter in processor_test.go (design.md Test
// Contract "Conventions").
type fakeMonthlyCapacityCalculator struct {
	report charging.MonthlyCapacityReport
	err    error

	calls      int
	gotCtx     context.Context
	gotPeriod  time.Time
	gotTeslaID *int64
}

func (f *fakeMonthlyCapacityCalculator) Calculate(ctx context.Context, period time.Time, teslaID *int64) (charging.MonthlyCapacityReport, error) {
	f.calls++
	f.gotCtx = ctx
	f.gotPeriod = period
	f.gotTeslaID = teslaID
	return f.report, f.err
}

var _ charging.MonthlyCapacityCalculator = (*fakeMonthlyCapacityCalculator)(nil)

// --- Group A — monthlyCapacityPeriod (pure, no fakes, no DB) ---

// TestMonthlyCapacityPeriod covers design.md Test Contract A1-A6. loc is
// clock.Zone() (America/Bogota) for every case except A6, which proves loc is
// a real parameter, not hardcoded (design.md D2, D1).
func TestMonthlyCapacityPeriod(t *testing.T) {
	bogota := clock.Zone()

	cases := []struct {
		id         string
		now        time.Time
		loc        *time.Location
		wantPeriod time.Time
		wantRun    bool
	}{
		{
			id:         "A1 plain case: today is the 1st, previous month is August",
			now:        time.Date(2026, 9, 1, 0, 30, 0, 0, bogota),
			loc:        bogota,
			wantPeriod: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			wantRun:    true,
		},
		{
			id:      "A2 last day of the month does not trigger",
			now:     time.Date(2026, 9, 30, 23, 59, 0, 0, bogota),
			loc:     bogota,
			wantRun: false,
		},
		{
			id:         "A3 year boundary: previous month of January is December of the prior year",
			now:        time.Date(2027, 1, 1, 10, 0, 0, 0, bogota),
			loc:        bogota,
			wantPeriod: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
			wantRun:    true,
		},
		{
			// Load-bearing case: the UTC calendar day is the 1st, but the Bogota
			// calendar day is still the 31st (UTC-5). A naive UTC-day check would
			// wrongly trigger here.
			id:      "A4 UTC day is the 1st but Bogota day is still the 31st",
			now:     time.Date(2026, 9, 1, 3, 0, 0, 0, time.UTC),
			loc:     bogota,
			wantRun: false,
		},
		{
			// Zone-aware counterpart to A4: the UTC instant's UTC day is also the
			// 1st, and it correctly triggers here too, because it IS the 1st in
			// Bogota — not by coincidence.
			id:         "A5 UTC instant is 2026-09-01 01:00 in Bogota, correctly triggers",
			now:        time.Date(2026, 9, 1, 6, 0, 0, 0, time.UTC),
			loc:        bogota,
			wantPeriod: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
			wantRun:    true,
		},
		{
			id:         "A6 loc is a real parameter: time.UTC works too, not hardcoded to Bogota",
			now:        time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
			loc:        time.UTC,
			wantPeriod: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
			wantRun:    true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			period, run := monthlyCapacityPeriod(tc.now, tc.loc)

			if run != tc.wantRun {
				t.Fatalf("run = %v, want %v", run, tc.wantRun)
			}
			if !tc.wantRun {
				if !period.IsZero() {
					t.Errorf("period = %v, want the zero time.Time", period)
				}
				return
			}
			if !period.Equal(tc.wantPeriod) {
				t.Errorf("period = %v, want %v", period, tc.wantPeriod)
			}
		})
	}
}

// --- Group B — callMonthlyCapacityCalculator (fake port, no clock) ---

// TestCallMonthlyCapacityCalculator_Success is Test Contract B1: the happy
// path. The port is called once with the right period and a nil teslaID
// (Context fact 8 — this caller always passes nil); nothing is returned to
// the caller since the function has no return value.
func TestCallMonthlyCapacityCalculator_Success(t *testing.T) {
	calc := &fakeMonthlyCapacityCalculator{
		report: charging.MonthlyCapacityReport{VehiclesFound: 4, Measured: 3, Thin: 1},
	}
	p := &processor{monthlyCapacityCalculator: calc}
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	p.callMonthlyCapacityCalculator(context.Background(), period)

	if calc.calls != 1 {
		t.Fatalf("calls = %d, want 1", calc.calls)
	}
	if !calc.gotPeriod.Equal(period) {
		t.Errorf("period seen by the fake = %v, want %v", calc.gotPeriod, period)
	}
	if calc.gotTeslaID != nil {
		t.Errorf("teslaID seen by the fake = %v, want nil", calc.gotTeslaID)
	}
}

// TestCallMonthlyCapacityCalculator_ErrorIsLoggedNotPropagated is Test
// Contract B2: an error from the port is logged and swallowed, never
// propagated — the function's own signature has no error return, so the
// compiler already proves nothing can leak out; this test proves the port
// was still called with the right period despite the error.
func TestCallMonthlyCapacityCalculator_ErrorIsLoggedNotPropagated(t *testing.T) {
	calc := &fakeMonthlyCapacityCalculator{err: errors.New("db down")}
	p := &processor{monthlyCapacityCalculator: calc}
	period := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	p.callMonthlyCapacityCalculator(context.Background(), period)

	if calc.calls != 1 {
		t.Fatalf("calls = %d, want 1", calc.calls)
	}
	if !calc.gotPeriod.Equal(period) {
		t.Errorf("period seen by the fake = %v, want %v", calc.gotPeriod, period)
	}
}
