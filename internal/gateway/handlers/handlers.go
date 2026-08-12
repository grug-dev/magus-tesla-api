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
	"math"
	"net/http"
	"strconv"
	"strings"
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

// connectedFreshnessWindow is how recent a snapshot's CapturedAt must be for the
// primary vehicle to show as "Connected" in the nav header (DD3). It tolerates
// ONE missed nightly poll (24 h cycle + 24 h buffer); the vehicle only flips to
// "Asleep" after two missed polls. DISTINCT from stalenessThreshold (≈36 h),
// which gates the dashboard CARD stale marker — a stricter signal about a single
// reading's freshness, not ongoing connectivity. Both are named (no magic 48).
const connectedFreshnessWindow = 48 * time.Hour

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
	// SuperchargerReader is the telemetry Supercharger-sessions read port; injected
	// at construction. Called by the Supercharger Stats page/fragment handlers.
	// NEVER import internal/telemetry/db — all access through this interface only.
	SuperchargerReader telemetry.SuperchargerReader
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
	superchargerReader telemetry.SuperchargerReader
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
		superchargerReader: d.SuperchargerReader,
		manualChargeWriter: d.ManualChargeWriter,
		manualChargeReader: d.ManualChargeReader,
		teslaClientID:      d.TeslaClientID,
		teslaClientSecret:  d.TeslaClientSecret,
		teslaRedirectURL:   d.TeslaRedirectURL,
	}
}

// Dashboard renders the signed-in user's single-vehicle dashboard (the selected
// vehicle's latest telemetry as the Apex bento grid). It also performs the one-time
// Tesla vehicle SEED when the account has none registered yet (vehiclesFor owns
// that side effect), and auto-selects the first OWNER vehicle as the dashboard's
// context when the session has none. Every page reached from the dashboard then
// has a valid vehicle context.
func (h *Handler) Dashboard(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// vehiclesFor performs the one-time Tesla seed when the account has no
	// registered vehicles yet; its VehiclesData result is otherwise unused here
	// — the dashboard renders the single-vehicle bento built by dashboardFor from
	// the (now possibly freshly-seeded) registered vehicles + their latest
	// snapshot. Calling vehiclesFor first guarantees a registered vehicle exists
	// for resolveSelectedVehicle / dashboardFor to select.
	_ = h.vehiclesFor(c.Request.Context(), uid)
	// Auto-select the user's vehicle context when none is set yet (first OWNER).
	// Best-effort: a failure here does not block the dashboard — dashboardFor
	// degrades to NeedsConnect on its own.
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	selectedTeslaID := int64(0)
	if sOK {
		selectedTeslaID = selected.TeslaID
	}
	render(c, http.StatusOK, pages.Dashboard(h.dashboardFor(c.Request.Context(), uid, selectedTeslaID, browserToday(c))))
}

// DashboardFragment renders ONLY the dashboard bento fragment (htmx swap served by
// GET /ui/dashboard). The #dashboard-content region on the dashboard page
// subscribes to the "vehicle-changed" event the sidebar switcher fires (via the
// HX-Trigger response header on VehicleSelect) and re-fetches this fragment so the
// bento reflects the newly-selected vehicle WITHOUT a full page reload. It mirrors
// the Dashboard full-page handler's vehicle resolution, then renders only the
// "dashboard" fragment of pages.Dashboard — the same template tree, filtered to the
// swappable region (mirrors ChargesListFragment / NavHeaderFragment).
func (h *Handler) DashboardFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// Resolve the session's selected vehicle (auto-selecting the first OWNER when
	// none). The switch that triggered this fetch has already persisted the new
	// selection via VehicleSelect, so this reads the just-chosen vehicle.
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	selectedTeslaID := int64(0)
	if sOK {
		selectedTeslaID = selected.TeslaID
	}
	renderFragment(c, http.StatusOK, pages.Dashboard(h.dashboardFor(c.Request.Context(), uid, selectedTeslaID, browserToday(c))), "dashboard")
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

