// Package clock owns the platform's single default time zone (America/Bogota) and the
// primitives every other module uses to obtain the current time or resolve a zone name.
//
// This package imports stdlib "time" and NOTHING ELSE — with no exception, ever. It is
// never extended with an unrelated helper, no matter how small (design.md D2 of
// RM35-clock-add-bogota-time-package). A package called "clock" states what may live here;
// a "util" or "shared" dump does not (ai/architecture.md's named anti-pattern). Any future
// non-time helper belongs in its own concern-named package, not here.
package clock

import "time"

// platformZone is computed once, at package init, via time.LoadLocation. It deliberately
// does NOT blank-import "time/tzdata" (design.md D9 — settled by the owner 2026-08-30): this
// project ships no containers, every deployment target already carries a system tzdata, and
// keeping the stdlib-time-only import rule absolute (no standing exception) is worth more
// than the insurance a bundled tzdata would buy today. If a containerized deploy ever lands,
// adding `_ "time/tzdata"` above is a one-line fix, and the panic below guarantees the gap
// cannot be missed silently.
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
