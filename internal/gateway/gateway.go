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
	"github.com/cristianpena/magus-tesla-api/internal/gateway/handlers"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
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
	// TelemetryReader is the telemetry read port. The gateway calls
	// LatestSnapshotsByAccount once per dashboard render to populate vehicle card
	// telemetry. Injected from cmd/web via telemetry.NewReader(pool).
	// NEVER import internal/telemetry/db (telemetrydb) — all access through this
	// interface only.
	TelemetryReader telemetry.Reader
	// SuperchargerReader is the telemetry Supercharger-sessions read port. The
	// gateway calls SuperchargerSessionsByVehicle once per Supercharger Stats
	// page render. Injected from cmd/web via telemetry.NewSuperchargerReader(pool).
	// NEVER import internal/telemetry/db (telemetrydb) — all access through this
	// interface only.
	SuperchargerReader telemetry.SuperchargerReader
	// ManualChargeWriter is the manualcharge write port. Called by write handlers
	// (create/update/delete) on explicit user-initiated form submissions only.
	// See AGENTS.md "Exception: user-initiated writes" for the full amendment.
	ManualChargeWriter manualcharge.Writer
	// ManualChargeReader is the manualcharge read port. Called by read handlers
	// and the dataForCharges helper to list charge entries.
	ManualChargeReader manualcharge.Reader
	SessionSecret      string
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

	// handlers.LanguageMiddleware resolves the active render language exactly
	// once per request (design.md D3, RM24-gateway-add-i18n-foundation) and
	// MUST be registered after sessions: its signed-in branch calls
	// currentUID, which reads the session set up just above.
	r.Use(handlers.LanguageMiddleware(d.Account))

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
		Pool:               d.Pool,
		Account:            d.Account,
		Google:             d.Google,
		Tesla:              d.Tesla,
		TelemetryReader:    d.TelemetryReader,
		SuperchargerReader: d.SuperchargerReader,
		ManualChargeWriter: d.ManualChargeWriter,
		ManualChargeReader: d.ManualChargeReader,
		TeslaClientID:      d.TeslaClientID,
		TeslaClientSecret:  d.TeslaClientSecret,
		TeslaRedirectURL:   d.TeslaRedirectURL,
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
	r.POST("/ui/vehicle/select", h.VehicleSelect)
	r.GET("/healthz", h.Healthz)
	r.POST("/ui/lang/switch", h.LangSwitch)

	r.GET("/charges", h.ChargePage)
	r.GET("/ui/charges", h.ChargesContentFragment)
	r.GET("/ui/charges/list", h.ChargesListFragment)
	r.GET("/ui/charges/row/:id", h.ChargeRowStatic)
	r.GET("/ui/charges/row/:id/edit", h.ChargeRowEditFragment)
	r.POST("/ui/charges/create", h.ChargeCreate)
	r.PUT("/ui/charges/row/:id", h.ChargeRowUpdate)
	r.DELETE("/ui/charges/row/:id", h.ChargeRowDelete)

	r.GET("/supercharger-stats", h.SuperchargerStatsPage)
	r.GET("/ui/supercharger-stats", h.SuperchargerStatsFragment)

	return r, nil
}
