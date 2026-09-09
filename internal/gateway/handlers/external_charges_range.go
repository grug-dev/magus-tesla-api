// external_charges_range.go contains the External charges page's date-filter range parser
// and preset builder (design.md §D-Range/§D-Presets,
// RM33-gateway-add-entries-dashboard). Mirrors history.go's/supercharger.go's
// ?start=&end= shape, with two deliberate divergences — no future-end
// rejection, and a default window ending at today, not today-1 — both
// explained in design.md §D-Range; do not "fix" them to match
// parseHistoryRange/parseSuperchargerRange.
package handlers

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/gateway/i18n"
	"github.com/cristianpena/magus-tesla-api/internal/gateway/templates/fragments"
)

// externalChargesRangeDefaultDays is the default window size (in days) applied when
// both ?start=&end= params are absent (design.md §D-Range).
const externalChargesRangeDefaultDays = 7

// externalChargesRangeMaxDays is the hard cap on the window width, protecting the
// read path from an unbounded range scan (Performance-Profile: read-heavy).
// Reuses superchargerRangeMaxDays's exact value and rationale
// (manual_charge_entries is, if anything, sparser than charge_sessions) —
// design.md §D-Range.
const externalChargesRangeMaxDays = 400

// parseExternalChargesRange validates the ?start=&end= query params for
// GET /ui/external-charges/list (design.md §D-Range, verbatim). today is the caller's
// resolved "today" (browserToday(c) — the browser-local calendar day,
// D-RM33-10), threaded through rather than read internally. Returns the
// parsed (start, end) calendar days and ok=true, or the zero values and
// ok=false when the request is malformed. The handler renders HTTP 400 on
// ok=false.
//
// Deliberately diverges from parseHistoryRange/parseSuperchargerRange in two
// ways (design.md §D-Range points 1-2):
//  1. No "future end" rejection — "This month" is the full calendar month,
//     whose end is routinely in the future relative to today.
//  2. Default end is today, not today-1 — a manual entry the user just saved
//     is visible immediately, unlike telemetry's next-day capture lag.
func parseExternalChargesRange(c *gin.Context, today time.Time) (start, end time.Time, ok bool) {
	rawStart := c.Query("start")
	rawEnd := c.Query("end")
	if rawStart == "" && rawEnd == "" {
		end = today
		start = end.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1))
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
	if e.Sub(s).Hours()/24 > float64(externalChargesRangeMaxDays) {
		return time.Time{}, time.Time{}, false
	}
	return s, e, true
}

// endOfMonth returns midnight UTC on the LAST day of t's month — the one new
// date-math primitive this tier adds (design.md §D-Presets). startOfMonth is
// the EXISTING package-level helper (supercharger.go:57) — reused, not
// duplicated.
func endOfMonth(t time.Time) time.Time {
	return startOfMonth(t).AddDate(0, 1, 0).AddDate(0, 0, -1)
}

// buildExternalChargesPresets builds the three named date-filter presets — "last 7
// days", "this month" and "last month" — as fragments.RangePreset entries
// (design.md §D-Order/§D-Presets, verbatim). Reuses fragments.RangePreset
// unchanged; no new preset type. Active is computed by exact-match against
// each preset's own recomputed window (against today), not against an
// arbitrary caller-passed window boundary.
//
// "Last month" is appended AFTER the two existing entries — no reordering
// (design.md §D-Order, RM51, roadmap RD7). time.Time.AddDate normalizes the
// month/year rollback, so a January "today" correctly resolves to the prior
// December with no special-cased branch.
func buildExternalChargesPresets(ctx context.Context, start, end, today time.Time) []fragments.RangePreset {
	last7Start := today.AddDate(0, 0, -(externalChargesRangeDefaultDays - 1))
	monthStart := startOfMonth(today)
	monthEnd := endOfMonth(today)
	prevStart := startOfMonth(today).AddDate(0, -1, 0)
	prevEnd := endOfMonth(prevStart)
	return []fragments.RangePreset{
		{
			Label:    i18n.T(ctx, i18n.KeyChargesRangeLast7Days),
			StartStr: last7Start.Format("2006-01-02"),
			EndStr:   today.Format("2006-01-02"),
			Active:   start.Equal(last7Start) && end.Equal(today),
		},
		{
			Label:    i18n.T(ctx, i18n.KeyChargesRangeThisMonth),
			StartStr: monthStart.Format("2006-01-02"),
			EndStr:   monthEnd.Format("2006-01-02"),
			Active:   start.Equal(monthStart) && end.Equal(monthEnd),
		},
		{
			Label:    i18n.T(ctx, i18n.KeyChargesRangeLastMonth),
			StartStr: prevStart.Format("2006-01-02"),
			EndStr:   prevEnd.Format("2006-01-02"),
			Active:   start.Equal(prevStart) && end.Equal(prevEnd),
		},
	}
}
