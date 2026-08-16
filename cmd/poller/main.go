// Command poller is the background entrypoint for nightly telemetry collection. It
// wires config, a pgx pool, the account + tesla ports, and the telemetry collector,
// then runs the in-app daily scheduler with graceful shutdown. Thin by design — all
// collection logic lives in internal/telemetry (ai/go-conventions.md).
//
// It is also the composition root for charge-gap reconciliation: after each
// successful cycle it asks internal/battery for the trailing window of consumed-per-day
// figures and hands the flagged days to internal/telemetry's gap writer. That
// orchestration lives HERE, and only here, because neither module may depend on the
// other in that direction — telemetry never calls battery. See internal/battery's
// ConsumedByDay and telemetry's GapWriter.
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
	"github.com/cristianpena/magus-tesla-api/internal/battery"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
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

	// The charge-gap reconciliation step (D4/D4a) decorates the collector rather
	// than sitting beside it, so BOTH the scheduled path and --once get it by
	// construction. battery.NewReader's window argument is required by the
	// signature but unused by ConsumedByDay — only RecentEfficiency reads it, and
	// this command never calls that.
	batteryReader := battery.NewReader(
		telemetry.NewReader(pool),
		telemetry.NewSuperchargerReader(pool),
		manualcharge.NewReader(pool),
		acct,
		battery.DefaultWindow,
	)
	collector := &reconcilingCollector{
		inner:     telemetry.NewService(pool, acct, tesla.NewClient(), tcfg),
		reconcile: newGapReconciler(acct, batteryReader, telemetry.NewGapWriter(pool), loc),
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

// reconcilingCollector decorates telemetry.Collector so the nightly charge-gap
// reconciliation runs after every successful collection cycle — the scheduled one
// and --once alike.
//
// Decorating (rather than calling reconcile beside CollectAll) is what makes both
// paths share the step by construction: Scheduler.Run owns its own loop and calls
// CollectAll internally, so cmd/ has no seam to hook after a *scheduled* cycle.
// Wrapping the port the scheduler already depends on keeps the orchestration in
// the composition root — D4a: cmd/poller orchestrates battery → telemetry, and
// telemetry never calls battery — instead of adding a post-cycle hook to
// telemetry.Scheduler, which would push knowledge of this tier into a module that
// must not have it.
type reconcilingCollector struct {
	inner     telemetry.Collector
	reconcile func(ctx context.Context)
}

// CollectAll runs the wrapped cycle, then reconciles charge gaps only if the cycle
// itself succeeded: detection reads the night's freshly written snapshot, so it must
// not run before that snapshot exists (D4). A whole-cycle failure is returned
// untouched, leaving every existing caller's error handling unchanged.
func (c *reconcilingCollector) CollectAll(ctx context.Context) (telemetry.CycleReport, error) {
	report, err := c.inner.CollectAll(ctx)
	if err != nil {
		return report, err
	}
	c.reconcile(ctx)
	return report, nil
}

// newGapReconciler builds the per-cycle charge-gap reconciliation step (D4/D4a,
// D7b). For every registered vehicle it recomputes the trailing
// battery.GapReconciliationWindow of consumed-per-day figures and hands the flagged
// days to telemetry's gap writer, which upserts the days that flag and deletes the
// days that no longer do.
//
// Errors are logged, never fatal: a missed reconciliation self-heals on the next
// cycle, because flagged is recomputed from scratch every run rather than
// accumulated. Its log lines are prefixed "gap reconciliation:" so they stay
// greppable and unambiguous — they are emitted inside CollectAll, hence before the
// caller's own telemetry-cycle summary line.
func newGapReconciler(acct account.Service, batteryReader battery.Reader, gapWriter telemetry.GapWriter, loc *time.Location) func(context.Context) {
	return func(ctx context.Context) {
		// "Yesterday" is resolved in the POLLER'S OWN ZONE, not UTC (roadmap D6/D18,
		// design D-B12): the composition root owns the zone that answers "which days
		// am I asking about", while internal/battery needs no *time.Location of its
		// own because each row's bucket day travels with it. time.Now().UTC() here
		// would ask for the wrong day for 5 hours out of every 24. The window ends
		// yesterday because today's data is not captured until tomorrow's poll.
		y, m, d := time.Now().In(loc).Date()
		end := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
		start := end.AddDate(0, 0, -int(battery.GapReconciliationWindow.Hours()/24)+1)

		vehicles, err := acct.AllRegisteredVehicles(ctx)
		if err != nil {
			// Whole-cycle failure, mirroring CollectAll's own enumeration-failure shape.
			log.Printf("gap reconciliation: listing vehicles: %v", err)
			return
		}

		for _, v := range vehicles {
			days, err := batteryReader.ConsumedByDay(ctx, v.AccountID, v.TeslaID, start, end)
			if err != nil {
				// Per-vehicle isolation, mirroring CollectAll: one vehicle's failure
				// never aborts another vehicle's reconciliation.
				log.Printf("gap reconciliation: vehicle %d: consumed-by-day: %v", v.TeslaID, err)
				continue
			}

			var flagged []telemetry.ChargeGap
			for _, day := range days {
				if !day.Flagged {
					continue
				}
				flagged = append(flagged, telemetry.ChargeGap{
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
