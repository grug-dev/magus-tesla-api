// history.go contains the DashboardHistoryFragment handler for
// GET /ui/dashboard/history?start=YYYY-MM-DD&end=YYYY-MM-DD, which returns the
// #dashboard-history region: the preset selector + three SVG bar charts
// (odometer km/day delta + battery level % + battery-consumed %/day) rendered
// over a FIXED [start..end] calendar-day axis (one bar/day, identical labels
// across charts — the MAG-7 fix). All numeric computation happens in
// buildHistoryView — the Templ template is dumb.
// Design decisions: RM8-gateway-history-date-range (D1-D6) + RD5-RD7 from
// openspec/changes/gateway-dashboard-history-charts/; the consumed chart +
// D11 default/cap shift added by RM28-gateway-add-consumed-graph (tier 4).
package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/battery"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// historyRangeWindowDays is the default window size in days applied when both
// start and end params are absent — the self-load and anonymous defaults.
// Replaces the old defaultHistoryDays/day-count vocabulary (RM8 design D1).
const historyRangeWindowDays = 6

// historyRangeMaxDays is the hard cap on the window width, protecting the hot
// read path from an unbounded range scan (Performance-Profile: read-heavy). A
// wider request is rejected with 400 before the port is called (RM8 design D1).
const historyRangeMaxDays = 90

// historyPresetDayCounts is the closed vocabulary of preset day counts the
// selector renders. Each button's absolute ?start=<today-N>&end=<today> href is
// built by the handler at render time — the closed vocabulary is a UI-only
// convenience, not an HTTP contract (RM8 design D3/D4, Decision #3).
var historyPresetDayCounts = []int{6, 14, 30}

// labelVerticalFor decides per-bar label orientation for a history chart from
// the number of bars in the fixed [start..end] window (design D3: a single
// chart-level flag, computed once in the handler — the template never compares
// the window or computes rotation). This is the one named location for the
// wide/narrow split: true (rotated vertical labels) when numBars >= 14 (the
// 14- and 30-day presets), false (horizontal labels) for the wide 6-bar default.
func labelVerticalFor(numBars int) bool {
	return numBars >= 14
}

// startOfDay returns midnight UTC for the given time t (truncates to the day).
func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// effectiveDayUTC returns the UTC-midnight calendar day of a snapshot's
// EffectiveDate — the map key for fixed-axis bucketing (design D3).
func effectiveDayUTC(t time.Time) time.Time {
	return startOfDay(t)
}

// calendarDateAfter reports whether a's calendar date (Y/M/D, evaluated in a's
// own Location) is strictly later than b's calendar date (evaluated in b's own
// Location). Unlike time.Time.After — which compares absolute instants — this
// compares each side's wall-clock calendar day in its own frame, which is the
// comparison parseHistoryRange's end<=today cap actually needs: e is
// UTC-midnight-of-D (from time.Parse), today is midnight-of-D in the browser's
// own timezone (browserToday(c)) — two different frames whose INSTANTS are not
// safely comparable with .After.
//
// Do NOT simplify this back to e.After(today): for any IANA zone with a
// POSITIVE UTC offset (most of Europe/Africa/Asia/Australia/NZ),
// UTC-midnight-of-D is always a LATER instant than local-midnight-of-D, so
// e.After(today) spuriously rejects a request carrying the user's own
// browser-local "today" with an HTTP 400 — confirmed against Pacific/Auckland
// (+12), Asia/Tokyo (+9), and Europe/London (+1 BST) in MAG-7 review finding
// R1-1. Negative-offset zones (America/Bogota, America/Los_Angeles, …) and UTC
// happened to work with .After, which is exactly what made the bug invisible
// until a positive-offset zone was tested.
func calendarDateAfter(a, b time.Time) bool {
	aY, aM, aD := a.Date()
	bY, bM, bD := b.Date()
	if aY != bY {
		return aY > bY
	}
	if aM != bM {
		return aM > bM
	}
	return aD > bD
}

