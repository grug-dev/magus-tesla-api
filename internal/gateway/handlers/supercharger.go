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
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/charging"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
)

// csrfSuperchargerKey is the session key for the Supercharger Stats row-edit
// CSRF token (design.md D8, RM31-gateway-add-session-battery-edit) — distinct
// from csrfManualChargeKey (charges.go). Issued only by SuperchargerStatsPage/
// SuperchargerStatsFragment; read (never re-issued) by the row-level handlers
// below.
const csrfSuperchargerKey = "csrf_supercharger"

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
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC) // tz:allow: month granularity, not a day truncator — deliberately distinct from clock.CalendarDay (RM35 roadmap correction 2026-08-30)
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
// csrfToken is the already-issued/read csrf_supercharger session value
// (design.md D8) — SuperchargerStatsPage/SuperchargerStatsFragment resolve it
// and thread it in here; this function does no session I/O of its own.
//
// Returned status is StatusBadRequest ONLY for a malformed/out-of-range
// window, in which case Presets is left nil so the template renders no
// selector (design.md D1).
func (h *Handler) superchargerStatsViewFor(c *gin.Context, uid uuid.UUID, csrfToken string) (fragments.SuperchargerStatsView, int) {
	today := startOfDay(time.Now().UTC()) // design.md D9a — plain UTC, NOT browserToday(c); tz:allow: RM30 D9a, deliberately UTC not the browser cookie's zone

	start, end, ok := parseSuperchargerRange(c, today)
	if !ok {
		return fragments.SuperchargerStatsView{
			Chart:     fragments.HistoryChart{Empty: true},
			Empty:     true,
			CSRFToken: csrfToken,
		}, http.StatusBadRequest
	}

	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		return fragments.SuperchargerStatsView{
			Presets:        buildSuperchargerPresets(c.Request.Context(), start, end, today),
			Chart:          fragments.HistoryChart{Empty: true},
			Empty:          true,
			CSRFToken:      csrfToken,
			WindowStartStr: start.Format("2006-01-02"),
			WindowEndStr:   end.Format("2006-01-02"),
		}, http.StatusOK
	}

	return h.buildSuperchargerStatsView(c.Request.Context(), uid, selected.TeslaID, start, end, today, csrfToken), http.StatusOK
}

// fetchSuperchargerRowVM resolves a single Supercharger session row's view
// model by re-listing that vehicle's sessions within the SAME ?start=&end=
// window the row's own action link carries (design.md D1/D2) and matching id
// in memory — mirrors fetchEntryVM's documented no-GetEntry-port shape
// (charges.go, design decision D6 there / D1 here). Every failure mode — a
// malformed/absent window, no resolvable selected vehicle, a reader error, or
// "no session in the window matches id" — collapses to (zero, false); none is
// distinguished from another (design.md D1's documented non-distinction).
func (h *Handler) fetchSuperchargerRowVM(c *gin.Context, uid uuid.UUID, id uuid.UUID) (fragments.SuperchargerRowVM, bool) {
	today := startOfDay(time.Now().UTC()) // design.md D9a — plain UTC, NOT browserToday(c); tz:allow: RM30 D9a, deliberately UTC not the browser cookie's zone
	start, end, ok := parseSuperchargerRange(c, today)
	if !ok {
		return fragments.SuperchargerRowVM{}, false
	}

	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		return fragments.SuperchargerRowVM{}, false
	}

	sessions, err := h.superchargerReader.ListSessionsByVehicleBetween(c.Request.Context(), uid, selected.TeslaID, start, end)
	if err != nil {
		return fragments.SuperchargerRowVM{}, false
	}

	for _, s := range sessions {
		if s.ID == id {
			return superchargerRowVMFromSession(s), true
		}
	}
	return fragments.SuperchargerRowVM{}, false
}

// superchargerWindowStrs re-derives the request's ?start=&end= window as
// pre-formatted "2006-01-02" strings, for echoing back onto a row's own
// Edit/Cancel/Save action URLs (design.md D2). parseSuperchargerRange is pure
// and side-effect-free, so a second parse alongside fetchSuperchargerRowVM's
// own internal one costs nothing beyond the one extra (in-memory) call; it
// keeps fetchSuperchargerRowVM's return shape (VM, bool) unchanged rather than
// widening it to also hand back the window strings.
func superchargerWindowStrs(c *gin.Context) (startStr, endStr string) {
	today := startOfDay(time.Now().UTC()) // design.md D9a — plain UTC, NOT browserToday(c); tz:allow: RM30 D9a, deliberately UTC not the browser cookie's zone
	start, end, ok := parseSuperchargerRange(c, today)
	if !ok {
		return "", ""
	}
	return start.Format("2006-01-02"), end.Format("2006-01-02")
}