// connectedAt reports whether a snapshot captured at capturedAt is fresh enough
// (within connectedFreshnessWindow) for the nav header to show "Connected". Pure
// function of (capturedAt, now) so the exact-at-threshold boundary is
// deterministically testable, mirroring isStale. Boundary semantics: at exactly
// the window the snapshot is still connected (<= window, strict inverse of
// isStale's strict >), one nanosecond older is asleep.
func connectedAt(capturedAt, now time.Time) bool {
	return now.Sub(capturedAt) <= connectedFreshnessWindow
}

// relativeLastSeen returns a human-readable "N days ago" / "N hours ago" /
// "N minutes ago" label for a captured-at time relative to now. Pre-computed by
// the handler so the template does no time arithmetic (gateway spec invariant).
// Whole units, rounded down. Used for the "Asleep • Last seen …" label (>=48 h,
// so typically "2 days ago" or coarser); the same helper works for any age.
func relativeLastSeen(capturedAt, now time.Time) string {
	d := now.Sub(capturedAt)
	switch {
	case d >= 48*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	case d >= time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	default:
		minutes := int(d.Minutes())
		if minutes <= 1 {
			return "just now"
		}
		return fmt.Sprintf("%d minutes ago", minutes)
	}
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
			fv.Battery = fmt.Sprintf("%d%%", snap.BatteryLevelPct)
			fv.BatteryRange = fmt.Sprintf("%.1f km", snap.BatteryRangeKm)
			fv.ChargingState = snap.ChargingState
			fv.Odometer = fmt.Sprintf("%.1f km", snap.OdometerKm)
			fv.InsideTemp = fmt.Sprintf("%.1f °C", snap.InsideTempC)
			fv.OutsideTemp = fmt.Sprintf("%.1f °C", snap.OutsideTempC)
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

// dashboardFor is the single-vehicle dashboard's core logic, decoupled from
// gin/session so it is unit-testable with fake account/telemetry implementations
// (mirrors vehiclesFor). It reads the account's registered vehicles through the
// account port, selects the user's chosen vehicle (or auto-selects the first), then
// reads the latest per-vehicle snapshots through telemetry.Reader and maps the
// selected vehicle's snapshot onto a logic-free view model.
//
// Degradation rules (same resilient shape as vehiclesFor / navHeaderFor): a read
// error NEVER returns a 500 — it degrades to a flagged view model:
//   - account error              → Notice-only shell (no vehicle).
//   - no registered vehicles      → NeedsConnect (connect prompt).
//   - telemetry Reader error     → TelemetryUnavailable + vehicle identity only.
//   - selected vehicle, no snapshot → HasSnapshot=false placeholder ("—").
//
// selectedTeslaID is 0 when no selection persisted; the caller (Dashboard) has
// already run resolveSelectedVehicle, so a non-zero id is the persisted choice.
func (h *Handler) dashboardFor(ctx context.Context, uid uuid.UUID, selectedTeslaID int64, today time.Time) fragments.DashboardData {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		return fragments.DashboardData{Notice: "Could not load your dashboard. Please try again."}
	}
	if len(registered) == 0 {
		return fragments.DashboardData{NeedsConnect: true}
	}
	primary := registered[0]
	for _, v := range registered {
		if selectedTeslaID != 0 && v.TeslaID == selectedTeslaID {
			primary = v
			break
		}
	}
	vm := fragments.DashboardData{
		VehicleName:        primary.DisplayName,
		VIN:                primary.VIN,
		DefaultHistoryHref: defaultHistoryHref(today),
	}
	snaps, snapErr := h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)
	if snapErr != nil {
		log.Printf("gateway: dashboard telemetry reader error for account %s: %v", uid, snapErr)
		vm.TelemetryUnavailable = true
		vm.Notice = "Telemetry unavailable — showing vehicle identity only."
		return vm
	}
	snap, ok := mergeSnapshots(snaps)[primary.TeslaID]
	if !ok {
		// Registered vehicle, no stored snapshot yet → placeholder. No notice —
		// the hero subtitle "Awaiting first snapshot" + "—" tiles convey it.
		return vm
	}
	mapDashboardSnapshot(&vm, snap, time.Now())
	return vm
}

