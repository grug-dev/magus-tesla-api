// Command poller is the background entrypoint for nightly telemetry collection. It
// wires config, a pgx pool, the account + tesla ports, and the telemetry collector,
// then runs the in-app daily scheduler with graceful shutdown. Thin by design — all
// collection logic lives in internal/telemetry (ai/go-conventions.md).
//
// It is also the composition root for nightly reconciliation, which after each
// successful cycle runs two halves per vehicle, in order: internal/analytics's
// Reconcile advances that module's own precomputed read model (vehicle_metrics)
// from its watermarks, and then charge-gap reconciliation asks the same module for
// the trailing window of consumed-per-day figures and hands the flagged days to
// internal/analytics's own gap writer. The order matters — consumed-per-day is now a
// read of the model the first half writes. Since RM29 tier 5 both halves belong to
// internal/analytics, so this is composition of one module's parts rather than a
// cross-module handoff; the orchestration still lives HERE because a domain module
// does not drive the poller's per-vehicle loop or own its scheduling. See
// internal/analytics's Reconcile, ConsumedByDay and GapWriter.
//
// This is the ONLY production caller of Reconcile: the gateway calls Recalculate
// after a manual charge write, which covers only the days that write touches.
//
// BOTH paths load and validate POLLER_TIMEZONE: the nightly path schedules in it, and
// both paths date each capture by it (telemetry.Config.Location). An invalid value is
// therefore fatal for --once too, which was not the case before this timezone became
// part of the write path.
//
//	go run ./cmd/poller                # nightly scheduled collection (blocks)
//	go run ./cmd/poller --once          # one immediate cycle, then exit
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

func main() {
	once := flag.Bool("once", false, "run one collection cycle immediately and exit (skip the daily scheduler)")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required for the poller")
	}

	// Signals cancel this context, which the scheduler watches to shut down cleanly
	// (it finishes without starting a new cycle). Both the nightly path and the
	// --once path reuse this ctx so a SIGINT/SIGTERM during a wake-wait is honored.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	acct := account.NewService(pool, cfg.ClientID, cfg.ClientSecret)

	// Loaded unconditionally, before tcfg: BOTH paths need this timezone now. The
	// nightly path schedules in it, and both paths date every capture by it —
	// telemetry derives a snapshot's calendar day (CapturedDate) from this same
	// *time.Location, so the day a snapshot is dated always agrees with the day the
	// scheduler considers "today". "Local" resolves to the host's zone.
	loc, err := time.LoadLocation(cfg.PollerTimezone)
	if err != nil {
		log.Fatalf("invalid POLLER_TIMEZONE %q: %v", cfg.PollerTimezone, err)
	}

	// One telemetry.Config drives both the collector (wake timeout, calendar-day
	// timezone) and the scheduler (clock seam, left nil here so both use the wall
	// clock).
	tcfg := telemetry.Config{WakeTimeout: cfg.PollerWakeTimeout, Location: loc}

	// The nightly reconciliation step (D4/D4a) decorates the collector rather
	// than sitting beside it, so BOTH the scheduled path and --once get it by
	// construction. analytics.NewReader's window argument is required by the
	// signature but unused by ConsumedByDay — only RecentEfficiency reads it, and
	// this command never calls that.
	//
	// The three sibling ports are built once and shared by the reader and the
	// recalculator: they are stateless handles over the same pool, and building
	// them twice would only obscure that both halves of the step read exactly the
	// same sources.
	telemetryReader := telemetry.NewReader(pool)
	superchargerReader := telemetry.NewSuperchargerReader(pool)
	chargingReader := charging.NewReader(pool)

	analyticsReader := analytics.NewReader(
		pool,
		telemetryReader,
		superchargerReader,
		chargingReader,
		acct,
		analytics.DefaultWindow,
	)
	recalculator := analytics.NewRecalculator(pool, telemetryReader, superchargerReader, chargingReader)

	collector := &reconcilingCollector{
		inner:     telemetry.NewService(pool, acct, tesla.NewClient(), tcfg),
		reconcile: newNightlyReconciler(acct, recalculator, analyticsReader, analytics.NewGapWriter(pool), loc),
	}

	if !*once {
		scheduler := telemetry.NewScheduler(collector, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)

		log.Printf("poller started: nightly collection at %02d:%02d %s (wake timeout %s)",
			cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, cfg.PollerWakeTimeout)

		// Run blocks until ctx is cancelled, then returns ctx.Err() — the expected
		// exit on SIGINT/SIGTERM, so that is not a failure.
		if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("scheduler: %v", err)
		}
		log.Println("poller stopped")
	} else {
		// One-shot mode: run a single collection cycle immediately, log the shared
		// per-cycle report, then exit. Reuses CollectAll + LogCycle so it provably
		// shares the exact collection + report path with the nightly scheduler.
		// CollectAll returns nil for per-vehicle failures (per-vehicle isolation);
		// a non-nil error means a whole-cycle (enumeration) failure → exit 1.
		report, err := collector.CollectAll(ctx)
		telemetry.LogCycle(report, err)
		if err != nil {
			log.Fatalf("one-shot collection: %v", err)
		}
	}
}

// reconcilingCollector decorates telemetry.Collector so the nightly reconciliation
// (metrics, then charge gaps) runs after every successful collection cycle — the
// scheduled one and --once alike.
//
// Decorating (rather than calling reconcile beside CollectAll) is what makes both
// paths share the step by construction: Scheduler.Run owns its own loop and calls
// CollectAll internally, so cmd/ has no seam to hook after a *scheduled* cycle.
// Wrapping the port the scheduler already depends on keeps the orchestration in
// the composition root — D4a: cmd/poller orchestrates analytics → telemetry, and
// telemetry never calls analytics — instead of adding a post-cycle hook to
// telemetry.Scheduler, which would push knowledge of this tier into a module that
// must not have it.
type reconcilingCollector struct {
	inner     telemetry.Collector
	reconcile func(ctx context.Context)
}

