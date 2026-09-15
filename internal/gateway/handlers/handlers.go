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
	"github.com/cristianpena/magus-tesla-api/internal/analytics"
	"github.com/cristianpena/magus-tesla-api/internal/auth"
	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/clock"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/googleauth"
	"github.com/cristianpena/magus-tesla-api/internal/tesla"
	"github.com/cristianpena/magus-tesla-api/internal/vehicleref"
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
	// SuperchargerReader is the charging module's SessionReader port over
	// charge_sessions; injected at construction. Called by the Supercharger
	// Stats page/fragment handlers, one ListSessionsByVehicleBetween read per
	// render bounded by the requested ?start=&end= window.
	// NEVER import internal/charging/db (chargingdb) for this path — all
	// access through this interface only.
	SuperchargerReader charging.SessionReader
	// SuperchargerVerifier is the charging module's SessionVerifier write
	// port over charge_sessions; injected at construction. Called ONLY by
	// SuperchargerRowUpdate (PATCH /ui/supercharger-stats/row/:id), on an
	// explicit user-initiated row save, to call VerifySession — the sole
	// write this port permits (start/end battery percentage correction).
	// See AGENTS.md "Exception: Supercharger session battery verification"
	// for constraints. NEVER import internal/charging/db (chargingdb) for
	// this path — all access through this interface only. Added by
	// RM31-gateway-add-session-battery-edit.
	SuperchargerVerifier charging.SessionVerifier
	// ChargingWriter is the charging write port. Called by write handlers
	// on explicit user-initiated form submissions (create/update/delete).
	// See AGENTS.md "Exception: user-initiated writes" for constraints.
	ChargingWriter charging.Writer
	// ChargingReader is the charging read port. Called by read handlers
	// and the dataForCharges helper to list charge entries.
	ChargingReader charging.Reader
	// AnalyticsReader is the analytics module's read port; injected at
	// construction (mirrors TelemetryReader/SuperchargerReader/
	// ChargingReader — the gateway calls ConsumedByDay once per history
	// fragment render). NEVER construct an internal/analytics internal type
	// here — internal/analytics owns no database, so there is no db package
	// this could even accidentally import.
	AnalyticsReader analytics.Reader
	// AnalyticsRecalculator is the analytics module's write-path port
	// (RM29-analytics-add-vehicle-metrics design.md D5). Called ONLY by the
	// manual-charge write handlers (ExternalChargeCreate, ExternalChargeRowUpdate,
	// ExternalChargeRowDelete), after their corresponding chargingWriter call
	// succeeds, so the precomputed history charts reflect the edit
	// immediately (mirrors ChargingWriter's own narrow write-aperture
	// exception — AGENTS.md "Exception: user-initiated writes"). Never
	// called from a Reader-only handler.
	AnalyticsRecalculator analytics.Recalculator
	TeslaClientID         string
	TeslaClientSecret     string
	TeslaRedirectURL      string
	// VehicleImageResolver maps a vehicle's (CarType, ExteriorColor) to a
	// /static/img/<carType><ExteriorColor>.png URL, falling back to
	// defaultCar.png when either field is unset or the composed file is not in
	// the embedded image set. Built by the gateway package from its embedded
	// static/img subtree (handlers cannot embed ../static) and injected here.
	VehicleImageResolver VehicleImageResolver
}

// Handler carries the gateway's dependencies.
type Handler struct {
	pool                  *pgxpool.Pool
	acct                  account.Service
	google                *googleauth.Client
	tesla                 tesla.VehicleService
	superchargerReader    charging.SessionReader
	superchargerVerifier  charging.SessionVerifier
	chargingWriter        charging.Writer
	chargingReader        charging.Reader
	analyticsReader       analytics.Reader
	analyticsRecalculator analytics.Recalculator
	teslaClientID         string
	teslaClientSecret     string
	teslaRedirectURL      string
	vehicleImage          VehicleImageResolver
}

// VehicleImageResolver maps a vehicle's (CarType, ExteriorColor) to a static
// image URL with a defaultCar.png fallback. Implemented by the gateway package
// against its embedded static/img set.
type VehicleImageResolver func(carType, exteriorColor *string) string

// vehicleImageDefaultURL is the fallback image used when the resolver is unset
// (test fakes) or when (CarType, ExteriorColor) yields no matching embedded
// image. Kept here so handlers need not import the gateway package that owns
// the embedded FS.
const vehicleImageDefaultURL = "/static/img/defaultCar.png"

