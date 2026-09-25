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

	// The gasoline cost-parity tile's price lookup is a SECOND read, over the
	// same [start, end] window, right after the primary months read is
	// confirmed non-empty. A failure here degrades only that one tile: the
	// map stays empty, every price lookup below misses, and the other seven
	// figures still render from the months already fetched.
	priceByMonth := map[monthKey]float64{}
	if prices, err := h.referenceReader.PricesForMonths(ctx, start, end); err != nil {
		logging.Note("Handler", "vehicleStatsViewFor", "reference price read failed for account %s, vehicle %d: %v", uid, teslaID, err)
	} else {
		for _, p := range prices {
			priceByMonth[monthKeyOf(p.Period)] = p.Price
		}
	}
	totals := sumVehicleStatsMonths(months, priceByMonth)

	// The trend comparison is a SECOND read of the same port, for the period
	// immediately before this one. It is skipped entirely when that period
	// would start before the account's analysis_start_date (no comparable
	// data), or when it has no stored month — a first-ever period shows its
	// figures with no arrows rather than a fabricated comparison.
	var prev *vehicleStatsTotals
	if ps, pe, ok := vehicleStatsPrevWindow(start, end, minDate); ok {
		prevMonths, err := h.analyticsMonthly.MonthlyMetricsBetween(ctx, teslaID, ps, pe)
		if err != nil {
			logging.Note("Handler", "vehicleStatsViewFor", "previous-period read failed for account %s, vehicle %d: %v", uid, teslaID, err)
		} else if len(prevMonths) > 0 {
			// No price read for the previous period — the tile carries no
			// trend, so nothing here needs one; nil makes every lookup miss.
			p := sumVehicleStatsMonths(prevMonths, nil)
			prev = &p
		}
	}
	v.HasPrev = prev != nil

	v.Tiles = buildVehicleStatsTiles(totals, prev)
	v.MonthChart = buildVehicleStatsMonthChart(ctx, months, start, end)
	v.Why = buildVehicleStatsWhy(ctx, months)

	// The missing-records warning needs day grain, so it is a SECOND read —
	// the monthly rollup stores no per-day flag. The window is the period's
	// own, with no lookback: a flagged day is a fact about that day, not a
	// delta needing a predecessor. ConsumedByDay is sparse and bounded by the
	// window (at most ~366 rows for a whole year), and a failure here degrades
	// the warning only: the figures above are already built, so the page still
	// renders without it rather than erroring.
	if flagDays, err := h.analyticsReader.ConsumedByDay(ctx, teslaID, start, end); err != nil {
		logging.Note("Handler", "vehicleStatsViewFor", "ConsumedByDay error for account %s, vehicle %d: %v", uid, teslaID, err)
	} else {
		v.Gaps, v.GapsMore = buildVehicleStatsGaps(ctx, flagDays)
	}

	return v, http.StatusOK
}
