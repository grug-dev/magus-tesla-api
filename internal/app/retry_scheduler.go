package app

import (
	"context"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/logging"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// NotDoneVehicles answers "which of these registered vehicles have not yet
// finished producing their metrics for date" — the retry schedule's only way
// to ask that question. Declared here, not in internal/analytics: this
// module has no single requesting account to check ownership against, so it
// asks for exactly the narrow shape it needs and lets cmd/poller wire in
// whatever implements it.
type NotDoneVehicles interface {
	NotDone(ctx context.Context, teslaIDs []int64, date time.Time) ([]int64, error)
}

// RetryScheduler is a second, independent schedule next to Scheduler. Every
// retryInterval from retryWindowStartHour until the end of the local day it
// asks which registered vehicles have not finished yesterday's metrics, and
// re-runs the full cycle only for that set. It never touches a vehicle that
// already finished, and it writes nothing when every vehicle already has.
type RetryScheduler struct {
	processor Processor
	notDone   NotDoneVehicles
	acct      account.Service
	loc       *time.Location
	// now is the clock seam, same as Scheduler's own. Real runs use time.Now;
	// tests inject a fake clock so the tick math is exercised without
	// waiting for a wall-clock day to pass.
	now func() time.Time
}

// NewRetryScheduler builds a retry scheduler wired to processor, notDone and
// acct. A nil loc falls back to the platform's default zone, clock.Zone(),
// mirroring NewScheduler. cfg.Clock (when set) is used as the scheduler's
// clock so tests stay deterministic; otherwise time.Now is used.
func NewRetryScheduler(processor Processor, notDone NotDoneVehicles, acct account.Service, loc *time.Location, cfg telemetry.Config) *RetryScheduler {
	if loc == nil {
		loc = clock.Zone()
	}
	nowFn := time.Now
	if cfg.Clock != nil {
		nowFn = cfg.Clock
	}
	return &RetryScheduler{
		processor: processor,
		notDone:   notDone,
		acct:      acct,
		loc:       loc,
		now:       nowFn,
	}
}

// Run blocks, checking for unfinished vehicles every retryInterval from
// retryWindowStartHour until the end of the local day, until ctx is
// cancelled. Graceful shutdown mirrors Scheduler.Run exactly: once ctx is
// cancelled, Run returns without starting a new tick, and a per-tick error
// is logged, never fatal — one bad tick never stops the schedule.
func (s *RetryScheduler) Run(ctx context.Context) error {
	for {
		next := nextRetryTick(s.now(), s.loc)

		// A timer (not time.Sleep) so ctx cancellation wakes us immediately for a
		// clean shutdown rather than blocking until the next tick.
		timer := time.NewTimer(next.Sub(s.now()))

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			s.tick(ctx)
		}
	}
}

// tick runs one retry check: fetch every registered vehicle, ask which of
// them are still not done for yesterday, and re-run the cycle only for that
// set. An empty or errored NotDone answer makes no Processor call and writes
// no poll_runs row — the tick is a no-op, the roadmap's "writes nothing"
// guarantee for an already-finished night.
func (s *RetryScheduler) tick(ctx context.Context) {
	vehicles, err := distinctRegisteredVehicles(ctx, s.acct, nil)
	if err != nil {
		logging.Note("RetryScheduler", "tick", "listing vehicles: %v", err)
		return
	}

	allTeslaIDs := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		allTeslaIDs = append(allTeslaIDs, v.TeslaID)
	}

	// "Yesterday" in the retry schedule's own configured zone — the same
	// question recalculateAnalytics asks, resolved the same way.
	cutoff := clock.CalendarDay(s.now(), s.loc).AddDate(0, 0, -1)

	notDoneIDs, err := s.notDone.NotDone(ctx, allTeslaIDs, cutoff)
	if err != nil {
		logging.Note("RetryScheduler", "tick", "checking unfinished vehicles: %v", err)
		return
	}
	if len(notDoneIDs) == 0 {
		return
	}

	report, err := s.processor.ProcessVehicleDataForVehicles(ctx, telemetry.TriggeredByRetry, notDoneIDs)
	telemetry.LogCycle(report, err)
}