// New builds the gateway handlers.
func New(d Deps) *Handler {
	resolver := d.VehicleImageResolver
	if resolver == nil {
		// Nil-safe default so test/fake Handlers (and any construction that
		// omits the resolver) degrade to the fallback image rather than
		// nil-deref on every dashboard render.
		resolver = func(_, _ *string) string { return vehicleImageDefaultURL }
	}
	return &Handler{
		pool:                  d.Pool,
		acct:                  d.Account,
		google:                d.Google,
		tesla:                 d.Tesla,
		superchargerReader:    d.SuperchargerReader,
		superchargerVerifier:  d.SuperchargerVerifier,
		chargingWriter:        d.ChargingWriter,
		chargingReader:        d.ChargingReader,
		analyticsReader:       d.AnalyticsReader,
		analyticsRecalculator: d.AnalyticsRecalculator,
		teslaClientID:         d.TeslaClientID,
		teslaClientSecret:     d.TeslaClientSecret,
		teslaRedirectURL:      d.TeslaRedirectURL,
		vehicleImage:          resolver,
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
// swappable region (mirrors ExternalChargesListFragment / NavHeaderFragment).
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
// h.analyticsReader.LatestMetricsForVehicles to enrich each vehicle card with its
// latest per-vehicle status (RM38-gateway-read-dashboard-from-metrics, design.md
// D3 — replaces the earlier snapshot-based reader call, retired by RM40). If
// the Reader fails, it degrades gracefully — all vehicles render in placeholder
// state with a non-fatal notice.
func (h *Handler) vehiclesFor(ctx context.Context, uid uuid.UUID) fragments.VehiclesData {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		return fragments.VehiclesData{Notice: i18n.T(ctx, i18n.KeyVehiclesNoticeCouldNotLoadVehicles)}
	}
	if len(registered) > 0 {
		// Fetch the latest status per vehicle from the analytics read port.
		// On error: log and degrade gracefully — use an empty status map so all
		// vehicles render with HasSnapshot: false (placeholder). Never return early.
		statuses, statusErr := h.analyticsReader.LatestMetricsForVehicles(ctx, vehicleref.All(teslaIDsOf(registered)))
		statusMap := mergeVehicleStatuses(statuses)
		var notice string
		if statusErr != nil {
			log.Printf("gateway: analytics reader error for account %s: %v", uid, statusErr)
			notice = i18n.T(ctx, i18n.KeyVehiclesNoticeTelemetryUnavailable)
		}
		return fragments.VehiclesData{
			Vehicles: mapVehicles(registered, statusMap),
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
		return fragments.VehiclesData{Notice: i18n.T(ctx, i18n.KeyVehiclesNoticeCouldNotReachTesla)}
	}

	vs, err := h.tesla.ListVehicles(ctx, tesla.Credentials{AccessToken: token})
	if errors.Is(err, tesla.ErrUnauthorized) {
		return fragments.VehiclesData{Notice: i18n.T(ctx, i18n.KeyVehiclesNoticeSessionExpired), NeedsConnect: true}
	}
	if err != nil {
		return fragments.VehiclesData{Notice: i18n.T(ctx, i18n.KeyVehiclesNoticeCouldNotLoadFromTesla)}
	}

	if len(vs) == 0 {
		return fragments.VehiclesData{Notice: i18n.T(ctx, i18n.KeyVehiclesNoticeNoVehiclesFound)}
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

// mergeVehicleStatuses builds a map from TeslaID to VehicleStatus for O(1) lookup
// per vehicle. A nil or empty slice produces an empty map (no panic on range).
// Renamed from mergeSnapshots: same shape, new source type --
// LatestMetricsForVehicles already returns at most one row per TeslaID, so,
// exactly as before with
// LatestSnapshotsByVehicles, this function does no de-duplication of its own; it
// only indexes an already-unique slice for lookup by an arbitrary caller-supplied
// TeslaID (the selected/primary vehicle), which the slice's own uniqueness does
// not provide by itself.
func mergeVehicleStatuses(statuses []analytics.VehicleStatus) map[int64]analytics.VehicleStatus {
	m := make(map[int64]analytics.VehicleStatus, len(statuses))
	for _, s := range statuses {
		m[s.TeslaID] = s
	}
	return m
}

// vehiclesTempOrDash formats a nilable temperature at this region's existing
// precision (one decimal). nil -> "—". Deliberately NOT shared with
// dashTempOrDash (dashboard hero's own %.0f precision) — see design.md D2
// "Rejected" note (RM38-gateway-read-dashboard-from-metrics).
func vehiclesTempOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.1f °C", *v)
}

// mapVehicles converts the account module's clean Vehicle domain structs to the
// presentation model, enriching each with the matching status from statusMap when
// available. Passing a nil statusMap produces placeholder cards for all vehicles.
//
// All derivation (km conversion, staleness, timestamp formatting, nil-safe
// formatting) happens here — the template receives fully-computed display fields.
//
// Retyped from the retired snapshot-based status map (RM40) to
// map[int64]analytics.VehicleStatus
// (RM38-gateway-read-dashboard-from-metrics, design.md D3): ChargingState/
// InsideTempC/OutsideTempC/CapturedAt are now pointers; nil never fabricates a
// value. Locked is now *bool on fragments.Vehicle too (design.md D3) since the
// source is now nilable.
func mapVehicles(vs []account.Vehicle, statusMap map[int64]analytics.VehicleStatus) []fragments.Vehicle {
	out := make([]fragments.Vehicle, 0, len(vs))
	for _, v := range vs {
		fv := fragments.Vehicle{
			DisplayName: v.DisplayName,
			VIN:         v.VIN,
		}
		if s, ok := statusMap[v.TeslaID]; ok {
			fv.HasSnapshot = true
			fv.Battery = fmt.Sprintf("%d%%", s.BatteryLevelPct)
			fv.BatteryRange = fmt.Sprintf("%.1f km", s.BatteryRangeKm)
			if s.ChargingState != nil {
				fv.ChargingState = *s.ChargingState
			}
			fv.Odometer = fmt.Sprintf("%.1f km", s.OdometerKm)
			fv.InsideTemp = vehiclesTempOrDash(s.InsideTempC)
			fv.OutsideTemp = vehiclesTempOrDash(s.OutsideTempC)
			fv.Locked = s.Locked
			fv.SentryMode = s.SentryMode
			if s.CapturedAt != nil {
				fv.LastUpdated = s.CapturedAt.UTC().Format("2006-01-02 15:04 UTC")
				fv.IsStale = isStale(*s.CapturedAt, clock.Now())
			}
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
// gin/session so it is unit-testable with fake account/analytics implementations
// (mirrors vehiclesFor). It reads the account's registered vehicles through the
// account port, selects the user's chosen vehicle (or auto-selects the first), then
// reads the latest per-vehicle status through analytics.Reader.LatestMetricsForVehicles
// (RM38 tier 2 — it was a retired snapshot-based reader before, since removed by
// RM40) and maps the selected vehicle's status onto a logic-free view model.
//
// Degradation rules (same resilient shape as vehiclesFor / navHeaderFor): a read
// error NEVER returns a 500 — it degrades to a flagged view model:
//   - account error              → Notice-only shell (no vehicle).
//   - no registered vehicles      → NeedsConnect (connect prompt).
//   - analytics Reader error     → TelemetryUnavailable + vehicle identity only.
//   - selected vehicle, no snapshot → HasSnapshot=false placeholder ("—").
//
// selectedTeslaID is 0 when no selection persisted; the caller (Dashboard) has
// already run resolveSelectedVehicle, so a non-zero id is the persisted choice.
func (h *Handler) dashboardFor(ctx context.Context, uid uuid.UUID, selectedTeslaID int64, today time.Time) fragments.DashboardData {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		return fragments.DashboardData{Notice: i18n.T(ctx, i18n.KeyDashboardNoticeCouldNotLoadDashboard)}
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
		VehicleImage:       h.vehicleImage(primary.CarType, primary.ExteriorColor),
		DefaultHistoryHref: defaultHistoryHref(today),
	}
	statuses, statusErr := h.analyticsReader.LatestMetricsForVehicles(ctx, vehicleref.All(teslaIDsOf(registered)))
	if statusErr != nil {
		log.Printf("gateway: dashboard analytics reader error for account %s: %v", uid, statusErr)
		vm.TelemetryUnavailable = true
		// Reuses the vehicles_notice key (leader amendment): byte-identical copy to
		// vehiclesFor's telemetry-unavailable notice — no separate dashboard_notice
		// key exists for this string (design.md D2 reuse).
		vm.Notice = i18n.T(ctx, i18n.KeyVehiclesNoticeTelemetryUnavailable)
		return vm
	}
	vs, ok := mergeVehicleStatuses(statuses)[primary.TeslaID]
	if !ok {
		// Registered vehicle, no stored status yet → placeholder. No notice —
		// the hero subtitle "Awaiting first snapshot" + "—" tiles convey it.
		return vm
	}
	mapDashboardSnapshot(ctx, &vm, vs, clock.Now())
	return vm
}

// dashTempOrDash formats a nilable temperature at the dashboard hero's existing
// precision (whole degrees). nil -> "—", the existing HasSnapshot placeholder
// (design.md D2) — a vehicle_metrics row can exist (HasSnapshot true) while its
// inside/outside temp columns are still NULL (pre-migration row). Deliberately
// NOT shared with vehiclesFor's own nil-temp formatter (%.1f precision, a
// different display region) — see design.md D2 "Rejected" note.
func dashTempOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.0f °C", *v)
}

// dashCountOrDash formats a nilable whole-number count for the dashboard hero.
// nil -> "—", the same placeholder dashTempOrDash uses, because the counter's
// NULL is equally ambiguous (the vehicle did not report it, or the
// vehicle_metrics row predates the column) and neither case is a zero. A
// reported 0 is a real reading and renders as "0".
func dashCountOrDash(v *int) string {
	if v == nil {
		return "—"
	}
	return strconv.Itoa(*v)
}

// dashDistanceOrDash formats the latest day's driven distance. nil -> "—" (no
// predecessor day, or the row predates RM50 tier 1). Reuses formatKm — the same
// formatter the Odometer tile already uses — so a raw/negative correction-day
// value (see analytics.VehicleStatus.DistanceTraveledKmCalc's own doc comment:
// "never averaged", never clamped) renders exactly as the history chart already
// renders the same field (formatKmRaw in history.go) — not a new edge case.
func dashDistanceOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatKm(*v)
}

// dashBatteryUsedOrDash formats the latest day's battery percent used. nil -> "—",
// same rule as dashDistanceOrDash. Reuses formatPctRaw — the consumed chart's own
// one-decimal formatter — so the tile and the chart agree on precision.
func dashBatteryUsedOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatPctRaw(*v) + "%"
}

// dashEfficiencyOrDash formats the latest day's driving efficiency. nil -> "—",
// which means either the day has no predecessor or its battery used was zero or
// less. Both are real "we cannot say" cases, never a zero.
func dashEfficiencyOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatKmPerPct(*v)
}

// dashPSIOrDash formats a raw tyre-pressure reading. nil -> "—" (the vehicle did
// not report TPMS at capture, or the row predates the RM50 tier 1 migration).
// Same nil-placeholder rule as dashTempOrDash.
func dashPSIOrDash(v *float64) string {
	if v == nil {
		return "—"
	}
	return formatPSI(*v)
}

// dashTireTrend maps a tyre-pressure delta to a StatTile Trend value. nil (no
// predecessor day, or either day's raw wheel reading missing — analytics.
// VehicleStatus.TpmsPressureFLPSICalc's own doc comment) and exactly 0.0 (a
// real "no change" reading, RD13) both render no icon: ui.StatTileProps.Trend
// only models "up"/"down"/"", and the roadmap explicitly forbids inventing a
// third, neutral glyph. Positive -> "up", negative -> "down".
func dashTireTrend(v *float64) string {
	if v == nil || *v == 0 {
		return ""
	}
	if *v > 0 {
		return "up"
	}
	return "down"
}

// dashTireDelta formats a tyre-pressure delta as the tile's stat-desc line
// (RD10), e.g. "+0.4 vs prev. day". nil -> "" (no line at all) — never a
// fabricated "0.0 vs prev. day" for an unknown delta. A real 0.0 DOES render
// ("0.0 vs prev. day"), because it is a known value, not an absent one (RD13)
// — distinct from dashTireTrend's own "0.0 gets no icon" rule; the two
// functions answer different questions from the same input.
func dashTireDelta(ctx context.Context, v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardTireDeltaDesc), formatSignedPSI(*v))
}

