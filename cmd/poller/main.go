// Command poller is the background entrypoint for nightly telemetry collection. It
// wires config, a pgx pool, the account + tesla ports, and the telemetry collector,
// then runs the in-app daily scheduler with graceful shutdown. Thin by design — all
// collection logic lives in internal/telemetry (ai/go-conventions.md).
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
	collector := telemetry.NewService(pool, acct, tesla.NewClient(), tcfg)

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