// mapDashboardSnapshot fills the dashboard view model's display fields from a
// latest snapshot. All derivation/rounding/unit-formatting happens here so the
// template receives fully-computed strings (gateway spec invariant). now is passed
// in (not read from a clock) so staleness is deterministic in tests, mirroring isStale.
func mapDashboardSnapshot(vm *fragments.DashboardData, snap telemetry.Snapshot, now time.Time) {
	vm.HasSnapshot = true
	vm.StatusLabel = dashStatus(snap)
	if snap.CarVersion != "" {
		vm.SoftwareVer = "Software v" + snap.CarVersion
	}
	vm.LastUpdated = snap.CapturedAt.UTC().Format("2006-01-02 15:04 UTC")
	vm.IsStale = isStale(snap.CapturedAt, now)
	vm.Odometer = formatKm(snap.OdometerKm)
	vm.InsideTemp = fmt.Sprintf("%.0f °C", snap.InsideTempC)
	vm.OutsideTemp = fmt.Sprintf("%.0f °C", snap.OutsideTempC)
	vm.Battery = fmt.Sprintf("%d%%", snap.BatteryLevelPct)
	vm.BatteryPct = strconv.Itoa(snap.BatteryLevelPct)
	vm.RangeNow = fmt.Sprintf("%.0f km", snap.BatteryRangeKm)
	if snap.ChargeLimitSocPct > 0 {
		vm.ChargeLimit = fmt.Sprintf("Limit %d%%", snap.ChargeLimitSocPct)
	}
}

// dashStatus maps a snapshot's ChargingState to the dashboard's two-state status
// vocabulary. "Charging" stays "Charging"; every other Tesla state (Stopped,
// Disconnected, Complete, NoPower, or empty) collapses to "Parked" — the dashboard
// only distinguishes actively charging from not. Presentation-only mapping; no
// business logic.
func dashStatus(s telemetry.Snapshot) string {
	if s.ChargingState == "Charging" {
		return "Charging"
	}
	return "Parked"
}

// formatKm renders a kilometre value as a whole, thousands-separated "N,NNN km"
// string — e.g. 19312.07 → "19,312 km". The value arrives already in kilometres
// from the telemetry.Reader port (converted once at capture time, not here);
// this helper only rounds and groups.
func formatKm(km float64) string {
	whole := int(math.Round(km))
	return commaGroup(strconv.Itoa(whole)) + " km"
}

// defaultHistoryHref returns the pre-formatted absolute href the #dashboard-history
// region self-loads on first render: the default 6-day-wide window ending
// YESTERDAY (today-1), because the nightly batch captures today's data tomorrow
// — sending end=today would render an always-empty last bar. The window is
// start = (today-1) - historyRangeWindowDays .. end = today-1 (end inclusive),
// formatted as /ui/dashboard/history?start=YYYY-MM-DD&end=YYYY-MM-DD. Computed
// once by the dashboard handler so the page template emits it verbatim — no
// time math in the template. today is the caller's "browser today"
// (browserToday(c)) so "yesterday" is the user's local yesterday, not UTC's;
// API contract unchanged (parseHistoryRange still accepts end=today; the
// dashboard just defaults to yesterday).
func defaultHistoryHref(today time.Time) string {
	end := today.AddDate(0, 0, -1)
	start := end.AddDate(0, 0, -historyRangeWindowDays)
	return fmt.Sprintf("/ui/dashboard/history?start=%s&end=%s",
		start.Format("2006-01-02"), end.Format("2006-01-02"))
}

