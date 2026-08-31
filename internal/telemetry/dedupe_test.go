package telemetry

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// The tests below exercise clock.CalendarDay (via its telemetry call site) and
// (*service).location() fully OFFLINE: pure functions/methods with no DB and no
// network dependency (design.md D2/D2a, tasks.md T8.1/T8.2 of
// telemetry-dedupe-daily-snapshots; repaired by RM35-telemetry-adopt-clock, which
// deleted the module's own dateOnly in favor of clock.CalendarDay — same formula,
// same expected values, per roadmap D6/D7).

// TestCalendarDay_ComfortablyInsideLocalDay covers tasks.md T8.1(a): a capture
// comfortably inside a calendar day in a non-UTC zone returns that same local
// calendar date.
func TestCalendarDay_ComfortablyInsideLocalDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-15T20:00:00Z is 2026-01-15T15:00:00-05:00 in America/Bogota — the
	// same calendar day (Jan 15) in both UTC and local.
	capturedAt := time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)

	got := clock.CalendarDay(capturedAt, loc)
	want := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("CalendarDay(%v, %v) = %v, want %v", capturedAt, loc, got, want)
	}
}

// TestCalendarDay_UTCDayAheadOfLocalDay covers tasks.md T8.1(b): a capture whose
// UTC instant is on one calendar day but whose local instant (in a
// negative-offset zone) is the PREVIOUS calendar day — asserting CalendarDay
// follows the local day, not the UTC day. This is the exact scenario design
// D2's rejected UTC-expression alternative would get wrong.
func TestCalendarDay_UTCDayAheadOfLocalDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-02T02:00:00Z is 2026-01-01T21:00:00-05:00 in America/Bogota:
	// UTC day is Jan 2, but the local day is still Jan 1.
	capturedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)

	got := clock.CalendarDay(capturedAt, loc)
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("CalendarDay(%v, %v) = %v, want %v (local day, not UTC day)", capturedAt, loc, got, want)
	}
}

// TestCalendarDay_UTCLocationIsNoOp covers tasks.md T8.1(c): time.UTC as loc is a
// no-op, returning the same UTC calendar date as the input.
func TestCalendarDay_UTCLocationIsNoOp(t *testing.T) {
	capturedAt := time.Date(2026, 3, 10, 5, 30, 0, 0, time.UTC)

	got := clock.CalendarDay(capturedAt, time.UTC)
	want := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("CalendarDay(%v, time.UTC) = %v, want %v", capturedAt, got, want)
	}
}

// TestService_Location_FallsBackToClockZone covers tasks.md T8.2, repaired by
// RM35-telemetry-adopt-clock (roadmap D4): Config{} (zero value, Location nil)
// now returns clock.Zone() (America/Bogota), not time.Local.
func TestService_Location_FallsBackToClockZone(t *testing.T) {
	s := &service{cfg: Config{}}

	got := s.location()
	if got != clock.Zone() {
		t.Errorf("location() = %v, want clock.Zone() (fallback)", got)
	}
}

// TestService_Location_UsesConfiguredLocation covers tasks.md T8.2:
// Config{Location: someLoc} returns someLoc unchanged.
func TestService_Location_UsesConfiguredLocation(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	s := &service{cfg: Config{Location: loc}}

	got := s.location()
	if got != loc {
		t.Errorf("location() = %v, want configured %v", got, loc)
	}
}