// parseHistoryRange validates the ?start=&end= query params (RM8 design D1 /
// Decision #2). today is the caller's "browser today" (browserToday(c)),
// resolved ONCE by the caller (DashboardHistoryFragment) rather than re-derived
// here, so a single request never re-runs time.LoadLocation more than once. It
// returns the parsed (start, end) calendar days and ok=true, or the zero values
// and ok=false when the request is malformed. The handler renders HTTP 400 on
// ok=false.
//
// "Today" is the browser's today, so a user in PST at 10pm local can still
// request end=their-local-today without a spurious 400 from the UTC cap.
// Direct API callers without a browser_tz cookie get UTC today (browserToday's
// fallback).
//
// yesterday (today.AddDate(0,0,-1)) — not today — is what the default window
// and the cap actually compare against (D11): the nightly batch captures
// today's data tomorrow, so an end=today window's last bar is always empty.
//
// Validation, in order:
//  1. Both absent → default 6-day window (end = browser-yesterday UTC
//     midnight, start = end.AddDate(0,0,-historyRangeWindowDays)), ok=true.
//  2. Either present → both required and well-formed YYYY-MM-DD.
//  3. end >= start (end.Before(start) → false).
//  4. end's calendar date <= browser-yesterday's calendar date
//     (calendarDateAfter(e, yesterday) → false — no future/today dates,
//     compared as calendar days, not instants; see calendarDateAfter).
//  5. Window <= historyRangeMaxDays days (read-path protection).
func parseHistoryRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
	yesterday := today.AddDate(0, 0, -1)
	rawStart := c.Query("start")
	rawEnd := c.Query("end")

	// 1. Default window when both are absent.
	if rawStart == "" && rawEnd == "" {
		end = yesterday
		start = end.AddDate(0, 0, -historyRangeWindowDays)
		return start, end, true
	}

	// 2. Either present → both must parse as YYYY-MM-DD.
	s, err := time.Parse("2006-01-02", rawStart)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	e, err := time.Parse("2006-01-02", rawEnd)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	// time.Parse("2006-01-02", ...) yields a time.Time at 00:00 UTC for a bare
	// date string, so start/end are already UTC-midnight-bounded.

	// 3. end >= start.
	if e.Before(s) {
		return time.Time{}, time.Time{}, false
	}
	// 4. end's calendar date <= browser-yesterday's calendar date. See
	// calendarDateAfter for why this must NOT be e.After(yesterday).
	if calendarDateAfter(e, yesterday) {
		return time.Time{}, time.Time{}, false
	}
	// 5. Window <= max days (inclusive end → width in days = end-start+1 ≤ max+1
	// is allowed; equivalently end-start ≤ max days, reject when > max).
	if e.Sub(s).Hours()/24 > float64(historyRangeMaxDays) {
		return time.Time{}, time.Time{}, false
	}
	return s, e, true
}

// DashboardHistoryFragment is the handler for GET /ui/dashboard/history.
// It authenticates the caller, resolves the selected vehicle, validates the
// start/end params, calls telemetry.Reader once (SnapshotsByVehicleBetween with
// a 1-day lookback), builds a logic-free view model over a fixed [start..end]
// calendar-day axis, and renders the #dashboard-history fragment.
//
// Read-only: one read per request; no writes, no Tesla API calls, no side
// effects. Degrades gracefully on reader errors (empty charts, no 500) and on
// malformed params (400 with the empty-state placeholder). Anonymous callers
// are redirected to /login (no data served).
func (h *Handler) DashboardHistoryFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	// Resolve "browser today" ONCE per request and thread it through —
	// parseHistoryRange, buildHistoryPresets (no-vehicle branch), and
	// buildHistoryView all need it, and each browserToday(c) call re-runs
	// time.LoadLocation. Resolving once also removes a theoretical
	// day-boundary race between repeated time.Now() calls within one request
	// (MAG-7 review finding R1-7).
	today := browserToday(c)

	start, end, ok := parseHistoryRange(c, today)
	if !ok {
		// Malformed/invalid window → 400 with the empty-state placeholder and
		// NO preset selector (the request shape was malformed; render a graceful
		// degrade, not a 500 and not a selector). design D1 / spec scenario.
		v := fragments.HistoryView{
			Odometer: fragments.HistoryChart{Empty: true},
			Battery:  fragments.HistoryChart{Empty: true},
			Consumed: fragments.HistoryChart{Empty: true},
		}
		renderFragmentError(c, http.StatusBadRequest, pages.DashboardHistory(v), "dashboard-history")
		return
	}

	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		// No vehicle registered or account error — render an empty history block
		// (all three charts in empty state) but WITH the preset selector so the user
		// can still switch windows. Mirrors dashboardFor degradation.
		v := fragments.HistoryView{
			Start:    start,
			End:      end,
			Presets:  buildHistoryPresets(c.Request.Context(), start, end, today),
			Odometer: fragments.HistoryChart{Empty: true},
			Battery:  fragments.HistoryChart{Empty: true},
			Consumed: fragments.HistoryChart{Empty: true},
		}
		renderFragment(c, http.StatusOK, pages.DashboardHistory(v), "dashboard-history")
		return
	}

	v := h.buildHistoryView(c.Request.Context(), uid, selected.TeslaID, start, end, today)
	renderFragment(c, http.StatusOK, pages.DashboardHistory(v), "dashboard-history")
}

