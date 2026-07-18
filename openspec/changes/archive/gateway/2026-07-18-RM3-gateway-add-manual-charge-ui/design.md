## Context

Tier 2 (frontend) of the RM3-manual-charge-log roadmap. This change wires the tier-1
`manualcharge.Writer` and `manualcharge.Reader` ports into `internal/gateway`, adds a
standalone `/charges` "Charge log" page with htmx/Templ fragments, and handles create,
inline-edit, and delete flows. All decisions below are final; they incorporate the
leader's Step-2 user interview (2026-07-18) and the binding decisions D3–D5 in the
dispatch.

**This change adds NO database object.** All persistence lives in the `internal/manualcharge`
module (tier 1). The gateway calls tier-1 ports only. No migration, no sqlc changes, no
new tables, columns, or indexes. The design gate for `database` objects is not triggered.

---

## D1 — Page and Route Design

### Full-page routes

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| `GET` | `/charges` | `h.ChargePage` | Full-page Charge log; auth-guarded. |

### htmx fragment routes (all under `/ui/charges/...`)

| Method | Path | Handler | Description |
|--------|------|---------|-------------|
| `GET` | `/ui/charges/list` | `h.ChargesListFragment` | Renders the charges table fragment (list of entries, newest first). |
| `POST` | `/ui/charges/create` | `h.ChargeCreate` | Creates a new entry; swaps the create form fragment (success resets form + refreshes list; validation error re-renders form with errors). |
| `GET` | `/ui/charges/row/{id}/edit` | `h.ChargeRowEditFragment` | Swaps the static row for an inline edit form. |
| `PUT` | `/ui/charges/row/{id}` | `h.ChargeRowUpdate` | Saves the edited row; on success swaps back to the static row; on error re-renders the edit form with errors. |
| `DELETE` | `/ui/charges/row/{id}` | `h.ChargeRowDelete` | Deletes the entry; swaps the row for an empty `<tr>` (htmx `hx-swap="outerHTML"` removes the row). |

**Route naming rationale:** The `/ui/charges/...` prefix follows the project's
`/ui/<region>` convention (AGENTS.md, htmx-conventions.md). The `/charges` top-level
page follows the existing `/dashboard` pattern. Fragment IDs match the `templ.Fragment`
names they carry.

### Template fragment IDs (invariant: root `<div id="X">` matches `@templ.Fragment("X")`)

| Fragment name | What it wraps |
|---------------|---------------|
| `charges-list` | The full `<div>` wrapping the charges table (rows + empty state). |
| `charges-create-form` | The create form panel (required fields visible + expander for optional fields). |
| `charges-row-{id}` | A single `<tr>` for one entry (static view or inline edit form). Named with the entry UUID suffix for targeted swaps. |

---

## D2 — Handler Design (thin: auth → validate ownership → call port → render)

All handlers follow the same structural pattern established in the existing gateway:

```
1. currentUID(c)             → uuid.UUID or redirect to /login
2. h.acct.RegisteredVehicles → validate vehicle ownership (for write paths)
3. h.manualChargeWriter.*    → call Writer port (write paths only)
   h.manualChargeReader.*    → call Reader port (read paths)
4. mapToViewModel(entries)   → build gateway view models (no manualcharge types in templates)
5. render(c, status, comp)   → stream HTML fragment or full page
```

### `dataForCharges(ctx, uid, teslaID, limit)` helper (gin-free, unit-testable)

A `dataFor`-style helper that:
1. Calls `h.acct.RegisteredVehicles(ctx, uid)` → builds the vehicle picker view model.
2. Calls `h.manualChargeReader.ListEntriesByVehicle(ctx, uid, teslaID, limit)` or
   `ListEntriesByAccount(ctx, uid, limit)` depending on whether a `teslaID` filter is active.
3. Maps the resulting `[]manualcharge.Entry` to `[]ChargeEntryVM` (see D6 — view models).
4. Returns `ChargesPageData` — a flat Go struct with no `manualcharge.*` types and no
   `pgtype.*` values (the template sees only presentation-ready data).

Because `dataForCharges` is decoupled from Gin, it can be tested with fake interface
implementations without an HTTP server (same pattern as the existing `buildVehicleData`
in the dashboard handlers).

---

## D3 — Create/Edit Form Layout (binding decision from leader interview)

