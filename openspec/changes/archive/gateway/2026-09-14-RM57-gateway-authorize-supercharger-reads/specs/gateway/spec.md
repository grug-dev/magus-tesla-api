## MODIFIED Requirements

### Requirement: Supercharger session battery edit is CSRF-protected and account-scoped

The gateway SHALL require a valid CSRF token, issued for this feature's own session key and
checked via the gateway's existing generic CSRF-check mechanism, on every state-changing request
against a Supercharger session's battery percentages (the `PATCH` save); a missing, stale, or
mismatched token SHALL be rejected with `HTTP 403` without calling `VerifySession`. The token used
for this feature SHALL be distinct from the manual-charge-log feature's own CSRF token, and SHALL
be issued only by the Supercharger Stats page/fragment routes, never re-issued by the row-level
edit, cancel, or save routes themselves.

**This is a CHANGE from the prior revision of this requirement.** The prior revision relied
solely on `VerifySession`'s own vehicle-scoped `WHERE` clause as the tenant boundary and
performed no separate ownership check before calling it. `VerifySession` no longer accepts a
bare vehicle identifier — it requires a proof value that only exists once ownership has already
been established. The gateway SHALL, after CSRF and body validation pass, read the vehicle the
user's session already has selected and prove that vehicle belongs to the signed-in account,
reusing the same account-vehicle-list read the page render already performs elsewhere on this
route, before calling `VerifySession`. A request with no vehicle selected in the session, or
whose selected vehicle does not belong to the signed-in account, SHALL be rejected with
`HTTP 404` without calling `VerifySession` — the same response the gateway already gives an
unresolvable vehicle, not a new status.

`VerifySession`'s own `WHERE` clause remains the boundary that ultimately matches or excludes a
session row. A session belonging to a DIFFERENT account's data SHALL NOT be reachable or
alterable through any account-scoped id. A session belonging to a DIFFERENT vehicle on the SAME
account SHALL NOT be reachable through this route unless that vehicle is the one currently
selected in the session — selecting the other vehicle first, then repeating the save, scopes the
write to that vehicle instead.

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

#### Scenario: A request with no vehicle selected in the session is rejected

- **GIVEN** a signed-in user's session that holds no selected vehicle — a stale session (the
  vehicle selection was cleared or never made), or a hand-crafted `PATCH` sent without first
  loading a page that selects one
- **WHEN** the gateway handles an otherwise well-formed, CSRF-valid `PATCH`
- **THEN** the response is `HTTP 404`
- **AND** `VerifySession` is never called

#### Scenario: The save is scoped to the vehicle currently selected in the session

- **GIVEN** a signed-in user whose account has more than one vehicle, with the SECOND vehicle
  currently selected in the session
- **WHEN** they save a battery-percentage correction
- **THEN** the gateway proves the selected vehicle belongs to their account and calls
  `VerifySession` scoped to that vehicle, never the account's first vehicle

#### Scenario: A session id belonging to a different account is never altered

- **GIVEN** a signed-in user submitting a well-formed, CSRF-valid `PATCH`, with a vehicle
  correctly selected in their own session, naming a session id that belongs to a different
  account's vehicle
- **WHEN** the gateway authorizes the user's own selected vehicle and calls `VerifySession`
- **THEN** the write matches zero rows (the vehicle-scoped `WHERE` clause excludes it)
- **AND** no data belonging to the other account is altered
