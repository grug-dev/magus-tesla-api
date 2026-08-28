# Design — RM31-gateway-add-session-battery-edit

## Context

`/supercharger-stats` (tier 4) already renders all four `charging.Session` battery fields
read-only. Tier 1 already shipped `charging.SessionVerifier.VerifySession` — an account-scoped
write over exactly `start_battery_pct`, `end_battery_pct`, `battery_pct_source` (plus
`updated_at`) — but nothing in the gateway calls it. Tier 2 gave `charging` the read shapes
`analytics` needs, and tier 3 switched `analytics`'s Supercharger source to `charge_sessions`, so
a `VerifySession` write is now visible to `vehicle_metrics` through the existing
`analytics.Recalculator.Reconcile` nightly pass — but the ticket wants the correction to show up
immediately, the same way a manual charge entry does today (`recalculateAfterChargeWrite`).

This tier wires the missing UI: an inline per-row edit affordance on the sessions table, a PATCH
route that calls `VerifySession`, and an immediate, bounded `Recalculate` call.

Every open product/design question for this tier was interviewed and settled by the owner in
this session BEFORE this document was written (see the dispatch's "BINDING interview outcomes,"
labeled D-I1 through D-I8). This design.md restates each as a binding decision (D1-D8, same
order, cross-referenced back to its D-I label) and adds the additional decisions (D9-D11) needed
to fully specify the slice: writer-error branching, the read-only-boundary amendment, and the
row-mapper refactor.

## Goals / Non-Goals

**Goals**

- Let a user correct a session's `start_battery_pct`/`end_battery_pct` inline on
  `/supercharger-stats`, through the existing `charging.SessionVerifier` port only.
- Trigger `analytics.Recalculator.Recalculate` immediately after a successful write, over a
  window proven to cover the session's true metric day regardless of poller-local timing.
- Keep the change entirely inside `internal/gateway` (plus the KB docs) — no new `charging` port,
  no database object, no `cmd/` code from this worker.

**Non-Goals**

- No delete action, no ordering validation, no `_est`-column write, no `battery_pct_source`
  gateway-side write, no new `charging.SessionReader`/`SessionVerifier` method, no database
  object, no change to `recalculateAfterChargeWrite`.

## Decisions

### D1 — Read-back via list-and-match; no new `charging` read port (= D-I1, binding — owner)

The edit-row fragment (`SuperchargerRowEditFragment`) and the cancel-to-static fragment
(`SuperchargerRowStatic`) each resolve their target session the same way:
`resolveSelectedVehicle(ctx, c, uid)` → `parseSuperchargerRange(c, today)` on the request's own
`?start=&end=` query params → `h.superchargerReader.ListSessionsByVehicleBetween(ctx, uid,
teslaID, start, end)` → linear match on `s.ID == id`. This is `fetchSuperchargerRowVM`, mirroring
`fetchEntryVM`'s documented "no `GetEntry` on the port" shape in `charges.go` exactly — down to
the same design rationale: `charging.SessionReader` exposes only the bounded-window read, and
adding a by-ID method is `charging` code that is explicitly out of scope for a `gateway` tier
(roadmap Decision 9 already drew this exact module line for tier 2; re-opening it here would
spawn a sixth tier for one lookup method).

