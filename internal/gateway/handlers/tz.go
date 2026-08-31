package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cristianpena/magus-tesla-api/internal/clock"
)

// browserTZCookieName is the cookie read by browserLocation to derive the
// user's local calendar day. Set by a single inline <script> in
// layouts.BaseAuth (every authenticated page) — expires in 1 year, refreshed
// on each auth page load. Absent for direct API calls / noscript; the server
// falls back to the platform's default time zone (clock.Zone(), America/Bogota
// — RM35-gateway-adopt-clock, roadmap D1/D4).
const browserTZCookieName = "browser_tz"

// browserLocation reads the browser_tz cookie and returns the browser's local
// *time.Location. On any failure (missing cookie, malformed IANA name,
// time.LoadLocation error, noscript, direct API call) it returns clock.Zone()
// (the platform default, America/Bogota) — the caller never has to handle an
// error, and a signed-in user's own cookie still wins whenever it is present
// (RM35-gateway-adopt-clock, roadmap D1: this fallback default is the only
// thing that changed here — was time.UTC before this tier).
//
// The cookie carries an IANA timezone identifier (e.g. "America/Bogota",
// "Europe/London") set by `Intl.DateTimeFormat().resolvedOptions().timeZone`
// in the BaseAuth <script>; URL-decoded here. The allow-list is implicit in
// time.LoadLocation (only valid IANA names parse); arbitrary strings are
// rejected. A ReadHeaderTimeout-less response writer is fine — we only read
// an existing cookie, never block.
func browserLocation(c *gin.Context) *time.Location {
	if c == nil || c.Request == nil {
		return clock.Zone()
	}
	cookie, err := c.Cookie(browserTZCookieName)
	if err != nil || cookie == "" {
		return clock.Zone()
	}
	loc, err := time.LoadLocation(cookie)
	if err != nil {
		return clock.Zone()
	}
	return loc
}

// browserLocationFromHeader is the http.Header variant for tests that build a
// raw *http.Request without a gin.Context — reads the Cookie header directly.
// Same clock.Zone() fallback as browserLocation (RM35-gateway-adopt-clock).
func browserLocationFromHeader(r *http.Request) *time.Location {
	if r == nil {
		return clock.Zone()
	}
	cookie, err := r.Cookie(browserTZCookieName)
	if err != nil || cookie.Value == "" {
		return clock.Zone()
	}
	loc, err := time.LoadLocation(cookie.Value)
	if err != nil {
		return clock.Zone()
	}
	return loc
}

// startOfDayIn returns midnight (00:00:00) in the given location for the
// calendar day containing t in that location. Generalizes startOfDay (which
// always truncates to UTC midnight) so a caller with a browser timezone gets
// the browser's local start-of-day, not UTC's. The returned time carries loc
// as its Location, so subsequent .Format / .AddDate arithmetic stays in the
// browser's local frame.
//
// Deliberately NOT reimplemented on top of clock.CalendarDay (RM35-gateway-
// adopt-clock, roadmap D4 / design.md D-gw-3): CalendarDay always returns its
// result expressed at UTC midnight (the pgtype.Date storage encoding), while
// this function's whole contract is to return a time carrying loc itself as
// its Location — a different, incompatible representation this call site's
// callers (browserToday, and every .Format/.AddDate that follows it) rely on.
//
// Known limitation (accepted, not fixed — MAG-7 review finding R1-6):
// time.Date(Y,M,D,0,0,0,0,loc) is not guaranteed to name a real wall-clock
// instant when loc's DST transition lands at exactly 00:00 local — Go
// silently normalizes such an invalid time, which can roll the returned
// time's own .Day() back to the previous calendar day. Reproduced with
// historical America/Sao_Paulo and America/Havana dates that spring-forward
// at midnight. This is accepted rather than fixed: no zone in the current
// user base transitions at midnight (the vast majority transition at
// 01:00/02:00 local), the affected dates are historical, and a general fix
// (probing neighboring instants for a zone whose midnight is invalid) is
// disproportionate to a risk with no known current impact.
func startOfDayIn(t time.Time, loc *time.Location) time.Time {
	t = t.In(loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
}

// browserToday returns midnight in the browser's timezone for the request's
// "now" — the calendar day the user is currently living through, not UTC's.
// Used by parseHistoryRange (the default end and the end<=today cap) and by
// the dashboard default href / preset builders so "yesterday" means the
// user's yesterday, not UTC's. Falls back to the platform default zone
// (clock.Zone(), via browserLocation) when no cookie is present — was UTC
// before RM35-gateway-adopt-clock; the "now" instant itself is unchanged
// (out of this tier's scope — the roadmap's per-tier cell for gateway names
// only the browserLocation/browserLocationFromHeader fallbacks, history.go's
// startOfDay, and four handlers.go time.Now() call sites, not this one).
func browserToday(c *gin.Context) time.Time {
	return startOfDayIn(time.Now(), browserLocation(c))
}
