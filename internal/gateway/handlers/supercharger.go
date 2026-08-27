// supercharger.go contains the Supercharger Stats page/fragment handlers
// (GET /supercharger-stats, GET /ui/supercharger-stats?start=&end=). Mirrors
// history.go's shape: a ?start=&end= date-range parser + closed month-preset
// selector built from RangePreset, and the view-building handler logic
// (buildSuperchargerStatsView + its tiles/chart/table helpers) over
// charging.SessionReader.
// Design decisions D1-D10 from
// openspec/changes/RM30-gateway-read-supercharger-stats-from-charging/design.md
// (original D1-D7 from openspec/changes/gateway-add-supercharger-stats/ are
// superseded by this tier's read-port swap + ?start=&end= migration).
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

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
)

// superchargerMonthPresets is the closed, small vocabulary of month counts
// the preset selector renders (unchanged UX — design.md D5/D-preset).
var superchargerMonthPresets = []int{3, 6, 12}

// superchargerRangeDefaultMonths is the window size (in months) applied when
// both start and end params are absent (design.md D1/D5).
const superchargerRangeDefaultMonths = 6

// superchargerRangeMaxDays is the hard cap on the window width, protecting
// the read path from an unbounded range scan (Performance-Profile:
// read-heavy). Wider than historyRangeMaxDays (90) because charge_sessions is
// sparse relative to vehicle_snapshots (design.md D6).
const superchargerRangeMaxDays = 400

// startOfMonth returns midnight UTC on the 1st of t's month (truncates to the
// calendar month). Mirrors history.go's startOfDay, one level coarser.
func startOfMonth(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// monthsBackFrom returns the whole-day, month-aligned start of the
// n-calendar-month window ending in end's own month: the 1st of the month
// n-1 months before end's month. Mirrors the pre-RM30 months-based window's
// exact chart-bucket behavior (design.md D5) — n calendar buckets, never
// n+1 — so the 3/6/12 presets and the 6-month default still render exactly n
// bars, not n or n+1 depending on today's day-of-month.
func monthsBackFrom(end time.Time, n int) time.Time {
	return startOfMonth(end).AddDate(0, -(n - 1), 0)
}

// parseSuperchargerRange validates the ?start=&end= query params (design.md
// D1). today is UTC today (startOfDay(time.Now().UTC())), resolved ONCE by
// the caller and threaded through — this endpoint does NOT consult the
// browser_tz cookie (design.md D9a). It returns the parsed (start, end)
// calendar days and ok=true, or the zero values and ok=false when the
// request is malformed. The handler renders HTTP 400 on ok=false.
//
// Validation, in order:
//  1. Both absent → month-aligned default window (end=today,
//     start=monthsBackFrom(today, superchargerRangeDefaultMonths)), ok=true.
//  2. Either present → both required and well-formed YYYY-MM-DD.
//  3. end.Before(start) → false.
//  4. end.After(today) → false (design.md D9b). end == today IS accepted —
//     the boundary is strictly after, unlike parseHistoryRange's
//     browser-yesterday cap.
//  5. Window wider than superchargerRangeMaxDays (400) → false.
func parseSuperchargerRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
	rawStart := c.Query("start")
	rawEnd := c.Query("end")

	if rawStart == "" && rawEnd == "" {
		end = today
		start = monthsBackFrom(end, superchargerRangeDefaultMonths) // D5
		return start, end, true
	}

	s, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	e, err := time.Parse("2006-01-02", rawEnd)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	if e.Before(s) {
		return time.Time{}, time.Time{}, false
	}
	if e.After(today) { // D9 — no future window; e == today is accepted
		return time.Time{}, time.Time{}, false
	}
	if e.Sub(s).Hours()/24 > float64(superchargerRangeMaxDays) {
		return time.Time{}, time.Time{}, false
	}
	return s, e, true
}