`fetchSuperchargerRowVM` collapses every failure mode to a single `(vm, false)` return, exactly
as `fetchEntryVM` does: a malformed `start`/`end`, no resolvable vehicle, a reader error, and "no
session in the window matches `id`" are NOT distinguished from each other. The dominant real case
of the last one is the documented inherited failure mode: **an `id` outside the request's
`[start, end]` window renders as not-found.** This is reachable only via a hand-built request —
every Edit/Cancel/Save link the page emits is generated FROM a row that is, by construction,
already inside the currently-rendered window (see D2, which threads that exact window onto every
row's action URLs).

### D2 — Row action URLs carry the SAME `?start=&end=` the table was rendered under (new — required by D1)

`SuperchargerStatsView` gains `WindowStartStr`/`WindowEndStr` (pre-formatted `YYYY-MM-DD`,
computed once in `buildSuperchargerStatsView` from the already-resolved `start`/`end`, mirroring
how `RangePreset.StartStr`/`EndStr` are already pre-formatted for the month-preset selector). Every
row's Edit (`GET .../row/:id/edit?start=..&end=..`), Cancel (`GET .../row/:id?start=..&end=..`),
and Save (`PATCH .../row/:id?start=..&end=..`, on the `<form>`'s own action URL) carries these two
values as a query string, plain string concatenation exactly like `buildSuperchargerPresets`'s existing
`fmt.Sprintf("/ui/supercharger-stats?start=%s&end=%s", ...)` call. This is what makes D1's
list-and-match resolve deterministic: without it, a GET fired after the default window rolled
over (e.g. a stale tab open past a UTC day boundary) could silently fail to find a session that
was visible when the row was rendered. No new date-parsing code is introduced — `parseSuperchargerRange`
already reads `c.Query("start")`/`c.Query("end")` for any HTTP method, PATCH included.

### D3 — Verb is `PATCH`; STRICT body semantics — absent key is 400, empty value clears (= D-I2, binding — owner)

`PATCH /ui/supercharger-stats/row/:id` is this gateway's first `PATCH` (`charges.go`'s sibling
uses `PUT`). This divergence is deliberate, not an inconsistency to "fix": at the resource level
the write genuinely IS partial — `VerifySession`'s own SET clause structurally reaches only 3 of
`charge_sessions`' ~18 columns, so `PATCH`'s "partial representation" semantics are the accurate
HTTP verb, unlike `ChargeRowUpdate`'s `PUT`, which replaces the full `Entry` the form submits.

The body contract is STRICT, and this is the load-bearing rule: **both** `start_battery_pct` and
`end_battery_pct` keys MUST be present, read via gin's `c.GetPostForm(key) (string, bool)` (NOT
`c.PostForm`, which cannot distinguish "absent" from "present-but-empty").

- **Either key absent** → `HTTP 400`, bare text (`KeySuperchargerErrorMalformedBody`), no write
  attempted, no fragment rendered (there is nothing valid to re-render).
- **A key present with an empty (or whitespace-only) value** → that percentage is `nil` — an
  explicit clear, not an error.
- **A key present with a non-empty value** → parsed as an integer, range-validated (D6).

Rationale: `VerifySession`'s own doc comment states it is full-replace over the fields it takes —
"a caller wanting to add `endBatteryPct` to a session that already has `startBatteryPct` verified
must re-supply the existing `startBatteryPct` value ... or that column is overwritten to NULL."
`PATCH` invites a client to send a partial body; the `400` on a missing key closes that gap with
an explicit, loud rejection instead of a silent unintended NULL. The gateway's OWN form always
submits both inputs (`ui.Input type="number"` fields, present in the DOM even when the user
clears them to blank), so this branch never fires from the shipped UI — it exists for any other
caller of this route.

### D4 — Recalculation window is `±1 day` around the metric day, via a NEW helper (= D-I3, binding — owner)

`recalculateAfterSessionVerify(ctx, uid, teslaID, chargeStopDateTime time.Time)` is a SEPARATE
function from `recalculateAfterChargeWrite` — it is NOT called by this tier, and
`recalculateAfterChargeWrite`'s existing single-day window is NOT widened. The manual-charge path
passes a user-picked `ChargedOn`, and `recalculateAfterChargeWrite`'s single-day semantics are
correct for that input; changing it to accommodate this tier would alter tier-unrelated behavior
for a different write path with a different day source.

```go
func (h *Handler) recalculateAfterSessionVerify(ctx context.Context, uid uuid.UUID, teslaID int64, chargeStopDateTime time.Time) {
	day := startOfDay(chargeStopDateTime.UTC()) // D5 — plain UTC, ChargeStopDateTime
	from := day.AddDate(0, 0, -1)
	to := day.AddDate(0, 0, 1)
	if err := h.analyticsRecalculator.Recalculate(ctx, uid, teslaID, from, to); err != nil {
		log.Printf("gateway: analytics recalculate error for account %s, vehicle %d, session window %s..%s: %v",
			uid, teslaID, from.Format("2006-01-02"), to.Format("2006-01-02"), err)
	}
}
```

