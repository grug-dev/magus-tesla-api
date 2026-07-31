// history.go contains the DashboardHistoryFragment handler for
// GET /ui/dashboard/history?days=N, which returns the #dashboard-history region:
// the days selector + two SVG bar charts (odometer km/day delta + battery level %).
// All numeric computation happens in buildHistoryView — the Templ template is dumb.
// Design decisions RD5-RD7 from openspec/changes/gateway-dashboard-history-charts/.
package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/pages"
	"github.com/cristianpena/magus-tesla-api/internal/telemetry"
)

// historyDayPresets is the closed, small vocabulary of allowed day-count values
// for the history charts selector. Validation rejects anything not in this list
// (design: closed vocabulary, one named location — AI-efficiency principle).
var historyDayPresets = []int{6, 14, 30}

// defaultHistoryDays is the fallback when the days parameter is missing, invalid,
// or not a member of historyDayPresets.
const defaultHistoryDays = 6

// clampHistoryDays parses raw and returns the nearest allowed preset. Any
// missing/non-numeric/out-of-set value becomes defaultHistoryDays.
func clampHistoryDays(raw string) int {
	if raw == "" {
		return defaultHistoryDays
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return defaultHistoryDays
	}
	for _, p := range historyDayPresets {
		if n == p {
			return n
		}
	}
	return defaultHistoryDays
}

// startOfDay returns midnight UTC for the given time t (truncates to the day).
func startOfDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// DashboardHistoryFragment is the handler for GET /ui/dashboard/history.
// It authenticates the caller, resolves the selected vehicle, validates the
// days parameter, calls telemetry.Reader once, builds a logic-free view model,
// and renders the #dashboard-history fragment.
//
// Read-only: one read (SnapshotsByVehicleSince) per request; no writes, no Tesla
// API calls, no side effects. Degrades gracefully on reader errors (empty charts,
// no 500). Anonymous callers are redirected to /login (no data served).
func (h *Handler) DashboardHistoryFragment(c *gin.Context) {
	uid, ok := currentUID(c)
	if !ok {
		c.Redirect(http.StatusFound, "/login")
		return
	}

	selected, sOK := h.resolveSelectedVehicle(c.Request.Context(), c, uid)
	if !sOK {
		// No vehicle registered or account error — render an empty history block
		// (both charts in empty state). Mirrors dashboardFor degradation.
		v := fragments.HistoryView{
			Days:    defaultHistoryDays,
			Presets: historyDayPresets,
			Odometer: fragments.HistoryChart{Empty: true},
			Battery:  fragments.HistoryChart{Empty: true},
		}
		renderFragment(c, http.StatusOK, pages.DashboardHistory(v), "dashboard-history")
		return
	}

	days := clampHistoryDays(c.Query("days"))
	since := startOfDay(time.Now()).AddDate(0, 0, -days)

	v := h.buildHistoryView(c.Request.Context(), uid, selected.TeslaID, days, since)
	renderFragment(c, http.StatusOK, pages.DashboardHistory(v), "dashboard-history")
}

// buildHistoryView is the core logic for the history fragment, decoupled from
// gin/session so it is unit-testable with a fake telemetry.Reader. It performs
// ONE read (SnapshotsByVehicleSince) and maps the result into a logic-free view
// model (HistoryView) — all heights and tooltip strings are pre-computed here;
// the template does no arithmetic or domain-method calls (RD7).
//
// On reader error the function degrades (both charts empty) rather than panicking
// or returning a 500 — resilience mirrors dashboardFor.
func (h *Handler) buildHistoryView(ctx context.Context, uid uuid.UUID, teslaID int64, days int, since time.Time) fragments.HistoryView {
	v := fragments.HistoryView{
		Days:    days,
		Presets: historyDayPresets,
	}

	snaps, err := h.telemetryReader.SnapshotsByVehicleSince(ctx, uid, teslaID, since)
	if err != nil {
		log.Printf("gateway: history reader error for account %s vehicle %d: %v", uid, teslaID, err)
		v.Odometer = fragments.HistoryChart{Empty: true}
		v.Battery = fragments.HistoryChart{Empty: true}
		return v
	}

	v.Odometer = buildOdometerChart(snaps, days)
	v.Battery = buildBatteryChart(snaps, days)
	return v
}

