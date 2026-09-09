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

// formatPctRaw renders a percentage value to one decimal place with no "%"
// suffix (the caller's i18n format string supplies it) — e.g. 11.0 -> "11.0",
// -3.4 -> "-3.4". Mirrors formatKmRaw's "no unit, caller appends it" shape.
// One decimal because the consumed chart's typical 6-20%/day range needs
// sub-integer precision to be readable (roadmap D19 rationale) — unlike
// formatKm's whole-number odometer/battery values.
func formatPctRaw(pct float64) string {
	return strconv.FormatFloat(pct, 'f', 1, 64)
}

// formatPSI renders a tyre-pressure value to one decimal place with a " PSI"
// suffix — e.g. 42.06 -> "42.1 PSI". Mirrors formatKm's "round once, unit-
// suffix here" shape; PSI needs sub-integer precision (a ~1 PSI leak is
// meaningful), unlike formatKm's whole-number odometer.
// formatKmPerPct renders a driving-efficiency value as kilometres per battery
// percent, to one decimal place -- e.g. 2.8 -> "2.8 km / %". One decimal because
// the figure is small: whole numbers would collapse 2.4 and 2.9 into "2" and "3".
func formatKmPerPct(kmPerPct float64) string {
	return strconv.FormatFloat(kmPerPct, 'f', 1, 64) + " km/%"
}

func formatPSI(psi float64) string {
	return strconv.FormatFloat(psi, 'f', 1, 64) + " PSI"
}

// formatSignedPSI renders a signed tyre-pressure delta to one decimal place,
// no unit suffix (the caller's i18n format string supplies the "vs prev. day"
// context) — e.g. 0.4 -> "+0.4", -0.4 -> "-0.4", 0.0 -> "0.0". Explicit "+"
// only for a strictly positive value; strconv's own "-" already covers a
// negative one; zero gets neither sign, matching dashTireTrend's own "0.0 is
// neither up nor down" rule.
func formatSignedPSI(v float64) string {
	s := strconv.FormatFloat(v, 'f', 1, 64)
	if v > 0 {
		return "+" + s
	}
	return s
}