**Why a single day is provably wrong, and why ±1 is provably sufficient** (this is the
load-bearing rationale, restated in full because it is the reason this decision could not be a
one-line reuse of the manual-charge helper):

1. A session is attributed to one `vehicle_metrics` row by `sumSuperchargerPctBetween`
   (`internal/analytics/consumed.go:116`), which buckets each session into the metric row whose
   `[prev.CapturedAt, cur.CapturedAt)` window contains the session.
2. That metric row is dated `effectiveDay(cur) = CapturedDate − 1 day`
   (`internal/analytics/consumed.go:84`), because the nightly poller runs at ~03:30 and its
   snapshot "describes the PRIOR day."
3. Consequence: a session that stops between local midnight and ~03:30 poller-local belongs to
   the metric row for the **PREVIOUS** calendar day relative to its own `ChargeStopDateTime`'s
   calendar day — a one-day offset that a naive single-day `Recalculate(day, day)` call would
   miss entirely.
4. Precedent for widening rather than trying to compute the offset exactly: `analytics.Reconcile`
   already derives its own affected range "coarse, widened +/-1 day" (`analytics.go`, D8) rather
   than computing an exact per-source day — the same imprecise-but-safe pattern this decision
   reuses.
5. `Recalculate` is documented idempotent (`analytics.go`: "re-running over an unchanged window
   produces a byte-identical UPSERT"), so widening the window costs nothing beyond one accepted
   consequence (D5 below) — there is no correctness risk from calling it on days that turn out
   not to need it.

### D5 — The day is computed from `ChargeStopDateTime`, in UTC (= D-I4, binding — owner)

`ChargeStopDateTime`, not `ChargeStartDateTime`, because `sumSuperchargerPctBetween` filters
sessions on `ChargeStopDateTime` (`consumed.go:116`) — using the start time would derive the
window from a timestamp the aggregation itself does not key on. Plain UTC (`.UTC()`), not
`browserToday(c)`'s cookie-derived zone, for two reasons: (a) this page already commits to plain
UTC for its own date math — `supercharger.go:120`'s `startOfDay(time.Now().UTC())`, documented
"design.md D9a — plain UTC, NOT `browserToday(c)`" — and this write path should not introduce a
second timezone convention onto the same page; (b) the gateway has no access to the poller's
configured `Config.Location` (`internal/telemetry`'s own zone, used to compute `effectiveDay`),
so there is no more-correct zone available to reach for even if UTC were not already the
page's convention.

**Safety argument for why UTC + `±1 day` still covers the true metric day in every case**, even
though the poller's `effectiveDay` computation runs in ITS OWN configured local zone, not UTC:

- The metric day sits 0 or −1 calendar days from the poller-LOCAL calendar day of
  `ChargeStopDateTime` (proven in D4).
- Any local calendar day sits within ±1 of the UTC calendar day of the SAME instant — a timezone
  offset can shift a wall-clock date by at most one day in either direction.
- These two ±1 shifts cannot compound to ±2: the poller-local −1-day shift only happens for a
  stop time BEFORE ~03:30 poller-local (D4, point 3). A UTC-vs-poller-local zone shift that ALSO
  moves the calendar date backward requires the instant to fall late in the UTC day (a
  negative-offset zone, e.g. UTC−5, rolls its LOCAL date back only when the UTC clock is already
  past local midnight, i.e. UTC evening/night) — which is never simultaneously true of an instant
  that is also before 03:30 poller-local. The two conditions that would each contribute a −1 are
  mutually exclusive on the same instant, so the two shifts cannot stack.
- Therefore: UTC-day(`ChargeStopDateTime`) `− 1`, UTC-day(`ChargeStopDateTime`), and UTC-day(`ChargeStopDateTime`) `+ 1`
  — the exact `[day−1, day+1]` window `recalculateAfterSessionVerify` calls — always contains the
  true metric day.

**Accepted consequence:** `Recalculate`'s own self-healing DELETE (of any `vehicle_metrics` row in
the call's window that the fresh computation did not reproduce) now spans 3 days per verification
write instead of 1. This is intentional and safe per D4 point 5 (idempotence) — it costs one
extra UPSERT/DELETE pass over two additional days that were very likely already correct, not a
correctness risk.

### D6 — `TeslaID == nil` skips recalculation and logs; the write still succeeds (= D-I5, binding — owner)

`charging.Session.TeslaID` is `*int64`. A read through `SessionReader`/`SuperchargerSessionAnalyticsReader`
can never surface a nil-`TeslaID` row (RM29 D6, RM30 D6 — `SQL NULL = value` is never true, so an
orphaned session is definitionally excluded from every vehicle-scoped read), which is why D1's
list-and-match resolve never needs to handle it. But `VerifySession` is scoped only
`WHERE id = @id AND account_id = @account_id` — **no `TeslaID`/vehicle predicate at all** — so a
hand-built `PATCH` naming a session whose VIN is not (or no longer) a registered vehicle can
still return a `Session` with `TeslaID == nil`.

`SuperchargerRowUpdate`, after a successful `VerifySession` call, branches on the returned
`updated.TeslaID`:

```go
if updated.TeslaID != nil {
	h.recalculateAfterSessionVerify(c.Request.Context(), uid, *updated.TeslaID, updated.ChargeStopDateTime)
} else {
	log.Printf("gateway: supercharger session %s verified with nil TeslaID for account %s — skipping recalculation", id, uid)
}
```

The write itself is NOT rejected, and no pre-write read is added to reject it: account-scoping
(`WHERE ... account_id`) is this port's security boundary and it holds regardless of `TeslaID`;
a cross-vehicle write within one account is not a tenancy escalation (the account owns both
sessions), so there is nothing to gate on a vehicle check that isn't already covered by the
account check `VerifySession` performs. The row still swaps back to its static, now-corrected
display either way — only the immediate recalculation is skipped, and the nightly `Reconcile`
pass would still (redundantly, harmlessly) pick up any TeslaID that later resolves.

### D7 — Blanks clear; NO ordering validation (= D-I6, binding — owner)

**(a) Clearing.** Submitting both keys with empty values calls `VerifySession(ctx, accountID, id,
nil, nil)`, which clears both percentages AND `battery_pct_source` to `NULL` in one statement
(tier 1 design.md D7) — honoring the `charge_sessions_pct_source_required` CHECK (source is
`NOT NULL` iff at least one percentage is non-null). This is intentional, not a missing
confirmation: an irreversible verification UI (no way to "un-verify" a wrong correction) is a
trap for the user.

**(b) No ordering check.** The gateway does NOT reject `end_battery_pct < start_battery_pct`.
Tier 1 design.md D8 deliberately declined to enforce ordering at the port level, matching
`charge_sessions`' own deliberate absence of such a CHECK constraint — the gateway does not
re-introduce a stricter rule than the port and the schema both chose not to have.

**(c) Range + type validation IS enforced, gateway-side, first.** Each present, non-empty value
is validated to `[0, 100]` inclusive (integer only) in the handler BEFORE calling `VerifySession`,
so an out-of-range or non-integer submission gets a field-level `422` with a specific message
(D9) rather than falling through to `VerifySession`'s own range check and surfacing as a generic
`500` from a wrapped port error. `VerifySession`'s own `[0,100]` validation remains the backstop,
per tier 1's own documented rationale ("the DB's own SMALLINT CHECK is the backstop, not the
error message" — the same layering principle applied one level up).

### D8 — New CSRF key; page issues, fragments read; new read-only-boundary amendment (= D-I7, binding — owner)

`csrfSuperchargerKey = "csrf_supercharger"` is a new session key. Every CSRF check on this tier's
write route goes through the EXISTING generic `checkCSRFKey(c, key string) bool` — no new CSRF
verification code, no new error message (its existing `403` bare-text response, keyed off
`KeyChargesErrorInvalidCSRFToken`'s generic wording, is reused as-is; the key name is historical,
the copy is generic ("invalid csrf token") and not charges-specific).

Reusing `csrfManualChargeKey` was considered and rejected: it is issued ONLY by `GET /charges` /
`GET /ui/charges`, and `checkCSRFKey` fails closed on an empty session token — a user who lands
directly on `/supercharger-stats` without ever visiting `/charges` in the same session would be
permanently unable to save a verification. A page-scoped token avoids that coupling.

**Issuance split**, mirroring `ChargePage`/`ChargesListFragment` exactly:

- `SuperchargerStatsPage` (`GET /supercharger-stats`) generates a fresh token via the existing
  `generateCSRFToken()` helper (already in `charges.go`, same package) and saves it to the
  session under `csrfSuperchargerKey`.
- `SuperchargerStatsFragment` (`GET /ui/supercharger-stats`) reads the EXISTING session value —
  it does NOT call `generateCSRFToken()` again.
- The new row-level handlers (`SuperchargerRowEditFragment`, `SuperchargerRowStatic`,
  `SuperchargerRowUpdate`) also only READ the session value, never issue one. This is load-bearing:
  the session holds ONE value per key, so if the edit-row fragment minted a fresh token, opening
  a second row's edit form in the same session would silently invalidate the first row's
  already-rendered token, and that row's Save would then 403 with no visible cause.

**Accepted consequence:** an expired/cleared session yields a plain `403` on Save rather than a
redirect to `/login` — identical to the existing behavior on `/charges`' own write routes; this
tier does not change that trade-off, it inherits it.

**New AGENTS.md amendment required.** `internal/gateway/AGENTS.md`'s current "Exception:
user-initiated writes" (D4) names `charging.Writer` as its sole aperture; `SessionVerifier` is a
DIFFERENT port. Rather than stretching D4's language to cover a port it does not name, this tier
adds a THIRD documented amendment (alongside D4 and the D-lang amendment), listing this route's
own guard order (auth → CSRF → `VerifySession`, whose own `account_id` scoping is the tenant
boundary — see D6, no separate `RegisteredVehicles` ownership call is added, unlike D4's write
path) and stating explicitly that only `charging.SessionVerifier.VerifySession` is permitted
under this aperture. See tasks.md for the doc-update task.

### D9 — Writer-error branching reuses the list-and-match resolve; no `pgx` import in gateway (new — required to fully specify `SuperchargerRowUpdate`)

`VerifySession`'s own doc comment states a not-found write (bad `id`, or an `id` under a
different account) "surfaces as an error wrapping `pgx.ErrNoRows`." Distinguishing that case from
a genuine write failure (e.g. a transient DB error) would normally call for `errors.Is(err,
pgx.ErrNoRows)` — but `internal/gateway` does not, and per `ai/architecture.md` §2 SHOULD NOT,
import `github.com/jackc/pgx/v5` (only `pgxpool`, for the session store's connection pool, is
already imported — a different, narrower surface). Adding a raw `pgx` sentinel dependency into
the gateway to special-case one write-error branch is exactly the kind of DB-library leak the
module boundary exists to prevent, and `charging` exports no `ErrNotFound`-style sentinel of its
own (unlike `tesla.ErrUnauthorized`) for this to key on instead.

`SuperchargerRowUpdate` therefore treats EVERY `VerifySession` error identically, and
distinguishes 404 from 500 by RE-RESOLVING the row via D1's existing `fetchSuperchargerRowVM`
rather than by inspecting the error:

1. `VerifySession` returns an error → log it.
2. Call `fetchSuperchargerRowVM(c, uid, id)` (the SAME helper D1 already defines for the
   edit/cancel GETs).
3. If that ALSO fails to resolve the row → `404`, bare text
   (`KeySuperchargerErrorSessionNotFound`) — covers both "the id never existed / belongs to
   another account" (matches `VerifySession`'s actual not-found case) and "the id fell outside
   the request's window" (D1's inherited failure mode); the two are deliberately not
   distinguished, exactly as D1 already does not distinguish them for the GET paths.
