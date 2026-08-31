package config

import (
	"testing"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// TestPollerTimezoneOrDefault_Unset covers the one POLLER_TIMEZONE case that had
// zero existing test coverage before RM35-config-adopt-clock: an unset value must
// resolve to the platform's default zone (internal/clock's "America/Bogota"), not
// silently fall through to UTC or the host's "Local" zone. This is the single
// narrow exception RM35 D6 permits for tiers 2-6 (which otherwise add no new
// tests) — the default is what stands between the platform and a silent UTC
// poller (tier 1 design D10's documented time.LoadLocation("") gotcha).
func TestPollerTimezoneOrDefault_Unset(t *testing.T) {
	got := pollerTimezoneOrDefault("")
	want := clock.Zone().String()
	if got != want {
		t.Fatalf("pollerTimezoneOrDefault(\"\") = %q, want %q", got, want)
	}
	if got != "America/Bogota" {
		t.Fatalf("pollerTimezoneOrDefault(\"\") = %q, want the platform default America/Bogota", got)
	}
}
