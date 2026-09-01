# Platform time zone — maintenance guide

> The map for changing this concept without re-scanning the codebase. Paths + symbols only;
> for current signatures/callers/callees, ask CodeGraph. Pin to file paths, never line numbers.
> All KB links are relative to `kkpa/context/`.

## Glossary

- **Known as:** `platform time zone`, `default time zone`, `America/Bogota`, `clock`, `calendar day`
- **Internal name:** `clock` capability — `internal/clock` (`Zone`, `Now`, `LoadOrDefault`, `CalendarDay`)

## Component map

> Not mapped yet. This guide was created by a spec sync (`apply-sync`), and a capability
> `spec.md` carries behavior, not file paths. Run `/kkpa-context-curate platform-time-zone` to discover
> and record the component map.

## How maintenance works

The capability guarantees four behaviors. Change any of them here, never by hand-rolling an
equivalent in a consuming module.

1. **A single default zone.** The platform defines exactly one default time zone,
   `America/Bogota`, used wherever a zone is needed and none was configured or supplied. One
   capability owns it; everything else obtains it from there rather than naming the zone itself.
2. **Current time in that zone.** "Now" is available already expressed in the default zone, so no
   consumer calls the system clock and converts the result itself.
3. **Zone-name resolution with fallback.** An IANA name resolves to its zone; an unresolvable name
   falls back to the default zone **without surfacing an error** to the caller.
4. **Calendar-day normalization.** Any moment normalizes to the calendar day it falls on *in a
   caller-supplied zone*, expressed in the platform's calendar-day storage representation (UTC
   midnight, which this capability leaves unchanged). The day comes from the moment's wall-clock
   date in that zone — not its UTC date — so a moment near a boundary is attributed to the correct
   local day.

## Conventions & gotchas

- **Never hard-code a zone name, and never hand-roll a UTC-midnight truncation.** Obtain the
  default zone, "now", and a calendar day from the `clock` capability. It is the single owner of
  the default, which is the whole point of the capability.
  _Source: spec clock — Requirement: Platform Default Time Zone._
- **An unresolvable zone name falls back silently to the default — no error reaches the caller.**
  Callers that must distinguish "bad input" from "used the default" cannot do it through this
  path and need their own validation first.
  _Source: spec clock — Requirement: Time Zone Name Resolution With Fallback._
- **The zone for a calendar day is the caller's, not the capability's.** Normalization takes the
  zone as an explicit input; passing the wrong one silently attributes a moment to the wrong day.
  Day attribution is what the poller, analytics, and charging all bucket on, so an error here is
  invisible until the numbers are already wrong.
  _Source: spec clock — Requirement: Calendar Day Normalization._
- **UTC is still the storage representation for a calendar day, and that is deliberate.** The
  capability expresses a normalized day at UTC midnight and explicitly does not change that
  encoding — it is what the DB layer expects. "Do not use UTC" applies to the *default zone*, never
  to this representation.
  _Source: spec clock — Requirement: Calendar Day Normalization._
- **A daylight-saving transition must not move a moment's calendar day.** Two moments either side
  of a DST change on the same local date normalize to the same day.
  _Source: spec clock — Requirement: Calendar Day Normalization._

## Related KB

All KB links are relative to `kkpa/context/`, never to this file.
