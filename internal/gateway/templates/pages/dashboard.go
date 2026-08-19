package pages

import (
	"context"
	"strconv"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
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
	sub := d.StatusLabel
	if d.SoftwareVer != "" {
		sub += " • " + d.SoftwareVer
	}
	return sub
}

// dashPctAttr returns the bare battery percentage string for the <progress value="">
// attribute. "0" when there is no snapshot or no value, so the bar renders empty in
// the placeholder state rather than as an indeterminate progress widget.
func dashPctAttr(d fragments.DashboardData) string {
	if !d.HasSnapshot || d.BatteryPct == "" {
		return "0"
	}
	return d.BatteryPct
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

// dashBatteryColorClass returns the token-backed color utility for the battery
// level, driving currentColor for BOTH the big % number and the <progress> fill
// (DaisyUI's progress value reads currentColor). Bands — end inclusive at the
// lower bound (10 = low/red, 20 = mid/orange, 40 = warn/yellow):
//
//	0–10  → text-battery-low  (red)
//	11–20 → text-battery-mid  (orange)
//	21–40 → text-battery-warn (yellow)
//	41+   → text-battery-full (green)
//
// Returns "text-primary" when there is no snapshot or the value won't parse, so
// the placeholder state keeps the current brand-red look instead of going colorless.
// See internal/gateway/static/themes/apex.css §"Battery-level metric colors".
func dashBatteryColorClass(d fragments.DashboardData) string {
	if !d.HasSnapshot || d.BatteryPct == "" {
		return "text-primary"
	}
	p, err := strconv.Atoi(d.BatteryPct)
	if err != nil {
		return "text-primary"
	}
	switch {
	case p <= 10:
		return "text-battery-low"
	case p <= 20:
		return "text-battery-mid"
	case p <= 40:
		return "text-battery-warn"
	default:
		return "text-battery-full"
	}
}