// commaGroup inserts thousands separators into a non-negative integer string:
// "19312" → "19,312". Odometers are non-negative, so the sign path is intentionally
// absent. Pure string formatting — no number parsing overhead.
func commaGroup(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// NavHeaderFragment renders ONLY the nav-header fragment (htmx swap served by
// GET /ui/nav-header). The #nav-header placeholder is rendered by layouts.BaseAuth
// on every authed page; htmx fetches this fragment on load and swaps it in, keeping
// the account + telemetry reads OFF the critical page-render path (DD2).
func (h *Handler) NavHeaderFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// Ensure a vehicle is selected (auto-select first OWNER when none). The
	// switcher and every downstream page rely on a valid context.
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	selectedTeslaID := int64(0)
	if sOK {
		selectedTeslaID = selected.TeslaID
	}
	vm := h.navHeaderFor(c.Request.Context(), uid, selectedTeslaID)

	// Issue/refresh the vehicle-select CSRF token so the switcher's <select>
	// form (POST /ui/vehicle/select) is protected. Regenerated on every
	// nav-header render (including after a switch) so a fresh token rides each
	// swap; constant-time validated in VehicleSelect.
	csrf, err := generateCSRFToken()
	if err != nil {
		log.Printf("gateway: nav-header csrf token error for account %s: %v", uid, err)
		// No token → the switcher form still renders but will 403 on submit.
		// Degrade gracefully (status display is unaffected).
	} else {
		sess := sessions.Default(c)
		sess.Set(csrfVehicleSelectKey, csrf)
		_ = sess.Save()
		vm.CSRFToken = csrf
	}

	renderFragment(c, http.StatusOK, fragments.NavHeader(vm), "nav-header")
}

// VehicleSelect handles POST /ui/vehicle/select — the sidebar context switcher.
// It auth-guards, CSRF-checks (csrf_vehicle_select), validates that the chosen
// vehicle belongs to the calling user's account (tenant scoping, same pattern as
// the manual-charge write handlers), persists the selection to the session, and
// re-renders the nav-header fragment so the switcher reflects the new choice.
func (h *Handler) VehicleSelect(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRFKey(c, csrfVehicleSelectKey) {
		return
	}

	// Form value is "{TeslaID}:{VIN}" (the VehicleOptionVM.Value shape).
	raw := c.PostForm("vehicle")
	parts := strings.SplitN(raw, ":", 2)
	if len(parts) != 2 {
		c.String(http.StatusBadRequest, "invalid vehicle")
		return
	}
	teslaID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || teslaID == 0 {
		c.String(http.StatusBadRequest, "invalid vehicle id")
		return
	}
	vin := parts[1]
	if vin == "" {
		c.String(http.StatusBadRequest, "invalid vehicle")
		return
	}

	// Tenant ownership: the submitted (TeslaID, VIN) must belong to the calling
	// user's account. Cross-module FK does not exist in the DB (D4), so the
	// gateway enforces tenant scoping at the app layer. Reject silently with 403.
	vehicles, err := h.acct.RegisteredVehicles(c.Request.Context(), uid)
	if err != nil {
		c.String(http.StatusInternalServerError, "could not validate vehicle")
		return
	}
	owned := false
	for _, v := range vehicles {
		if v.TeslaID == teslaID && v.VIN == vin {
			owned = true
			break
		}
	}
	if !owned {
		c.String(http.StatusForbidden, "vehicle not in your account")
		return
	}

	setCurrentVehicle(c, teslaID, vin)

	// Re-render the nav-header fragment with the freshly-persisted selection
	// so the <select> shows the new option selected and the status reflects the
	// newly-selected vehicle. CSRF token is refreshed by NavHeaderFragment's
	// path; re-issue here so the next switch from the same fragment works.
	vm := h.navHeaderFor(c.Request.Context(), uid, teslaID)
	if csrf, err := generateCSRFToken(); err == nil {
		sess := sessions.Default(c)
		sess.Set(csrfVehicleSelectKey, csrf)
		_ = sess.Save()
		vm.CSRFToken = csrf
	}
	// Fire the cross-region refresh event. htmx bubbles "vehicle-changed" to <body>;
	// any page region listening with hx-trigger="vehicle-changed from:body" (the
	// dashboard's #dashboard-content) then re-fetches itself for the newly-selected
	// vehicle. The switcher stays page-agnostic — it fires one event, regions opt in.
	// Set before renderFragment: templ.Handler only sets Content-Type/status and does
	// not clear already-set response headers.
	c.Header("HX-Trigger", "vehicle-changed")
	renderFragment(c, http.StatusOK, fragments.NavHeader(vm), "nav-header")
}