// dashTireWheel builds one wheel's TireWheelVM from its raw reading and delta.
// Small builder so mapDashboardSnapshot's four call sites (D6) are one line
// each instead of three.
func dashTireWheel(ctx context.Context, raw, delta *float64) fragments.TireWheelVM {
	return fragments.TireWheelVM{
		Value: dashPSIOrDash(raw),
		Trend: dashTireTrend(delta),
		Delta: dashTireDelta(ctx, delta),
	}
}

// mapDashboardSnapshot fills the dashboard view model's display fields from the
// account's latest per-vehicle status row. All derivation/rounding/unit-formatting
// happens here so the template receives fully-computed strings (gateway spec
// invariant). now is passed in (not read from a clock) so staleness is
// deterministic in tests, mirroring isStale. ctx threads through to dashStatus and
// the software-version i18n.T lookup (D5).
//
// Retyped from the retired snapshot type (RM40) to analytics.VehicleStatus
// (RM38-gateway-read-dashboard-from-metrics, design.md D2): eight fields are now
// pointers (InsideTempC, OutsideTempC, CarVersion, ChargeLimitSocPct,
// ChargingState, CapturedAt, Locked, SentryMode) tracking whether that column has
// been computed since the RM38 migration. nil never fabricates a value — it omits
// the corresponding display field per the per-field table in design.md D2.
func mapDashboardSnapshot(ctx context.Context, vm *fragments.DashboardData, vs analytics.VehicleStatus, now time.Time) {
	vm.HasSnapshot = true
	vm.StatusLabel = dashStatus(ctx, vs.ChargingState)
	if vs.CarVersion != nil && *vs.CarVersion != "" {
		vm.SoftwareVer = fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardStatusSoftwareVersion), *vs.CarVersion)
	}
	if vs.CapturedAt != nil {
		vm.LastUpdated = vs.CapturedAt.UTC().Format("2006-01-02")
		vm.IsStale = isStale(*vs.CapturedAt, now)
	}
	vm.Odometer = formatKm(vs.OdometerKm)
	vm.InsideTemp = dashTempOrDash(vs.InsideTempC)
	vm.OutsideTemp = dashTempOrDash(vs.OutsideTempC)
	vm.MaxRangeCharges = dashCountOrDash(vs.MaxRangeChargeCounter)
	vm.DistanceTraveled = dashDistanceOrDash(vs.DistanceTraveledKmCalc)
	vm.BatteryUsed = dashBatteryUsedOrDash(vs.ConsumedPct)
	vm.Efficiency = dashEfficiencyOrDash(vs.KmPerPctCalc)
	vm.TirePressureFL = dashTireWheel(ctx, vs.TpmsPressureFLPSI, vs.TpmsPressureFLPSICalc)
	vm.TirePressureFR = dashTireWheel(ctx, vs.TpmsPressureFRPSI, vs.TpmsPressureFRPSICalc)
	vm.TirePressureRL = dashTireWheel(ctx, vs.TpmsPressureRLPSI, vs.TpmsPressureRLPSICalc)
	vm.TirePressureRR = dashTireWheel(ctx, vs.TpmsPressureRRPSI, vs.TpmsPressureRRPSICalc)
	vm.Battery = fmt.Sprintf("%d%%", vs.BatteryLevelPct)
	vm.BatteryPct = strconv.Itoa(vs.BatteryLevelPct)
	vm.RangeNow = fmt.Sprintf("%.0f km", vs.BatteryRangeKm)
	if vs.ChargeLimitSocPct != nil && *vs.ChargeLimitSocPct > 0 {
		vm.ChargeLimit = fmt.Sprintf(i18n.T(ctx, i18n.KeyDashboardChargeLimit), *vs.ChargeLimitSocPct)
	}
	vm.Locked = vs.Locked
	vm.SentryMode = vs.SentryMode
}

