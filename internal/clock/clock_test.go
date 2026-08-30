package clock_test

import (
	"testing"
	"time"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// TestZone transcribes design.md's Test Contract for Zone() verbatim
// (RM35-clock-add-bogota-time-package).
func TestZone(t *testing.T) {
	if clock.Zone().String() != "America/Bogota" {
		t.Fatalf("Zone() = %s, want America/Bogota", clock.Zone())
	}
}

// TestNow transcribes design.md's Test Contract for Now() verbatim. The instant Now()
// returns must match the real current time regardless of which zone it is expressed in —
// comparing time.Time values compares the underlying instant, not the zone, so this
// assertion is zone-agnostic by construction. The 2-second slack window absorbs test
// execution time, not clock drift.
func TestNow(t *testing.T) {
	before := time.Now()
	got := clock.Now()
	after := time.Now()

	if got.Location().String() != "America/Bogota" {
		t.Fatalf("Now().Location() = %s, want America/Bogota", got.Location())
	}
	if got.Before(before.Add(-2*time.Second)) || got.After(after.Add(2*time.Second)) {
		t.Fatalf("Now() = %v, want within [%v, %v] (allowing test execution slack)", got, before, after)
	}
}

// TestLoadOrDefault transcribes design.md's Test Contract table for LoadOrDefault verbatim,
// including the stdlib-special-case gotchas that do NOT fall back to Zone() (design.md D10).
func TestLoadOrDefault(t *testing.T) {
	cases := []struct {
		name string
		want string // .String() of the returned *time.Location
	}{
		{"America/New_York", "America/New_York"}, // valid IANA name -> itself
		{"Not/AZone", "America/Bogota"},          // unresolvable -> falls back to Zone()
		{"", "UTC"},                              // stdlib special case -- NOT Bogota (D10)
		{"UTC", "UTC"},                           // stdlib special case
		{"Local", "Local"},                       // stdlib special case -- host-dependent, not Bogota
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := clock.LoadOrDefault(c.name)
			if got.String() != c.want {
				t.Fatalf("LoadOrDefault(%q) = %s, want %s", c.name, got, c.want)
			}
		})
	}
}
