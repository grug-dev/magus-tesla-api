// vehicle_stats.go contains the Vehicle Stats page/fragment handlers
// (GET /vehicle-stats, GET /ui/vehicle-stats). Mirrors supercharger.go's
// shape: a page handler that renders the full shell, a fragment handler that
// renders only the vehicle-scoped region, and one shared view builder so the
// two can never drift apart.
//
// The page reads analytics' precomputed monthly rollup through
// analytics.MonthlyReader — never the daily Reader the other pages use, and
// never a database. The period control offers whole months and whole years
// because that rollup's grain is a month; see vehicle_stats_period.go.
//
// It is READ-ONLY: no CSRF token, no session write, no sibling write port.
package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/logging"
)

// VehicleStatsPage renders the full Vehicle Stats page. Mirrors
// SuperchargerStatsPage minus the CSRF token: this page has no write of any
// kind, so it mints no token and stores nothing on the session.
func (h *Handler) VehicleStatsPage(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	v, status := h.vehicleStatsViewFor(c, uid)
	if status != http.StatusOK {
		renderError(c, status, pages.VehicleStatsPage(v))
		return
	}
	render(c, status, pages.VehicleStatsPage(v))
}

// VehicleStatsFragment renders ONLY the #vehicle-stats-content region (htmx
// swap served by GET /ui/vehicle-stats) — the vehicle switcher's
// "vehicle-changed" subscriber and the period control's hx-get target.
func (h *Handler) VehicleStatsFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	v, status := h.vehicleStatsViewFor(c, uid)
	if status != http.StatusOK {
		renderFragmentError(c, status, pages.VehicleStatsPage(v), "vehicle-stats")
		return
	}
	renderFragment(c, status, pages.VehicleStatsPage(v), "vehicle-stats")
}

// vehicleStatsViewFor builds the view both handlers render, so the full page
// and the htmx swap can never disagree about what the period shows.
//
// No vehicle on the session yields the empty state, NOT an error — a signed-in
// account with no Tesla connected yet is a normal state, and it renders the
// same card an empty period does (design note on VehicleStatsView.Empty).
//
// A malformed window returns HTTP 400 with the no-chrome empty state and NO
// period control, matching what every other date-filtered page in this
// module does: a malformed request gets no chrome (AGENTS.md §HTTP
// date-filter convention).
func (h *Handler) vehicleStatsViewFor(c *gin.Context, uid uuid.UUID) (fragments.VehicleStatsView, int) {
	ctx := c.Request.Context()
	today := browserToday(c)

	// The account's analysis_start_date is the period control's lower bound
	// and the parser's rule 6. A lookup failure falls back to today's month,
	// which offers the current month alone — a control that is too narrow is
	// recoverable by reloading; one that is too wide would offer periods the
	// account is not allowed to read.
	minDate := today
	if d, err := h.acct.AnalysisStartDateFor(ctx, uid); err != nil {
		logging.Note("Handler", "vehicleStatsViewFor", "AnalysisStartDateFor error for account %s: %v", uid, err)
	} else {
		minDate = d
	}

	start, end, ok := parseVehicleStatsRange(c, today, minDate)
	if !ok {
		return fragments.VehicleStatsView{Empty: true}, http.StatusBadRequest
	}

	v := fragments.VehicleStatsView{
		Periods: buildVehicleStatsPeriods(c, today, minDate, start, end),
		Empty:   true,
	}
	v.PeriodLabel = activePeriodLabel(v.Periods, start, end)

	teslaID, _, hasVehicle := currentVehicle(c)
	if !hasVehicle {
		return v, http.StatusOK
	}

	months, err := h.analyticsMonthly.MonthlyMetricsBetween(ctx, teslaID, start, end)
	if err != nil {
		logging.Note("Handler", "vehicleStatsViewFor", "monthly reader error for account %s, vehicle %d: %v", uid, teslaID, err)
		v.Error = i18n.T(ctx, i18n.KeyVehicleStatsErrorCouldNotLoad)
		return v, http.StatusOK
	}

	// The port is sparse BY MONTH: the nightly poller only ever wrote the
	// current and previous month, and nothing backfilled the rest, so a whole
	// year legitimately returns fewer than twelve rows. No rows at all is the
	// empty state, not an error — the period is simply one this account has
	// no stored data for.
	if len(months) == 0 {
		return v, http.StatusOK
	}

	v.Empty = false
	v.Tiles = buildVehicleStatsTiles(months)
	v.Why = buildVehicleStatsWhy(ctx, months)
	return v, http.StatusOK
}