// SuperchargerStatsPage renders the full Supercharger Stats page for the
// session-selected vehicle (initial load). It generates a fresh
// csrf_supercharger token and saves it to the session (design.md D8) —
// mirroring ChargePage's generateCSRFToken/sess.Set/sess.Save shape exactly.
func (h *Handler) SuperchargerStatsPage(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	csrfToken, err := generateCSRFToken()
	if err != nil {
		c.String(http.StatusInternalServerError, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorCouldNotSave))
		return
	}
	sess := sessions.Default(c)
	sess.Set(csrfSuperchargerKey, csrfToken)
	_ = sess.Save()

	v, status := h.superchargerStatsViewFor(c, uid, csrfToken)
	if status != http.StatusOK {
		renderError(c, status, pages.SuperchargerStatsPage(v))
		return
	}
	render(c, status, pages.SuperchargerStatsPage(v))
}

// SuperchargerStatsFragment renders ONLY the #supercharger-stats-content
// fragment (htmx swap served by GET /ui/supercharger-stats?start=&end=) — the
// month-preset selector's hx-get target and the vehicle switcher's
// "vehicle-changed" subscriber. It READS the existing csrf_supercharger
// session value — it does NOT re-issue one (design.md D8; mirrors
// ChargesListFragment's sess.Get shape exactly).
func (h *Handler) SuperchargerStatsFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfSuperchargerKey).(string)

	v, status := h.superchargerStatsViewFor(c, uid, csrfToken)
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
//
// csrfToken/WindowStartStr/WindowEndStr are threaded onto the returned view
// verbatim (design.md D2/D8, RM31-gateway-add-session-battery-edit) — every
// row's own Edit/Cancel/Save action URL is built from the SAME window this
// view was resolved under, keeping fetchSuperchargerRowVM's list-and-match
// resolve deterministic.
func (h *Handler) buildSuperchargerStatsView(ctx context.Context, uid uuid.UUID, teslaID int64, start, end, today time.Time, csrfToken string) fragments.SuperchargerStatsView {
	v := fragments.SuperchargerStatsView{
		Presets:        buildSuperchargerPresets(ctx, start, end, today),
		CSRFToken:      csrfToken,
		WindowStartStr: start.Format("2006-01-02"),
		WindowEndStr:   end.Format("2006-01-02"),
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
		return fragments.HistoryChart{Empty: true, LabelVertical: true}
	}

	anchor := startOfMonth(start)
	numMonths := monthsBetween(anchor, startOfMonth(end)) + 1

	type bucket struct {
		month time.Time
		kwh   float64
	}
	buckets := make([]bucket, numMonths)
	for i := 0; i < numMonths; i++ {
		buckets[i].month = anchor.AddDate(0, i, 0)
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
			Tooltip:   fmt.Sprintf("%s · %.1f kWh", b.month.Format("Jan 2006"), b.kwh),
			Label:     b.month.Format("2006-01"),
		}
	}
	return fragments.HistoryChart{
		Bars:          bars,
		Empty:         false,
		LabelVertical: true,
		YAxisTicks:    buildYAxisTicks(maxKWh, func(kWh float64) string { return fmt.Sprintf("%.1f kWh", kWh) }),
	}
}

// monthsBetween returns how many calendar months t is after since (0 when
// t falls in since's own month), used to index buildSuperchargerChart's
// zero-filled month buckets.
func monthsBetween(since, t time.Time) int {
	return (t.Year()-since.Year())*12 + int(t.Month()) - int(since.Month())
}

// buildSuperchargerRows maps every session in the window (unpaginated) to a
// display row via superchargerRowVMFromSession — no behavior change from the
// pre-extraction inline body (design.md D11).
func buildSuperchargerRows(sessions []charging.Session) []fragments.SuperchargerRowVM {
	rows := make([]fragments.SuperchargerRowVM, 0, len(sessions))
	for _, s := range sessions {
		rows = append(rows, superchargerRowVMFromSession(s))
	}
	return rows
}