// CollectAll runs the wrapped cycle, then reconciles only if the cycle itself
// succeeded: both halves read the night's freshly written snapshot, so neither must
// run before that snapshot exists (D4). A whole-cycle failure is returned
// untouched, leaving every existing caller's error handling unchanged.
func (c *reconcilingCollector) CollectAll(ctx context.Context) (telemetry.CycleReport, error) {
	report, err := c.inner.CollectAll(ctx)
	if err != nil {
		return report, err
	}
	c.reconcile(ctx)
	return report, nil
}

// newNightlyReconciler builds the per-cycle reconciliation step, which since
// RM29 tier 3 has TWO halves run per vehicle, in this order:
//
//  1. analytics.Reconcile — advances the module's own precomputed read model,
//     vehicle_metrics, from its three per-source watermarks (design.md D7/D8).
//  2. charge-gap reconciliation (D4/D4a, D7b) — recomputes the trailing
//     analytics.GapReconciliationWindow of consumed-per-day figures and hands the
//     flagged days to analytics's own gap writer, which upserts the days that flag and
//     deletes the days that no longer do.
//
// The order is a correctness requirement, not a preference. ConsumedByDay no
// longer derives anything: it is a SELECT over vehicle_metrics (design.md D13),
// so without step 1 first, step 2 would reconcile the night's charge gaps against
// yesterday's metrics and never see the snapshot just collected. This is also the
// ONLY production caller of Reconcile — the gateway's post-write Recalculate
// (D5) only covers days a manual charge write touches, so without this call a
// vehicle's charts would stop advancing entirely.
//
// A vehicle whose Reconcile fails is skipped for step 2 as well, rather than
// falling through to it: the gap writer's ReconcileWindow DELETES flags for days
// that no longer flag, so running it against knowingly-stale metrics would clear
// gap rows on the strength of data we just failed to refresh. Skipping costs one
// cycle; falling through would destroy state.
//
// Errors are logged, never fatal: a missed reconciliation self-heals on the next
// cycle, because both halves are recomputed from scratch every run rather than
// accumulated. Log lines stay prefixed by their half ("metrics reconciliation:",
// "gap reconciliation:") so they remain greppable and unambiguous — they are
// emitted inside CollectAll, hence before the caller's own telemetry-cycle
// summary line.
func newNightlyReconciler(acct account.Service, recalculator analytics.Recalculator, analyticsReader analytics.Reader, gapWriter analytics.GapWriter, loc *time.Location) func(context.Context) {
	return func(ctx context.Context) {
		// "Yesterday" is resolved in the POLLER'S OWN ZONE, not UTC (roadmap D6/D18,
		// design D-B12): the composition root owns the zone that answers "which days
		// am I asking about", while internal/analytics needs no *time.Location of its
		// own because each row's bucket day travels with it. time.Now().UTC() here
		// would ask for the wrong day for 5 hours out of every 24. The window ends
		// yesterday because today's data is not captured until tomorrow's poll.
		y, m, d := time.Now().In(loc).Date()
		end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		start := end.AddDate(0, 0, -int(analytics.GapReconciliationWindow.Hours()/24)+1)

		log.Printf("gap reconciliation: %s → %s", start, end)

		vehicles, err := acct.AllRegisteredVehicles(ctx)
		if err != nil {
			// Whole-cycle failure, mirroring CollectAll's own enumeration-failure shape.
			log.Printf("gap reconciliation: listing vehicles: %v", err)
			return
		}

		for _, v := range vehicles {
			// Step 1 — advance vehicle_metrics before anything reads it. Reconcile
			// derives its own affected window from its watermarks, so it takes no
			// start/end from here: the [start, end] below is the gap step's trailing
			// window, a different and unrelated question.
			if err := recalculator.Reconcile(ctx, v.AccountID, v.TeslaID); err != nil {
				// Per-vehicle isolation, mirroring CollectAll. Skips this vehicle's gap
				// step too — see the doc comment: reconciling gaps against metrics we
				// just failed to refresh would delete gap rows on stale evidence.
				log.Printf("metrics reconciliation: vehicle %d: %v", v.TeslaID, err)
				continue
			}

			// Step 2 — charge gaps, now reading the model step 1 just advanced.
			days, err := analyticsReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)
			if err != nil {
				// Per-vehicle isolation, mirroring CollectAll: one vehicle's failure
				// never aborts another vehicle's reconciliation.
				log.Printf("gap reconciliation: vehicle %d: consumed-by-day: %v", v.TeslaID, err)
				continue
			}

			var flagged []analytics.ChargeGap
			for _, day := range days {
				if !day.Flagged {
					continue
				}
				flagged = append(flagged, analytics.ChargeGap{
					AccountID:           v.AccountID,
					TeslaID:             v.TeslaID,
					VIN:                 v.VIN,
					Date:                day.Date,
					MissingChargingType: day.MissingChargingType,
				})
			}

			if err := gapWriter.ReconcileWindow(ctx, v.AccountID, v.TeslaID, start, end, flagged); err != nil {
				log.Printf("gap reconciliation: vehicle %d: reconcile window: %v", v.TeslaID, err)
				continue
			}
		}
	}
}
