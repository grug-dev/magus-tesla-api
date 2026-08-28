## ADDED Requirements

### Requirement: Supercharger session battery percentages are correctable inline

The gateway SHALL let a signed-in user, on `/supercharger-stats`, correct the `start_battery_pct`
and `end_battery_pct` of one of their own account's Supercharger sessions inline, by swapping
that session's table row for an editable row and saving through the existing
`charging.SessionVerifier.VerifySession` port. The gateway SHALL NOT add a delete action for a
Supercharger session, and SHALL NOT add a new `charging` read or write port for this feature —
the write goes through `SessionVerifier` exactly as it already exists, and any read the gateway
needs to resolve one session by id SHALL be performed by listing the account/vehicle-scoped,
`?start=&end=`-windowed session set and matching the id in memory, never by adding a by-id method
to `charging.SessionReader`.

The edit row SHALL render the session's date, site, energy, cost, and both battery percentage
ESTIMATE fields as read-only display text; ONLY the two verified battery percentage fields SHALL
be editable inputs. The gateway SHALL NOT write `battery_pct_source`, `start_battery_pct_est`, or
`end_battery_pct_est` from any value it receives from this row — `battery_pct_source` is always
computed by `VerifySession` itself, and the two estimate columns are not reachable through this
write path at all.

#### Scenario: A user edits and saves a session's battery percentages

- **GIVEN** a signed-in user viewing `/supercharger-stats` with a session row showing start
  battery 40% and end battery 80%
- **WHEN** they click Edit on that row, change the values to 42% and 82%, and click Save
- **THEN** the gateway calls `charging.SessionVerifier.VerifySession` with the new values for
  that session's id, scoped to the caller's account
- **AND** on success the row swaps back to its static display showing 42% and 82%
- **AND** no other row, tile, or chart in the page changes

#### Scenario: No delete action exists for a Supercharger session

- **GIVEN** a signed-in user viewing a Supercharger session row, in either its static or edit
  state
- **WHEN** they inspect the available row controls
- **THEN** no delete control, delete route, or delete confirmation exists for a Supercharger
  session anywhere on the page

#### Scenario: Editing a session's battery percentages does not change the KPI tiles or the chart

- **GIVEN** a signed-in user who successfully edits and saves a session's battery percentages
- **WHEN** the row swaps back to its static display
- **THEN** the page's KPI tiles (session count, energy, cost, average kWh/session) and the
  kWh-per-month chart are NOT refreshed or altered by the edit — neither value is derived from a
  battery percentage

### Requirement: Supercharger session battery edit uses a strict PATCH body — an absent field is rejected, an empty field clears it

The gateway SHALL accept the save as an `HTTP PATCH` to a per-session route. The request body
SHALL be REQUIRED to contain BOTH the `start_battery_pct` and `end_battery_pct` form fields as
present keys — a request missing either key SHALL be rejected with `HTTP 400` and SHALL NOT
result in any call to `VerifySession`. A present key whose value is empty SHALL be treated as an
explicit instruction to clear that percentage (passed as `nil`/NULL to `VerifySession`), NOT as
an error and NOT as "leave the existing value unchanged."

When BOTH percentages are cleared in the same request, the gateway SHALL rely on
`VerifySession`'s own behavior of also clearing `battery_pct_source` to NULL in the same write —
the gateway SHALL NOT attempt to set or preserve a source value itself.

A present, non-empty value for either field SHALL be validated as an integer in `[0, 100]`
BEFORE any call to `VerifySession`; a value outside that range or not a valid integer SHALL be
rejected with `HTTP 422`, a field-specific error message attached to the offending field, and
the submitted (unmodified) values redisplayed in the re-rendered edit row — without calling
`VerifySession`. The gateway SHALL NOT reject a request where the new end percentage is less
than the new start percentage — no relative ordering is enforced.

#### Scenario: A request missing one of the two required keys is rejected

