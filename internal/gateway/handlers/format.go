package handlers

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// commaGroup inserts thousands separators into a non-negative integer string:
// "19312" → "19,312". Odometers are non-negative, so the sign path is intentionally
// absent. Pure string formatting — no number parsing overhead.
func commaGroup(s string) string {
	n := len(s)
	if n <= 3 {
		return s
	}
	var b strings.Builder
	pre := n % 3
	if pre > 0 {
		b.WriteString(s[:pre])
		b.WriteByte(',')
	}
	for i := pre; i < n; i += 3 {
		b.WriteString(s[i : i+3])
		if i+3 < n {
			b.WriteByte(',')
		}
	}
	return b.String()
}

// formatMoney renders a monetary amount with two decimal places and
// thousands separators, paired with its currency code — e.g.
// formatMoney(58000, "COP") -> "58,000.00 COP" (design.md's CostLines
// example). Two decimals + comma-grouping reuses commaGroup (formatKm's
// grouping helper); money is the documented suffix-exemption paired with a
// currency string instead of a unit suffix (ai/go-conventions.md).
func formatMoney(amount float64, currency string) string {
	cents := int64(math.Round(amount * 100))
	neg := cents < 0
	if neg {
		cents = -cents
	}
	whole := cents / 100
	frac := cents % 100
	s := commaGroup(strconv.FormatInt(whole, 10)) + "." + fmt.Sprintf("%02d", frac) + " " + currency
	if neg {
		s = "-" + s
	}
	return s
}

// formatKm renders a kilometre value as a whole, thousands-separated "N,NNN km"
// string — e.g. 19312.07 → "19,312 km". The value arrives already in kilometres
// from the telemetry.Reader port (converted once at capture time, not here);
// this helper only rounds and groups.
func formatKm(km float64) string {
	whole := int(math.Round(km))
	return commaGroup(strconv.Itoa(whole)) + " km"
}

// formatKmRaw renders a kilometre value as a whole number string without the " km"
// suffix — used inside tooltip strings where the unit is appended by the caller.
// Mirrors formatKm but returns only the number + thousands-separator, no unit.
func formatKmRaw(km float64) string {
	whole := int(math.Round(km))
	return commaGroup(strconv.Itoa(whole))
}