4. If the row DOES resolve (a genuine write failure on an otherwise-valid, in-window session) →
   `renderError(c, http.StatusInternalServerError, fragments.SuperchargerRowEdit(vm, csrfToken,
   map[string]string{"_top": i18n.T(ctx, i18n.KeySuperchargerErrorCouldNotSave)}))`, mirroring
   `ChargeRowUpdate`'s own `_top`-key 500 shape exactly.

This costs one extra read on the (rare) writer-error path only — never on the success path, which
uses `VerifySession`'s own returned `Session` directly (D11) — and keeps the gateway's import
surface unchanged.

### D10 — The edit row shows full row context; only the two percentages are inputs (new — judgment call, mirrors `ChargeRowEdit`)

`SuperchargerRowEdit(vm fragments.SuperchargerRowVM, csrfToken string, validationErrors
map[string]string)` renders a single `<tr id="supercharger-row-{vm.ID}"><td colspan="9">` — the
UPDATED column count after tier 4's four battery columns plus this tier's new "Actions" header —
containing ONE `<form hx-patch=... >`:

- `DateLabel`, `SiteLabel`, `EnergyLabel`, `CostLabel`, `StartBatteryPctEstLabel`,
  `EndBatteryPctEstLabel` render as plain read-only text (NOT inputs) — these six fields are
  never editable through this port.