// buildOdometerChart computes km-driven-per-day bars from snapshots (RD5).
// It takes up to days+1 of the most recent snapshots to produce days consecutive
// deltas. A negative delta (clock skew / odometer anomaly) is clamped to 0.
// Bar heights are expressed as a percentage of the maximum delta (so the tallest
// bar is always 100%). When fewer than 2 snapshots exist, the chart is empty.
func buildOdometerChart(snaps []telemetry.Snapshot, days int) fragments.HistoryChart {
	// Take the last min(len(snaps), days+1) snapshots (oldest-first from the port).
	want := days + 1
	start := 0
	if len(snaps) > want {
		start = len(snaps) - want
	}
	pts := snaps[start:]

	if len(pts) < 2 {
		return fragments.HistoryChart{Empty: true}
	}

	type delta struct {
		date      time.Time
		kmDriven  float64
		odometerKm float64
	}
	deltas := make([]delta, 0, len(pts)-1)
	maxKm := 0.0
	for i := 1; i < len(pts); i++ {
		d := pts[i].OdometerKm() - pts[i-1].OdometerKm()
		if d < 0 {
			d = 0 // clamp negative (RD5: clock skew / odometer anomaly)
		}
		deltas = append(deltas, delta{
			date:       pts[i].CapturedAt.UTC(),
			kmDriven:   d,
			odometerKm: pts[i].OdometerKm(),
		})
		if d > maxKm {
			maxKm = d
		}
	}

	bars := make([]fragments.HistoryBar, 0, len(deltas))
	for _, d := range deltas {
		pct := 0
		if maxKm > 0 {
			pct = int(math.Round(d.kmDriven / maxKm * 100))
		}
		tooltip := fmt.Sprintf("%s · %s km driven · odometer %s",
			d.date.Format("2006-01-02"),
			formatKmRaw(d.kmDriven),
			formatKm(d.odometerKm),
		)
		bars = append(bars, fragments.HistoryBar{HeightPct: pct, Tooltip: tooltip})
	}
	return fragments.HistoryChart{Bars: bars, Empty: len(bars) == 0}
}

// buildBatteryChart computes battery-level-% bars from the N most recent snapshots
// (RD6). Bar height = BatteryLevel directly (already 0–100). Empty when no points.
func buildBatteryChart(snaps []telemetry.Snapshot, days int) fragments.HistoryChart {
	// Take up to days of the most recent snapshots.
	start := 0
	if len(snaps) > days {
		start = len(snaps) - days
	}
	pts := snaps[start:]

	if len(pts) == 0 {
		return fragments.HistoryChart{Empty: true}
	}

	bars := make([]fragments.HistoryBar, 0, len(pts))
	for _, s := range pts {
		tooltip := fmt.Sprintf("%s · %d%% · %s km range",
			s.CapturedAt.UTC().Format("2006-01-02"),
			s.BatteryLevel,
			formatKmRaw(s.BatteryRangeKm()),
		)
		bars = append(bars, fragments.HistoryBar{HeightPct: s.BatteryLevel, Tooltip: tooltip})
	}
	return fragments.HistoryChart{Bars: bars, Empty: false}
}

// formatKmRaw renders a kilometre value as a whole number string without the " km"
// suffix — used inside tooltip strings where the unit is appended by the caller.
// Mirrors formatKm but returns only the number + thousands-separator, no unit.
func formatKmRaw(km float64) string {
	whole := int(math.Round(km))
	return commaGroup(strconv.Itoa(whole))
}
