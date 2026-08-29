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
		TelemetryReader:    telemetry.NewReader(pool),
		SuperchargerReader: charging.NewSessionReader(pool),
		// Write port for the Supercharger row-edit save (PATCH
		// /ui/supercharger-stats/row/:id). Its VerifySession is account-scoped,
		// which is the tenant boundary for that route.
		SuperchargerVerifier: charging.NewSessionVerifier(pool),
		ChargingWriter:       charging.NewWriter(pool),
		ChargingReader:       charging.NewReader(pool),
		// The history fragment's battery-consumed chart reads through this port.
		// analytics.NewReader's window argument is required by the signature but
		// unused by ConsumedByDay — only RecentEfficiency reads it, and the
		// gateway never calls that (mirrors cmd/poller's own construction).
		AnalyticsReader: analytics.NewReader(
			pool,
			telemetry.NewReader(pool),
			charging.NewSuperchargerSessionAnalyticsReader(pool),
			charging.NewReader(pool),
			acct,
			analytics.DefaultWindow,
		),
		// The manual-charge write handlers call Recalculate through this port
		// after their charging.Writer call succeeds, so the precomputed
		// history charts stay current without a separate refresh
		// (RM29-analytics-add-vehicle-metrics design D5). It takes no
		// vehicleLookup and no window: the write path recomputes an explicit
		// date range for one known vehicle.
		AnalyticsRecalculator: analytics.NewRecalculator(
			pool,
			telemetry.NewReader(pool),
			charging.NewSuperchargerSessionAnalyticsReader(pool),
			charging.NewReader(pool),
		),
		SessionSecret:     cfg.SessionSecret,
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
