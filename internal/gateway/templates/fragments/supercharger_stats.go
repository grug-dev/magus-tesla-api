package fragments

import "strings"

// superchargerCostValue returns the Cost tile's display string: "—" when no
// session in the window carried both a known cost and currency, otherwise
// every pre-formatted CostLines entry joined for multi-line display inside
// the tile (D4 — never a single summed total across currencies). This is a
// presence check plus a join of already-formatted strings, not currency
// math or formatting — every line's number/currency text arrives
// pre-computed from the handler. Mirrors dashStat's "—" fallback pattern
// (templates/pages/dashboard.go).
func superchargerCostValue(lines []string) string {
	if len(lines) == 0 {
		return "—"
	}
	return strings.Join(lines, "\n")
}
