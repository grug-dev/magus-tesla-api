// Command poller is the background entrypoint for nightly telemetry collection. It
// wires config, a pgx pool, the account + tesla ports and internal/app's Processor,
// then starts internal/app's daily scheduler with graceful shutdown.
//
// Since RM29 tier 7 this file is WIRING ONLY. It owns no business logic at all:
// what one nightly run does — sync fleet data, process charging data, recalculate
// analytics, in that order, short-circuiting if the sync fails — is internal/app's
// Processor.ProcessVehicleData, and WHEN it runs is internal/app's Scheduler. Both
// used to live here (a reconcilingCollector decorator plus two closures) because
// there was no application layer to hold them; tier 7 created one. The rule this
// serves is CLAUDE.md §Non-negotiables: cmd/ stays thin, zero business logic.
//
// That matters beyond tidiness: this package has no tests and never has, so every
// line here is invisible to go test. Code that lives in internal/app can at least
// be tested; code that lives here can only be read. Keep it wiring.
//
// Both paths drive the SAME port, so they cannot diverge: the scheduler's tick and
// --once both call ProcessVehicleData and then telemetry.LogCycle. Each invocation
// stamps its own run_id across every poll_attempts row it writes, plus
// triggered_by = "scheduler" for both paths (a future manual-rerun API would pass
// "api" — RM29 tier 8, parked).
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
	"github.com/cristianpena/magus-tesla-api/internal/app"
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

	// The sibling ports internal/app's Processor composes. Both paths get all
	// three steps by construction now, because both call the same port — the
	// decorator that used to guarantee that is gone with tier 7.
	// analytics.NewReader's window argument is required by the signature but
	// unused by ConsumedByDay — only RecentEfficiency reads it, and this command
	// never calls that.
	//
	// The three sibling ports are built once and shared by the reader and the
	// recalculator: they are stateless handles over the same pool, and building
	// them twice would only obscure that both halves of the step read exactly the
	// same sources.
	telemetryReader := telemetry.NewReader(pool)
	// Two Supercharger-session ports, deliberately, reading two different tables.
	// superchargerHistoryReader is telemetry's, and stays: the mirror step reads
	// supercharger_history to WRITE charge_sessions, so it must keep its source.
	// sessionAnalyticsReader is charging's, and is what analytics now reads --
	// since RM31 tier 3 the metrics derive from charge_sessions, the table a
	// human's verified battery percentages land in.
	superchargerHistoryReader := telemetry.NewSuperchargerHistoryReader(pool)
	sessionAnalyticsReader := charging.NewSuperchargerSessionAnalyticsReader(pool)
	chargingReader := charging.NewReader(pool)

	analyticsReader := analytics.NewReader(
		pool,
		telemetryReader,
		sessionAnalyticsReader,
		chargingReader,
		acct,
		analytics.DefaultWindow,
	)
	recalculator := analytics.NewRecalculator(pool, telemetryReader, sessionAnalyticsReader, chargingReader)

	// The use case itself. Since RM29 tier 7 the three-step cycle lives behind
	// internal/app's Processor port, not in this file: what a run DOES is
	// application logic, and cmd/ stays thin (CLAUDE.md §Non-negotiables). Both
	// paths below drive the same port, so neither can silently diverge from the
	// other the way a decorator wrapped around only one of them could.
	processor := app.NewProcessor(
		telemetry.NewService(pool, acct, tesla.NewClient(), tcfg),
		superchargerHistoryReader,
		telemetry.NewRunWriter(pool),
		charging.NewSessionWriter(pool),
		acct,
		recalculator,
		analyticsReader,
		analytics.NewGapWriter(pool),
		loc,
	)

	if !*once {
		scheduler := app.NewScheduler(processor, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)

		log.Printf("poller started: nightly collection at %02d:%02d %s (wake timeout %s)",
			cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, cfg.PollerWakeTimeout)

		// Run blocks until ctx is cancelled, then returns ctx.Err() — the expected
		// exit on SIGINT/SIGTERM, so that is not a failure.
		if err := scheduler.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("scheduler: %v", err)
		}
		log.Println("poller stopped")
	} else {
		// One-shot mode: run a single cycle immediately, log the shared per-cycle
		// report, then exit. Calls the same ProcessVehicleData + LogCycle pair the
		// scheduler's tick calls, so it provably shares the exact three-step path
		// with the nightly run. The cycle records triggered_by = "scheduler": this
		// is still the poller, and the owner does not need --once distinguishable
		// (RD7). ProcessVehicleData returns nil for per-vehicle failures
		// (per-vehicle isolation); a non-nil error means a whole-cycle
		// (enumeration) failure → exit 1.
		report, err := processor.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)
		telemetry.LogCycle(report, err)
		if err != nil {
			log.Fatalf("one-shot collection: %v", err)
		}
	}
}
