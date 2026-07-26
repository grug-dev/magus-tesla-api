// Package handlers holds the gateway's HTTP handlers. They receive requests, call
// domain-module interfaces, and render Templ components to HTML. Templ/htmx
// knowledge lives here and in templates/ — never in a domain module
// (ai/architecture.md §2).
package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cristianpena/magus-tesla-api/internal/account"
	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/manualcharge"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
)

// stalenessThreshold is the duration after which a snapshot is considered stale.
// At ~36 h a missed 03:30 nightly poll has elapsed (24 h cycle + 12 h buffer).
const stalenessThreshold = 36 * time.Hour

// Deps are the gateway handlers' dependencies.
type Deps struct {
	Pool    *pgxpool.Pool
	Account account.Service
	Google  *googleauth.Client
	Tesla   tesla.VehicleService
	// TelemetryReader is the telemetry read port; injected at construction.
	// The gateway calls LatestSnapshotsByAccount once per dashboard render.
	// NEVER import internal/telemetry/db — all access through this interface only.
	TelemetryReader telemetry.Reader
	// ManualChargeWriter is the manualcharge write port. Called by write handlers
	// on explicit user-initiated form submissions (create/update/delete).
	// See AGENTS.md "Exception: user-initiated writes" for constraints.
	ManualChargeWriter manualcharge.Writer
	// ManualChargeReader is the manualcharge read port. Called by read handlers
	// and the dataForCharges helper to list charge entries.
	ManualChargeReader manualcharge.Reader
	TeslaClientID      string
	TeslaClientSecret  string
	TeslaRedirectURL   string
}

// Handler carries the gateway's dependencies.
type Handler struct {
	pool               *pgxpool.Pool
	acct               account.Service
	google             *googleauth.Client
	tesla              tesla.VehicleService
	telemetryReader    telemetry.Reader
	manualChargeWriter manualcharge.Writer
	manualChargeReader manualcharge.Reader
	teslaClientID      string
	teslaClientSecret  string
	teslaRedirectURL   string
}

// New builds the gateway handlers.
func New(d Deps) *Handler {
	return &Handler{
		pool:               d.Pool,
		acct:               d.Account,
		google:             d.Google,
		tesla:              d.Tesla,
		telemetryReader:    d.TelemetryReader,
		manualChargeWriter: d.ManualChargeWriter,
		manualChargeReader: d.ManualChargeReader,
		teslaClientID:      d.TeslaClientID,
		teslaClientSecret:  d.TeslaClientSecret,
		teslaRedirectURL:   d.TeslaRedirectURL,
	}
}

// Dashboard renders the signed-in user's vehicle list page.
func (h *Handler) Dashboard(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	render(c, http.StatusOK, pages.Dashboard(h.vehiclesFor(c.Request.Context(), uid)))
}

// VehiclesFragment renders ONLY the vehicles fragment (htmx refresh).
func (h *Handler) VehiclesFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	renderFragment(c, http.StatusOK, pages.Dashboard(h.vehiclesFor(c.Request.Context(), uid)), "vehicles")
}