// dashStatus maps a snapshot's ChargingState to the dashboard's two-state status
// vocabulary. nil (not yet recomputed since the RM38 migration) collapses to
// "Parked", identical to the pre-migration behavior for an empty string — there is
// no third visual state for "unknown charging state" on the dashboard subtitle
// (design.md D2, RM38-gateway-read-dashboard-from-metrics).
func dashStatus(ctx context.Context, chargingState *string) string {
	if chargingState != nil && *chargingState == "Charging" {
		return i18n.T(ctx, i18n.KeyDashboardStatusCharging)
	}
	return i18n.T(ctx, i18n.KeyDashboardStatusParked)
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

// NavHeaderFragment renders ONLY the nav-header fragment (htmx swap served by
// GET /ui/nav-header). The #nav-header placeholder is rendered by layouts.BaseAuth
// on every authed page; htmx fetches this fragment on load and on the
// "vehicle-changed" event (so the status dot/battery reflect the switched-to
// vehicle), keeping the account + telemetry reads OFF the critical page-render
// path (DD2).
//
// The vehicle context-switcher <select> used to live here; it now lives in its
// own fragment (GET /ui/vehicle-select, see VehicleSelectFragment), so this
// handler no longer issues the vehicle-select CSRF token — that moved with the
// switcher.
func (h *Handler) NavHeaderFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	// Ensure a vehicle is selected (auto-select first OWNER when none). The
	// status dot and every downstream page rely on a valid context.
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	selectedTeslaID := int64(0)
	if sOK {
		selectedTeslaID = selected.TeslaID
	}
	vm := h.navHeaderFor(c.Request.Context(), uid, selectedTeslaID, browserToday(c))
	renderFragment(c, http.StatusOK, fragments.NavHeader(vm), "nav-header")
}

