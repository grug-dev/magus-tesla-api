package pages

import (
	"context"
	"strconv"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/ui"
)

// dashStat returns the snapshot's display value, or "—" when there is no snapshot
// yet (placeholder state). Centralising the placeholder keeps the template free of
// per-field branching (ai/htmx-conventions.md §"No business logic in templates").
func dashStat(hasSnapshot bool, v string) string {
	if !hasSnapshot {
		return "—"
	}
	return v
}

// dashSubtitle renders the hero subtitle under "Vehicle Status". When no snapshot
// exists it shows "Awaiting first snapshot"; otherwise it composes the pre-computed
// StatusLabel with the optional SoftwareVer ("Parked • Software v11.1.2"). Pure
// presentation assembly — no business logic, no time math. ctx is an explicit
// first parameter (this is a plain Go helper called from a .templ block, not a
// templ component itself — mirrors layouts.navItems(ctx, active)).
func dashSubtitle(ctx context.Context, d fragments.DashboardData) string {
	if !d.HasSnapshot {
		return i18n.T(ctx, i18n.KeyDashboardAwaitingSnapshot)
	}
	//sub := d.StatusLabel
	sub := ""
	if d.SoftwareVer != "" {
		sub += d.SoftwareVer
	}
	return sub
}

// dashPctAttr returns the battery percentage as the plain number ui.Progress takes
// for its Value. 0 when there is no snapshot or no parseable value, so the bar
// renders empty in the placeholder state rather than as an indeterminate widget.
func dashPctAttr(d fragments.DashboardData) int {
	if !d.HasSnapshot || d.BatteryPct == "" {
		return 0
	}
	p, err := strconv.Atoi(d.BatteryPct)
	if err != nil {
		return 0
	}
	return p
}

// dashChargeLimit returns the charge-limit label, or "—" when there is no snapshot
// or no limit is set. Mirrors dashStat so the battery card's right-hand metric never
// renders an empty cell.
func dashChargeLimit(d fragments.DashboardData) string {
	if !d.HasSnapshot || d.ChargeLimit == "" {
		return "—"
	}
	return d.ChargeLimit
}

// dashBatteryColorClass adapts the dashboard's DashboardData to the kit's single
// band definition, ui.BatteryBandClass. It only parses the VM's string percentage
// and reports whether there is a value at all; the bands themselves live in one
// place (templates/ui/ui.go) so the sidebar vehicle block and this card can never
// drift apart.
func dashBatteryColorClass(d fragments.DashboardData) string {
	if !d.HasSnapshot || d.BatteryPct == "" {
		return ui.BatteryBandClass(0, false)
	}
	p, err := strconv.Atoi(d.BatteryPct)
	if err != nil {
		return ui.BatteryBandClass(0, false)
	}
	return ui.BatteryBandClass(p, true)
}

// dashLockedBadge returns the ui.Badge text/kind for the locked/unlocked pill and
// whether it should render at all. nil -> show=false, no badge (roadmap D5) --
// the vehicle_metrics row predates the RM38 migration or has not been
// recomputed since (design.md D4/D7, RM38-gateway-read-dashboard-from-metrics).
func dashLockedBadge(ctx context.Context, locked *bool) (text, kind string, show bool) {
	if locked == nil {
		return "", "", false
	}
	if *locked {
		return i18n.T(ctx, i18n.KeyVehiclesLocked), "success", true
	}
	return i18n.T(ctx, i18n.KeyVehiclesUnlocked), "error", true
}

// dashSentryBadge mirrors dashLockedBadge for the sentry-mode pill (roadmap D5).
// The rendered text pairs the existing "Sentry:" label with On/Off, reusing three
// catalogue keys with no new key (KeyVehiclesSentryLabel + KeyVehiclesOn/Off)
// (design.md D4/D7, RM38-gateway-read-dashboard-from-metrics).
func dashSentryBadge(ctx context.Context, sentry *bool) (text, kind string, show bool) {
	if sentry == nil {
		return "", "", false
	}
	state := i18n.T(ctx, i18n.KeyVehiclesOff)
	kind = "ghost"
	if *sentry {
		state = i18n.T(ctx, i18n.KeyVehiclesOn)
		kind = "warning"
	}
	return i18n.T(ctx, i18n.KeyVehiclesSentryLabel) + " " + state, kind, true
}