// navHeaderFor is the nav-header's core logic, decoupled from gin/session so it
// is unit-testable with fake account/telemetry implementations. It calls ONLY
// Reader ports — account.RegisteredVehicles + telemetry.Reader.LatestSnapshotsBy
// Account (never a Writer/Collector, never Tesla). It never imports a DB package.
//
// Degradation rules (DD2 resilience): a read error never returns a 500 — the
// fragment degrades to a name-only "unavailable" state (telemetry error) or a
// no-name "unavailable" state (account error), so the page that already rendered
// stays intact.
func (h *Handler) navHeaderFor(ctx context.Context, uid uuid.UUID, selectedTeslaID int64) fragments.NavHeaderVM {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: nav-header RegisteredVehicles error for account %s: %v", uid, err)
		// No vehicles, no name — degraded Unavailable state (no dot color useful
		// since there is no vehicle to describe). NeedsConnect stays false: we
		// don't know the account state, so the connect-prompt is misleading.
		return fragments.NavHeaderVM{
			Status:      fragments.NavStatusUnavailable,
			StatusLabel: "Unavailable",
		}
	}
	if len(registered) == 0 {
		// No vehicles registered → "awaiting connect" state: connect link, no
		// dot, no name, no battery (mirrors the dashboard's NeedsConnect).
		return fragments.NavHeaderVM{NeedsConnect: true}
	}

	// Build the vehicle context-switcher options from the registered list. The
	// selected marker is set from selectedTeslaID; when 0 (no session selection
	// yet) the caller has already run resolveSelectedVehicle, so this is the
	// already-persisted id. The template only renders the <select> when there
	// is more than one vehicle, but we always populate it for parity.
	vehicles := make([]fragments.VehicleOptionVM, 0, len(registered))
	for _, v := range registered {
		vehicles = append(vehicles, fragments.VehicleOptionVM{
			TeslaID:     v.TeslaID,
			VIN:         v.VIN,
			DisplayName: v.DisplayName,
			Value:       fmt.Sprintf("%d:%s", v.TeslaID, v.VIN),
			Selected:    selectedTeslaID != 0 && v.TeslaID == selectedTeslaID,
		})
	}

	// Primary = the selected vehicle when known, else the first registered entry
	// (the auto-select policy in resolveSelectedVehicle picks the first OWNER,
	// which is also registered[0] in the single-vehicle case). The status dot +
	// battery reflect THIS vehicle's latest snapshot, not just registered[0].
	primary := registered[0]
	if selectedTeslaID != 0 {
		for _, v := range registered {
			if v.TeslaID == selectedTeslaID {
				primary = v
				break
			}
		}
	}

	vm := fragments.NavHeaderVM{
		Vehicles:               vehicles,
		SelectedVehicleTeslaID: primary.TeslaID,
	}

	// Read the latest snapshot per vehicle for this account. On error: degrade —
	// keep the vehicle name, show "Unavailable", no battery. Never return early.
	snaps, snapErr := h.telemetryReader.LatestSnapshotsByAccount(ctx, uid)
	if snapErr != nil {
		log.Printf("gateway: nav-header telemetry reader error for account %s: %v", uid, snapErr)
		vm.VehicleName = primary.DisplayName
		vm.Status = fragments.NavStatusUnavailable
		vm.StatusLabel = "Unavailable"
		return vm
	}

	// Merge by TeslaID to find the primary vehicle's latest snapshot.
	snapMap := mergeSnapshots(snaps)
	snap, ok := snapMap[primary.TeslaID]
	if !ok {
		// Registered vehicle, but no stored snapshot yet → awaiting.
		vm.VehicleName = primary.DisplayName
		vm.Status = fragments.NavStatusAwaiting
		vm.StatusLabel = "Awaiting first snapshot"
		return vm
	}

	now := time.Now()
	if connectedAt(snap.CapturedAt, now) {
		// Fresh snapshot → Connected + battery %.
		vm.VehicleName = primary.DisplayName
		vm.Status = fragments.NavStatusConnected
		vm.StatusLabel = "Connected"
		vm.BatteryPct = fmt.Sprintf("%d%%", snap.BatteryLevelPct)
		return vm
	}
	// Stale snapshot → Asleep + relative "Last seen" label.
	vm.VehicleName = primary.DisplayName
	vm.Status = fragments.NavStatusAsleep
	vm.StatusLabel = "Asleep"
	vm.LastSeenLabel = relativeLastSeen(snap.CapturedAt, now)
	return vm
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

