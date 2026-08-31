package clock_test

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// TestCalendarDay transcribes design.md's four Test Contract cases for CalendarDay verbatim
// (RM35-clock-add-bogota-time-package design.md D7). These four cases are the regression net
// for tiers 2-6: any adopting tier that swaps a truncator for
// clock.CalendarDay(t, <the same loc it always used>) is, by D7's construction, guaranteed
// byte-identical output for every input its existing tests already exercise.

// Case 1: UTC input, UTC zone -- a moment already at its own UTC-day start.
func TestCalendarDay_UTCSelfTruncation(t *testing.T) {
	in := time.Date(2026, 6, 15, 14, 30, 0, 0, time.UTC)
	want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	got := clock.CalendarDay(in, time.UTC)

	if !got.Equal(want) {
		t.Fatalf("CalendarDay(%v, UTC) = %v, want %v", in, got, want)
	}
}

// Case 2: cross-day zone conversion -- Bogota is UTC-5, no DST, ever (Colombia abolished DST
// in 1993). A moment that is 2026-06-16 in UTC is still 2026-06-15 in Bogota local time. This
// is the case that distinguishes CalendarDay from a naive t.UTC() truncation (which would
// incorrectly return June 16 here) -- proof that zone-aware bucketing, not UTC-only
// bucketing, is what the function does when given a non-UTC zone.
func TestCalendarDay_BogotaCrossDay(t *testing.T) {
	in := time.Date(2026, 6, 16, 3, 0, 0, 0, time.UTC)   // = 2026-06-15 22:00 in Bogota (UTC-5)
	want := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC) // the PREVIOUS day, not June 16

	got := clock.CalendarDay(in, clock.Zone())

	if !got.Equal(want) {
		t.Fatalf("CalendarDay(%v, Bogota) = %v, want %v", in, got, want)
	}
}

// Case 3: DST spring-forward transition, same local day either side (America/New_York,
// 2026-03-08, the US's second Sunday in March, 02:00->03:00 local, EST->EDT).
func TestCalendarDay_DSTSameDayEitherSide(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(America/New_York): %v", err)
	}

	beforeTransition := time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC) // = 01:59 EST
	afterTransition := time.Date(2026, 3, 8, 7, 1, 0, 0, time.UTC)   // = 03:01 EDT
	want := time.Date(2026, 3, 8, 0, 0, 0, 0, time.UTC)

	gotBefore := clock.CalendarDay(beforeTransition, ny)
	if !gotBefore.Equal(want) {
		t.Fatalf("CalendarDay(%v, NY) = %v, want %v", beforeTransition, gotBefore, want)
	}

	gotAfter := clock.CalendarDay(afterTransition, ny)
	if !gotAfter.Equal(want) {
		t.Fatalf("CalendarDay(%v, NY) = %v, want %v (same day -- the 2am->3am jump does not perturb the calendar day)", afterTransition, gotAfter, want)
	}
}

// Case 4: DST-adjacent cross-day case, proving zone-aware bucketing still holds right next to
// a transition (same zone/date as case 3, but the instant's UTC date and NY local date
// differ).
func TestCalendarDay_DSTCrossDay(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation(America/New_York): %v", err)
	}

	in := time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC)  // = 2026-03-07 23:30 EST (pre-transition)
	want := time.Date(2026, 3, 7, 0, 0, 0, 0, time.UTC) // the PREVIOUS day, not March 8

	got := clock.CalendarDay(in, ny)

	if !got.Equal(want) {
		t.Fatalf("CalendarDay(%v, NY) = %v, want %v", in, got, want)
	}
}
