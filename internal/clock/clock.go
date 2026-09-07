// Package clock owns the platform's single default time zone (America/Bogota) and the
// primitives every other module uses to obtain the current time or resolve a zone name.
//
// This package imports stdlib "time" plus the blank import of stdlib "time/tzdata" — and
// NOTHING ELSE. tzdata is the single exception, named in advance by design.md D9 and taken
// when the Docker deploy landed; see platformZone below. It is never extended with an
// unrelated helper, no matter how small (design.md D2 of
// RM35-clock-add-bogota-time-package). A package called "clock" states what may live here;
// a "util" or "shared" dump does not (ai/architecture.md's named anti-pattern). Any future
// non-time helper belongs in its own concern-named package, not here.
package clock

import (
	"time"

	// Blank-imported for its SIDE EFFECT ONLY: it embeds a copy of the IANA time
	// zone database in the binary, so time.LoadLocation works on a host that has no
	// system tzdata. This is the one-line fix D9 sanctioned in advance, and the
	// containerized deploy it named has now landed: deploy/docker/Dockerfile's final
	// stages are alpine:3.20, which ships no tzdata, so platformZone's panic below
	// fired at init in every container and the migrate service exited before it
	// could run a single migration.
	//
	// This does NOT loosen the package rule above. The rule bars unrelated helpers
	// and third-party imports; this is stdlib, it is time-related, and D9 named it
	// explicitly as the sanctioned exception. Nothing else may be added here.
	_ "time/tzdata"
)

// platformZone is computed once, at package init, via time.LoadLocation. The tzdata it
// reads is embedded in the binary by the blank import above, so this works on a host with
// no system tzdata.
//
// D9 (settled by the owner 2026-08-30) originally left tzdata OUT, on the reasoning that the
// project shipped no containers and every deployment target carried a system tzdata. It also
// named the exit condition: "if a containerized deploy ever lands, adding `_ \"time/tzdata\"`
// above is a one-line fix, and the panic below guarantees the gap cannot be missed silently."
// Both halves came true. The Docker deploy landed on alpine:3.20 (no tzdata), the panic fired
// at init in every container, and the fix was the predicted one line.
//
// The panic stays. It is the reason the gap surfaced loudly instead of silently attributing
// every captured day to UTC.
var platformZone = func() *time.Location {
	loc, err := time.LoadLocation("America/Bogota")
	if err != nil {
		// Deliberate crash-on-boot (design.md D9): a silent UTC fallback here would defeat
		// the entire point of this roadmap without anyone noticing until day attribution had
		// already drifted. America/Bogota is a canonical, permanently-stable IANA zone name,
		// so on any host with a tzdata this branch is unreachable.
		panic("internal/clock: failed to load America/Bogota: " + err.Error())
	}
	return loc
}()

// Zone returns the platform's default time zone, America/Bogota. Every other part of the
// platform that needs a default zone obtains it through Zone rather than hard-coding the
// zone name itself (design.md D2/D9).
func Zone() *time.Location {
	return platformZone
}

// Now returns the current moment expressed in the platform's default zone. Callers that need
// "now" use this instead of calling time.Now() directly and converting the result themselves.
func Now() time.Time {
	return time.Now().In(Zone())
}

// LoadOrDefault resolves name to its *time.Location via time.LoadLocation, falling back to
// Zone() only when LoadLocation returns a non-nil error (design.md D10). It adds no
// special-casing beyond that.
//
// Documented gotcha, not a bug: time.LoadLocation itself special-cases two inputs before ever
// touching the tz database — "" and "UTC" both return UTC with no error, and "Local" returns
// the process's time.Local (host/environment-dependent) with no error. Because LoadOrDefault
// adds no logic beyond "did LoadLocation error", these inputs do NOT fall back to Zone():
// LoadOrDefault("") and LoadOrDefault("UTC") both return UTC, and LoadOrDefault("Local")
// returns whatever the host considers local — never America/Bogota. A caller that means
// "unset" should call Zone() directly rather than passing "" through LoadOrDefault.
func LoadOrDefault(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return Zone()
	}
	return loc
}