// superchargerStatsViewFor resolves the Supercharger Stats view AND the HTTP
// status it must be rendered with, for BOTH SuperchargerStatsPage and
// SuperchargerStatsFragment. Mirrors DashboardHistoryFragment's
// parse -> 400-empty-no-selector -> resolve-vehicle -> build shape
// (history.go); it lives as a shared helper here — unlike history, which has
// only a fragment handler — so the two Supercharger entry points cannot drift
// apart. today is resolved ONCE, in UTC (design.md D9a — NOT browserToday(c)).
//
// Returned status is StatusBadRequest ONLY for a malformed/out-of-range
// window, in which case Presets is left nil so the template renders no
// selector (design.md D1).
func (h *Handler) superchargerStatsViewFor(c *gin.Context, uid uuid.UUID) (fragments.SuperchargerStatsView, int) {
	today := startOfDay(time.Now().UTC()) // design.md D9a — plain UTC, NOT browserToday(c)

	start, end, ok := parseSuperchargerRange(c, today)
	if !ok {
		return fragments.SuperchargerStatsView{
			Chart: fragments.HistoryChart{Empty: true},
			Empty: true,
		}, http.StatusBadRequest
	}

	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		return fragments.SuperchargerStatsView{
			Presets: buildSuperchargerPresets(c.Request.Context(), start, end, today),
			Chart:   fragments.HistoryChart{Empty: true},
			Empty:   true,
		}, http.StatusOK
	}

	return h.buildSuperchargerStatsView(c.Request.Context(), uid, selected.TeslaID, start, end, today), http.StatusOK
}

// SuperchargerStatsPage renders the full Supercharger Stats page for the
// session-selected vehicle (initial load).
func (h *Handler) SuperchargerStatsPage(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	v, status := h.superchargerStatsViewFor(c, uid)
	if status != http.StatusOK {
		renderError(c, status, pages.SuperchargerStatsPage(v))
		return
	}
	render(c, status, pages.SuperchargerStatsPage(v))
}

// SuperchargerStatsFragment renders ONLY the #supercharger-stats-content
// fragment (htmx swap served by GET /ui/supercharger-stats?start=&end=) — the
// month-preset selector's hx-get target and the vehicle switcher's
// "vehicle-changed" subscriber.
func (h *Handler) SuperchargerStatsFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	v, status := h.superchargerStatsViewFor(c, uid)
	if status != http.StatusOK {
		renderFragmentError(c, status, pages.SuperchargerStatsPage(v), "supercharger-stats")
		return
	}
	renderFragment(c, status, pages.SuperchargerStatsPage(v), "supercharger-stats")
}

// buildSuperchargerPresets builds the []fragments.RangePreset for the
// selector at render time (design.md D5/D-preset), mirroring
// buildHistoryPresets: for each n in superchargerMonthPresets ({3, 6, 12}),
// pEnd = today, pStart = monthsBackFrom(today, n), the absolute href is
// pre-formatted, and Active is true when (start, end) matches the requested
// window.
func buildSuperchargerPresets(ctx context.Context, start, end, today time.Time) []fragments.RangePreset {
	out := make([]fragments.RangePreset, 0, len(superchargerMonthPresets))
	for _, n := range superchargerMonthPresets {
		pEnd := today
		pStart := monthsBackFrom(today, n)
		out = append(out, fragments.RangePreset{
			Label:    fmt.Sprintf(i18n.T(ctx, i18n.KeySuperchargerMonthsPreset), n),
			StartStr: pStart.Format("2006-01-02"),
			EndStr:   pEnd.Format("2006-01-02"),
			Active:   start.Equal(pStart) && end.Equal(pEnd),
		})
	}
	return out
}