- **GIVEN** a signed-in user's browser issuing `PATCH` to a session's row route with a body that
  contains `start_battery_pct` but no `end_battery_pct` key at all
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 400`
- **AND** `VerifySession` is never called
- **AND** no session data is altered

#### Scenario: Both percentages present but empty clears both the percentages and the source

- **GIVEN** a signed-in user's edit row submitting `start_battery_pct=` and `end_battery_pct=`
  (both keys present, both values empty)
- **WHEN** the gateway handles the `PATCH`
- **THEN** `VerifySession` is called with both percentages `nil`
- **AND** on success the row's static display shows both battery cells as empty ("—")
- **AND** the underlying session's `battery_pct_source` is cleared to NULL as a consequence of
  `VerifySession`'s own behavior, not a separate gateway write

#### Scenario: An out-of-range value is rejected per-field without writing

- **GIVEN** a signed-in user's edit row submitting `start_battery_pct=101` and a valid
  `end_battery_pct`
- **WHEN** the gateway handles the `PATCH`
- **THEN** the response is `HTTP 422` and the edit row is re-rendered with a field-specific error
  on the start battery percentage input
- **AND** `VerifySession` is never called
- **AND** the submitted value `101` remains visible in the re-rendered input, not silently reset

#### Scenario: A lower end percentage than the start percentage is accepted

- **GIVEN** a signed-in user's edit row submitting a `start_battery_pct` of 80 and an
  `end_battery_pct` of 40
- **WHEN** the gateway handles the `PATCH`
- **THEN** `VerifySession` is called with both values exactly as submitted
- **AND** the request is not rejected for the values being in a decreasing order

### Requirement: A successful battery-percentage save immediately recalculates the affected vehicle's derived metrics

The gateway SHALL, immediately after a Supercharger session's battery percentages are
successfully verified, call `analytics.Recalculator.Recalculate` for the session's vehicle,
bounded to a window of exactly one calendar day before through one calendar day after the UTC
calendar day of the session's `ChargeStopDateTime` (inclusive on both ends) — a
THREE-calendar-day window total — rather than the session's own calendar day alone. The gateway
SHALL derive this window
using the session's `ChargeStopDateTime`, not its `ChargeStartDateTime`, and SHALL compute the
UTC calendar day without consulting any per-request or per-browser timezone. A `Recalculate`
error SHALL be logged and SHALL NOT be surfaced to the user or fail the request — the save has
already committed successfully regardless of the recalculation outcome.

When the verified session's vehicle identifier is absent (its VIN does not currently match a
registered vehicle on the account), the gateway SHALL skip the recalculation call entirely,
SHALL log that it did so, and SHALL still return the successful save response to the user.

This recalculation call SHALL be performed by logic separate from the existing manual-charge
write path's own single-day recalculation helper; the gateway SHALL NOT widen or otherwise alter
the manual-charge path's existing single-day recalculation window to accommodate this
requirement.

#### Scenario: A session stopping just after UTC midnight still recalculates its true metric day

- **GIVEN** a Supercharger session whose `ChargeStopDateTime` is 20 minutes after UTC midnight
  on a given calendar day
- **WHEN** its battery percentages are successfully verified
- **THEN** `analytics.Recalculator.Recalculate` is called for that vehicle with a window spanning
  from one calendar day before that UTC date through one calendar day after it
- **AND** the window is derived solely from `ChargeStopDateTime`, ignoring any different calendar
  day carried by the same session's `ChargeStartDateTime`

#### Scenario: A verified session with no resolvable vehicle skips recalculation but still saves

- **GIVEN** a Supercharger session whose VIN does not match any of the account's currently
  registered vehicles
- **WHEN** its battery percentages are successfully verified
- **THEN** the save succeeds and the row's static display reflects the new values
- **AND** `analytics.Recalculator.Recalculate` is never called
- **AND** the gateway logs that recalculation was skipped

#### Scenario: A recalculation failure does not undo or fail the save

- **GIVEN** a Supercharger session whose battery percentages are successfully verified and whose
  vehicle IS resolvable
- **WHEN** the subsequent `analytics.Recalculator.Recalculate` call returns an error
- **THEN** the save response the user sees is still successful, showing the newly verified values
- **AND** the recalculation error is logged, not surfaced to the user

#### Scenario: The manual-charge recalculation window is unaffected

- **GIVEN** the existing manual charge log write path (`ChargeCreate`, `ChargeRowUpdate`,
  `ChargeRowDelete`)
- **WHEN** any of those handlers triggers its existing post-write recalculation
- **THEN** it continues to recalculate only the single `ChargedOn` calendar day (and, for an
  update that changed the date, the single prior `ChargedOn` day) exactly as before this change
- **AND** it does not use a `±1 day` window

### Requirement: Supercharger session battery edit is CSRF-protected and account-scoped

The gateway SHALL require a valid CSRF token, issued for this feature's own session key and
checked via the gateway's existing generic CSRF-check mechanism, on every state-changing request
against a Supercharger session's battery percentages (the `PATCH` save); a missing, stale, or
mismatched token SHALL be rejected with `HTTP 403` without calling `VerifySession`. The token used
for this feature SHALL be distinct from the manual-charge-log feature's own CSRF token, and SHALL
be issued only by the Supercharger Stats page/fragment routes, never re-issued by the row-level
edit, cancel, or save routes themselves.

The gateway SHALL rely on `VerifySession`'s own account-scoped `WHERE` clause as the sole tenant
boundary for the save — it SHALL NOT perform an additional, separate vehicle-ownership check
before calling `VerifySession`, unlike the manual-charge-log write path's explicit
`RegisteredVehicles` check. A session belonging to a different vehicle on the SAME account remains
writable through this route; a session belonging to a DIFFERENT account's data SHALL NOT be
reachable or alterable through any account-scoped id.

#### Scenario: A save with no CSRF token ever issued for this session is rejected

- **GIVEN** a signed-in user's session that has never loaded `/supercharger-stats` or
  `/ui/supercharger-stats` (so no Supercharger-specific CSRF token has ever been issued)
- **WHEN** a `PATCH` is sent to a session's row route with an otherwise well-formed body
- **THEN** the response is `HTTP 403`
- **AND** `VerifySession` is never called

#### Scenario: A stale or mismatched CSRF token is rejected

- **GIVEN** a signed-in user whose session holds a Supercharger CSRF token that does not match
  the token submitted with the `PATCH` request
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 403`
- **AND** `VerifySession` is never called

