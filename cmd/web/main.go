// Command web is the HTTP entrypoint for the UI gateway. It wires config, a pgx
// pool, and the account service, builds the gateway engine, and serves it with
// graceful shutdown. Thin by design — no business logic (ai/go-conventions.md).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/config"
	"github.com/cristianpena/magus-tesla-api/internal/gateway"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/reference"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.DatabaseURL == "" {
		log.Fatal("DATABASE_URL is required for the web gateway")
	}
	if cfg.SessionSecret == "" {
		log.Fatal("SESSION_SECRET is required for the web gateway")
	}

	// Signals cancel this context, which both stops accepting new work and triggers
	// graceful shutdown below.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("db pool: %v", err)
	}
	defer pool.Close()

	acct := account.NewService(pool, cfg.ClientID, cfg.ClientSecret)
	google := googleauth.NewClient(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.GoogleRedirectURL())

	engine, err := gateway.NewEngine(gateway.Deps{
		Pool:               pool,
		Account:            acct,
		Google:             google,
		Tesla:              tesla.NewClient(),
		SuperchargerReader: charging.NewSessionReader(pool),
		// Write port for the Supercharger row-edit save (PATCH
		// /ui/supercharger-stats/row/:id). Its VerifySession is scoped by
		// vehicle; the handler resolves that vehicle from the signed-in
		// account, and that resolve is the tenant boundary for the route.
		SuperchargerVerifier: charging.NewSessionVerifier(pool),
		ChargingWriter:       charging.NewWriter(pool),
		ChargingReader:       charging.NewReader(pool),
		// The history fragment's battery-consumed chart reads through this port.
		// Every method it exposes reads vehicle_metrics alone, so the pool is
		// the only dependency it needs.
		AnalyticsReader: analytics.NewReader(pool),
		// The manual-charge write handlers call Recalculate through this port
		// after their charging.Writer call succeeds, so the precomputed
		// history charts stay current without a separate refresh. It needs
		// the sibling ports the reader does not: it WRITES the rows, over an
		// explicit date range for one known vehicle.
		AnalyticsRecalculator: analytics.NewRecalculator(
			pool,
			telemetry.NewReader(pool),
			charging.NewSuperchargerSessionAnalyticsReader(pool),
			charging.NewReader(pool),
		),
		// The Vehicle Stats page reads the per-month rollup through this port.
		// Like AnalyticsReader it reads one table and needs only the pool —
		// the months were derived ahead of time by the nightly poller.
		AnalyticsMonthlyReader: analytics.NewMonthlyReader(pool),
		// The Vehicle Stats page reads the monthly gasoline price through this
		// port to show km per gallon. It reads one small table and needs only
		// the pool.
		ReferenceReader: reference.NewReader(pool),
		SessionSecret:   cfg.SessionSecret,
		// Public base URL — the same value that builds the OAuth redirect URIs
		// above. The gateway uses it to make the SEO/social tags absolute
		// (layouts.seoHead via handlers.SiteMiddleware); set BASE_URL to the
		// https domain in production or link previews will point at localhost.
		BaseURL:           cfg.BaseURL,
		TeslaClientID:     cfg.ClientID,
		TeslaClientSecret: cfg.ClientSecret,
		TeslaRedirectURL:  cfg.TeslaConnectRedirectURL(),
	})
	if err != nil {
		log.Fatalf("gateway: %v", err)
	}

	srv := &http.Server{Addr: ":" + cfg.Port, Handler: engine}

	go func() {
		log.Printf("web gateway listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	stop()
	log.Println("shutting down…")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown: %v", err)
	}
	log.Println("stopped")
}
