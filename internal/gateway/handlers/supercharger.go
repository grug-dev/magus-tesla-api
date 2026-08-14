// supercharger.go contains the Supercharger Stats page/fragment handlers
// (GET /supercharger-stats, GET /ui/supercharger-stats?months=N). Mirrors
// history.go's shape: a closed month-preset vocabulary + clamp helper, the
// read cap, and the view-building handler logic (buildSuperchargerStatsView
// + its tiles/chart/table helpers).
// Design decisions D1-D7 from openspec/changes/gateway-add-supercharger-stats/.
package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// superchargerMonthPresets is the closed, small vocabulary of allowed
// month-count values for the Supercharger Stats month selector. Validation
// rejects anything not in this list (D5: closed vocabulary, one named
// location — AI-efficiency principle, mirrors historyDayPresets exactly).
var superchargerMonthPresets = []int{3, 6, 12}

// defaultSuperchargerMonths is the fallback when the months parameter is
// missing, invalid, or not a member of superchargerMonthPresets.
const defaultSuperchargerMonths = 6

// superchargerReadLimit is the safety cap on the single SuperchargerSessions
// ByVehicle read (D6), mirroring history's SnapshotsByVehicleSince port-level
// cap. The port orders results charge_start_date_time DESC, so the 500 most
// recent sessions are retained; the month-window filter (D5) then trims that
// set down to the selected range in Go.
const superchargerReadLimit = 500

// clampSuperchargerMonths parses raw and returns the nearest allowed preset.
// Any missing/non-numeric/out-of-set value becomes defaultSuperchargerMonths.
// Mirrors clampHistoryDays exactly (D5).
func clampSuperchargerMonths(raw string) int {
	if raw == "" {
		return defaultSuperchargerMonths
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultSuperchargerMonths
	}
	for _, p := range superchargerMonthPresets {
		if n == p {
			return n
		}
	}
	return defaultSuperchargerMonths
}

// startOfMonth returns midnight UTC on the 1st of t's month (truncates to the
// calendar month). Mirrors history.go's startOfDay, one level coarser.
func startOfMonth(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// SuperchargerStatsPage renders the full Supercharger Stats page for the
// session-selected vehicle (initial load). Mirrors DashboardHistoryFragment's
// auth-guard -> resolveSelectedVehicle -> clamp -> build -> render shape.
func (h *Handler) SuperchargerStatsPage(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	render(c, http.StatusOK, pages.SuperchargerStatsPage(h.superchargerStatsViewFor(c, uid)))
}

// SuperchargerStatsFragment renders ONLY the #supercharger-stats-content
// fragment (htmx swap served by GET /ui/supercharger-stats) — the month-preset
// selector's hx-get target and the vehicle switcher's "vehicle-changed"
// subscriber.
func (h *Handler) SuperchargerStatsFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	renderFragment(c, http.StatusOK, pages.SuperchargerStatsPage(h.superchargerStatsViewFor(c, uid)), "supercharger-stats")
}

// superchargerStatsViewFor resolves the selected vehicle and the requested
// month window, then builds the view — shared by the page and fragment entry
// points (mirrors DashboardHistoryFragment's shape). When no vehicle is
// registered/selected (resolveSelectedVehicle returns false), it renders the
// page's empty state rather than an error (design.md "Inherited precedent").
func (h *Handler) superchargerStatsViewFor(c *gin.Context, uid uuid.UUID) fragments.SuperchargerStatsView {
	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		return fragments.SuperchargerStatsView{
			Months:  defaultSuperchargerMonths,
			Presets: superchargerMonthPresets,
			Chart:   fragments.HistoryChart{Empty: true},
			Empty:   true,
		}
	}

	months := clampSuperchargerMonths(c.Query("months"))
	since := startOfMonth(time.Now()).AddDate(0, -months+1, 0)
	return h.buildSuperchargerStatsView(c.Request.Context(), uid, selected.TeslaID, months, since)
}