// sessionVehicleKey prefixes the session keys that carry the user's selected
// vehicle context (the vehicle switcher in the sidebar). Two keys keep the
// TeslaID (stable identity used by all module ports) and the VIN (durable
// human-readable key, useful for display/links) in sync.
const (
	sessionTeslaIDKey = "sel_tesla_id"
	sessionVINKey     = "sel_vin"
)

// currentVehicle returns the user's selected vehicle context (TeslaID + VIN)
// from the session. Both fields are zero/empty when no vehicle is selected.
// Mirrors currentUID so handlers read the context the same way they read the
// account id — one helper, no gin coupling for the value itself.
func currentVehicle(c *gin.Context) (teslaID int64, vin string, ok bool) {
	sess := sessions.Default(c)
	idVal, _ := sess.Get(sessionTeslaIDKey).(int64)
	vin, _ = sess.Get(sessionVINKey).(string)
	return idVal, vin, idVal != 0 && vin != ""
}

// setCurrentVehicle persists the selected vehicle to the session. teslaID must
// be non-zero and vin non-empty; callers validate ownership before calling.
func setCurrentVehicle(c *gin.Context, teslaID int64, vin string) {
	sess := sessions.Default(c)
	sess.Set(sessionTeslaIDKey, teslaID)
	sess.Set(sessionVINKey, vin)
	_ = sess.Save()
}

// clearCurrentVehicle removes the selected-vehicle context (e.g. on logout).
// Logout clears the whole session so this is only useful for edge cases.
func clearCurrentVehicle(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Delete(sessionTeslaIDKey)
	sess.Delete(sessionVINKey)
	_ = sess.Save()
}

// resolveSelectedVehicle returns the selected vehicle for the current session,
// auto-selecting the first OWNER vehicle when none is chosen yet. It calls
// RegisteredVehicles once (the same call most handlers already need) and is the
// single place that owns the auto-select policy, so every authed handler can
// rely on it returning a consistent (TeslaID, VIN, account.Vehicle) triple.
// Returns ok=false when the account has no vehicles or the read failed (caller
// then shows the NeedsConnect / degraded UX it already has).
func (h *Handler) resolveSelectedVehicle(ctx context.Context, c *gin.Context, uid uuid.UUID) (account.Vehicle, bool) {
	// Already selected in session → validate it still belongs to the account.
	if teslaID, _, sOK := currentVehicle(c); sOK {
		vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
		if err == nil {
			for _, v := range vehicles {
				if v.TeslaID == teslaID {
					return v, true
				}
			}
		}
		// Stale selection (vehicle removed) — fall through to re-select.
		clearCurrentVehicle(c)
	}

	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil || len(vehicles) == 0 {
		return account.Vehicle{}, false
	}
	// Auto-select first OWNER vehicle; fall back to the first vehicle when none
	// is flagged OWNER (the account only has DRIVER vehicles, e.g. shared cars).
	chosen := vehicles[0]
	for _, v := range vehicles {
		if v.AccessType != nil && *v.AccessType == "OWNER" {
			chosen = v
			break
		}
	}
	setCurrentVehicle(c, chosen.TeslaID, chosen.VIN)
	return chosen, true
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
	c.Redirect(http.StatusFound, "/dashboard")
}