// vehiclesFor is the dashboard's core logic, decoupled from gin/session so it is
// unit-testable with fake account/tesla implementations. It reads the account's
// registered vehicles through the account module first; only when none are
// registered does it obtain a Tesla access token, call tesla.ListVehicles once,
// and persist the result as a one-time registration. After that first seed the
// dashboard never calls Tesla again (openspec/changes/persist-tesla-vehicles).
//
// When vehicles are already registered, it additionally calls
// h.telemetryReader.LatestSnapshotsByAccount to enrich each vehicle card with its
// latest stored nightly snapshot. If the Reader fails, it degrades gracefully —
// all vehicles render in placeholder state with a non-fatal notice.
func (h *Handler) vehiclesFor(ctx context.Context, uid uuid.UUID) fragments.VehiclesData {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		return fragments.VehiclesData{Notice: "Could not load your vehicles. Please try again."}
	}
	if len(registered) > 0 {
		// Fetch the latest snapshot per vehicle from the telemetry read port.
		// On error: log and degrade gracefully — use an empty snapshot map so all
		// vehicles render with HasSnapshot: false (placeholder). Never return early.
		snaps, snapErr := h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)
		snapMap := mergeSnapshots(snaps)
		var notice string
		if snapErr != nil {
			log.Printf("gateway: telemetry reader error for account %s: %v", uid, snapErr)
			notice = "Telemetry unavailable — showing vehicle identity only."
		}
		return fragments.VehiclesData{
			Vehicles: mapVehicles(registered, snapMap),
			Notice:   notice,
		}
	}

	// No vehicles registered yet → one-time seed from Tesla. We need a current
	// access token to make the call; the known failure modes map to the same
	// user-facing states as before.
	token, err := h.acct.AccessTokenFor(ctx, uid)
	if errors.Is(err, account.ErrNoTeslaConnection) {
		return fragments.VehiclesData{NeedsConnect: true}
	}
	if err != nil {
		return fragments.VehiclesData{Notice: "Could not reach your Tesla connection. Please try again."}
	}

	vs, err := h.tesla.ListVehicles(ctx, tesla.Credentials{AccessToken: token})
	if errors.Is(err, tesla.ErrUnauthorized) {
		return fragments.VehiclesData{Notice: "Your Tesla session expired. Please reconnect your Tesla.", NeedsConnect: true}
	}
	if err != nil {
		return fragments.VehiclesData{Notice: "Could not load your vehicles from Tesla. Please try again."}
	}

	if len(vs) == 0 {
		return fragments.VehiclesData{Notice: "No vehicles found on your Tesla account."}
	}

	seed := make([]account.SeedVehicle, 0, len(vs))
	for _, v := range vs {
		// Boundary-nil convention: empty string from the adapter maps to nil on the
		// *string domain field; non-empty maps to a pointer to a local copy.
		var accessType *string
		if v.AccessType != "" {
			at := v.AccessType // local copy — avoids loop-variable alias
			accessType = &at
		}
		seed = append(seed, account.SeedVehicle{
			TeslaID:     v.ID,
			VIN:         v.VIN,
			DisplayName: v.DisplayName,
			AccessType:  accessType,
		})
	}
	persisted, err := h.acct.SeedVehicles(ctx, uid, seed)
	if err != nil {
		// Seeding failed, but we still have the freshly-listed vehicles — degrade
		// gracefully and show them from the Tesla response so the user sees
		// something. The next dashboard load will retry the seed.
		return fragments.VehiclesData{Vehicles: mapTeslasToVehicles(vs)}
	}
	// Freshly seeded: no snapshot exists yet, so pass an empty snapshot map —
	// all newly-seeded vehicles will render the "no data yet" placeholder card.
	return fragments.VehiclesData{Vehicles: mapVehicles(persisted, nil)}
}

// isStale reports whether a snapshot captured at capturedAt is stale relative to now.
// Pure function of (capturedAt, now) so the exact-at-threshold boundary is
// deterministically testable — a wall-clock time.Since would be flaky at the edge.
func isStale(capturedAt, now time.Time) bool {
	return now.Sub(capturedAt) > stalenessThreshold
}

// mergeSnapshots builds a map from TeslaID to Snapshot for O(1) lookup per vehicle.
// A nil or empty slice produces an empty map (no panic on range).
func mergeSnapshots(snaps []telemetry.Snapshot) map[int64]telemetry.Snapshot {
	m := make(map[int64]telemetry.Snapshot, len(snaps))
	for _, s := range snaps {
		m[s.TeslaID] = s
	}
	return m
}

// mapVehicles converts the account module's clean Vehicle domain structs to the
// presentation model, enriching each with the matching snapshot from snapMap when
// available. Passing a nil snapMap produces placeholder cards for all vehicles.
//
// All derivation (km conversion, staleness, timestamp formatting, sentry-nil
// passthrough) happens here — the template receives fully-computed display fields.
func mapVehicles(vs []account.Vehicle, snapMap map[int64]telemetry.Snapshot) []fragments.Vehicle {
	out := make([]fragments.Vehicle, 0, len(vs))
	for _, v := range vs {
		fv := fragments.Vehicle{
			DisplayName: v.DisplayName,
			VIN:         v.VIN,
		}
		if snap, ok := snapMap[v.TeslaID]; ok {
			fv.HasSnapshot = true
			fv.Battery = fmt.Sprintf("%d%%", snap.BatteryLevel)
			fv.BatteryRange = fmt.Sprintf("%.1f km", snap.BatteryRangeKm())
			fv.ChargingState = snap.ChargingState
			fv.Odometer = fmt.Sprintf("%.1f km", snap.OdometerKm())
			fv.InsideTemp = fmt.Sprintf("%.1f °C", snap.InsideTemp)
			fv.OutsideTemp = fmt.Sprintf("%.1f °C", snap.OutsideTemp)
			fv.Locked = snap.Locked
			fv.SentryMode = snap.SentryMode
			fv.LastUpdated = snap.CapturedAt.UTC().Format("2006-01-02 15:04 UTC")
			fv.IsStale = isStale(snap.CapturedAt, time.Now())
		}
		out = append(out, fv)
	}
	return out
}

// mapTeslasToVehicles maps the vendor DTOs directly, used only as a fallback when
// seeding failed mid-flight.
func mapTeslasToVehicles(vs []tesla.VehicleTesla) []fragments.Vehicle {
	out := make([]fragments.Vehicle, 0, len(vs))
	for _, v := range vs {
		out = append(out, fragments.Vehicle{DisplayName: v.DisplayName, VIN: v.VIN})
	}
	return out
}

