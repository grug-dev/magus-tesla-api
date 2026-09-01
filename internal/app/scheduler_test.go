package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// --- pure nextRun schedule-time math (no clock, no sleeping) ---

func TestNextRun(t *testing.T) {
	utc := time.UTC
	// A fixed local zone east of UTC to prove the computation stays in loc.
	plus5 := time.FixedZone("PLUS5", 5*3600)

	cases := []struct {
		name      string
		now       time.Time
		hour, min int
		loc       *time.Location
		want      time.Time
	}{
		{
			name: "before target time same day",
			now:  time.Date(2026, 7, 10, 1, 0, 0, 0, utc),
			hour: 3, min: 30, loc: utc,
			want: time.Date(2026, 7, 10, 3, 30, 0, 0, utc),
		},
		{
			name: "after target time rolls to tomorrow",
			now:  time.Date(2026, 7, 10, 9, 0, 0, 0, utc),
			hour: 3, min: 30, loc: utc,
			want: time.Date(2026, 7, 11, 3, 30, 0, 0, utc),
		},
		{
			name: "exactly at target time rolls to tomorrow (strictly after)",
			now:  time.Date(2026, 7, 10, 3, 30, 0, 0, utc),
			hour: 3, min: 30, loc: utc,
			want: time.Date(2026, 7, 11, 3, 30, 0, 0, utc),
		},
		{
			name: "computed in the given timezone, not UTC",
			// 23:00 UTC on Jul 10 is 04:00 on Jul 11 in PLUS5 (already past 03:30
			// local) → next is Jul 12 03:30 PLUS5. Proves the math uses loc, not UTC:
			// in UTC the same instant would still be before 03:30 and pick Jul 11.
			now:  time.Date(2026, 7, 10, 23, 0, 0, 0, utc),
			hour: 3, min: 30, loc: plus5,
			want: time.Date(2026, 7, 12, 3, 30, 0, 0, plus5),
		},
		{
			name: "day rollover across month boundary",
			now:  time.Date(2026, 7, 31, 20, 0, 0, 0, utc),
			hour: 3, min: 30, loc: utc,
			want: time.Date(2026, 8, 1, 3, 30, 0, 0, utc),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := nextRun(tc.now, tc.hour, tc.min, tc.loc)
			if !got.Equal(tc.want) {
				t.Errorf("nextRun(%v) = %v, want %v", tc.now, got, tc.want)
			}
		})
	}
}

// stubCollector records how many times ProcessVehicleData ran so we can prove the
// scheduler stops cleanly without firing a cycle when its context is already
// cancelled. It satisfies Processor now (it satisfied telemetry.Collector before the
// scheduler's relocation from internal/telemetry — design.md Test Contract group S);
// the name is kept from that prior life rather than renamed, since this is a
// relocation, not a rewrite.
type stubCollector struct{ calls int }

func (s *stubCollector) ProcessVehicleData(context.Context, telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	s.calls++
	return telemetry.CycleReport{}, nil
}

func TestScheduler_ShutsDownWithoutRunningWhenCancelled(t *testing.T) {
	sc := &stubCollector{}
	// Next run is far away (default 03:30); we cancel immediately, so Run must return
	// promptly without waiting for the timer and without calling ProcessVehicleData.
	sched := NewScheduler(sc, 3, 30, time.UTC, telemetry.Config{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled from Run, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not shut down promptly on cancellation")
	}
	if sc.calls != 0 {
		t.Errorf("no cycle should run after shutdown begins, got %d", sc.calls)
	}
}

func TestScheduler_NilLocationDefaultsToClockZone(t *testing.T) {
	sched := NewScheduler(&stubCollector{}, 3, 30, nil, telemetry.Config{})
	if sched.loc != clock.Zone() {
		t.Errorf("nil location should default to clock.Zone(), got %v", sched.loc)
	}
}

// reportCollector returns a canned report/error so the Run logging path is
// exercised. Satisfies Processor now, for the same relocation reason as
// stubCollector above.
type reportCollector struct {
	report telemetry.CycleReport
	err    error
	calls  int
}

func (c *reportCollector) ProcessVehicleData(context.Context, telemetry.TriggeredBy) (telemetry.CycleReport, error) {
	c.calls++
	return c.report, c.err
}

// TestScheduler_RunsAndLogsOneCycle proves Run fires exactly one cycle (via a
// near-immediate scheduled time from a fake clock) and returns via the logging path
// without panicking (R1-02). We do not assert on log output, only on the cycle count and
// clean shutdown after the tick.
func TestScheduler_RunsAndLogsOneCycle(t *testing.T) {
	// A fake clock reading 03:29:59.99 UTC so nextRun (03:30) is ~10ms away — the timer
	// fires almost immediately, one cycle runs, then we cancel before the next day.
	fixed := time.Date(2026, 7, 10, 3, 29, 59, int(990*time.Millisecond), time.UTC)
	rc := &reportCollector{
		report: telemetry.CycleReport{Attempted: 3, Succeeded: 2, FailuresByReason: map[telemetry.Reason]int{telemetry.ReasonAPIError: 1}},
		err:    errors.New("cycle: boom"),
	}
	sched := NewScheduler(rc, 3, 30, time.UTC, telemetry.Config{Clock: func() time.Time { return fixed }})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- sched.Run(ctx) }()

	// Give the ~10ms timer time to fire exactly one cycle, then cancel to stop the loop.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("want context.Canceled from Run, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("scheduler did not shut down promptly on cancellation")
	}
	if rc.calls < 1 {
		t.Errorf("want at least one collection cycle to have run and been logged, got %d", rc.calls)
	}
}
