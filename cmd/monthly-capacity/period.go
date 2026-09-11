package main

import (
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// previousMonth answers "what is the previous month, right now?" now and loc
// are parameters, never read inside, so this stays pure and fully testable.
//
// internal/app has its own month-resolution function for the nightly step. It
// runs only on the 1st of a month, so it can step straight back from the day.
// This tool runs on any day, so it cannot -- see the comment in the body.
func previousMonth(now time.Time, loc *time.Location) time.Time {
	today := clock.CalendarDay(now, loc)
	// Go to the 1st of today's month FIRST, then step back one month. Stepping
	// back from the day itself would return the 15th of last month when run on
	// the 15th, and the calculator requires the first instant of a month.
	monthStart := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	return monthStart.AddDate(0, -1, 0)
}

// parsePeriod parses a "YYYY-MM" period string into the first instant of that month,
// UTC-midnight-stamped -- the same representation clock.CalendarDay always returns
// (matching pgtype.Date's storage encoding, so it compares equal to what Calculate reads
// back). time.Parse with no zone information in the layout already returns a UTC time, so
// no separate zone conversion is needed or correct here: raw did not come from a clock
// reading, so there is no "which zone is now in" question to answer.
func parsePeriod(raw string) (time.Time, error) {
	return time.Parse("2006-01", raw)
}

// resolvePeriod is the single entry point main() calls: raw is the -period flag's raw
// string. Empty means "the previous month"; non-empty is parsed directly. This
// duplicates a few lines of internal/app's own month-math rather than exporting it --
// that function is deliberately unexported, carries a gate this tool does not want
// ("is today the 1st?"), and exporting it from an application-layer package for one
// manual-tool caller would widen that package's public surface for a need that is a
// few lines of straight-line date math, not a shared rule that could drift between
// two copies.
func resolvePeriod(raw string, now time.Time, loc *time.Location) (time.Time, error) {
	if raw == "" {
		return previousMonth(now, loc), nil
	}
	return parsePeriod(raw)
}

// teslaIDPointer turns a flag.Int64Var's value into the *int64 the port wants.
// wasSet comes from flag.Visit, never from checking id != 0 -- a caller could
// legitimately pass -tesla-id 0, and this function must not silently treat
// that as "no flag given."
func teslaIDPointer(id int64, wasSet bool) *int64 {
	if !wasSet {
		return nil
	}
	return &id
}