// VehicleSelectFragment renders ONLY the vehicle-switcher fragment (htmx swap
// served by GET /ui/vehicle-select). The #vehicle-select placeholder is mounted
// by layouts.BaseAuth in the navbar before the language switcher; htmx fetches
// this fragment on load. Authenticated-only by construction (BaseAuth is the
// shell for authed pages; the anonymous Base shell never renders the navbar).
//
// Issues/refreshes the vehicle-select CSRF token (csrf_vehicle_select) so the
// switcher's <select> form (POST /ui/vehicle/select) is protected — the token
// responsibility moved here from NavHeaderFragment when the switcher split out.
func (h *Handler) VehicleSelectFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	selectedTeslaID := int64(0)
	if sOK {
		selectedTeslaID = selected.TeslaID
	}
	vm := h.vehicleSelectFor(c.Request.Context(), uid, selectedTeslaID)
	csrf, err := generateCSRFToken()
	if err != nil {
		log.Printf("gateway: vehicle-select csrf token error for account %s: %v", uid, err)
		// No token → the switcher form still renders but will 403 on submit.
		// Degrade gracefully (the select itself is unaffected).
	} else {
		sess := sessions.Default(c)
		sess.Set(csrfVehicleSelectKey, csrf)
		_ = sess.Save()
		vm.CSRFToken = csrf
	}
	renderFragment(c, http.StatusOK, fragments.VehicleSelect(vm), "vehicle-select")
}