// buildHistoryPresets builds the []RangePreset for the selector at render time
// (design D4, Decision #3): for each n in {6, 14, 30}, pEnd = YESTERDAY (today-1,
// because the nightly batch captures today's data tomorrow — an end=today preset
// would always show an empty last bar), pStart = pEnd.AddDate(0,0,-n), the
// absolute href is pre-formatted, and Active is true when (Start, End) matches
// the requested (start, end) window. A custom (non-preset) window marks no
// preset active — the selector renders all-ghost. today is the caller's
// "browser today" (browserToday(c)) so "yesterday" is the user's local
// yesterday, not UTC's — direct API callers without a cookie pass UTC today.
func buildHistoryPresets(ctx context.Context, start, end, today time.Time) []fragments.RangePreset {
	yesterday := today.AddDate(0, 0, -1)
	out := make([]fragments.RangePreset, 0, len(historyPresetDayCounts))
	for _, n := range historyPresetDayCounts {
		pEnd := yesterday
		pStart := yesterday.AddDate(0, 0, -n)
		out = append(out, fragments.RangePreset{
			Label:    fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryDaysPreset), n),
			StartStr: pStart.Format("2006-01-02"),
			EndStr:   pEnd.Format("2006-01-02"),
			Active:   start.Equal(pStart) && end.Equal(pEnd),
		})
	}
	return out
}

