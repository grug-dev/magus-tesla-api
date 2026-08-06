package telemetry

import (
	"testing"
	"time"
)

// The tests below exercise dateOnly and (*service).location() fully OFFLINE:
// pure functions/methods with no DB and no network dependency (design.md D2/D2a,
// tasks.md T8.1/T8.2 of telemetry-dedupe-daily-snapshots).

// TestDateOnly_ComfortablyInsideLocalDay covers tasks.md T8.1(a): a capture
// comfortably inside a calendar day in a non-UTC zone returns that same local
// calendar date.
func TestDateOnly_ComfortablyInsideLocalDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-15T20:00:00Z is 2026-01-15T15:00:00-05:00 in America/Bogota — the
	// same calendar day (Jan 15) in both UTC and local.
	capturedAt := time.Date(2026, 1, 15, 20, 0, 0, 0, time.UTC)

	got := dateOnly(capturedAt, loc)
	want := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("dateOnly(%v, %v) = %v, want %v", capturedAt, loc, got, want)
	}
}

// TestDateOnly_UTCDayAheadOfLocalDay covers tasks.md T8.1(b): a capture whose
// UTC instant is on one calendar day but whose local instant (in a
// negative-offset zone) is the PREVIOUS calendar day — asserting dateOnly
// follows the local day, not the UTC day. This is the exact scenario design
// D2's rejected UTC-expression alternative would get wrong.
func TestDateOnly_UTCDayAheadOfLocalDay(t *testing.T) {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	// 2026-01-02T02:00:00Z is 2026-01-01T21:00:00-05:00 in America/Bogota:
	// UTC day is Jan 2, but the local day is still Jan 1.
	capturedAt := time.Date(2026, 1, 2, 2, 0, 0, 0, time.UTC)

	got := dateOnly(capturedAt, loc)
	want := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("dateOnly(%v, %v) = %v, want %v (local day, not UTC day)", capturedAt, loc, got, want)
	}
}

// TestDateOnly_UTCLocationIsNoOp covers tasks.md T8.1(c): time.UTC as loc is a
// no-op, returning the same UTC calendar date as the input.
func TestDateOnly_UTCLocationIsNoOp(t *testing.T) {
	capturedAt := time.Date(2026, 3, 10, 5, 30, 0, 0, time.UTC)

	got := dateOnly(capturedAt, time.UTC)
	want := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("dateOnly(%v, time.UTC) = %v, want %v", capturedAt, got, want)
	}
}

// TestService_Location_FallsBackToTimeLocal covers tasks.md T8.2: Config{}
// (zero value, Location nil) returns time.Local.
func TestService_Location_FallsBackToTimeLocal(t *testing.T) {
	s := &service{cfg: Config{}}

	got := s.location()
	if got != time.Local {
		t.Errorf("location() = %v, want time.Local (fallback)", got)
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
