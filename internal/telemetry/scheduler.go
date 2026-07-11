package telemetry

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"
)

// Scheduler is the in-app daily collection daemon (design D7). It drives a Collector
// once per day at a fixed local time (default 03:30) and blocks in Run until its
// context is cancelled, at which point it shuts down gracefully without starting a new
// cycle. It is intentionally tiny: all Tesla/DB work lives behind the Collector port,
// so the scheduler only owns the "when", never the "how".
type Scheduler struct {
	collector Collector
	hour      int
	minute    int
	loc       *time.Location
	// now is the clock seam. Real runs use time.Now; tests inject a fake clock so the
	// nextRun math is exercised without waiting for a wall-clock day to pass.
	now func() time.Time
}

// NewScheduler builds a daily scheduler that runs collector at hour:minute in loc. A
// nil loc falls back to time.Local, and an out-of-range clock defaults to the wall
// clock. cfg.Clock (when set) is used as the scheduler's clock so tests stay
// deterministic; otherwise time.Now is used.
func NewScheduler(collector Collector, hour, minute int, loc *time.Location, cfg Config) *Scheduler {
	if loc == nil {
		loc = time.Local
	}
	nowFn := time.Now
	if cfg.Clock != nil {
		nowFn = cfg.Clock
	}
	return &Scheduler{
		collector: collector,
		hour:      hour,
		minute:    minute,
		loc:       loc,
		now:       nowFn,
	}
}

// Run blocks, running one collection cycle each day at the configured local time,
// until ctx is cancelled. On each tick it calls CollectAll once, logs a one-line summary
// of the outcome, and reschedules for the next day. Graceful shutdown (D7): once ctx is
// cancelled, Run returns without starting a new cycle. It returns ctx.Err() on
// cancellation. A per-cycle CollectAll error is NOT fatal — it is logged and the daemon
// simply waits for the next day, so one bad night never stops the schedule. Logging the
// report here (rather than discarding it) is what gives an unattended nightly poller its
// only per-cycle operational visibility.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		next := nextRun(s.now(), s.hour, s.minute, s.loc)

		// A timer (not time.Sleep) so ctx cancellation wakes us immediately for a
		// clean shutdown rather than blocking until the next run time.
		timer := time.NewTimer(next.Sub(s.now()))

		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
			// Run one cycle. CollectAll contains all per-vehicle failures itself and
			// only errors on a whole-cycle failure; either way we log the outcome and
			// proceed to the next day so a single failed night does not stop the
			// schedule.
			report, err := s.collector.CollectAll(ctx)
			logCycle(report, err)
		}
	}
}

// logCycle emits a single operational line summarizing one collection cycle so an
// unattended poller's nightly outcome is visible in logs (attempted / succeeded /
// failures-by-reason), plus a separate line for any whole-cycle error. It uses the
// standard library log package, matching the rest of the repo (cmd/web, cmd/poller).
func logCycle(report CycleReport, err error) {
	if err != nil {
		log.Printf("telemetry cycle: whole-cycle error: %v", err)
	}
	log.Printf("telemetry cycle: attempted=%d succeeded=%d failures={%s}",
		report.Attempted, report.Succeeded, formatFailures(report.FailuresByReason))
}

// formatFailures renders the failures-by-reason map in a stable (reason-sorted) order so
// the log line is deterministic and greppable regardless of Go's map iteration order.
func formatFailures(byReason map[Reason]int) string {
	if len(byReason) == 0 {
		return ""
	}
	reasons := make([]string, 0, len(byReason))
	for r := range byReason {
		reasons = append(reasons, string(r))
	}
	sort.Strings(reasons)
	parts := make([]string, 0, len(reasons))
	for _, r := range reasons {
		parts = append(parts, fmt.Sprintf("%s=%d", r, byReason[Reason(r)]))
	}
	out := parts[0]
	for _, p := range parts[1:] {
		out += " " + p
	}
	return out
}

// nextRun returns the next occurrence of hour:minute in loc strictly after now. It is
// a PURE function (no clock, no sleeping) so the schedule-time math is unit-testable
// in isolation: if today's target time has already passed (or is exactly now), it
// rolls over to the same time tomorrow. Computed in loc so a DST change or a non-local
// timezone is honored correctly.
func nextRun(now time.Time, hour, minute int, loc *time.Location) time.Time {
	now = now.In(loc)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, loc)
	if !candidate.After(now) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate
}