- `StartBatteryPctLabel`'s underlying raw value (`RawStartBatteryPct`) and
  `EndBatteryPctLabel`'s (`RawEndBatteryPct`) render as the ONLY two `ui.Input type="number"`
  fields, `min="0" max="100" step="1"`, NOT marked `Required` (an empty value is valid — it
  clears, per D7).
- Save (`type="submit"`) and Cancel (`hx-get` to the static row, D2's window-carrying URL)
  buttons, mirroring `ChargeRowEdit`'s exact button pair.

This satisfies the dispatch's stated invariant directly: "the edit row must keep the table's
column count consistent so the `<tr>` swap does not break alignment," and it matches the ticket's
plain-language description ("two number inputs + save/cancel") without discarding the row's
identifying context (date/site) that a bare two-input row would strip, which the `charges` gold
standard already establishes as the pattern to mirror for THIS project's inline-edit rows.

### D11 — `superchargerRowVMFromSession` is extracted so the list build and the single-row handlers share one mapper (new — refactor, no behavior change)

`buildSuperchargerRows`'s current per-session body (nil-safe `"—"` formatting for all four
battery fields, date/site/energy/cost formatting) is extracted into
`superchargerRowVMFromSession(s charging.Session) fragments.SuperchargerRowVM`, called once per
element inside `buildSuperchargerRows`' existing loop AND once, directly, by
`SuperchargerRowUpdate`'s success path (`vm := superchargerRowVMFromSession(updated)` — using the
`Session` `VerifySession` itself returns, no extra read) and by `fetchSuperchargerRowVM` (mapping
the matched session from its list-and-match result). This is the same "one mapper, multiple
call sites" shape `chargeEntryVMFromEntry` already establishes for the `charges` slice — it adds
no new formatting rule, only relocates the existing one so it is not duplicated three times.

