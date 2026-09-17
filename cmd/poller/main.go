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
// triggered_by = "scheduler" for both paths. A third entry point, the
// manual-rerun HTTP listener (rerun.go), stamps triggered_by = "api" instead.
// All three call ProcessVehicleData through the same guardedProcessor
// (rerun.go), so no two of them can ever run at the same time (design.md D3 of
// platform-add-manual-rerun-api).
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
	"net/http"
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

// rerunAddr is the internal listen address for the manual-rerun HTTP listener
// (design.md D6). Never published to the host — only Caddy, inside the same
// compose network, dials it. Not a config var: the port has no legitimate
// reason to vary, so a knob here would be pure token cost for future readers.
const rerunAddr = ":8081"

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
	// The three sibling ports are built once and shared: they are stateless
	// handles over the same pool, and building them twice would only obscure
	// that both halves of the step read exactly the same sources.
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

	// The reader serves vehicle_metrics only, so it takes the pool alone. The
	// recalculator is what needs the sibling ports -- it writes those rows.
	analyticsReader := analytics.NewReader(pool)
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
		charging.NewMirrorWatermarkStore(pool),
		charging.NewMonthlyCapacityCalculator(pool),
		acct,
		recalculator,
		analyticsReader,
		analytics.NewGapWriter(pool),
		loc,
	)

	// Wrapped once, unconditionally, before branching on --once. Every call site
	// below uses guarded, never the raw processor, so zero code paths call
	// ProcessVehicleData un-guarded: the scheduler's nightly tick and an
	// API-triggered rerun (wired below) can never run at the same time
	// (design.md D3 of platform-add-manual-rerun-api).
	guarded := &guardedProcessor{inner: processor}

	if !*once {
		scheduler := app.NewScheduler(guarded, cfg.PollerScheduleHour, cfg.PollerScheduleMinute, loc, tcfg)

		// The manual-rerun HTTP listener. Off by default: it starts only when
		// POLLER_RERUN_TOKEN is set (fail-closed, design.md D2). The secret
		// itself is the last path segment of the only route this mux knows —
		// no manual comparison anywhere (design.md D5). The handler closes
		// over ctx, the ROOT context built above, never a *http.Request's own
		// context, so a rerun keeps running after the handler's 202 response
		// (design.md D4(b)).
		if cfg.PollerRerunToken == "" {
			log.Println("manual-rerun listener OFF (POLLER_RERUN_TOKEN not set)")
		} else if mux, err := newRerunMux(ctx, cfg.PollerRerunToken, guarded); err != nil {
			// A token that cannot be a URL path segment turns the listener off.
			// It never stops the poller (design.md D9): the nightly cycle is
			// worth more than this endpoint.
			log.Printf("manual-rerun listener OFF: %v", err)
		} else {
			go func() {
				if err := http.ListenAndServe(rerunAddr, mux); err != nil {
					// Logged, never fatal: the listener is an optional
					// capability and must never take the poller itself down.
					log.Printf("manual-rerun listener stopped: %v", err)
				}
			}()
			log.Printf("manual-rerun listener on %s (POLLER_RERUN_TOKEN set)", rerunAddr)
		}

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
		// (enumeration) failure → exit 1. Goes through guarded like every other
		// call site — inert here since nothing else can contend for the lock in
		// a one-shot CLI run, but it keeps the un-guarded call-site count at zero.
		report, err := guarded.ProcessVehicleData(ctx, telemetry.TriggeredByScheduler)
		telemetry.LogCycle(report, err)
		if err != nil {
			log.Fatalf("one-shot collection: %v", err)
		}
	}
}
