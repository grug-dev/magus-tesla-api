// supercharger.go contains the Supercharger Stats page/fragment handlers
// (GET /supercharger-stats, GET /ui/supercharger-stats?months=N). Mirrors
// history.go's shape: a closed month-preset vocabulary + clamp helper here;
// the read cap; and (wave 2) the view-building handler logic.
// Design decisions D5-D6 from openspec/changes/gateway-add-supercharger-stats/.
package handlers

import "strconv"

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