// currentUID returns the signed-in account id from the session, if any.
func currentUID(c *gin.Context) (uuid.UUID, bool) {
	s, _ := sessions.Default(c).Get("uid").(string)
	if s == "" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// ConnectTesla starts the Tesla OAuth connect flow for the signed-in user.
func (h *Handler) ConnectTesla(c *gin.Context) {
	if _, ok := currentUID(c); !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	state, err := randomState()
	if err != nil {
		c.String(http.StatusInternalServerError, "could not start Tesla connect")
		return
	}
	sess := sessions.Default(c)
	sess.Set("tesla_state", state)
	_ = sess.Save()
	c.Redirect(http.StatusFound, auth.BuildAuthURL(h.teslaClientID, h.teslaRedirectURL, state))
}

// TeslaCallback validates the state, exchanges the code, and stores the Tesla tokens
// for the signed-in user's account.
func (h *Handler) TeslaCallback(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	sess := sessions.Default(c)
	want, _ := sess.Get("tesla_state").(string)
	if want == "" || c.Query("state") != want {
		c.String(http.StatusBadRequest, "invalid oauth state")
		return
	}
	sess.Delete("tesla_state")
	_ = sess.Save()

	tokens, err := auth.ExchangeCode(h.teslaClientID, h.teslaClientSecret, c.Query("code"), h.teslaRedirectURL)
	if err != nil {
		c.String(http.StatusBadGateway, "Tesla connect failed")
		return
	}
	if err := h.acct.SaveTeslaTokens(c.Request.Context(), uid, account.TeslaTokens{
		AccessToken:     tokens.AccessToken,
		RefreshToken:    tokens.RefreshToken,
		AccessExpiresAt: time.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
	}); err != nil {
		c.String(http.StatusInternalServerError, "could not save Tesla connection")
		return
	}
	c.Redirect(http.StatusFound, "/")
}

// Home renders the landing page: auth state from the session (sign-in link when
// anonymous; email + dashboard/connect/log-out links when signed in).
func (h *Handler) Home(c *gin.Context) {
	sess := sessions.Default(c)
	uid, _ := sess.Get("uid").(string)
	email, _ := sess.Get("email").(string)

	render(c, http.StatusOK, pages.Home(pages.HomeView{
		SignedIn: uid != "",
		Email:    email,
	}))
}

// LoginPage renders the sign-in page.
func (h *Handler) LoginPage(c *gin.Context) {
	render(c, http.StatusOK, pages.Login())
}

// GoogleLogin starts the OAuth flow: store a CSRF state in the session and redirect
// to Google's consent screen.
func (h *Handler) GoogleLogin(c *gin.Context) {
	state, err := randomState()
	if err != nil {
		c.String(http.StatusInternalServerError, "could not start login")
		return
	}
	sess := sessions.Default(c)
	sess.Set("oauth_state", state)
	_ = sess.Save()
	c.Redirect(http.StatusFound, h.google.AuthCodeURL(state))
}

// GoogleCallback validates the state, exchanges the code, provisions/resolves the
// account, and establishes the authenticated session.
func (h *Handler) GoogleCallback(c *gin.Context) {
	sess := sessions.Default(c)
	want, _ := sess.Get("oauth_state").(string)
	if want == "" || c.Query("state") != want {
		c.String(http.StatusBadRequest, "invalid oauth state")
		return
	}
	sess.Delete("oauth_state")

	id, err := h.google.Exchange(c.Request.Context(), c.Query("code"))
	if err != nil {
		c.String(http.StatusBadGateway, "google login failed")
		return
	}

	acct, err := h.acct.UpsertFromOAuth(c.Request.Context(), account.OAuthIdentity{
		Provider:    "google",
		ProviderID:  id.Sub,
		Email:       id.Email,
		DisplayName: id.Name,
	})
	if err != nil {
		c.String(http.StatusInternalServerError, "could not provision account")
		return
	}

	sess.Set("uid", acct.ID.String())
	sess.Set("email", acct.Email)
	_ = sess.Save()
	c.Redirect(http.StatusFound, "/")
}

// Logout clears the session.
func (h *Handler) Logout(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Clear()
	_ = sess.Save()
	c.Redirect(http.StatusFound, "/")
}

// Healthz is the ops liveness/readiness check: 200 when the DB is reachable, 503 otherwise.
func (h *Handler) Healthz(c *gin.Context) {
	if err := h.pool.Ping(c.Request.Context()); err != nil {
		c.String(http.StatusServiceUnavailable, "unhealthy: %v", err)
		return
	}
	c.String(http.StatusOK, "ok")
}

// randomState returns a hex-encoded 256-bit CSRF state for the OAuth flow.
func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- render helpers: Templ component -> gin response ---

func render(c *gin.Context, status int, comp templ.Component) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = comp.Render(c.Request.Context(), c.Writer)
}

func renderFragment(c *gin.Context, status int, comp templ.Component, fragment string) {
	templ.Handler(comp, templ.WithStatus(status), templ.WithFragments(fragment)).ServeHTTP(c.Writer, c.Request)
}