## Database Changes

**None.** This change creates no table, column, index, constraint, view, migration, or SQL
query. All five verification columns on `charge_sessions` already exist
(`internal/charging/db/migrations/20260823000001_add_charge_sessions.sql`), and the write path
this tier calls — `charging.SessionVerifier.VerifySession` — already shipped in tier 1 with its
own query. This change performs zero direct database access; every read goes through
`charging.SessionReader` (already public) and every write through `charging.SessionVerifier`
(already public). The database design gate does not apply.

## Test Contract

Every test below is written as pure/offline `internal/gateway/handlers/supercharger_test.go`
coverage (fake `SessionReader`/`SessionVerifier`/`Recalculator`), authored to these EXACT
expected values before implementation, per the project's test-authoring-order rule.

**T1 — absent key is 400, not a silent clear.** GIVEN a `PATCH` body containing only
`start_battery_pct=50` (no `end_battery_pct` key at all), WHEN `SuperchargerRowUpdate` handles
it, THEN the response is `HTTP 400` with the malformed-body message, `VerifySession` is NEVER
called (the fake verifier records zero calls), and no fragment is rendered.

**T2 — empty value clears, both keys present.** GIVEN a `PATCH` body with `start_battery_pct=`
and `end_battery_pct=` (both keys present, both values empty), WHEN handled, THEN
`VerifySession` is called exactly once with `(nil, nil)`, and on the fake verifier returning a
session with both percentages and `BatteryPctSource` nil, the response is `HTTP 200` rendering
the static row with both battery cells showing `"—"`.