// VehicleSelect handles POST /ui/vehicle/select — the navbar context switcher.
// It auth-guards, CSRF-checks (csrf_vehicle_select), validates that the chosen
// vehicle belongs to the calling user's account (tenant scoping, same pattern as
// the manual-charge write handlers), persists the selection to the session, and
// re-renders the vehicle-select fragment so the switcher reflects the new choice.
// It fires "vehicle-changed" via HX-Trigger so nav-header (status dot/battery),
// the dashboard, and the charges region re-fetch themselves for the newly-
// selected vehicle.
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
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorInvalidVehicle))
		return
	}
	teslaID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || teslaID == 0 {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorInvalidVehicleID))
		return
	}
	vin := parts[1]
	if vin == "" {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorInvalidVehicle))
		return
	}

	// Tenant ownership: the submitted (TeslaID, VIN) must belong to the calling
	// user's account. Cross-module FK does not exist in the DB (D4), so the
	// gateway enforces tenant scoping at the app layer. Reject silently with 403.
	vehicles, err := h.acct.RegisteredVehicles(c.Request.Context(), uid)
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorCouldNotValidateVehicle))
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
		c.String(http.StatusForbidden, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorVehicleNotInAccount))
		return
	}

	setCurrentVehicle(c, teslaID, vin)

	// Re-render the vehicle-select fragment with the freshly-persisted selection
	// so the <select> shows the new option selected. Re-issue the CSRF token so
	// the next switch from the same fragment works.
	vm := h.vehicleSelectFor(c.Request.Context(), uid, teslaID)
	if csrf, err := generateCSRFToken(); err == nil {
		sess := sessions.Default(c)
		sess.Set(csrfVehicleSelectKey, csrf)
		_ = sess.Save()
		vm.CSRFToken = csrf
	}
	// Fire the cross-region refresh event. htmx bubbles "vehicle-changed" to <body>;
	// any page region listening with hx-trigger="vehicle-changed from:body" (the
	// nav-header status region, the dashboard's #dashboard-content, the manual
	// records #external-charges-content) then re-fetches itself for the newly-selected
	// vehicle. The switcher stays page-agnostic — it fires one event, regions opt in.
	// Set before renderFragment: templ.Handler only sets Content-Type/status and does
	// not clear already-set response headers.
	c.Header("HX-Trigger", "vehicle-changed")
	renderFragment(c, http.StatusOK, fragments.VehicleSelect(vm), "vehicle-select")
}

// navHeaderFor is the sidebar vehicle block's core logic, decoupled from
// gin/session so it is unit-testable with fake account/analytics
// implementations. It calls ONLY Reader ports — account.RegisteredVehicles +
// analytics.Reader.LatestMetricsForVehicles (never a Writer/Collector, never
// Tesla). It never imports a DB package.
//
// It reports only what is actually stored: the primary vehicle's name, its
// latest battery level, and its latest range. MAG-44 removed the
// connected/asleep status this helper used to compute — the app never observes a
// live connection, it renders the newest vehicle_metrics row, so a freshness
// window could not honestly produce a connectivity word. All CapturedAt
// branching went with it.
//
// Degradation rules (DD2 resilience): a read error never returns a 500 — the
// fragment degrades to a name-only block (analytics reader error) or an empty
// block (account error), so the page that already rendered stays intact. Both
// degraded states render the em-dash placeholder and no bar, because HasBattery
// stays false.
//
// The vehicle context-switcher option list lives in vehicleSelectFor; this
// helper resolves the primary (selected) vehicle only.
func (h *Handler) navHeaderFor(ctx context.Context, uid uuid.UUID, selectedTeslaID int64, today time.Time) fragments.NavHeaderVM {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: nav-header RegisteredVehicles error for account %s: %v", uid, err)
		// No vehicles, no name — degraded empty block. NeedsConnect stays false:
		// we don't know the account state, so the connect-prompt is misleading.
		return fragments.NavHeaderVM{}
	}
	if len(registered) == 0 {
		// No vehicles registered → connect link, no name, no battery (mirrors
		// the dashboard's NeedsConnect).
		return fragments.NavHeaderVM{NeedsConnect: true}
	}

	// Primary = the selected vehicle when known, else the first registered entry
	// (the auto-select policy in resolveSelectedVehicle picks the first OWNER,
	// which is also registered[0] in the single-vehicle case). The battery
	// reflects THIS vehicle's latest stored row, not just registered[0].
	primary := registered[0]
	if selectedTeslaID != 0 {
		for _, v := range registered {
			if v.TeslaID == selectedTeslaID {
				primary = v
				break
			}
		}
	}

	vm := fragments.NavHeaderVM{VehicleName: primary.DisplayName}

	// Read the latest status per vehicle for this account. On error: degrade —
	// keep the vehicle name, no battery. Never return early with a 500.
	statuses, statusErr := h.analyticsReader.LatestMetricsForVehicles(ctx, vehicleref.All(teslaIDsOf(registered)))
	if statusErr != nil {
		log.Printf("gateway: nav-header analytics reader error for account %s: %v", uid, statusErr)
		return vm
	}

	// Merge by TeslaID to find the primary vehicle's latest stored row. No row
	// yet (freshly registered vehicle) → name only, em dash, no bar.
	statusMap := mergeVehicleStatuses(statuses)
	vs, ok := statusMap[primary.TeslaID]
	if !ok {
		return vm
	}

	vm.HasBattery = true
	vm.BatteryLevel = vs.BatteryLevelPct
	vm.BatteryPct = fmt.Sprintf("%d%%", vs.BatteryLevelPct)
	// Same precision as the dashboard's own range field (mapDashboardSnapshot),
	// deliberately — one range format across the app, not two.
	vm.RangeKm = fmt.Sprintf("%.0f km", vs.BatteryRangeKm)

	// Data age. A nil CapturedAt (a row predating the RM38 migration) leaves both
	// fields zero, so the template renders NO label — never a guessed age.
	if vs.CapturedAt != nil {
		days := calendarDaysAgo(*vs.CapturedAt, today)
		vm.DataAge = dataAgeLabel(ctx, days)
		vm.DataAgeStale = days >= dataAgeStaleDays
	}
	return vm
}