#### Scenario: A session id belonging to a different account is never altered

- **GIVEN** a signed-in user submitting a well-formed, CSRF-valid `PATCH` naming a session id
  that belongs to a different account
- **WHEN** the gateway calls `VerifySession`
- **THEN** the write matches zero rows (the account-scoped `WHERE` clause excludes it)
- **AND** no data belonging to the other account is altered

### Requirement: A Supercharger session row not in the currently requested window is treated as not found

The gateway SHALL resolve a Supercharger session's edit, cancel-to-static, and save actions by
re-listing that vehicle's sessions within the SAME `?start=&end=` window the row's action link
carries (matching the window the table was rendered under), then matching the requested session
id within that result set — never by issuing an unbounded or by-id lookup. A session id that does
not fall within the requested window SHALL be treated identically to a session id that does not
exist at all: `HTTP 404` on a `GET` (edit or cancel-to-static), and the same not-found outcome on
a `PATCH` whose underlying write also cannot be resolved.

#### Scenario: Requesting the edit form for a session id outside the given window returns not found

- **GIVEN** a hand-built `GET` request to a session's edit-row route carrying a `?start=&end=`
  window that does not include the named session id
- **WHEN** the gateway handles the request
- **THEN** the response is `HTTP 404`
- **AND** no edit form is rendered

#### Scenario: Every row's own action links carry the table's rendered window

- **GIVEN** a signed-in user viewing `/supercharger-stats` for a given `?start=&end=` window
- **WHEN** the page renders each session row's Edit, Cancel, and Save action URLs
- **THEN** each of those URLs carries the SAME `start` and `end` values the table itself was
  rendered with