// buildSuperchargerStatsView is the core logic for the Supercharger Stats
// page/fragment, decoupled from gin/session so it is unit-testable with a
// fake telemetry.SuperchargerReader. It performs ONE read
// (SuperchargerSessionsByVehicle, capped at superchargerReadLimit, D6), then
// filters to sessions with ChargeStartDateTime >= since (D5: window math
// stays in the caller — the port takes a row limit, not a time range) before
// building the logic-free view model. On reader error it degrades to an
// empty view rather than propagating a 500 (mirrors buildHistoryView).
func (h *Handler) buildSuperchargerStatsView(ctx context.Context, uid uuid.UUID, teslaID int64, months int, since time.Time) fragments.SuperchargerStatsView {
	v := fragments.SuperchargerStatsView{
		Months:  months,
		Presets: superchargerMonthPresets,
	}

	sessions, err := h.superchargerReader.SuperchargerSessionsByVehicle(ctx, uid, teslaID, superchargerReadLimit)
	if err != nil {
		log.Printf("gateway: supercharger reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Chart = fragments.HistoryChart{Empty: true}
		v.Empty = true
		return v
	}

	filtered := make([]telemetry.SuperchargerSession, 0, len(sessions))
	for _, s := range sessions {
		if !s.ChargeStartDateTime.Before(since) {
			filtered = append(filtered, s)
		}
	}

	v.Tiles = buildSuperchargerTiles(filtered)
	v.Chart = buildSuperchargerChart(filtered, months, since)
	v.Sessions = buildSuperchargerRows(filtered)
	v.Empty = len(filtered) == 0
	return v
}

// buildSuperchargerTiles computes the four KPI tiles (D7) from the ONE
// filtered slice: Sessions is a plain count; Energy sums non-nil EnergyKWh
// (nil-skip); AvgKWh divides that sum by the count of sessions with a known
// EnergyKWh, guarded against divide-by-zero ("—" when the count is 0); Cost
// is built into a map[currency]float64 from sessions with BOTH a non-nil
// TotalCost AND a non-nil Currency (D4 — a nil-cost or nil-currency session
// is excluded here but still counted above), then rendered as one formatted
// line per currency sorted by currency code for a stable render order — never
// a single total summed across currencies.
func buildSuperchargerTiles(filtered []telemetry.SuperchargerSession) fragments.SuperchargerTiles {
	var energySum float64
	var energyCount int
	costByCurrency := make(map[string]float64)

	for _, s := range filtered {
		if s.EnergyKWh != nil {
			energySum += *s.EnergyKWh
			energyCount++
		}
		if s.TotalCost != nil && s.Currency != nil {
			costByCurrency[*s.Currency] += *s.TotalCost
		}
	}

	avgLabel := "—"
	if energyCount > 0 {
		avgLabel = fmt.Sprintf("%.1f kWh", energySum/float64(energyCount))
	}

	currencies := make([]string, 0, len(costByCurrency))
	for cur := range costByCurrency {
		currencies = append(currencies, cur)
	}
	sort.Strings(currencies)

	var costLines []string
	for _, cur := range currencies {
		costLines = append(costLines, formatMoney(costByCurrency[cur], cur))
	}

	return fragments.SuperchargerTiles{
		Sessions:  strconv.Itoa(len(filtered)),
		Energy:    fmt.Sprintf("%.1f kWh", energySum),
		CostLines: costLines,
		AvgKWh:    avgLabel,
	}
}

// buildSuperchargerChart buckets the filtered sessions into one bar per
// calendar month of the selected window (design.md "View model": "one bar per
// month in the window"), zero-filled so a month with no sessions still
// renders as a (zero-height) bar rather than compressing the timeline. Each
// bucket sums non-nil EnergyKWh; HeightPct is relative to the tallest bucket
// (mirrors buildOdometerChart's max-relative height math). Empty only when
// the filtered slice itself is empty (C.3) — a zero-energy month inside a
// non-empty window still renders as a bar.
func buildSuperchargerChart(filtered []telemetry.SuperchargerSession, months int, since time.Time) fragments.HistoryChart {
	if len(filtered) == 0 {
		return fragments.HistoryChart{Empty: true}
	}

	type bucket struct {
		label string
		kwh   float64
	}
	buckets := make([]bucket, months)
	for i := 0; i < months; i++ {
		buckets[i].label = since.AddDate(0, i, 0).Format("Jan 2006")
	}

	for _, s := range filtered {
		if s.EnergyKWh == nil {
			continue
		}
		idx := monthsBetween(since, s.ChargeStartDateTime.UTC())
		if idx < 0 || idx >= months {
			continue // defensive: post-filter sessions should always land in-window
		}
		buckets[idx].kwh += *s.EnergyKWh
	}

	maxKWh := 0.0
	for _, b := range buckets {
		if b.kwh > maxKWh {
			maxKWh = b.kwh
		}
	}

	bars := make([]fragments.HistoryBar, len(buckets))
	for i, b := range buckets {
		pct := 0
		if maxKWh > 0 {
			pct = int(math.Round(b.kwh / maxKWh * 100))
		}
		bars[i] = fragments.HistoryBar{
			HeightPct: pct,
			Tooltip:   fmt.Sprintf("%s · %.1f kWh", b.label, b.kwh),
		}
	}
	return fragments.HistoryChart{Bars: bars, Empty: false}
}

// monthsBetween returns how many calendar months t is after since (0 when
// t falls in since's own month), used to index buildSuperchargerChart's
// zero-filled month buckets.
func monthsBetween(since, t time.Time) int {
	return (t.Year()-since.Year())*12 + int(t.Month()) - int(since.Month())
}

// buildSuperchargerRows maps every filtered session (unpaginated, D7) to a
// display row. Energy/cost render "—" when their source field is nil —
// EnergyLabel needs only EnergyKWh; CostLabel needs BOTH TotalCost and
// Currency (D4).
func buildSuperchargerRows(filtered []telemetry.SuperchargerSession) []fragments.SuperchargerRowVM {
	rows := make([]fragments.SuperchargerRowVM, 0, len(filtered))
	for _, s := range filtered {
		energyLabel := "—"
		if s.EnergyKWh != nil {
			energyLabel = fmt.Sprintf("%.2f kWh", *s.EnergyKWh)
		}
		costLabel := "—"
		if s.TotalCost != nil && s.Currency != nil {
			costLabel = fmt.Sprintf("%.2f %s", *s.TotalCost, *s.Currency)
		}
		rows = append(rows, fragments.SuperchargerRowVM{
			DateLabel:   s.ChargeStartDateTime.UTC().Format("Mon Jan 2, 2006"),
			SiteLabel:   s.SiteLocationName,
			CountryCode: s.CountryCode,
			EnergyLabel: energyLabel,
			CostLabel:   costLabel,
			BillingType: s.BillingType,
		})
	}
	return rows
}
