package pages

import (
	"context"

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