// calendarDaysAgo returns how many whole CALENDAR days separate capturedAt from
// today, both read in today's location — 0 for the same day, 1 for the previous
// one, and so on. today is expected to be midnight in the user's zone
// (browserToday), so "yesterday" means the user's yesterday, not UTC's.
//
// Calendar days, NOT elapsed hours, and the difference is the whole point: the
// nightly poll runs at 03:30, so a reading taken last night is ~22 h old when
// looked at before midnight. An elapsed-duration label would call that
// "22 hours ago" while the user reasonably calls it "yesterday". This is why the
// helper MAG-44 deleted (relativeLastSeen, which measured now.Sub(capturedAt))
// could not simply be restored.
//
// The subtraction goes through midnights rather than the raw instants so a DST
// transition inside the window cannot shift the answer: two midnights in the same
// zone are n*24 h apart give or take an hour, which the rounding absorbs. A
// capturedAt in the future (clock skew between the poller's host and this one)
// clamps to 0 rather than reporting a negative age.
func calendarDaysAgo(capturedAt, today time.Time) int {
	capturedDay := startOfDayIn(capturedAt, today.Location())
	days := int(math.Round(today.Sub(capturedDay).Hours() / 24))
	if days < 0 {
		return 0
	}
	return days
}

// dataAgeStaleDays is the age at which the data-age label switches from muted to
// error-coloured. ONE calendar day: "today" is the only healthy state, because
// the nightly poller runs every day (~03:30 local), so the newest stored reading
// should always carry today's date. A "yesterday" label already means the last
// poll did not land today — worth showing in red, not treated as normal.
//
// Known consequence, accepted: between local midnight and the poller's ~03:30 run
// the newest reading is genuinely yesterday's, so the label is red for those few
// hours every day. That is honest rather than wrong — the app has no reading from
// today yet — and hiding it would need the label to know the poller's schedule,
// which is another module's concern.
//
// It is deliberately NOT the old 48 h connectedFreshnessWindow — that was an
// elapsed-hours window used to assert connectivity, a claim this app cannot make.
// This constant only drives emphasis.
const dataAgeStaleDays = 1

// dataAgeLabel resolves the translated data-age phrase for a calendar-day
// distance: 0 -> "today", 1 -> "yesterday", 2+ -> "N days ago". Resolved here in
// the handler (not the template) so the fragment receives a finished string, the
// same shape every other pre-computed label in this file follows.
func dataAgeLabel(ctx context.Context, days int) string {
	switch days {
	case 0:
		return i18n.T(ctx, i18n.KeyNavHeaderUpdatedToday)
	case 1:
		return i18n.T(ctx, i18n.KeyNavHeaderUpdatedYesterday)
	default:
		return fmt.Sprintf(i18n.T(ctx, i18n.KeyNavHeaderUpdatedDaysAgo), days)
	}
}

// vehicleSelectFor builds the vehicle context-switcher view model, decoupled from
// gin/session so it is unit-testable with fake account implementations. It calls
// ONLY the account Reader port (no telemetry read — the switcher option list is
// just the registered vehicles; the status dot/battery live in navHeaderFor).
//
// Returns an empty VM (no vehicles, no name) on account error or when the account
// has no registered vehicles. The fragment then falls back to the app brand as the
// navbar title and renders no <select>, so the htmx swap target (#vehicle-select)
// stays stable across renders. The handler pre-marks the selected option and
// populates SelectedVehicleName from the selected (or first registered) vehicle.
func (h *Handler) vehicleSelectFor(ctx context.Context, uid uuid.UUID, selectedTeslaID int64) fragments.VehicleSelectVM {
	registered, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		log.Printf("gateway: vehicle-select RegisteredVehicles error for account %s: %v", uid, err)
		return fragments.VehicleSelectVM{}
	}
	if len(registered) == 0 {
		return fragments.VehicleSelectVM{}
	}
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
	// Resolve the primary vehicle's name for the navbar title: the selected one
	// when known, else the first registered entry (the auto-select policy in
	// resolveSelectedVehicle picks the first OWNER, which is registered[0]).
	primary := registered[0]
	if selectedTeslaID != 0 {
		for _, v := range registered {
			if v.TeslaID == selectedTeslaID {
				primary = v
				break
			}
		}
	}
	return fragments.VehicleSelectVM{
		SelectedVehicleName:    primary.DisplayName,
		Vehicles:               vehicles,
		SelectedVehicleTeslaID: selectedTeslaID,
	}
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

// errVehicleNotAuthorized is the one error authorizeVehicle ever returns. It
// covers both "the account lookup failed" and "the vehicle is not owned by
// this account" — the caller must not be able to tell the two apart, or a
// failed lookup could be used to learn that a probed vehicle id is real.
var errVehicleNotAuthorized = errors.New("vehicle not authorized")