// buildSuperchargerStatsView is the core logic for the Supercharger Stats
// page/fragment, decoupled from gin/session so it is unit-testable with a
// fake charging.SessionReader. It performs ONE read
// (ListSessionsByVehicleBetween, bounded by the requested [start, end]
// window at the database, design.md D2/D3), reverses the returned
// ascending-ordered slice once (design.md D3) so the sessions table keeps
// its pre-existing newest-first display order, then builds the logic-free
// view model. On reader error it degrades to an empty view rather than
// propagating a 500 (mirrors buildHistoryView).
func (h *Handler) buildSuperchargerStatsView(ctx context.Context, uid uuid.UUID, teslaID int64, start, end, today time.Time) fragments.SuperchargerStatsView {
	v := fragments.SuperchargerStatsView{
		Presets: buildSuperchargerPresets(ctx, start, end, today),
	}

	sessions, err := h.superchargerReader.ListSessionsByVehicleBetween(ctx, uid, teslaID, start, end)
	if err != nil {
		log.Printf("gateway: supercharger reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Chart = fragments.HistoryChart{Empty: true}
		v.Empty = true
		return v
	}

	// D3 — the port returns ChargeStopDateTime ASC (oldest-first); reverse
	// once, before any of the three builders below, so the sessions table
	// keeps displaying newest-first (unchanged UX). Tiles/chart are
	// order-independent aggregations, so this single reverse is correct for
	// all three.
	for i, j := 0, len(sessions)-1; i < j; i, j = i+1, j-1 {
		sessions[i], sessions[j] = sessions[j], sessions[i]
	}

	v.Tiles = buildSuperchargerTiles(sessions)
	v.Chart = buildSuperchargerChart(sessions, start, end)
	v.Sessions = buildSuperchargerRows(sessions)
	v.Empty = len(sessions) == 0
	return v
}

// buildSuperchargerTiles computes the four KPI tiles from the sessions in
// the window: Sessions is a plain count; Energy sums non-nil EnergyKWh
// (nil-skip); AvgKWh divides that sum by the count of sessions with a known
// EnergyKWh, guarded against divide-by-zero ("—" when the count is 0); Cost
// is built into a map[currency]float64 from sessions with BOTH a non-nil
// TotalCost AND a non-nil Currency (a nil-cost or nil-currency session is
// excluded here but still counted above), then rendered as one formatted
// line per currency sorted by currency code for a stable render order —
// never a single total summed across currencies.
func buildSuperchargerTiles(sessions []charging.Session) fragments.SuperchargerTiles {
	var energySum float64
	var energyCount int
	costByCurrency := make(map[string]float64)

	for _, s := range sessions {
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
		Sessions:  strconv.Itoa(len(sessions)),
		Energy:    fmt.Sprintf("%.1f kWh", energySum),
		CostLines: costLines,
		AvgKWh:    avgLabel,
	}
}

// buildSuperchargerChart buckets the sessions into one bar per calendar
// month of the [start, end] window (design.md D10), zero-filled so a month
// with no sessions still renders as a (zero-height) bar rather than
// compressing the timeline. Bucket count and each bucket's calendar month
// are derived from the window itself (reusing startOfMonth/monthsBetween —
// no new date-math primitive), NOT from a caller-supplied months integer.
// Each bucket sums non-nil EnergyKWh; HeightPct is relative to the tallest
// bucket. Empty only when the sessions slice itself is empty — a
// zero-energy month inside a non-empty window still renders as a bar.
func buildSuperchargerChart(sessions []charging.Session, start, end time.Time) fragments.HistoryChart {
	if len(sessions) == 0 {
		return fragments.HistoryChart{Empty: true}
	}

	anchor := startOfMonth(start)
	numMonths := monthsBetween(anchor, startOfMonth(end)) + 1

	type bucket struct {
		label string
		kwh   float64
	}
	buckets := make([]bucket, numMonths)
	for i := 0; i < numMonths; i++ {
		buckets[i].label = anchor.AddDate(0, i, 0).Format("Jan 2006")
	}

	for _, s := range sessions {
		if s.EnergyKWh == nil {
			continue
		}
		idx := monthsBetween(anchor, s.ChargeStartDateTime.UTC())
		if idx < 0 || idx >= numMonths {
			continue // defensive: port-bounded sessions should always land in-window
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

// buildSuperchargerRows maps every session in the window (unpaginated) to a
// display row. Energy/cost render "—" when their source field is nil —
// EnergyLabel needs only EnergyKWh; CostLabel needs BOTH TotalCost and
// Currency. charging.Session carries no CountryCode/BillingType (design.md
// D4), so the row no longer sets either.
func buildSuperchargerRows(sessions []charging.Session) []fragments.SuperchargerRowVM {
	rows := make([]fragments.SuperchargerRowVM, 0, len(sessions))
	for _, s := range sessions {
		energyLabel := "—"
		if s.EnergyKWh != nil {
			energyLabel = fmt.Sprintf("%.2f kWh", *s.EnergyKWh)
		}
		costLabel := "—"
		if s.TotalCost != nil && s.Currency != nil {
			costLabel = formatMoney(*s.TotalCost, *s.Currency)
		}
		rows = append(rows, fragments.SuperchargerRowVM{
			DateLabel:   s.ChargeStartDateTime.UTC().Format("Mon Jan 2, 2006"),
			SiteLabel:   s.SiteLocationName,
			EnergyLabel: energyLabel,
			CostLabel:   costLabel,
		})
	}
	return rows
}