// superchargerRowVMFromSession maps one charging.Session to its row view
// model — the ONE place that formats these fields (design.md D11,
// RM31-gateway-add-session-battery-edit). Extracted out of
// buildSuperchargerRows's former per-session body so buildSuperchargerRows
// (list build), fetchSuperchargerRowVM (single-row GET resolve), and
// SuperchargerRowUpdate's success path (using VerifySession's own returned
// Session, no extra read) all share one mapper — mirrors
// chargeEntryVMFromEntry's identical "one mapper, multiple call sites" shape.
// Energy/cost render "—" when their source field is nil — EnergyLabel needs
// only EnergyKWh; CostLabel needs BOTH TotalCost and Currency.
// RawStartBatteryPct/RawEndBatteryPct are "" when the corresponding
// percentage is nil, matching ChargeEntryVM.RawStartBatteryPct's convention
// exactly.
func superchargerRowVMFromSession(s charging.Session) fragments.SuperchargerRowVM {
	energyLabel := "—"
	if s.EnergyKWh != nil {
		energyLabel = fmt.Sprintf("%.2f kWh", *s.EnergyKWh)
	}
	costLabel := "—"
	if s.TotalCost != nil && s.Currency != nil {
		costLabel = formatMoney(*s.TotalCost, *s.Currency)
	}
	rawStartPct := ""
	if s.StartBatteryPct != nil {
		rawStartPct = strconv.Itoa(*s.StartBatteryPct)
	}
	rawEndPct := ""
	if s.EndBatteryPct != nil {
		rawEndPct = strconv.Itoa(*s.EndBatteryPct)
	}
	return fragments.SuperchargerRowVM{
		ID:                   s.ID.String(),
		DateLabel:            s.ChargeStartDateTime.UTC().Format("Mon Jan 2, 2006"),
		SiteLabel:            s.SiteLocationName,
		EnergyLabel:          energyLabel,
		CostLabel:            costLabel,
		StartBatteryPctLabel: formatBatteryPct(s.StartBatteryPct),
		EndBatteryPctLabel:   formatBatteryPct(s.EndBatteryPct),
		RawStartBatteryPct:   rawStartPct,
		RawEndBatteryPct:     rawEndPct,
		RawStatus:            string(s.Status),
	}
}

func formatBatteryPct(pct *int) string {
	if pct == nil {
		return "—"
	}
	return fmt.Sprintf("%d%%", *pct)
}

// recalculateAfterSessionVerify calls the analytics module's recalculation
// port for the given vehicle, over a window spanning ONE calendar day before
// through ONE calendar day after the UTC calendar day of chargeStopDateTime
// (design.md D4/D5) — a SEPARATE function from recalculateAfterChargeWrite
// (charges.go), which this function does NOT call or modify. See design.md
// D4/D5 for the full safety argument for why a single day is provably wrong
// and this ±1-day window is provably sufficient to cover the true metric day
// regardless of the nightly poller's own configured local timezone.
func (h *Handler) recalculateAfterSessionVerify(ctx context.Context, uid uuid.UUID, teslaID int64, chargeStopDateTime time.Time) {
	day := startOfDay(chargeStopDateTime.UTC()) // D5 — plain UTC, ChargeStopDateTime
	from := day.AddDate(0, 0, -1)
	to := day.AddDate(0, 0, 1)
	if err := h.analyticsRecalculator.Recalculate(ctx, uid, teslaID, from, to); err != nil {
		log.Printf("gateway: analytics recalculate error for account %s, vehicle %d, session window %s..%s: %v",
			uid, teslaID, from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}
}

// SuperchargerRowStatic renders the static view of one Supercharger session
// row (used by the Cancel-edit path). Mirrors ChargeRowStatic exactly.
func (h *Handler) SuperchargerRowStatic(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorInvalidID))
		return
	}

	vm, ok2 := h.fetchSuperchargerRowVM(c, uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorSessionNotFound))
		return
	}

	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfSuperchargerKey).(string)
	windowStartStr, windowEndStr := superchargerWindowStrs(c)

	render(c, http.StatusOK, fragments.SuperchargerRow(vm, csrfToken, windowStartStr, windowEndStr))
}

// SuperchargerRowEditFragment swaps the static row for the inline edit form.
// Mirrors ChargeRowEditFragment exactly.
func (h *Handler) SuperchargerRowEditFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorInvalidID))
		return
	}

	vm, ok2 := h.fetchSuperchargerRowVM(c, uid, id)
	if !ok2 {
		c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorSessionNotFound))
		return
	}

	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfSuperchargerKey).(string)
	windowStartStr, windowEndStr := superchargerWindowStrs(c)

	render(c, http.StatusOK, fragments.SuperchargerRowEdit(vm, csrfToken, windowStartStr, windowEndStr, nil))
}