// authorizeVehicle proves that uid's account owns teslaID, returning a
// vehicleref.Ref that a module port can require in its own signature. It
// reuses the same RegisteredVehicles call resolveSelectedVehicle already
// makes above — no second account lookup, no new account.Service method.
//
// Render errVehicleNotAuthorized as HTTP 404, never 403: a 403 confirms the
// vehicle id exists and only access is denied, which tells an attacker
// probing sequential ids which ones are real. A 404 makes "not yours" and
// "does not exist" indistinguishable from outside.
func (h *Handler) authorizeVehicle(ctx context.Context, uid uuid.UUID, teslaID int64) (vehicleref.Ref, error) {
	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil {
		return vehicleref.Ref{}, errVehicleNotAuthorized
	}
	owned := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		owned = append(owned, v.TeslaID)
	}
	ref, ok := vehicleref.Authorize(owned, teslaID)
	if !ok {
		return vehicleref.Ref{}, errVehicleNotAuthorized
	}
	return ref, nil
}

// ownedVehicles resolves uid's registered vehicles once and returns both the plain
// account.Vehicle list and their proofs as vehicleref.Ref. Two callers need two
// different shapes of the same one read -- a display label needs the vehicle struct, a
// Reader-port call needs a plain []int64 -- so this returns both instead of making each
// caller re-derive one from the other. ok is false on a lookup error or an empty list: an
// empty vehicle list is never "no filter", it is "this account has nothing to see."
func (h *Handler) ownedVehicles(ctx context.Context, uid uuid.UUID) (refs []vehicleref.Ref, vehicles []account.Vehicle, ok bool) {
	vehicles, err := h.acct.RegisteredVehicles(ctx, uid)
	if err != nil || len(vehicles) == 0 {
		return nil, nil, false
	}
	return vehicleref.All(teslaIDsOf(vehicles)), vehicles, true
}

// teslaIDsOf pulls the tesla_id out of an already-resolved vehicle list. Reader
// ports take plain ids, so every caller that holds the vehicle structs needs this
// same one-line unwrap.
func teslaIDsOf(vehicles []account.Vehicle) []int64 {
	ids := make([]int64, 0, len(vehicles))
	for _, v := range vehicles {
		ids = append(ids, v.TeslaID)
	}
	return ids
}

// ConnectTesla starts the Tesla OAuth connect flow for the signed-in user.
func (h *Handler) ConnectTesla(c *gin.Context) {
	if _, ok := currentUID(c); !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	state, err := randomState()
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorCouldNotStartTeslaConnect))
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
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorInvalidOAuthState))
		return
	}
	sess.Delete("tesla_state")
	_ = sess.Save()

	tokens, err := auth.ExchangeCode(h.teslaClientID, h.teslaClientSecret, c.Query("code"), h.teslaRedirectURL)
	if err != nil {
		c.String(http.StatusBadGateway, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorTeslaConnectFailed))
		return
	}
	if err := h.acct.SaveTeslaTokens(c.Request.Context(), uid, account.TeslaTokens{
		AccessToken:     tokens.AccessToken,
		RefreshToken:    tokens.RefreshToken,
		AccessExpiresAt: clock.Now().Add(time.Duration(tokens.ExpiresIn) * time.Second),
	}); err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorCouldNotSaveTeslaConnection))
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
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorCouldNotStartLogin))
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
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorInvalidOAuthState))
		return
	}
	sess.Delete("oauth_state")

	id, err := h.google.Exchange(c.Request.Context(), c.Query("code"))
	if err != nil {
		c.String(http.StatusBadGateway, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorGoogleLoginFailed))
		return
	}

	acct, err := h.acct.UpsertFromOAuth(c.Request.Context(), account.OAuthIdentity{
		Provider:    "google",
		ProviderID:  id.Sub,
		Email:       id.Email,
		DisplayName: id.Name,
	})
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeyOAuthErrorCouldNotProvisionAccount))
		return
	}

	if rejectIfInactive(c, acct) {
		return
	}

	h.syncLoginLanguageCookie(c, acct.ID)

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
		c.String(http.StatusServiceUnavailable, "unhealthy: %v", err) // i18n:allow: ops health-check response, not user-facing UI
		return
	}
	c.String(http.StatusOK, "ok")
}

// rejectIfInactive renders the blocked page and returns true if acct is not
// Active — the caller (GoogleCallback) must return immediately without
// establishing a session. Returns false (and renders nothing) for an Active
// account. Factored out of GoogleCallback, like syncLoginLanguageCookie, so it
// is directly testable with a hand-built gin.Context and a plain
// account.Account, without a live call to h.google.Exchange (design.md D24,
// RM34-gateway-block-inactive-login). GoogleCallback is a full-page browser
// navigation from Google's own redirect — never an htmx request — so the
// blocked page is always rendered inline at HTTP 403, with no HX-Redirect
// branch to consider (design.md D19 was withdrawn along with the per-request
// gate it existed for; see roadmap D26).
func rejectIfInactive(c *gin.Context, acct account.Account) bool {
	if acct.Status == account.StatusActive {
		return false
	}
	renderError(c, http.StatusForbidden, pages.AccountBlocked())
	return true
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
// response body (e.g. GET /ui/external-charges returns the create-form + list regions that
// live inside #external-charges-content). fragmentNames (not "fragments") avoids shadowing the
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