The "Log a charge" form (both create and inline row-edit) has two tiers of fields:

**Always visible (required):**
- `charged_on` — date picker (type="date"), required.
- `energy_added_kwh` — number input (step="0.01", min="0.01"), required.
- `price` — number input (step="0.01", min="0"), required.
- `currency` — text input with default "COP", required.

**Under a `<details>` expander labeled "More details" (optional, ~8 fields):**
- `started_at` — datetime-local input.
- `ended_at` — datetime-local input.
- `start_battery_pct` — number input (min=0, max=100, step=1).
- `end_battery_pct` — number input (min=0, max=100, step=1).
- `charging_type` — select: blank / AC / DC.
- `location_kind` — select: blank / HOME / WORK / OTHER.
- `location_label` — text input (free text, shown when location_kind is selected).
- `notes` — textarea.

The vehicle is selected from a `<select>` populated by `account.Service.RegisteredVehicles`
(the user's own vehicles only — never a free-text VIN entry). The selected option provides
both `tesla_id` (int64) and `vin` (string) via a combined value (e.g. `{teslaID}:{vin}`) that
the handler parses and validates against the registered vehicles list before calling Writer.

**Inline row-edit (binding decision D3b from the interview):** Clicking "Edit" on a row
issues `GET /ui/charges/row/{id}/edit`, which swaps the static `<tr>` for an edit form
`<tr>`. The edit form has the same two-tier field layout. "Save" issues
`PUT /ui/charges/row/{id}`. "Cancel" issues `GET /ui/charges/row/{id}` to swap back to the
static row without a round-trip through the list query. This avoids a separate edit page and
keeps the table stable on save/cancel.

---

## D4 — Boundary-Rule Amendment: User-Initiated Writes in the Gateway

**Current rule** (`internal/gateway/AGENTS.md` — "Read-only at request time"): handlers
only call Reader ports; no writes, no Tesla API calls, no side effects on user requests.

**Amendment:** The gateway MAY call `manualcharge.Writer` (Create / Update / Delete) on
explicit **user-initiated form POSTs/PUTs/DELETEs** provided that:

1. **Auth guard first** — `currentUID(c)` must resolve a valid session UID or the handler
   redirects to `/login` and returns. No write proceeds without an authenticated user.
2. **Tenant ownership validated before every write** — the handler calls
   `h.acct.RegisteredVehicles(ctx, uid)` and confirms the submitted `tesla_id` + `vin`
   belong to the calling user's account. If the vehicle is not found in the user's list,
   the handler returns HTTP 403 (forbidden) without calling Writer. This is the referential
   integrity flow documented in the tier-1 design (D2f) — no cross-module FK exists in the
   DB, so the gateway enforces tenant scoping at the application layer.
3. **CSRF token on every state-changing route** — see D5.
4. **Only `manualcharge.Writer` is permitted**, not other modules' write surfaces. This
   is the narrow aperture: this amendment does not open general write access to the gateway.

**Rationale for the amendment:** the "read-only at request time" rule was designed to
prevent the hot read path (dashboard loads) from triggering inadvertent writes or having
side effects. Manual charge entry is a different class of request: the user explicitly
fills a form and submits it. Denying all writes at the gateway layer would force an
external HTTP API (`/api/v1/manual-charges`) that the user's own browser would then need to
call — an unnecessary layer when the gateway is already the only HTML surface and the only
place these forms live. The write is intentional (form POST), narrow (one module's Writer
port), CSRF-protected, and tenant-scoped. The trade-off (gateway no longer purely reads
at request time) is accepted for this one use case. Reader-only remains the default for
all non-form handlers (dashboard, telemetry fragments, etc.).

**AGENTS.md update:** the implementation task for this change includes amending the
"Read-only at request time" section in `internal/gateway/AGENTS.md` to document this
amendment, its constraints (auth + ownership + CSRF + only Writer), and its rationale.
This update happens during implementation, not here.

---

## D5 — CSRF Approach

**Context:** The existing gateway uses a `randomState` nonce for OAuth flows (Google login,
Tesla connect) stored in the cookie session and compared on the callback. The `/logout` route
today is an unprotected POST (no CSRF token — it is low-risk because it only clears the
session, not user data). There is no general CSRF middleware on the router.

**Decision for manual charge write routes:** Use the existing **session-stored random token**
pattern, consistent with the OAuth state mechanism already in the codebase:

1. When serving `GET /charges` (the full page), the handler generates a CSRF token
   (`crypto/rand`-sourced hex string, 16 bytes → 32 hex chars) and stores it in the
   session under the key `"csrf_manualcharge"`.
2. The page template and every form/button fragment that performs a state-changing action
   (create, update, delete) render a hidden `<input name="csrf_token">` field carrying
   this value, OR include it as a custom request header (e.g. `hx-headers`) for htmx
   requests.
3. Every write handler (`ChargeCreate`, `ChargeRowUpdate`, `ChargeRowDelete`) reads the
   submitted `csrf_token` from the form body (or htmx header), compares it to the session
   value via `subtle.ConstantTimeCompare`, and returns HTTP 403 if it does not match.
4. The CSRF token is **not** rotated per-request (it lives for the page session); this is
   the same strategy used by the OAuth `state` parameter already in use. For a local MVP
   this is sufficient. Future hardening (per-form token rotation, `SameSite=Strict`, custom
   header-only CSRF) is recorded as a follow-up.

**Why not a third-party CSRF library:** The codebase has no existing CSRF middleware, and
introducing one for a single new feature would add a dependency for minimal gain. The
session-stored token approach is already in the codebase (OAuth state) and provides CSRF
protection against the most common attack vector (cross-site form submission). Explicit
and auditable.

**Why not protect `/logout` the same way:** `/logout` only clears the session. Logout CSRF
attacks result in the attacker logging out the victim — annoying but not a data integrity
issue. The write handlers for manual charges carry a real data mutation risk; they need the
token.

---

## D6 — View Model Mapping (no leaking manualcharge types into templates)

### `ChargeEntryVM` (presentation model for one entry row)

```go
// ChargeEntryVM is the gateway presentation model for one manual charge entry.
// All derived values are pre-computed by the handler; templates do no arithmetic.
// No manualcharge.Entry, pgtype, or time.Duration in this struct.
type ChargeEntryVM struct {
    ID              string    // UUID formatted as string for URL path params
    VehicleLabel    string    // DisplayName from the registered vehicle (looked up by TeslaID)
    ChargedOnLabel  string    // formatted date, e.g. "Mon Jan 2, 2006"
    EnergyKWh       string    // formatted, e.g. "12.50 kWh"
    PriceLabel      string    // formatted with currency, e.g. "15,000 COP"
    Currency        string    // ISO 4217 code, e.g. "COP"
    CostPerKWhLabel string    // formatted, e.g. "1,200 COP/kWh"; empty string if nil
    BatteryDelta    string    // e.g. "+12%"; empty string if nil
    DurationLabel   string    // e.g. "1h 30m"; empty string if nil
    ChargingType    string    // "AC", "DC", or "" if nil
    LocationKind    string    // "HOME", "WORK", "OTHER", or "" if nil
    LocationLabel   string    // free text or ""
    Notes           string    // free text or ""
    // Raw values for the inline edit form (pre-populated inputs)
    RawChargedOn        string // "2006-01-02" (HTML date input format)
    RawEnergyKWh        string // "12.50"
    RawPrice            string // "15000.00"
    RawStartedAt        string // "2006-01-02T15:04" (datetime-local) or ""
    RawEndedAt          string // "2006-01-02T15:04" or ""
    RawStartBatteryPct  string // "80" or ""
    RawEndBatteryPct    string // "92" or ""
    TeslaID             int64  // for vehicle picker pre-selection in edit form
    VIN                 string // durable vehicle key for display
}
```

### `VehicleOptionVM` (vehicle picker option in the create/edit form)

```go
type VehicleOptionVM struct {
    TeslaID     int64
    VIN         string
    DisplayName string
    Value       string // "{TeslaID}:{VIN}" — the combined form value the handler parses
}
```

### `ChargesPageData` (the full page data)

```go
type ChargesPageData struct {
    Entries         []ChargeEntryVM
    VehicleOptions  []VehicleOptionVM
    ActiveTeslaID   int64  // 0 if no vehicle filter; used to pre-select the picker
    CSRFToken       string // for form hidden inputs
    EmptyState      bool   // true when Entries is empty
    Error           string // non-empty if a reader error degraded the page gracefully
}
```

**Pre-computation rule:** The handler calls `mapToChargeEntryVM(e manualcharge.Entry,
vehicles []account.Vehicle)` which calls `e.CostPerKWh()`, `e.BatteryDelta()`,
`e.SessionDuration()` and formats the results into display strings. Templates receive only
`ChargeEntryVM` — no method calls on domain types, no time arithmetic, no nil checks.

---

## D7 — Deps / Handler Wiring

### `gateway.Deps` extension

Add two new fields to `internal/gateway/gateway.go:Deps`:

```go
ManualChargeWriter manualcharge.Writer
ManualChargeReader manualcharge.Reader
```

Both are passed through to `handlers.Deps` and stored in `handlers.Handler` (unexported
fields `manualChargeWriter` and `manualChargeReader`).

### `cmd/web` injection

`cmd/web/main.go` constructs `manualcharge.NewWriter(pool)` and `manualcharge.NewReader(pool)`
from the existing `pgxpool.Pool` and injects them into `gateway.Deps`. No new DB connection,
no new config keys.

### Module import rule

`internal/gateway` imports `internal/manualcharge` for the `manualcharge.Writer`,
`manualcharge.Reader`, and `manualcharge.Entry` types (only the public API, never
`internal/manualcharge/db` or `manualchargedb`). This is exactly the Reader-port pattern
already used for `internal/telemetry`.

---

## D8 — Error, Empty, and Validation States

### Empty state

When `ListEntriesByVehicle` or `ListEntriesByAccount` returns an empty slice,
`ChargesPageData.EmptyState = true`. The template renders an empty-state message and the
create form, not the table header (no misleading "0 rows" table).

### Reader error (graceful degradation)

If the Reader call errors, the handler logs the error, sets `ChargesPageData.Error` to a
user-facing message (e.g. "Could not load your entries — please try again"), and renders the
page with an empty Entries slice. The page does not return a 500. Same degradation approach
used by the dashboard for telemetry reader failures.

### Validation errors (create / edit)

The handler validates form inputs before calling Writer:
- `charged_on`: required; parseable as a date (YYYY-MM-DD).
- `energy_added_kwh`: required; parseable as float64; > 0.
- `price`: required; parseable as float64; >= 0.
- `currency`: required; non-empty.
- `tesla_id` + `vin` (from the combined form value): must be in the user's registered
  vehicles list (tenant scoping — D4 rule 2).
- Optional fields: parsed only when non-empty; `start_battery_pct` / `end_battery_pct`
  validated as 0–100; `charging_type` validated as "AC" or "DC"; `location_kind` as
  "HOME", "WORK", or "OTHER".

On validation failure the handler re-renders the create form (or inline edit form) with
the submitted values pre-filled and error messages alongside the invalid fields. HTTP
status 422 (Unprocessable Entity), not 200, so htmx can distinguish success from failure
if needed. The fragment ID is preserved so htmx swaps the form in place.

### Write error (database / service error)

If `Writer.Create`, `Writer.Update`, or `Writer.Delete` returns an error, the handler
re-renders the form fragment with a top-level error message. No raw error string is exposed
to the template — only a user-facing message ("Could not save your entry — please try again").

### Delete confirmation (UX note)

The "Delete" button uses htmx `hx-confirm` to show a browser-native confirmation dialog
before issuing the `DELETE`. No separate confirm page. The `hx-confirm` text is set in the
template.

### 403 (CSRF mismatch or ownership violation)

Returns a short error fragment (`<div class="error">`)  with a user-facing message. Not a
full-page redirect — the table stays visible; only the relevant row/form region shows the
error.

---

## D9 — Performance Profile (read-heavy)

Manual charge writes are user-initiated (form POSTs), not poller writes. They are expected
to be low-frequency (a user logs a charge session at most a few times per week). The hot
path is the list read (`ListEntriesByVehicle`), which is covered by the
`(account_id, tesla_id, charged_on DESC)` index from tier 1. No additional indexes or
summary tables are needed at this tier.

The `limit` parameter defaults to 100 (server-side constant) for the initial list load.
Pagination is a follow-up; the limit keeps the initial page responsive without an explicit
OFFSET/cursor in this tier.

---

## D10 — Nav Link

A "Charge log" link is added to the site navigation (likely in `templates/layouts/base.templ`
or equivalent). It is auth-conditional: visible to signed-in users only (same as the
dashboard link). Implementation task (ix) in tasks.md covers this.