**T3 — recalculation window covers a just-after-midnight UTC session.** GIVEN a verified session
whose `ChargeStopDateTime` is `2026-03-15T00:20:00Z` (20 minutes after UTC midnight — the case
D5's safety argument exists to cover), WHEN `SuperchargerRowUpdate` succeeds with a non-nil
`TeslaID`, THEN the fake `Recalculator.Recalculate` is called exactly once with
`start=2026-03-14T00:00:00Z, end=2026-03-16T00:00:00Z` (i.e. `day-1 .. day+1` where
`day=2026-03-15`, computed from `ChargeStopDateTime` alone, ignoring any `ChargeStartDateTime`
value the fixture also sets to a different day).

**T4 — nil TeslaID skips recalculation, write still succeeds.** GIVEN a `PATCH` that succeeds
against the fake `SessionVerifier`, whose returned `Session.TeslaID` is `nil`, WHEN handled,
THEN the fake `Recalculator.Recalculate` records ZERO calls, the response is still `HTTP 200`
rendering the static row with the verified values, and a log line is emitted (assert via the
handler's log output or an injectable logger, matching the project's existing log-and-swallow
test pattern).

**T5 — out-of-range field errors are 422, per-field, and do not call the port.** GIVEN a `PATCH`
body with `start_battery_pct=101` and `end_battery_pct=50` (both keys present), WHEN handled,
THEN the response is `HTTP 422` with `HX-Error-Fragment: true`, the rendered edit-row fragment
carries a field-level error keyed `start_battery_pct` (not `_top`), the SUBMITTED raw values
(`"101"`, `"50"`) are echoed back into the re-rendered inputs (not reset to the old stored
values), and the fake `SessionVerifier` records ZERO calls.

**T6 — CSRF rejection with no issued token.** GIVEN a session with NO `csrf_supercharger` value
ever set (a client that reached `PATCH /ui/supercharger-stats/row/:id` without first loading
`GET /supercharger-stats` or `GET /ui/supercharger-stats` in this session), WHEN
`SuperchargerRowUpdate` handles any well-formed body, THEN the response is `HTTP 403` via the
existing `checkCSRFKey` fail-closed path, and the fake `SessionVerifier` records ZERO calls.

**T7 — a not-in-window id resolves as not-found on GET.** GIVEN a `GET .../row/:id/edit` request
whose `?start=&end=` window does not include a session with the given `id` (fixture: the fake
`SessionReader` returns sessions, none matching `id`), WHEN handled, THEN the response is
`HTTP 404` and no fragment other than the bare not-found text is rendered.

## Risks / Trade-offs

- **The extra read on the writer-error path (D9)** costs one additional bounded query only when
  `VerifySession` itself fails — a rare path (account-scoped write, already range-validated
  client-side) — in exchange for keeping `pgx` out of the gateway's import graph. Accepted.
- **The ±1-day recalculation window (D4/D5) recomputes two extra days per verification edit.**
  Bounded and idempotent (D4 point 5); a human editing a session is a low-frequency,
  user-triggered event, not a hot path, so the extra work is negligible against the
  Performance-Profile's write-side latitude.
- **A THIRD read-only-boundary amendment in `internal/gateway/AGENTS.md` (D8)** grows that
  document's exception list. This is the accepted cost of the project's own boundary-safety
  design (`ai/go-conventions.md`'s "closed, small vocabularies" principle, invoked by tier 1's own
  D9 for the identical reason) — a fat, all-purpose "gateway may write" exception would be worse:
  each amendment stays small, narrowly scoped to one port, and independently auditable.

## Verification signals

- Implementation: after the two new/changed `.templ` files, run `make templ` (regenerates
  `supercharger_row_templ.go`, `supercharger_row_edit_templ.go`,
  `supercharger_stats_templ.go`); run `make css` only if a new Tailwind/DaisyUI class is
  introduced (none is designed here — the edit row reuses `ui.Field`/`ui.Input`/`ui.Button`
  exactly as `ChargeRowEdit` already does). `go build ./...` and `go vet ./...` are the worker's
  own cheap signals per the Test-Execution-Policy.
- Owner verification (not the worker): `go test ./internal/gateway/...`.
- Artifact validation for the leader: `openspec validate RM31-gateway-add-session-battery-edit --strict`.