// Home is the entry route. Authenticated users go straight to the dashboard;
// anonymous users are sent to sign in. The old landing page (pages.Home) is no
// longer surfaced — the dashboard is the default authenticated experience.
func (h *Handler) Home(c *gin.Context) {
	if _, ok := currentUID(c); ok {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
	c.Redirect(http.StatusFound, "/login")
}

// LoginPage renders the sign-in page. An already-authenticated user is bounced
// to the dashboard — no point showing the login form to a signed-in session.
func (h *Handler) LoginPage(c *gin.Context) {
	if _, ok := currentUID(c); ok {
		c.Redirect(http.StatusFound, "/dashboard")
		return
	}
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
	c.Redirect(http.StatusFound, "/dashboard")
}

// Logout clears the session and sends the user back to the sign-in page.
func (h *Handler) Logout(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Clear()
	_ = sess.Save()
	c.Redirect(http.StatusFound, "/login")
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

// errorFragmentHeader marks a non-2xx response whose body is a deliberately
// re-rendered app fragment (a form carrying field-level validation errors, an
// error row, …) rather than an incidental error page.
//
// Why it exists: htmx's default responseHandling config
// ([{204,swap:false},{[23]..,swap:true},{[45]..,swap:false,error:true}]) does NOT
// swap 4xx/5xx bodies, and HX-Reswap cannot change that — it only overrides the
// swap STYLE (swapOverride); the shouldSwap decision comes solely from
// responseHandling and is only mutable from an htmx:beforeSwap listener. So every
// error fragment a handler renders is discarded by the browser unless we opt it in.
//
// The listener in layouts.Base honours this header by setting shouldSwap. Keeping
// the opt-in per response (instead of globally swapping 4xx/5xx) means an
// incidental error body we did NOT author — a proxy's 502 HTML page, a gin panic's
// plain-text 500 — is still never swapped into the DOM.
const errorFragmentHeader = "HX-Error-Fragment"

func render(c *gin.Context, status int, comp templ.Component) {
	c.Status(status)
	c.Header("Content-Type", "text/html; charset=utf-8")
	_ = comp.Render(c.Request.Context(), c.Writer)
}

// renderError is render for a non-2xx response whose body IS a renderable app
// fragment. Identical to render except it sets errorFragmentHeader so htmx swaps
// the body in instead of dropping it. Use it for every validation/error fragment;
// use c.String for bare error text that has no fragment to show.
func renderError(c *gin.Context, status int, comp templ.Component) {
	c.Header(errorFragmentHeader, "true")
	render(c, status, comp)
}

// renderFragmentError is renderFragment's non-2xx sibling — see renderError.
func renderFragmentError(c *gin.Context, status int, comp templ.Component, fragmentNames ...string) {
	c.Header(errorFragmentHeader, "true")
	renderFragment(c, status, comp, fragmentNames...)
}

// renderFragment emits only the named templ fragment(s) of comp — the htmx-swap path
// (contrast with render, which emits the whole page). Pass one name for a single
// region (the common case), or several to emit multiple sibling fragments in one
// response body (e.g. GET /ui/charges returns the create-form + list regions that
// live inside #charges-content). fragmentNames (not "fragments") avoids shadowing the
// imported templates/fragments package.
func renderFragment(c *gin.Context, status int, comp templ.Component, fragmentNames ...string) {
	// templ.WithFragments takes ...any, so widen the []string. Passing several names
	// emits each sibling fragment in one response body.
	ids := make([]any, len(fragmentNames))
	for i, n := range fragmentNames {
		ids[i] = n
	}
	templ.Handler(comp, templ.WithStatus(status), templ.WithFragments(ids...)).ServeHTTP(c.Writer, c.Request)
}