// buildHistoryView is the core logic for the history fragment, decoupled from
// gin/session so it is unit-testable with a fake telemetry.Reader and a fake
// battery.Reader. It performs TWO independent reads:
//
//  1. SnapshotsByVehicleBetween, with a 1-day lookback (readStart = start-1day
//     — design D2), feeding the odometer and battery charts. The returned
//     snapshots are bucketed by EffectiveDate.
//  2. ConsumedByDay, feeding the battery-consumed chart (RM28 tier 4). Its
//     entries are bucketed on DayConsumption.Date verbatim — internal/battery
//     already computed that day in the poller's zone (roadmap D18), so the
//     gateway must NOT re-project it through effectiveDayUTC.
//
// Both feed a fixed [start..end] calendar-day axis — one bar/day, identical
// MM-DD labels on all three charts (the MAG-7 fix, design D3). All heights and
// tooltip strings are pre-computed here; the template does no arithmetic or
// domain-method calls (RD7).
//
// The two reads fail independently: a snapshot-read error empties the odometer
// and battery charts and returns; a ConsumedByDay error empties only the
// consumed chart and leaves the two already-populated charts intact (design
// D-G10). Either way the function degrades rather than panicking or returning a
// 500 — resilience mirrors dashboardFor.
func (h *Handler) buildHistoryView(ctx context.Context, uid uuid.UUID, teslaID int64, start, end, today time.Time) fragments.HistoryView {
	v := fragments.HistoryView{
		Start:   start,
		End:     end,
		Presets: buildHistoryPresets(ctx, start, end, today),
	}

	// 1-day lookback: fetch from readStart so the snapshot whose EffectiveDate
	// == start-1 seeds the first odometer delta's km basis. The lookback is a
	// gateway concern; the port stays a clean Between(start, end) (design D2).
	readStart := start.AddDate(0, 0, -1)
	snaps, err := h.telemetryReader.SnapshotsByVehicleBetween(ctx, uid, teslaID, readStart, end)
	if err != nil {
		log.Printf("gateway: history reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Odometer = fragments.HistoryChart{Empty: true}
		v.Battery = fragments.HistoryChart{Empty: true}
		return v
	}

	v.Odometer = buildOdometerChart(ctx, snaps, start, end)
	v.Battery = buildBatteryChart(ctx, snaps, start, end)

	// The consumed chart is a SEPARATE read against a SEPARATE port — its own
	// error degrades ONLY v.Consumed, never the already-populated
	// v.Odometer/v.Battery above (design.md D-G10).
	days, err := h.batteryReader.ConsumedByDay(ctx, uid, teslaID, start, end)
	if err != nil {
		log.Printf("gateway: consumed-chart reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Consumed = fragments.HistoryChart{Empty: true}
		return v
	}
	v.Consumed = buildConsumedChart(ctx, days, start, end)
	return v
}

// buildOdometerChart computes km-driven-per-day bars over a FIXED [start..end]
// calendar-day axis (design D3 — the MAG-7 fix). It allocates exactly numDays
// slots (one per calendar day, inclusive end), buckets the returned snapshots
// by EffectiveDate into a map keyed by UTC midnight, and for each day d in
// [start, end] computes the delta odometerKm(snap[d]) - odometerKm(snap[d-1])
// when both exist (the lookback start-1 snapshot seeds the first bar). A
// negative delta (clock skew / odometer anomaly) is clamped to 0. A day with no
// snapshot for either d or d-1 renders as an empty labeled bar (Present=false,
// HeightPct=0, "<MM-DD> · no snapshot" tooltip). The chart's Empty fires only
// when fewer than 2 snapshots total exist in [start-1..end] (no delta possible).
// Bar heights are expressed as a percentage of the maximum delta (so the tallest
// bar is always 100%).
func buildOdometerChart(ctx context.Context, snaps []telemetry.Snapshot, start, end time.Time) fragments.HistoryChart {
	numDays := int(end.Sub(start).Hours()/24) + 1

	// Empty when fewer than 2 snapshots in the lookback+window set (the
	// returned slice covers [start-1..end]; < 2 → no delta possible).
	if len(snaps) < 2 {
		return fragments.HistoryChart{Empty: true, LabelVertical: labelVerticalFor(numDays)}
	}

	// Bucket by EffectiveDate UTC midnight — one entry per calendar day. The
	// lookback snapshot (EffectiveDate == start-1) lands outside [start..end]
	// and is consumed ONLY as the first bar's delta basis.
	byDay := make(map[time.Time]telemetry.Snapshot, len(snaps))
	for _, s := range snaps {
		byDay[effectiveDayUTC(s.EffectiveDate)] = s
	}

	type delta struct {
		label      string
		kmDriven   float64
		odometerKm float64
		present    bool
	}
	deltas := make([]delta, 0, numDays)
	maxKm := 0.0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		label := d.Format("01-02")
		cur, curOK := byDay[d]
		prev, prevOK := byDay[d.AddDate(0, 0, -1)]
		if !curOK || !prevOK {
			// Missing snapshot for d or d-1 → empty labeled bar.
			deltas = append(deltas, delta{
				label:   label,
				present: false,
			})
			continue
		}
		km := cur.OdometerKm - prev.OdometerKm
		if km < 0 {
			km = 0 // clamp negative (RD5: clock skew / odometer anomaly)
		}
		deltas = append(deltas, delta{
			label:      label,
			kmDriven:   km,
			odometerKm: cur.OdometerKm,
			present:    true,
		})
		if km > maxKm {
			maxKm = km
		}
	}

	bars := make([]fragments.HistoryBar, 0, numDays)
	for _, d := range deltas {
		if !d.present {
			bars = append(bars, fragments.HistoryBar{
				HeightPct: 0,
				Tooltip:   fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryNoSnapshotTooltip), d.label),
				Label:     d.label,
				Present:   false,
			})
			continue
		}
		pct := 0
		if maxKm > 0 {
			pct = int(math.Round(d.kmDriven / maxKm * 100))
		}
		tooltip := fmt.Sprintf("%s · %s km driven · odometer %s",
			d.label,
			formatKmRaw(d.kmDriven),
			formatKm(d.odometerKm),
		)
		bars = append(bars, fragments.HistoryBar{
			HeightPct: pct,
			Tooltip:   tooltip,
			Label:     d.label,
			Present:   true,
		})
	}
	return fragments.HistoryChart{Bars: bars, Empty: len(bars) == 0, LabelVertical: labelVerticalFor(numDays)}
}