// SuperchargerRowUpdate handles PATCH /ui/supercharger-stats/row/:id. Saves
// the verified battery percentages via charging.SessionVerifier.VerifySession
// and swaps back to the static row on success, or re-renders the edit form
// with validation errors / a top-of-form save error. See design.md D3
// (strict body — an absent key is 400, an empty value clears), D6 (nil
// TeslaID skips recalculation but the write still succeeds), D7 (blanks
// clear, no ordering validation, [0,100] range validated first), D8
// (CSRF via csrfSuperchargerKey; no separate RegisteredVehicles ownership
// check — VerifySession's own account-scoped WHERE clause is the sole tenant
// boundary, a deliberate divergence from ChargeRowUpdate/D4), and D9
// (writer-error branching re-resolves via fetchSuperchargerRowVM instead of
// inspecting the wrapped pgx error, keeping pgx out of the gateway's import
// graph).
func (h *Handler) SuperchargerRowUpdate(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}
	if !h.checkCSRFKey(c, csrfSuperchargerKey) {
		return
	}
	idStr := c.Param("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorInvalidID))
		return
	}

	sess := sessions.Default(c)
	csrfToken, _ := sess.Get(csrfSuperchargerKey).(string)
	windowStartStr, windowEndStr := superchargerWindowStrs(c)

	// D3 — STRICT body: c.GetPostForm distinguishes "absent" (ok=false) from
	// "present but empty" (ok=true, value==""), unlike c.PostForm. Either key
	// absent -> 400, VerifySession never called, no fragment rendered.
	startRaw, startPresent := c.GetPostForm("start_battery_pct")
	endRaw, endPresent := c.GetPostForm("end_battery_pct")
	if !startPresent || !endPresent {
		c.String(http.StatusBadRequest, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorMalformedBody))
		return
	}

	// D7c — range/type validation BEFORE calling VerifySession. A present,
	// empty (or whitespace-only) value is a valid explicit clear (D7a), not a
	// validation error; it stays nil and is NOT range-checked.
	validationErrors := make(map[string]string)
	var startPct, endPct *int

	startTrimmed := strings.TrimSpace(startRaw)
	if startTrimmed != "" {
		if n, perr := strconv.Atoi(startTrimmed); perr != nil || n < 0 || n > 100 {
			validationErrors["start_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorStartRange)
		} else {
			startPct = &n
		}
	}
	endTrimmed := strings.TrimSpace(endRaw)
	if endTrimmed != "" {
		if n, perr := strconv.Atoi(endTrimmed); perr != nil || n < 0 || n > 100 {
			validationErrors["end_battery_pct"] = i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorEndRange)
		} else {
			endPct = &n
		}
	}

	if len(validationErrors) > 0 {
		// Re-resolve the row's read-only display context (date/site/energy/
		// cost/estimates — not submitted by this form's two-input body) via
		// the same list-and-match helper the GET routes use, then override
		// ONLY the two raw values with what the user actually submitted
		// (design.md T5 — echoed unmodified, not reset to the old stored
		// values). A resolve failure here (rare: the session vanished mid-
		// edit) falls back to just the id + submitted raw values rather than
		// escalating a validation error into a 404.
		vm, vmOK := h.fetchSuperchargerRowVM(c, uid, id)
		if !vmOK {
			vm = fragments.SuperchargerRowVM{ID: idStr}
		}
		vm.RawStartBatteryPct = startRaw
		vm.RawEndBatteryPct = endRaw
		renderError(c, http.StatusUnprocessableEntity, fragments.SuperchargerRowEdit(vm, csrfToken, windowStartStr, windowEndStr, validationErrors))
		return
	}

	updated, err := h.superchargerVerifier.VerifySession(c.Request.Context(), uid, id, startPct, endPct)
	if err != nil {
		log.Printf("gateway: SuperchargerRowUpdate writer error for account %s, id %s: %v", uid, id, err)
		// D9 — distinguish 404 from 500 by RE-RESOLVING via
		// fetchSuperchargerRowVM rather than inspecting the (possibly
		// pgx.ErrNoRows-wrapping) error — the gateway does not import pgx.
		vm, vmOK := h.fetchSuperchargerRowVM(c, uid, id)
		if !vmOK {
			c.String(http.StatusNotFound, i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorSessionNotFound))
			return
		}
		renderError(c, http.StatusInternalServerError, fragments.SuperchargerRowEdit(vm, csrfToken, windowStartStr, windowEndStr, map[string]string{
			"_top": i18n.T(c.Request.Context(), i18n.KeySuperchargerErrorCouldNotSave),
		}))
		return
	}

	// D6 — a nil TeslaID (the session's VIN is not a currently-registered
	// vehicle) skips recalculation and logs it; the write itself already
	// succeeded above regardless.
	if updated.TeslaID != nil {
		h.recalculateAfterSessionVerify(c.Request.Context(), uid, *updated.TeslaID, updated.ChargeStopDateTime)
	} else {
		log.Printf("gateway: supercharger session %s verified with nil TeslaID for account %s — skipping recalculation", id, uid)
	}

	vm := superchargerRowVMFromSession(updated)
	render(c, http.StatusOK, fragments.SuperchargerRow(vm, csrfToken, windowStartStr, windowEndStr))
}
