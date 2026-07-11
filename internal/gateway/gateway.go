// Package gateway is the UI gateway — the single place HTML lives (ai/architecture.md
// §2). It builds the web engine: cookie sessions, embedded static assets, and the
// routes that map to Templ-rendering handlers.
package gateway

import (
	"crypto/sha256"
	"embed"
	"io/fs"
	"net/http"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/handlers"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
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
	Pool          *pgxpool.Pool
	Account       account.Service
	Google        *googleauth.Client
	Tesla         tesla.VehicleService
	// TelemetryReader is the telemetry read port. The gateway calls
	// LatestSnapshotsByAccount once per dashboard render to populate vehicle card
	// telemetry. Injected from cmd/web via telemetry.NewReader(pool).
	// NEVER import internal/telemetry/db (telemetrydb) — all access through this
	// interface only.
	TelemetryReader   telemetry.Reader
	SessionSecret string
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

	// Embedded static assets at /static (strip the "static" prefix from the embed FS).
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	r.StaticFS("/static", http.FS(sub))

	h := handlers.New(handlers.Deps{
		Pool:              d.Pool,
		Account:           d.Account,
		Google:            d.Google,
		Tesla:             d.Tesla,
		TelemetryReader:   d.TelemetryReader,
		TeslaClientID:     d.TeslaClientID,
		TeslaClientSecret: d.TeslaClientSecret,
		TeslaRedirectURL:  d.TeslaRedirectURL,
	})

	r.GET("/", h.Home)
	r.GET("/login", h.LoginPage)
	r.GET("/auth/google/login", h.GoogleLogin)
	r.GET("/auth/google/callback", h.GoogleCallback)
	r.POST("/logout", h.Logout)
	r.GET("/connect/tesla", h.ConnectTesla)
	r.GET("/connect/tesla/callback", h.TeslaCallback)
	r.GET("/dashboard", h.Dashboard)
	r.GET("/ui/vehicles", h.VehiclesFragment)
	r.GET("/ui/health", h.HealthFragment)
	r.GET("/healthz", h.Healthz)

	return r, nil
}