// buildBatteryChart computes battery-level-% bars over a FIXED [start..end]
// calendar-day axis (design D3 — the MAG-7 fix). It allocates exactly numDays
// slots, buckets snapshots by EffectiveDate, and for each day d in [start, end]
// emits a bar at HeightPct = snap.BatteryLevelPct when a snapshot exists, else
// an empty labeled bar (Present=false, HeightPct=0, "<MM-DD> · no snapshot").
// Bar height is the battery level percentage directly (already 0–100). The
// chart's Empty fires only when zero snapshots exist in the window (a partial
// axis is NOT an empty chart). Returns EXACTLY numDays bars so the odometer and
// battery Label slices are identical by construction.
func buildBatteryChart(ctx context.Context, snaps []telemetry.Snapshot, start, end time.Time) fragments.HistoryChart {
	numDays := int(end.Sub(start).Hours()/24) + 1

	// Empty only when zero snapshots in the window (a partial axis is NOT empty).
	if len(snaps) == 0 {
		return fragments.HistoryChart{Empty: true, LabelVertical: labelVerticalFor(numDays)}
	}

	byDay := make(map[time.Time]telemetry.Snapshot, len(snaps))
	for _, s := range snaps {
		byDay[effectiveDayUTC(s.EffectiveDate)] = s
	}

	bars := make([]fragments.HistoryBar, 0, numDays)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		label := d.Format("01-02")
		s, ok := byDay[d]
		if !ok {
			bars = append(bars, fragments.HistoryBar{
				HeightPct: 0,
				Tooltip:   fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryNoSnapshotTooltip), label),
				Label:     label,
				Present:   false,
			})
			continue
		}
		tooltip := fmt.Sprintf("%s · %d%% · %s km range",
			label,
			s.BatteryLevelPct,
			formatKmRaw(s.BatteryRangeKm),
		)
		bars = append(bars, fragments.HistoryBar{
			HeightPct: s.BatteryLevelPct,
			Tooltip:   tooltip,
			Label:     label,
			Present:   true,
		})
	}
	return fragments.HistoryChart{Bars: bars, Empty: false, LabelVertical: labelVerticalFor(numDays)}
}

