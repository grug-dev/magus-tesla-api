// Package gateway is the UI gateway — the single place HTML lives (ai/architecture.md
// §2). It builds the web engine: cookie sessions, embedded static assets, and the
// routes that map to Templ-rendering handlers.
package gateway

import (
	"crypto/sha256"
	"embed"
	"io/fs"
	"net/http"
	"os"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/handlers"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// staticFS embeds the vendored, pinned static assets (htmx) into the binary so the
// deploy is self-contained — no runtime CDN dependency.
//
//go:embed static
var staticFS embed.FS

// Deps are everything the gateway needs, wired by cmd/web.
type Deps struct {
	Pool    *pgxpool.Pool
	Account account.Service
	Google  *googleauth.Client
	Tesla   tesla.VehicleService
	// SuperchargerReader is the charging module's SessionReader port over
	// charge_sessions. The gateway calls ListSessionsByVehicleBetween once per
	// Supercharger Stats page/fragment render, bounded by the requested
	// ?start=&end= window. Injected from cmd/web via
	// charging.NewSessionReader(pool). NEVER import internal/charging/db
	// (chargingdb) for this path — all access through this interface only.
	SuperchargerReader charging.SessionReader
	// SuperchargerVerifier is the charging module's SessionVerifier write
	// port over charge_sessions. Injected from cmd/web via
	// charging.NewSessionVerifier(pool) and passed straight through to
	// handlers.Deps. Called ONLY by SuperchargerRowUpdate on an explicit
	// user-initiated row save. See internal/gateway/AGENTS.md "Exception:
	// Supercharger session battery verification". NEVER import
	// internal/charging/db (chargingdb) for this path. Added by
	// RM31-gateway-add-session-battery-edit.
	SuperchargerVerifier charging.SessionVerifier
	// ChargingWriter is the charging write port. Called by write handlers
	// (create/update/delete) on explicit user-initiated form submissions only.
	// See AGENTS.md "Exception: user-initiated writes" for the full amendment.
	ChargingWriter charging.Writer
	// ChargingReader is the charging read port. Called by read handlers
	// and the dataForCharges helper to list charge entries.
	ChargingReader charging.Reader
	// AnalyticsReader is the analytics module's read port; injected at
	// construction (mirrors SuperchargerReader/ChargingReader — the gateway
	// calls ConsumedByDay once per history
	// fragment render). Injected from cmd/web via analytics.NewReader(...).
	// NEVER import internal/analytics/db (analyticsdb). Since
	// RM29-analytics-add-vehicle-metrics that package DOES exist — this
	// comment used to say there was none to import — so the rule is the same
	// one that applies to every other sibling module: the interface, never the
	// database.
	AnalyticsReader analytics.Reader
	// AnalyticsRecalculator is the analytics module's write-path port
	// (RM29-analytics-add-vehicle-metrics design.md D5). Injected at
	// construction via cmd/web's analytics.NewRecalculator(...). Called ONLY
	// by the manual-charge write handlers (ExternalChargeCreate, ExternalChargeRowUpdate,
	// ExternalChargeRowDelete), after their corresponding charging.Writer call
	// succeeds, to keep the precomputed history charts current with no
	// separate refresh step — mirrors the ChargingWriter exception's narrow
	// aperture (AGENTS.md "Exception: user-initiated writes"). Never called
	// from a Reader-only handler.
	AnalyticsRecalculator analytics.Recalculator
	SessionSecret         string
	// Tesla OAuth app credentials + the web connect redirect URI.
	TeslaClientID     string
	TeslaClientSecret string
	TeslaRedirectURL  string
}

// NewEngine builds the gateway's Gin engine with cookie sessions, embedded /static,
// and all routes.
func NewEngine(d Deps) (*gin.Engine, error) {
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Signed + AES-encrypted cookie sessions. The auth (HMAC) key may be any length;
	// the encryption (block) key must be 16/24/32 bytes, so derive a 32-byte key from
	// the secret. No server-side state — sessions survive restarts.
	encKey := sha256.Sum256([]byte(d.SessionSecret))
	store := cookie.NewStore([]byte(d.SessionSecret), encKey[:])
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   60 * 60 * 24 * 7, // 7 days
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure stays false for local http; set true behind HTTPS in production.
	})
	r.Use(sessions.Sessions("magus", store))

	// handlers.PreferencesMiddleware resolves the active render language AND
	// theme in ONE account.Service.PreferencesFor call per request (RM42 tier 2
	// design.md D1; it replaced LanguageMiddleware, which resolved language
	// alone). It MUST be registered after sessions: its signed-in branch calls
	// currentUID, which reads the session set up just above.
	r.Use(handlers.PreferencesMiddleware(d.Account))

	// Static assets at /static — dev vs production.
	//
	// Production: serve from the //go:embed copy (self-contained deploy, no
	// runtime file dependency).
	//
	// Dev (MAGUS_DEV truthy): serve from the on-disk internal/gateway/static
	// directory instead, so `tailwindcss --watch` can rewrite app.css on every
	// .templ/class edit and a browser refresh picks up the new CSS WITHOUT a Go
	// rebuild or a server restart. Templ edits still need `air` to rebuild the
	// server (the generated *_templ.go is Go source), but CSS-only changes hot-
	// reload without the rebuild pause.
	if os.Getenv("MAGUS_DEV") != "" {
		r.Static("/static", "internal/gateway/static")
	} else {
		sub, err := fs.Sub(staticFS, "static")
		if err != nil {
			return nil, err
		}
		r.StaticFS("/static", http.FS(sub))
	}

	h := handlers.New(handlers.Deps{
		Pool:                  d.Pool,
		Account:               d.Account,
		Google:                d.Google,
		Tesla:                 d.Tesla,
		SuperchargerReader:    d.SuperchargerReader,
		SuperchargerVerifier:  d.SuperchargerVerifier,
		ChargingWriter:        d.ChargingWriter,
		ChargingReader:        d.ChargingReader,
		AnalyticsReader:       d.AnalyticsReader,
		AnalyticsRecalculator: d.AnalyticsRecalculator,
		TeslaClientID:         d.TeslaClientID,
		TeslaClientSecret:     d.TeslaClientSecret,
		TeslaRedirectURL:      d.TeslaRedirectURL,
		VehicleImageResolver:  newVehicleImageResolver(staticFS),
	})

	r.GET("/", h.Home)
	r.GET("/login", h.LoginPage)
	r.GET("/auth/google/login", h.GoogleLogin)
	r.GET("/auth/google/callback", h.GoogleCallback)
	r.POST("/logout", h.Logout)
	r.GET("/connect/tesla", h.ConnectTesla)
	r.GET("/connect/tesla/callback", h.TeslaCallback)
	r.GET("/dashboard", h.Dashboard)
	r.GET("/ui/dashboard", h.DashboardFragment)
	r.GET("/ui/dashboard/history", h.DashboardHistoryFragment)
	r.GET("/ui/nav-header", h.NavHeaderFragment)
	r.GET("/ui/vehicle-select", h.VehicleSelectFragment)
	r.POST("/ui/vehicle/select", h.VehicleSelect)
	r.GET("/healthz", h.Healthz)
	r.POST("/ui/lang/switch", h.LangSwitch)

	// Settings + theme switch (RM42 tier 2). Unlike /ui/lang/switch above, the
	// theme write takes a CSRF token: SettingsPage mints csrfThemeKey and
	// ThemeSwitch checks it. The language switch's missing CSRF check is a
	// narrow exception the user declined to extend here — see AGENTS.md.
	r.GET("/settings", h.SettingsPage)
	r.POST("/ui/theme/switch", h.ThemeSwitch)

	r.GET("/external-charges", h.ExternalChargesPage)
	r.GET("/ui/external-charges", h.ExternalChargesContentFragment)
	r.GET("/ui/external-charges/list", h.ExternalChargesListFragment)
	r.GET("/ui/external-charges/row/:id", h.ExternalChargeRowStatic)
	r.GET("/ui/external-charges/row/:id/edit", h.ExternalChargeRowEditFragment)
	r.POST("/ui/external-charges/create", h.ExternalChargeCreate)
	r.PUT("/ui/external-charges/row/:id", h.ExternalChargeRowUpdate)
	r.DELETE("/ui/external-charges/row/:id", h.ExternalChargeRowDelete)

	r.GET("/supercharger-stats", h.SuperchargerStatsPage)
	r.GET("/ui/supercharger-stats", h.SuperchargerStatsFragment)
	r.GET("/ui/supercharger-stats/row/:id", h.SuperchargerRowStatic)
	r.GET("/ui/supercharger-stats/row/:id/edit", h.SuperchargerRowEditFragment)
	r.PATCH("/ui/supercharger-stats/row/:id", h.SuperchargerRowUpdate)

	return r, nil
}
