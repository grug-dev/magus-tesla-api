package clock

import "time"

// CalendarDay normalizes t to the calendar day it falls on when observed in loc, expressed
// at UTC midnight — the representation pgtype.Date expects (the storage encoding design.md
// D1 keeps unchanged). The calendar day is determined by t's wall-clock date in loc, not by
// t's date in UTC, so a moment near a day boundary is attributed to the correct local day
// rather than whichever day UTC happens to be in at that instant.
//
// This is the single replacement for three previously copy-pasted UTC-midnight truncators
// (design.md "Context" / D7): analytics.calendarDay(t) and gateway.startOfDay(t) — which
// always bucketed by UTC — become CalendarDay(t, time.UTC); telemetry.dateOnly(t, loc) —
// already zone-aware — becomes CalendarDay(t, loc), literally the same computation.
//
// loc is mandatory and NOT nil-checked: passing nil panics exactly as t.In(nil) would
// (design.md D7). Silently substituting a default zone here would smuggle a hidden behavior
// change into what is meant to be a thin, honest wrapper — that decision belongs to each
// adopting call site, not to this function.
func CalendarDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}