// buildConsumedChart computes battery-consumed-%/day bars over the FIXED
// [start..end] calendar-day axis (matching the other two charts' window),
// bucketed on battery.DayConsumption.Date DIRECTLY — never effectiveDayUTC
// (D18/D18a, design.md D-G2: the port's Date is already a final, zoned
// bucket key). Scaled RELATIVE to the window's max displayed value (D19),
// mirroring buildOdometerChart's maxKm pattern, not buildBatteryChart's
// absolute 0-100 — every bar's height is math.Max(0, ConsumedPct) (design.md
// D-G1; the same clamp for every bar, no per-state branch). A day absent
// from days renders as the existing empty labeled "no data" bar
// (Present=false). A day present carries MarkerFlagged when Flagged (D10)
// and/or MarkerSpan when DaysSpanned > 1 (D20) — BOTH markers, independently,
// when both conditions hold (D21, design.md D-G4/D-G5). The tooltip is
// composed from independently-translated clauses joined by " · " (design.md
// D-G7) — never a value clause when MarkerFlagged && !MarkerSpan (D10 hides
// the number), always the real signed value otherwise. The chart's Empty
// fires only when days is empty (mirrors buildBatteryChart: a single
// computable day is enough to draw, unlike buildOdometerChart's need for a
// delta pair).
func buildConsumedChart(ctx context.Context, days []battery.DayConsumption, start, end time.Time) fragments.HistoryChart {
	numDays := int(end.Sub(start).Hours()/24) + 1

	if len(days) == 0 {
		return fragments.HistoryChart{Empty: true, LabelVertical: labelVerticalFor(numDays)}
	}

	byDay := make(map[time.Time]battery.DayConsumption, len(days))
	for _, d := range days {
		byDay[d.Date] = d // D-G2: d.Date verbatim, never effectiveDayUTC(d.Date)
	}

	type entry struct {
		label         string
		present       bool
		displayVal    float64
		markerFlagged bool
		markerSpan    bool
		tooltip       string
	}
	entries := make([]entry, 0, numDays)
	maxVal := 0.0
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		label := d.Format("01-02")
		day, ok := byDay[d]
		if !ok {
			entries = append(entries, entry{label: label, present: false})
			continue
		}
		e := entry{
			label:         label,
			present:       true,
			displayVal:    math.Max(0, day.ConsumedPct), // D-G1: one clamp, no per-state branch
			markerFlagged: day.Flagged,                  // D10
			markerSpan:    day.DaysSpanned > 1,          // D20
		}

		// D-G7: compose independently-translated clauses, joined by " · ".
		pctStr := formatPctRaw(day.ConsumedPct) // the REAL signed value, never displayVal
		var clauses []string
		switch {
		case e.markerSpan:
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedSpanClause), pctStr, day.DaysSpanned))
		case !e.markerFlagged:
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedPctClause), pctStr))
			// markerFlagged && !markerSpan: no value clause at all — D10 hides the number.
		}
		if e.markerFlagged {
			clauses = append(clauses, fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedFlaggedClause), chargeTypeLabel(ctx, day.MissingChargingType)))
		}
		tooltip := label
		for _, c := range clauses {
			tooltip += " · " + c
		}
		e.tooltip = tooltip

		entries = append(entries, e)
		if e.displayVal > maxVal {
			maxVal = e.displayVal
		}
	}

	bars := make([]fragments.HistoryBar, 0, numDays)
	for _, e := range entries {
		if !e.present {
			bars = append(bars, fragments.HistoryBar{
				HeightPct: 0,
				Tooltip:   fmt.Sprintf(i18n.T(ctx, i18n.KeyHistoryConsumedNoDataTooltip), e.label),
				Label:     e.label,
				Present:   false,
			})
			continue
		}
		pct := 0
		if maxVal > 0 {
			pct = int(math.Round(e.displayVal / maxVal * 100))
		}
		bars = append(bars, fragments.HistoryBar{
			HeightPct:     pct,
			Tooltip:       e.tooltip,
			Label:         e.label,
			Present:       true,
			MarkerFlagged: e.markerFlagged,
			MarkerSpan:    e.markerSpan,
		})
	}
	return fragments.HistoryChart{Bars: bars, Empty: false, LabelVertical: labelVerticalFor(numDays)}
}

// chargeTypeLabel resolves the bilingual charge-type noun used inside a
// flagged-day tooltip. telemetry.MissingChargingType is a closed 2-value
// enum (tier 1); this is the gateway's own closed mapping to a catalogue
// key, kept here rather than in internal/telemetry because it is
// presentation vocabulary, not domain vocabulary.
func chargeTypeLabel(ctx context.Context, t telemetry.MissingChargingType) string {
	if t == telemetry.MissingChargingTypeSupercharger {
		return i18n.T(ctx, i18n.KeyHistoryChargeTypeSupercharger)
	}
	return i18n.T(ctx, i18n.KeyHistoryChargeTypeManual)
}
