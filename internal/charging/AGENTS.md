# charging — Module Agent Identity

Agent-Name: charging

## Doc-Pack (module)

No module-specific docs beyond the base pack. Every worker dispatched to this module reads
the base doc-pack declared in `CLAUDE.md` § Pipeline config:

- `CLAUDE.md`
- `ai/architecture.md`
- `ai/go-conventions.md`

No htmx, no template, no Templ conventions apply here — this module is backend-only (no HTML).
If a future task touches the gateway integration (wiring this module's ports into `cmd/web` Deps),
that work belongs in the `gateway` module and its agent, not here.

---

## Responsibility

`internal/charging` is the domain module for two related but distinct charging record
types, in two separate tables with two separate vocabularies (see §Data Ownership):

- **User-asserted charge entries** (`manual_charge_entries`) — home/work/third-party
  charging sessions that Tesla's Fleet API cannot attribute to a specific vehicle. Users
  manually log the date, energy added (kWh), cost, and optional metadata (battery
  before/after, timing, charging type, location). The module stores and retrieves these
  entries, enforces multi-tenant data isolation, and computes derived values
  (cost-per-kWh, battery delta, session duration) on read as value-receiver methods on
  `charging.Entry`.
- **Mirrored Supercharger charge sessions** (`supercharger_sessions`, renamed from
  `charge_sessions` in RM39 tier 3, RM29 tier 6,
  RM29-charging-add-charge-sessions) — a dense, one-row-per-session nightly mirror of
  `internal/telemetry`'s `supercharger_sessions`, plus a human-owned battery-percentage
  verification channel that only this module ever writes. The nightly orchestrator
  (`internal/app` since RM29 tier 7; `cmd/poller` before it) reads sessions from
  telemetry's public `SuperchargerReader` port,
  maps each one to a `charging.SessionMirror`, and calls
  `SessionWriter.MirrorSessions` to upsert them here — the mapping itself lives in
  `internal/app`, the application layer, never in this module or in `telemetry`
  (design.md D7; the mapping moved out of `cmd/poller` with RM29 tier 7). The two
  tables are deliberately not merged: roadmap D4 defers that convergence to
  backlog item 12.

This module:

- Owns the `manual_charge_entries` table exclusively.
- Owns the `supercharger_sessions` table exclusively (renamed from `charge_sessions`,
  RM39 tier 3, D5b).
- Is isolated from the Tesla Fleet API — it imports no `internal/tesla` package, needs no OAuth
  scope, and wakes no car.
- Exposes CRUD (Writer) and read (Reader) ports for manual entries, and both a
  `SessionWriter` (mirroring) and a `SessionReader` (windowed per-vehicle reads) port for
  Supercharger sessions — all public Go interfaces. `supercharger_sessions` gained its reader in
  RM30 tier 1 (RM30-charging-add-session-read-port), superseding RM29 tier 6's design.md D9
  note that no consumer needed one.
- Computes no HTML, no templates, no htmx fragments — that is the gateway's job (Tier 2).

This module was renamed from `manualcharge` in RM29 tier 2, and gained `charge_sessions`
(renamed to `supercharger_sessions` in RM39 tier 3, D5b) in RM29 tier 6 — a scope this
module did not have when it was named `manualcharge`.

---

## Public Interface

```go
// Writer is the CRUD port. The gateway calls this after validating the user owns
// the vehicle (resolved via account.Service — outside this module's scope).
type Writer interface {
    Create(ctx context.Context, e Entry) (Entry, error)
    Update(ctx context.Context, e Entry) (Entry, error)
    Delete(ctx context.Context, accountID uuid.UUID, id uuid.UUID) error
}

// Reader is the read port shaped for dashboard access patterns.
// All methods return a non-nil empty slice when no entries exist.
// For ListEntriesByVehicle and ListEntriesByAccount, limit = 0 uses a server default (100).
type Reader interface {
    ListEntriesByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Entry, error)
    ListEntriesByAccount(ctx context.Context, accountID uuid.UUID, limit int) ([]Entry, error)

    // ListEntriesByVehicleBetween returns entries for a specific vehicle within an
    // account whose charged_on falls within [from, to], inclusive of both bounds.
    // Ordered charged_on DESC, matching ListEntriesByVehicle. No limit parameter —
    // the [from, to] window itself bounds the result.
    ListEntriesByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Entry, error)
}

// Constructors — these are the only publicly exported factory functions.
func NewWriter(pool *pgxpool.Pool) Writer
func NewReader(pool *pgxpool.Pool) Reader
```

**`Entry.InferredCapacityKWhCalc *float64`** (MAG-25, charging-add-inferred-capacity)
is reachable through both `Writer` and `Reader` above — it is a field on `Entry`, not
a new method. It is **database-computed and read-only**: a value set on the `Entry`
passed to `Writer.Create` / `Writer.Update` is silently ignored (the underlying
`INSERT`/`UPDATE` never names the column), and the database physically rejects any
direct write to it. `nil` means the record's inputs (energy, both percentages, a
strictly increasing delta) did not support the formula — never an error. See
§Data Ownership below for the guard and §Units convention for the naming rule.

The gateway (Tier 2 `RM3-gateway-add-manual-charge-ui`) wires these interfaces into `cmd/web`
Deps and calls them from handlers. The gateway never imports `chargingdb` directly.

### Entry lifecycle status and energy provenance (MAG-18/RM33, RM33-charging-add-entry-status)

```go
// Status is the lifecycle state of a manual charge entry (TEXT + CHECK in the DB).
type Status string

const (
    StatusInProgress Status = "IN_PROGRESS" // logged at plug-in time; may lack end-of-session facts
    StatusDone       Status = "DONE"        // complete; no transition rule -- DONE -> IN_PROGRESS is permitted
)

// EnergySource is the provenance of Entry.EnergyAddedKWh. ALWAYS COMPUTED BY THIS
// MODULE on Create/Update -- a value set on the Entry passed to Writer is ignored
// and overwritten, the same shape supercharger_sessions.battery_pct_source already uses.
type EnergySource string

const (
    EnergySourceUser      EnergySource = "USER"      // the value came from the person
    EnergySourceEstimated EnergySource = "ESTIMATED" // derived from pack capacity + battery delta on write
)

// Field names one field of an Entry whose presence RequiredFieldsFor can evaluate.
// Its string value is the database column name, which is ALSO the gateway's form
// input name and its validation-error map key (handlers/external_charges.go).
type Field string

const (
    FieldChargedOn     Field = "charged_on"
    FieldLocationKind  Field = "location_kind"
    FieldEndedAt       Field = "ended_at"
    FieldEndBatteryPct Field = "end_battery_pct"
)

// RequiredFieldsFor is the SINGLE SOURCE OF TRUTH for which fields an entry must
// carry to be stored with the given status: Writer.Create/Update enforce exactly
// this, and internal/gateway (tier 2) drives which inputs render as required from
// exactly this. Returns a fresh slice on every call. An unrecognized status
// returns the DONE (strictest) set -- fail-closed.
func RequiredFieldsFor(s Status) []Field
```

| `Status` | `RequiredFieldsFor` set |
|---|---|
| `IN_PROGRESS` | `charged_on`, `location_kind` |
| `DONE` | `charged_on`, `location_kind`, `ended_at`, `end_battery_pct` |

### Auto-promotion to `DONE` (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source)

`Writer.Create` and `Writer.Update` both call `promoteIfComplete(e Entry) Entry`
(`validation.go`) right after `normalizeStatus` and right before `missingFields`. It
promotes an entry submitted as `IN_PROGRESS` to `DONE` when every field
`RequiredFieldsFor(StatusDone)` demands is already present. It reuses
`missingFields`/`RequiredFieldsFor` — no hardcoded field list — so a future change to
the `DONE` set tightens promotion automatically. It never demotes: an entry already
`DONE` is returned unchanged.

This does **not** change `RequiredFieldsFor`'s own rule. It changes only when an entry
gets stored as `DONE` without the caller moving the status control by hand. An explicit
`DONE` submission missing a required field is still rejected exactly as before.

**Known cost:** because promotion also runs on `Update`, and `DONE → IN_PROGRESS` has no
transition rule (still true — see above), "reopening" a `DONE` entry without also
clearing `ended_at` or `end_battery_pct` gets immediately re-promoted back to `DONE`.
To really reopen an entry, the caller must clear at least one of those two fields.

**`Entry.EnergyAddedKWh` is `*float64`, not `float64`** (was `float64` before this
change). `nil` means not supplied and not derivable -- an `IN_PROGRESS` entry
legitimately has no end-of-session facts, so no honest value exists yet. When
`nil` and both battery percentages are present with `EndBatteryPct >
StartBatteryPct`, `Writer.Create`/`Update` derives a value on write from the
(currently hardcoded `62.0`) pack capacity and sets `EnergySource` to
`EnergySourceEstimated` -- never a fabricated `0`. `CostPerKWh()` is nil-safe
over the pointer: `nil` when `EnergyAddedKWh == nil` and (as before) when it
points at `0`.

`Entry` gained three more fields in the same change:

- **`Status Status`** -- see above; an empty `Status` normalizes to
  `StatusInProgress` on `Create`/`Update` (any other unrecognized value is
  rejected in Go, before any DB call).
- **`EnergySource EnergySource`** -- **module-computed and ignored when
  supplied**: a value set on the `Entry` handed to `Writer.Create`/`Update` is
  never read; the module always overwrites it (`EnergySourceEstimated` when it
  derived the energy, `EnergySourceUser` in every other case, including a `nil`
  it could not derive).
- **`OdometerKm *int`** -- the odometer reading, in kilometres, observed **at**
  this charge event (an observation belonging to the event, not current vehicle
  state). `nil` means not recorded.

### Price provenance (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source)

```go
// PriceSource is the provenance of Entry.Price: PriceSourceUser when the amount
// is known to be real (a positive price, or a caller-confirmed zero),
// PriceSourceUnconfirmed when a zero price has not been confirmed as a real
// free charge. ALWAYS COMPUTED BY internal/charging on Create/Update -- a value
// set on the Entry passed to Writer is ignored and overwritten, the same shape
// EnergySource already uses.
type PriceSource string

const (
    PriceSourceUser        PriceSource = "USER"
    PriceSourceUnconfirmed PriceSource = "UNCONFIRMED"
)
```

`Entry` gained two more fields in the same change:

- **`PriceSource PriceSource`** -- module-computed output. A value set here on the
  `Entry` passed to `Writer` is never read; the module always overwrites it. The rule:
  a positive `Price` is always `USER`; a zero `Price` is `USER` only when
  `PriceConfirmed` is `true`, else `UNCONFIRMED`. A positive price always wins over
  `PriceConfirmed` — the flag is only consulted when `Price == 0`.
- **`PriceConfirmed bool`** -- caller-supplied intent, read **only** when `Price == 0`;
  ignored when `Price > 0`. **Not persisted directly** -- it drives `PriceSource`,
  which is what gets written and read back. A round-trip through `Reader` always
  returns `PriceConfirmed: false` on every entry.

### The Supercharger mirror port (RM29 tier 6)

```go
// SessionMirror is the mirrorable subset of one Supercharger charge session: the
// identity, the time window, and the session facts internal/telemetry collects.
// It deliberately has NO battery-percentage fields.
type SessionMirror struct {
    AccountID uuid.UUID
    VIN       string
    TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
    SessionID int64

    ChargeStartDateTime time.Time
    ChargeStopDateTime  time.Time

    SiteLocationName string
    EnergyKWh        *float64 // nil when the session had no kWh fee
    TotalCost        *float64 // nil when the session had no fees
    Currency         *string  // nil when the session had no fees
    IsPaid           *bool    // nil when the session had no fees
}

// SessionWriter is the synchronization port called by the nightly orchestrator
// (internal/app since RM29 tier 7). Upsert-only: a session that disappears from Tesla's history
// stays mirrored.
type SessionWriter interface {
    // MirrorSessions upserts every supplied session under accountID, in one
    // transaction. Every entry's AccountID must equal accountID; a single
    // mis-scoped entry rejects the WHOLE call and writes nothing.
    MirrorSessions(ctx context.Context, accountID uuid.UUID, sessions []SessionMirror) error
}

// NewSessionWriter — the only publicly exported factory function for this port.
func NewSessionWriter(pool *pgxpool.Pool) SessionWriter
```

**`SessionMirror` has NO field for `start_battery_pct`, `end_battery_pct`, or
`battery_pct_source` — by design, and this is the single most important invariant in
this file.** The three battery-percentage columns are absent from `SessionMirror` and
absent from the `MirrorSuperchargerSession` SQL query entirely (`db/query.sql`), so the
nightly sync path has no field and no column to
bind one to even if a future edit tried — a human's verified reading is protected by a
**compile error**, not by a comment a reviewer has to notice (design.md D6). Do not
"complete" `SessionMirror` by adding these fields; the future verification UI (backlog
item 11) writes them directly, never through this port.

**The refresh set `MirrorSuperchargerSession`'s `ON CONFLICT DO UPDATE SET` touches is
telemetry's own conflict set, minus `raw_data`** (a column `supercharger_sessions` does not
carry): `energy_kwh, total_cost, currency, is_paid, tesla_id`. This is not five
independent judgement calls — it is one rule applied mechanically: *a mirrored column gets
exactly the write semantics its source column has* (design.md D1). Concretely,
`site_location_name` is **not** refreshed on a re-mirror, and the reason is purely
structural: `telemetry`'s own upsert never refreshes `site_location_name` either, so
neither does this one — **not** because a site name was judged unlikely to change. Apply
the same reasoning before adding any future mirrored column: check telemetry's conflict
clause first, and mirror it exactly.

**`updated_at` means "this row's data changed," not "the last mirror pass touched this
row"** (`RM44-charging-add-change-detecting-mirror`, MAG-48; design.md D1–D3). Before
this change, the query set `updated_at = now()` on every mirror pass, whether or not any
of the five refreshed columns above actually changed. That made
`internal/analytics.Recalculator.Reconcile` — which reads this column to find sessions
worth recalculating — see every session as new, every night. The fix: `updated_at` now
advances only when the row's current values differ from the five columns this SET clause
writes. The comparison is a `to_jsonb` deny-list, not a hand-picked `WHERE`: it deny-lists
every column this query never refreshes — bookkeeping (`id`, `created_at`, `updated_at`
itself), human-owned columns (`start_battery_pct`, `end_battery_pct`,
`battery_pct_source`, `status`, `inferred_capacity_kwh_calc`), and write-once mirrored
columns (`account_id`, `vin`, `session_id`, `charge_start_date_time`,
`charge_stop_date_time`, `site_location_name`) — so the comparison covers exactly the
five refreshed columns and nothing else. This keeps working even as the table grows:
`db_mirror_schema_selfcheck_integration_test.go` fails the moment a new column belongs to
neither list. See that test file, and
`internal/charging/db_session_mirror_change_detection_integration_test.go`, for the full
behavioral proof.

The gateway and any other future caller of `SessionWriter` never import `chargingdb`
directly, exactly as for `Writer`/`Reader` above.

### The Supercharger session read ports (RM30-charging-add-session-read-port, widened by RM31-charging-add-session-read-ports)

```go
// SessionStatus is the lifecycle status of one supercharger_sessions row's
// battery-percentage data (TEXT + CHECK in the DB, RM41-charging-add-session-status,
// MAG-45). ALWAYS COMPUTED by SessionVerifier.VerifySession on every call -- no port
// accepts a Session as input for this field, so there is no way for a caller to set
// it directly.
type SessionStatus string

const (
    // SessionStatusInProgress: at least one of start_battery_pct/end_battery_pct is
    // NULL. This is the status of a session nobody has recorded anything for, AND of
    // a session with only a start percentage recorded and no end percentage --
    // deliberately not a fourth state (design.md "State truth table"): this is what
    // keeps "sessions still in progress" a meaningful worklist for the gateway's own
    // follow-up recommendation (RM41 tier 5) even when a session is half-recorded.
    SessionStatusInProgress SessionStatus = "IN_PROGRESS"
    // SessionStatusDoneCalculated: both percentages are present, AND the
    // VerifySession call that produced this row's CURRENT start_battery_pct derived
    // it via derivedStartBatteryPct (capacity.go) rather than storing a
    // caller-supplied value. This is the one place in the schema that keeps a typed
    // percentage apart from a derived one -- battery_pct_source itself cannot
    // (MAG-36 design.md D1).
    SessionStatusDoneCalculated SessionStatus = "DONE_CALCULATED"
    // SessionStatusDone: both percentages are present, and the CURRENT
    // start_battery_pct was supplied directly by VerifySession's caller.
    SessionStatusDone SessionStatus = "DONE"
)

// Session is the full domain representation of one supercharger_sessions row: identity, the
// session's time window, the session facts internal/telemetry collects, and the three
// charging-owned battery-percentage verification columns, plus (since RM41 tier 4) a
// fourth charging-owned column, Status, computed from them. Read-only counterpart
// to SessionMirror — NOT built by widening it: SessionMirror stays deliberately
// percentage-free (RM29 design.md D6) so the nightly sync path has no field to bind a
// human-verified percentage to, even by mistake. Session and SessionMirror are
// distinct types for exactly that reason, even though Session's first thirteen fields
// duplicate SessionMirror's eleven (design.md D4).
type Session struct {
    ID        uuid.UUID
    AccountID uuid.UUID
    VIN       string
    TeslaID   *int64 // nil when the VIN is not a currently-registered vehicle
    SessionID int64

    ChargeStartDateTime time.Time
    ChargeStopDateTime  time.Time

    SiteLocationName string
    EnergyKWh        *float64 // nil when the session had no kWh fee
    TotalCost        *float64 // nil when the session had no fees
    Currency         *string  // nil when the session had no fees
    IsPaid           *bool    // nil when the session had no fees

    // Charging-owned verification channel — never written by the nightly sync.
    StartBatteryPct    *int
    EndBatteryPct      *int
    BatteryPctSource   *string

    // Status is this session's lifecycle status -- ALWAYS COMPUTED by
    // SessionVerifier.VerifySession, never settable through any port
    // (RM41-charging-add-session-status, MAG-45). See SessionStatus's own doc
    // comment for the three values and the rule that produces each.
    Status SessionStatus

    // InferredCapacityKWhCalc — database-computed, read-only (MAG-25,
    // charging-add-inferred-capacity). nil when EnergyKWh is nil, either
    // percentage is nil, or the delta is not strictly positive. See §Data
    // Ownership below.
    InferredCapacityKWhCalc *float64

    CreatedAt time.Time
    UpdatedAt time.Time
}

// SessionReader is the read port over supercharger_sessions. One method, shaped like
// Reader.ListEntriesByVehicleBetween's bounded-per-vehicle-window pattern but NOT
// identical to it: ascending order (not descending) and whole-UTC-calendar-day,
// half-open bound semantics (not exact-value BETWEEN) — see design.md D3/D5. This
// interface is NOT widened by RM31: internal/gateway depends on exactly this
// interface (Deps.SuperchargerReader) and calls only ListSessionsByVehicleBetween —
// see SuperchargerSessionAnalyticsReader below, which embeds this interface instead
// of restating or widening it (RM31 design.md D8).
type SessionReader interface {
    // ListSessionsByVehicleBetween returns sessions for a specific vehicle within an
    // account whose ChargeStopDateTime falls within [from, to], to inclusive of its
    // entire UTC calendar day (to is translated to a half-open upper bound in Go).
    // Ordered ASCENDING by ChargeStopDateTime — the opposite of Reader's DESC order,
    // matching telemetry's own ordering for the identical access pattern; do not
    // "correct" this to DESC. No limit parameter; always a non-nil empty slice on no
    // match. A session whose TeslaID is nil is never returned, for any teslaID.
    ListSessionsByVehicleBetween(ctx context.Context, accountID uuid.UUID, teslaID int64, from, to time.Time) ([]Session, error)
}

// NewSessionReader — the only publicly exported factory function for this port.
// Signature and return type MUST NOT change: cmd/web wires this exact function into
// the gateway's Deps.SuperchargerReader (RM31 design.md D8).
func NewSessionReader(pool *pgxpool.Pool) SessionReader

// SuperchargerSessionAnalyticsReader is the read port over supercharger_sessions for
// internal/analytics (RM31-charging-add-session-read-ports design.md D8). It is a
// SEPARATE interface from SessionReader, not a widening of it — internal/gateway's
// fakeSessionReader test double implements only ListSessionsByVehicleBetween, and a
// first pass of this change widened SessionReader itself, which broke that double
// and failed `go vet ./...` repo-wide. internal/analytics needs all three shapes
// (ListSessionsByVehicleBetween, ListSessionsByVehicleUpdatedSince,
// ListSessionsByVehicle), which is why this interface EMBEDS SessionReader instead
// of restating its method. The three methods reachable through this interface do
// NOT share a single sort-direction convention — sort direction is chosen per query
// against the shared idx_supercharger_sessions_vehicle_stop index, not as a port-family
// rule (design.md D3). "Supercharger" is a deliberate divergence from this module's
// Session* family, for call-site readability in internal/analytics — do not "tidy"
// it into the family (design.md D8).
type SuperchargerSessionAnalyticsReader interface {
    SessionReader

    // ListSessionsByVehicleUpdatedSince returns every session for a specific
    // vehicle within an account whose updated_at is at or after since. Ordered
    // ASCENDING by ChargeStopDateTime — NOT by updated_at itself (design.md D1: this
    // mirrors Reader.ListEntriesByVehicleUpdatedSince's index reasoning in this
    // module, not telemetry.SuperchargerReader's updated_at-ordering choice). No
    // limit parameter; always a non-nil empty slice on no match. A session whose
    // TeslaID is nil is never returned, for any teslaID. This is the mechanism by
    // which a SessionVerifier.VerifySession edit becomes visible to
    // analytics.Recalculator.Reconcile (design.md D1).
    ListSessionsByVehicleUpdatedSince(ctx context.Context, accountID uuid.UUID, teslaID int64, since time.Time) ([]Session, error)

    // ListSessionsByVehicle returns the limit most recent sessions for a specific
    // vehicle within an account, ordered DESCENDING by ChargeStopDateTime — the
    // opposite of ListSessionsByVehicleBetween's ASC, deliberately (design.md D3).
    // limit <= 0 uses the server default (defaultLimit, 100), mirroring
    // Reader.ListEntriesByVehicle's identical contract. Always a non-nil empty
    // slice on no match. A session whose TeslaID is nil is never returned, for any
    // teslaID.
    ListSessionsByVehicle(ctx context.Context, accountID uuid.UUID, teslaID int64, limit int) ([]Session, error)
}

// NewSuperchargerSessionAnalyticsReader — the only publicly exported factory
// function for this port. Returns the same underlying *sessionReader
// NewSessionReader returns — one concrete type satisfies both interfaces.
func NewSuperchargerSessionAnalyticsReader(pool *pgxpool.Pool) SuperchargerSessionAnalyticsReader
```

The gateway and any other future caller of `SessionReader` never import `chargingdb`
directly, exactly as for `Writer`/`Reader`/`SessionWriter` above. The same applies to
`SuperchargerSessionAnalyticsReader`'s future caller, `internal/analytics`.

### The Supercharger session verification port (RM31-charging-add-session-verification-port)

```go
// SessionVerifier is the human-write port over supercharger_sessions' verification channel
// (design.md D9). It is a deliberately separate interface from SessionWriter, not a
// method added to it — SessionWriter's own doc comment states "The gateway never
// calls this," and adding a gateway-triggered, human-facing method to that interface
// would make that sentence false and would hand the nightly-orchestrator wiring path
// and the gateway's human-edit wiring path the same Go type to depend on: the "one
// fat interface, two callers with different trust models" shape the AI-efficiency
// "closed, small vocabularies" principle (CLAUDE.md §Non-negotiables) argues against.
type SessionVerifier interface {
    // VerifySession updates exactly four columns on one account-scoped
    // supercharger_sessions row — start_battery_pct, end_battery_pct,
    // battery_pct_source, status — plus updated_at (RM41-charging-add-session-status,
    // MAG-45, added the fourth). No other column is reachable through this method: the
    // underlying query's SET clause names only these four plus updated_at (RM31 design.md
    // D1, extended by RM41 tier 4). status is ALWAYS COMPUTED by this method from the
    // same startToStore/endBatteryPct/derivation-outcome values used to compute
    // battery_pct_source — never accepted as a parameter; VerifySession's own signature
    // is unchanged by this addition.
    //
    // The two frozen
    // estimate columns formerly named here as columns this method could never
    // reach were dropped from the table entirely by
    // RM41-charging-drop-estimate-columns — there is no longer a column to be
    // unreachable from.
    //
    // battery_pct_source is always computed by this method, never supplied by the
    // caller: "user_verified" when either startBatteryPct or endBatteryPct is
    // non-nil, NULL when both are nil. The method takes no source parameter, so
    // "polled" cannot be written by any caller of this port (design.md D2/D7).
    //
    // A partial call (one percentage non-nil, the other nil) is legal. Every call
    // supplies both parameters' FINAL values, not a delta — a caller wanting to add
    // one field to an already-verified session must re-supply the other field's
    // current value (read via SessionReader) or it is overwritten to NULL
    // (design.md D6). Calling VerifySession(ctx, accountID, id, nil, nil) clears
    // both percentages AND battery_pct_source to NULL in the same statement
    // (design.md D7).
    //
    // Each non-nil percentage is validated to [0, 100] before the query runs; the
    // database's own SMALLINT CHECK is the backstop, not the error message
    // (design.md D3). No ordering between startBatteryPct and endBatteryPct is
    // enforced, deliberately (design.md D8).
    //
    // id/accountID scope the update exactly like Writer.Update: WHERE id = @id AND
    // account_id = @account_id. Zero rows matched — unknown id or wrong account,
    // indistinguishable — surfaces as an error wrapping pgx.ErrNoRows, with no
    // NotFound special-casing (design.md D5).
    VerifySession(ctx context.Context, accountID uuid.UUID, id uuid.UUID, startBatteryPct, endBatteryPct *int) (Session, error)
}

// NewSessionVerifier — the only publicly exported factory function for this port.
func NewSessionVerifier(pool *pgxpool.Pool) SessionVerifier
```

`SessionVerifier` is this module's third narrow, single-purpose interface over
`supercharger_sessions` (alongside `SessionWriter` and `SessionReader`) — one port per access
pattern (batch write, read, human write), not one port per table, consistent with how
`manual_charge_entries` already splits `Writer`/`Reader` (design.md D9). The gateway and
any other future caller never import `chargingdb` directly, exactly as for
`Writer`/`Reader`/`SessionWriter`/`SessionReader` above. This port ships with no caller
in this tier — `cmd/web`/`internal/gateway` wiring is deferred to
`RM31-gateway-add-session-battery-edit` (tier 4).

### Derived start battery percentage (MAG-36, charging-add-derived-start-battery-pct)

`SessionVerifier.VerifySession` can now derive `start_battery_pct` instead of leaving it
absent. The trigger is exactly four conditions, all required: the caller's
`startBatteryPct` is `nil`, the caller's `endBatteryPct` is non-`nil`, the session row's
`energy_kwh` is non-`NULL`, and the algebraic result — `start = end -
energy_kwh/packCapacityKWh*100`, rounded `math.Round` (half away from zero) — lands in
`[0, 100]` (design.md D2/D4). A caller-supplied `startBatteryPct` is **never** recomputed
or overridden, under any condition — clearing the start field is the caller's way of
asking for it to be calculated. When `energy_kwh` is `SQL NULL` (design.md D5) or the
derived result falls outside `[0, 100]` (design.md D3), `start_battery_pct` is left
`NULL`, silently — no error, no clamp to `0`/`100`. `battery_pct_source` computation is
otherwise unaffected: still `batteryPctSourceUserVerified` when either the (possibly
derived) start or the end percentage is non-nil, still the only value this port ever
writes (design.md D1 — no new source value). The derivation runs inside a transaction
(`pool.Begin`/`WithTx`/`SELECT ... FOR UPDATE` via the new `LockSessionForVerification`
query/`Commit`) only when the trigger fires, mirroring `internal/account`'s
`AccessTokenFor` and this module's own `SessionWriter.MirrorSessions` (design.md D7/D9);
every other call keeps the prior single-statement, non-transactional path unchanged.

### Session lifecycle status (RM41 tier 4, MAG-45)

`SessionVerifier.VerifySession` now also computes a stored `status` column on every
call, alongside `battery_pct_source`, sharing its "never accepted from a caller"
property — no port takes a `Session` as input for this field. One-sentence rule: a
session's status is `IN_PROGRESS` unless BOTH percentage columns end up non-`NULL`
after the write; when both are present, it is `DONE_CALCULATED` if THIS write derived
the start percentage rather than storing a caller-supplied one, and `DONE` otherwise.

| `start_battery_pct` (final, after this write) | `end_battery_pct` (final) | This write derived `start`? | `status` |
|---|---|---|---|
| NULL | NULL | — | `IN_PROGRESS` |
| NULL | non-NULL | derivation not attempted or failed (no `energy_kwh`, or out of `[0,100]`) | `IN_PROGRESS` |
| non-NULL | NULL | — | `IN_PROGRESS` |
| non-NULL (derived this call) | non-NULL | yes | `DONE_CALCULATED` |
| non-NULL (caller-supplied) | non-NULL | no | `DONE` |

A session carrying a typed `start_battery_pct` but no `end_battery_pct` is
`IN_PROGRESS` — the same status as a session with nothing recorded at all,
deliberately not a fourth state (design.md "State truth table"): this is what keeps
"sessions still in progress" a meaningful worklist for the gateway's own follow-up
recommendation (RM41 tier 5) even when a session is half-recorded.

**BACKFILL decision:** every row that existed before this migration
(`20260903000004_add_session_status.sql`) was backfilled to `DONE_CALCULATED`
unconditionally — an owner decision about data provenance (every one of those rows'
percentages was manually reconstructed by the owner, not read from the car) the
stored percentages themselves cannot show, not a recompute of the truth table above
against their actual values (design.md "Rationale").

### The Supercharger mirror watermark (RM44-platform-add-mirror-watermark, MAG-48)

`charging.mirror_watermarks` is a new table, one row per account, holding the
highest `telemetry.supercharger_history.updated_at` this module's nightly
mirror has already synchronized. It has no `tesla_id` column: the read it
bounds is account-wide, not per vehicle, so a per-vehicle cursor would miss
the orphan-recovery case (a session whose vehicle re-registers). It has no
`source` column either: this table mirrors exactly one upstream table, so a
second source column would be speculative, not something a caller needs
today.

```go
// MirrorWatermarkStore is the cursor port for the Supercharger mirror read.
// One interface, two methods, one caller (internal/app) — not split like
// SessionWriter/SessionReader/SessionVerifier, because those three serve
// callers with different trust models and this port does not.
type MirrorWatermarkStore interface {
    MirrorWatermark(ctx context.Context, accountID uuid.UUID) (time.Time, error)
    AdvanceMirrorWatermark(ctx context.Context, accountID uuid.UUID, observed time.Time) error
}

// NewMirrorWatermarkStore — the only publicly exported factory function for this port.
func NewMirrorWatermarkStore(pool *pgxpool.Pool) MirrorWatermarkStore
```

**The most important rule: the watermark never advances to `now()`.** It
advances only to the maximum `updated_at` the caller actually observed on a
run, and only when that run's bounded read returned at least one row. A run
that reads zero rows leaves the watermark untouched. This is deliberate: a
row that commits to `telemetry.supercharger_history` a moment late would
otherwise sit permanently behind an advanced cursor and never get mirrored —
a silent, undetectable loss of data. `AdvanceMirrorWatermark` itself does no
row-count check; the caller (`internal/app.processChargingData`) must call
it only after a non-empty read, exactly mirroring
`analytics.recalculator`'s own `watermark`/`advanceWatermark` split.

No row yet for an account means "epoch" — `MirrorWatermark` returns the zero
`time.Time`, not an error, translating `pgx.ErrNoRows` the same way
`analytics.recalculator.watermark` does. A missing cursor backfills that
account's whole Supercharger history once, on its first-ever mirror run.

Implementation lives in `mirror_watermark.go` (`mirrorWatermarkStore`,
mirroring `session_writer.go`'s exact concrete-type pattern). `pgtype` stays
confined to that one file.

---

## Allowed Imports

This module may import:

- `context`, `time`, `math`, `errors`, and other Go standard library packages.
- `github.com/google/uuid` — for `uuid.UUID` primary and tenant keys.
- `github.com/jackc/pgx/v5` and `github.com/jackc/pgx/v5/pgxpool` — for DB connectivity.
- `github.com/jackc/pgx/v5/pgtype` — ONLY inside the four files that talk to the database
  directly: `service.go`, `session_writer.go`, `session_reader.go`, and
  `mirror_watermark.go`. Never in public types, interfaces, `charging.go`, or any
  `_test.go` file. The rule is "only the files that own a query", not "only these names":
  each of them translates plain Go `*T` fields into a generated params struct's nullable
  pgtype fields, and translates them back on the way out.
- `internal/charging/db` (package `chargingdb`) — ONLY inside the five files that talk to
  the database directly: `service.go`, `session_writer.go`, `session_reader.go`,
  `session_verifier.go`, and `mirror_watermark.go`. The generated package is module-private
  by convention; no other module imports it, and no `_test.go` file does either.

  Both lists above have gone stale before. MAG-36 corrected the `chargingdb` list, which had
  named only `service.go` and `session_writer.go` while `session_reader.go` and
  `session_verifier.go` had imported it since RM31. RM44
  (`RM44-platform-add-mirror-watermark`) corrected both lists again: it added
  `mirror_watermark.go` to each, and added `session_reader.go` to the `pgtype` list, which
  had been missing it. In every case the access was correct and only the doc was wrong.
  When you add a file that owns a query, add it to both lists in the SAME change — a stale
  list here reads as a boundary rule and gets trusted like one.

This module MUST NOT import:

- `internal/tesla` — no Fleet API, no Tesla credentials, no VehicleService.
- `internal/account` — no token resolution, no OwnedVehicle types.
- `internal/telemetry` — no snapshot or Supercharger types. This is unchanged by the
  RM29 tier 6 Supercharger-session mirror: `SessionWriter.MirrorSessions` receives its
  data already mapped to `charging.SessionMirror`, from `internal/app` (the application
  layer; it was `cmd/poller` until RM29 tier 7 moved the mirror step there), never
  fetched here. The one path-only exception is test-scoped: this package's
  `_test.go` files provision a second migration DIRECTORY from `../telemetry/db/migrations`
  (see §Testing Notes) — a filesystem path, not a Go import, and it does not appear in any
  non-test file.
- `internal/gateway` — no HTML, no Templ, no handlers.
- Any other module's `db` sub-package.
- `html/template`, `templ`, or any rendering library.

---

## Units convention

Platform-wide unit rule: `openspec/specs/unit-of-measure/spec.md` / `ai/go-conventions.md`
§Coding Rules — display units, unit-suffixed column names, converted once on write. This table
is **compliant**: `energy_added_kwh`, `start_battery_pct`, `end_battery_pct` already carry their
unit suffix. `price` is the platform's named monetary exemption — it takes no suffix and is
paired with the `currency` column instead of a unit. `inferred_capacity_kwh_calc`
(MAG-25, charging-add-inferred-capacity) is compliant too — see the naming rule below.

### Naming a stored, derived column: `<what>_<unit>_calc`

This project names a **stored, derived** column `<what>_<unit>_calc` — unit suffix
first, `_calc` last. `internal/analytics/vehicle_metrics`
(`internal/analytics/db/migrations/20260821000001_add_vehicle_metrics.sql:67-71`) is
the precedent: it carries five such columns — `distance_traveled_km_calc`,
`battery_used_pct_calc`, `km_per_pct_calc`, `estimated_range_km_calc`,
`days_spanned_calc` — each a value computed rather than observed, persisted rather
than derived on read, each placing `_calc` **after** its unit suffix.
`inferred_capacity_kwh_calc` follows the identical shape.

**`ai/go-conventions.md` documents the unit-suffix half of this pattern and says
nothing about `_calc`** — it is an established *code* convention with no written
home until this paragraph. The next agent naming a derived column should read this
rule rather than re-deriving it from `vehicle_metrics` (or missing it entirely).
This is also why `inferred_capacity_kwh_calc` is not the ticket's literal
`inferred_capacity_calc`: that name is the same convention, missing the mandatory
unit segment (design.md D1, charging-add-inferred-capacity).

## Data Ownership

`internal/charging` is the **sole owner** of two tables, both living in the dedicated
`charging` Postgres schema (`charging.manual_charge_entries`,
`charging.supercharger_sessions`) since `RM39-charging-move-to-own-schema` (MAG-31,
`internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql`) — moved
out of `public`, in the same migration that renamed `charge_sessions` to
`supercharger_sessions` (design.md D5b) and the Go db model `ChargeSession` to
`SuperchargerSession` (design.md D5c). **Every** catalog object still carrying the old
table name was renamed with it (design.md D16) — the index, the named CHECK, the primary
key, the unique constraint, and the five CHECKs Postgres auto-named from inline column
constraints (`battery_pct_source`, `start`/`end_battery_pct`, and their `_est` siblings).
The completeness criterion is the **catalog, not a list**: no relation, index or constraint
owned by this module may have a name beginning `charge_sessions`. Verify with
`SELECT conname FROM pg_constraint WHERE conrelid = 'charging.supercharger_sessions'::regclass`
— the five auto-named CHECKs appear nowhere in this repo, so grep cannot confirm this.

### `manual_charge_entries`

- No other module may read or write this table directly (ai/architecture.md §2).
- All access goes through the `Writer` and `Reader` public Go interfaces.
- The migration file `internal/charging/db/migrations/20260718000001_add_manual_charge_entries.sql`
  is the single schema source of truth (no separate `schema.sql` — ai/go-conventions.md §persistence).
- sqlc generates `package chargingdb` into `internal/charging/db/` from `query.sql`
  against the migration directory. Only `service.go` and `session_writer.go` (inside
  this module) may import it.
- `inferred_capacity_kwh_calc` (MAG-25, charging-add-inferred-capacity,
  `internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`) —
  **engine-generated**: a PostgreSQL `GENERATED ALWAYS AS (...) STORED` column,
  written by nobody. Present (non-`NULL`) only when `start_battery_pct` and
  `end_battery_pct` are both non-`NULL` and `end_battery_pct > start_battery_pct`;
  `NULL` otherwise (an equal delta is a division by zero, a decreasing one a
  negative capacity — design.md D3). Recomputed automatically by the engine on
  every `INSERT`/`UPDATE` through `Writer.Create`/`Writer.Update`; unwritable by
  any caller (`428C9` on a direct attempt).
- **`status`, `energy_source`, `odometer_km`, and a relaxed `energy_added_kwh`**
  (MAG-18/RM33, RM33-charging-add-entry-status,
  `internal/charging/db/migrations/20260829000002_add_entry_status.sql`):
  - `status TEXT NOT NULL DEFAULT 'IN_PROGRESS' CHECK (status IN ('IN_PROGRESS','DONE'))`
    — every pre-existing row backfilled to `IN_PROGRESS` by the column `DEFAULT`
    (deliberate: historical entries surface as unreviewed, design.md D1). The
    required-field set per status lives in Go (`RequiredFieldsFor`), **not** as a
    second DB `CHECK` — a `CHECK` backstop would turn every future change to the
    skip set into a migration (design.md D5).
  - `energy_source TEXT NOT NULL DEFAULT 'USER' CHECK (energy_source IN ('USER','ESTIMATED'))`
    — always computed by this module, never accepted from a caller (design.md D4).
  - `odometer_km INTEGER CHECK (odometer_km >= 0)` — nullable; the odometer
    reading observed at the charge event.
  - `energy_added_kwh` **drops its `NOT NULL`** (was `NOT NULL` before this
    change). `CHECK (energy_added_kwh > 0)` is **retained unchanged** and still
    rejects `0` and every negative — a `CHECK` evaluates `NULL`, not `false`, on
    a `NULL` input, so the same constraint now also accepts `NULL` for free
    (design.md D2).
  - **No index was added on any of the three new columns** — none is
    predicated on by any read query in this tier; an index on a column nothing
    filters/orders/joins by is pure write and storage cost for no read benefit
    (design.md §Index Plan, D9). If a future change needs to filter by `status`,
    the design's revisit trigger is a **partial**, `account_id`-leading index
    over `WHERE status = 'IN_PROGRESS'` — not a standalone `(status)` index.
  - Energy may be **derived on write** via the unexported `packCapacityKWh(ctx,
    vin) (float64, error)` seam in `capacity.go`, which today returns a
    hardcoded `62.0` for every vehicle (`TODO(MAG-18)`). Backlog #18 replaces
    its body with a real per-vehicle lookup — a one-file change by design — and
    that lookup **must filter `WHERE energy_source = 'USER'`** when averaging
    inferred capacities, or it averages this constant back into itself
    (design.md D4/D7).
- **`price_source`** (RM51 tier 1, MAG-58, RM51-charging-derive-status-and-price-source,
  `internal/charging/db/migrations/20260909000001_add_price_source.sql`):
  - `price_source TEXT NOT NULL DEFAULT 'UNCONFIRMED' CHECK (price_source IN ('USER','UNCONFIRMED'))`
    — always computed by this module (`resolvePriceSource` in `service.go`), never
    accepted from a caller.
  - **Not indexed.** Nothing filters, orders, joins, or groups by this column, in this
    tier or any planned one (design.md D4/§Index Plan). If a future change needs to
    filter by `price_source`, the revisit trigger is a **partial**, `account_id`-leading
    index — not a standalone `(price_source)` index.
  - **The `DEFAULT` alone does not implement the price-based rule.** A raw `INSERT` at
    the SQL level that omits `price_source` always lands on `'UNCONFIRMED'`, even for a
    positive `price` — a column `DEFAULT` cannot see another column's value. The
    `price > 0 ⇒ USER` half of the rule is enforced in exactly two places: the
    migration's own one-time backfill `UPDATE` (for rows that existed before this
    change) and `resolvePriceSource` in Go (on every future write, via `Writer`).

### `supercharger_sessions` (renamed from `charge_sessions` in RM39 tier 3, D5b; RM29 tier 6,
RM29-charging-add-charge-sessions)

- No other module may read or write this table directly. Access goes through the
  `SessionWriter` port (write) and, since RM30-charging-add-session-read-port, the
  `SessionReader` port (read) — see §Public Interface above.
- The migration file
  `internal/charging/db/migrations/20260823000001_add_charge_sessions.sql` is the single
  schema source of truth, including its one-time backfill of every Supercharger session
  already collected (guarded so it is a no-op on a `charging`-only database — design.md
  D8a).
- `internal/telemetry.supercharger_history` keeps its own copy of the same three
  battery-percentage columns (`start_battery_pct`, `end_battery_pct`,
  `battery_pct_source`) — permanently, not temporarily. Tier 3 of this roadmap
  (`RM41-telemetry-drop-estimate-columns`, 2026-09-03) dropped telemetry's own
  `start_battery_pct_est`/`end_battery_pct_est` pair, the same drop this change
  performed one tier earlier on `charging.supercharger_sessions`; neither table
  has carried an `_est` column since.
- Column-by-column:
  - `account_id`, `vin`, `session_id`, `charge_start_date_time`, `charge_stop_date_time`,
    `site_location_name` — mirrored, **write-once**: telemetry never refreshes these
    either, so this table doesn't (design.md D1's rule).
  - `tesla_id`, `energy_kwh`, `total_cost`, `currency`, `is_paid` — mirrored,
    **refreshed on every nightly pass** (telemetry's own `ON CONFLICT DO UPDATE SET`
    refreshes them too — fees settle, invoices finalize).
  - `start_battery_pct`, `end_battery_pct`, `battery_pct_source` — **charging-owned**,
    never mirrored, never written by the nightly sync (see §Public Interface above for
    why that is a compile error, not a discipline). Since
    RM31-charging-add-session-verification-port, these three (and only these three) are
    writable through `SessionVerifier.VerifySession` — a human-triggered write, never
    the nightly sync. `battery_pct_source` is always computed by that port, never
    supplied by a caller (design.md D2/D7 of that change). Since MAG-36
    (`charging-add-derived-start-battery-pct`), a `VerifySession` call that leaves
    `start_battery_pct` unsupplied MAY derive it from `energy_kwh` and the supplied end
    percentage — still writable only through this same port, no new writer, no schema
    change (see §Public Interface above). **Accepted trade-off (design.md D1):** a
    derived value is stored under the same `battery_pct_source = 'user_verified'` value
    a human-typed one gets, so the two are indistinguishable in this column — a future
    MAG-18 capacity-averaging implementer over `supercharger_sessions` cannot filter derived
    rows out by provenance alone; read design.md D1 before building that feature rather
    than rediscovering this limitation. Since RM41 tier 4 (MAG-45), a fourth
    charging-owned column, `status`, is computed from these two on every `VerifySession`
    call — see §Public Interface above and the new "Session lifecycle status"
    subsection for the full rule; `status` is not itself mirrored, refreshed, or
    engine-generated, it is Go-computed.
  - Deliberately **not** carried, and the list is closed: `country_code`,
    `unlatch_date_time`, `billing_type`, `vehicle_make_type`, `raw_data` (design.md D1).
  - `inferred_capacity_kwh_calc` (MAG-25, charging-add-inferred-capacity) —
    a **fourth** category, alongside mirrored-write-once / mirrored-refreshed /
    charging-owned: **engine-generated**. Written by nobody — a PostgreSQL
    `GENERATED ALWAYS AS (...) STORED` column
    (`internal/charging/db/migrations/20260829000001_add_inferred_capacity.sql`) —
    recomputed automatically whenever `energy_kwh`, `start_battery_pct` or
    `end_battery_pct` changes, through either `SessionWriter.MirrorSessions`
    (the nightly refresh of `energy_kwh`) or `SessionVerifier.VerifySession`
    (a human correcting the percentages), without either write path naming the
    column. `NULL` when `energy_kwh` is `NULL` (no kWh fee), either percentage is
    `NULL`, or the delta is not strictly positive (design.md D3). The RM29
    "protection by compile error" pattern — `SessionMirror` has no field for the
    battery percentages, so the nightly sync cannot touch them even by mistake —
    is here strengthened to **protection by the database itself**: there is no
    query, port method, or Go code path that can write this column at all.
- sqlc generates the `SuperchargerSession` model and the `MirrorSuperchargerSession`,
  `ListSessionsByVehicleBetween`, and `VerifySuperchargerSession` queries into the same
  `chargingdb` package as `manual_charge_entries`'s queries. Only `session_writer.go`
  may call `MirrorSuperchargerSession`, only `session_reader.go` may call
  `ListSessionsByVehicleBetween`, and only `session_verifier.go` may call
  `VerifySuperchargerSession` (inside this module).

---

## Testing Notes

- **Unit tests** (`charging_test.go`): test derived value-receiver methods (`CostPerKWh`,
  `BatteryDelta`, `SessionDuration`) with no DB and no Tesla API. Run offline as part of
  `go test ./...`.
- **Integration tests** (`db_integration_test.go`, `db_session_integration_test.go`,
  `db_backfill_integration_test.go`, `db_session_reader_integration_test.go`,
  `db_session_verifier_integration_test.go`,
  `db_session_reader_updated_since_integration_test.go`,
  `db_session_reader_by_vehicle_integration_test.go`,
  `db_inferred_capacity_entries_integration_test.go`,
  `db_inferred_capacity_sessions_integration_test.go`): cover full CRUD round-trips,
  ordering guarantees, multi-tenant isolation, CHECK constraint enforcement, the
  Supercharger session mirror (`SessionWriter.MirrorSessions`), the one-time backfill,
  the `supercharger_sessions` read ports (`SessionReader.ListSessionsByVehicleBetween`,
  RM30-charging-add-session-read-port; and
  `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`/
  `ListSessionsByVehicle`, RM31-charging-add-session-read-ports,
  `db_session_reader_updated_since_integration_test.go`/
  `db_session_reader_by_vehicle_integration_test.go`, design.md Test Contract
  T1-T14/T-Order2), and — since RM31-charging-add-session-verification-port — the
  `supercharger_sessions` verification write port (`SessionVerifier.VerifySession`,
  `db_session_verifier_integration_test.go`, design.md Test Contract T1-T9). Reads still
  assert only against `charging.Session` domain fields or direct SQL column values —
  never `pgtype`, in this file or any other.
  Since MAG-25 (charging-add-inferred-capacity),
  `db_inferred_capacity_entries_integration_test.go` and
  `db_inferred_capacity_sessions_integration_test.go` cover
  `inferred_capacity_kwh_calc` on both tables against the change's design.md Test
  Contract Groups A/B (T1-T24) — the guard, the type's overflow safety, the
  column's unwritability, and recomputation on `Writer.Update`,
  `SessionWriter.MirrorSessions`'s nightly refresh, and
  `SessionVerifier.VerifySession`. **Backfill of rows that existed before the
  migration is deliberately NOT covered by an integration test** (design.md D10):
  the package's test database is provisioned fresh with every migration applied
  before any row exists, so there is nothing to backfill in that environment —
  the real check is the owner's post-`migrate-up` query
  (`openspec/changes/charging-add-inferred-capacity/tasks.md` §"Owner
  verification").
  Since MAG-18/RM33 (RM33-charging-add-entry-status), `entry_status_test.go`
  (package `charging`, not `charging_test` — it needs the unexported
  `derivedEnergyKWh`/`packCapacityKWh`) covers `RequiredFieldsFor`'s two sets,
  its defensive-copy and fail-closed properties, the derivation table, and the
  rounding rule, against design.md Test Contract Group A (A1-A5, A7-A9);
  `db_entry_status_integration_test.go` covers the schema/`CHECK` behaviour and
  the `Writer`/`Reader` write-path rules against Groups B and C (B1-B8, C1-C16).
  As with MAG-25, the pre-existing-row **backfill is NOT covered by an
  integration test** for the identical reason (design.md D10 of this change):
  the package's test database is provisioned fresh with every migration
  applied before any row exists, so B1 proves the `DEFAULT` *mechanism* rather
  than the real backfill outcome — the owner confirms the outcome after
  `make migrate-up` (`openspec/changes/RM33-charging-add-entry-status/tasks.md`
  §"Owner verification").
  Since MAG-36 (`charging-add-derived-start-battery-pct`),
  `session_verifier_derivation_test.go` (package `charging`, not `charging_test` —
  both `derivedStartBatteryPct` and `needsDerivedStartBatteryPct` are deliberately
  unexported, mirroring `derivedEnergyKWh`/`packCapacityKWh`'s own privacy invariant,
  the same reason `entry_status_test.go` already lives in package `charging`; design.md
  D11) covers the derivation's two pure functions offline against design.md Test
  Contract Groups A (A1-A7) and B (B1-B5) — no database, no `pool`. This change also
  **corrected two pre-existing `DATABASE_URL`-gated integration tests** whose expected
  values its own new behavior made false (design.md D12): a `TestVerifySession_PartialEndOnlyStillSetsSource`
  expected value in `db_session_verifier_integration_test.go` (now asserts a derived
  `41`, not `nil`), and one table-case's `energyKWh` fixture (`"T15"`, `52.273` →
  `124.0`) in `db_inferred_capacity_sessions_integration_test.go`, keeping that case's
  `want: nil` reached through the out-of-range guard instead of "never touched." Both
  edits are DB-gated and, per `Test-Execution-Policy`, unrun by the assistant that made
  them.
  The test database is provisioned by `testdb_test.go`:
    - When `DATABASE_URL` is set, that managed Postgres is used (CI with a service container,
      or a local DB you've already provisioned).
    - Otherwise `TestMain` starts a disposable `postgres:16-alpine` container via
      `testcontainers-go` and shares one `*pgxpool.Pool` across the whole package. No
      `createdb`/`make migrate-up` step is required; `make check`/`go test ./...` runs the
      integration tests green with zero manual DB setup as long as Docker is running locally.
    - **Since RM29 tier 6, TWO migration DIRECTORIES are applied, in this order:**
      `../telemetry/db/migrations` first, then this module's own `db/migrations` second, via
      `testdb.ProvisionDirs` (not the single-directory `testdb.Provision` this package used
      before). **Why:** the `20260823000001_add_charge_sessions.sql` migration (which
      created what is now `charging.supercharger_sessions`) ships a backfill that reads
      `telemetry.supercharger_sessions`, and `db_backfill_integration_test.go` seeds that
      table and needs it to already exist before the backfill statement runs. This is a
      **path dependency on a migration directory, not a Go import** — no `_test.go` file in
      this package imports `internal/telemetry` (see §Allowed Imports). Same pattern
      `internal/analytics` already uses (`ai/go-conventions.md` §Testing: "more than one
      module's tables → `ProvisionDirs`").
    - Migrations are applied programmatically via the `github.com/pressly/goose/v3` Go API;
      `goose.NewProvider` records applied versions in `goose_db_version`, so re-running against
      a managed DB (`DATABASE_URL` set) is a no-op.
    - Production impact is NONE: the testcontainers/goose imports live only in `_test.go`
      files and are never compiled into the deployed binary; no Docker daemon is required in
      production.
- **The backfill test extracts its statement from the shipped migration at runtime**
  (`db_backfill_integration_test.go`), slicing the file read through the package's existing
  `//go:embed db/migrations/*.sql` between the `-- BACKFILL-BEGIN` / `-- BACKFILL-END`
  sentinel comments, rather than duplicating the SQL as a Go const — so the test can never
  drift from what actually ships to production (design.md D8c).
- **No Tesla API call fires in any test** — this module has no Fleet API dependency; this
  invariant is structural (no import of `internal/tesla`), not just disciplinary.
- The `tesla-exploration` exception (CLAUDE.md) does NOT apply here. Tests for this module are
  welcome and required (no paid-API risk).
- `pgtype` must not appear in any test helper or assertion — test against `charging.Entry` /
  `charging.SessionMirror` / `charging.Session` domain fields and raw SQL column values
  only. `db_session_integration_test.go` (write-path tests, predating the reader) reads
  rows back with direct SQL, scanning nullable columns into plain Go `*T` fields (pgx v5
  supports NULL-into-pointer-to-pointer scanning natively) — never into a
  `chargingdb.SuperchargerSession` (which is all `pgtype`).
  `db_session_reader_integration_test.go` (RM30-charging-add-session-read-port) instead
  asserts against `SessionReader.ListSessionsByVehicleBetween`'s returned
  `charging.Session` values directly — the port's own domain mapping already keeps
  `pgtype` out, so no direct-SQL read-back is needed there.
  `db_session_verifier_integration_test.go` (RM31-charging-add-session-verification-port)
  asserts against `SessionVerifier.VerifySession`'s returned `charging.Session` for the
  columns it changes, and reuses `db_session_integration_test.go`'s existing
  `fetchSuperchargerSession` direct-SQL helper (plain Go `*T` fields, never `pgtype`) for the
  structural bit-identical-column proof (T8). Since RM41 tier 4 (MAG-45), the same file
  also covers the `status` column's full state truth table (Test Contract Group S1-S8,
  `RM41-charging-add-session-status` design.md) and the backfill's identical "DEFAULT
  mechanism only, not the real backfill outcome" exemption already established for
  MAG-25/RM33's own backfilled columns.
  `db_session_reader_updated_since_integration_test.go` and
  `db_session_reader_by_vehicle_integration_test.go` (RM31-charging-add-session-read-ports)
  assert against `SuperchargerSessionAnalyticsReader.ListSessionsByVehicleUpdatedSince`/
  `ListSessionsByVehicle`'s returned `charging.Session` values directly, same as
  `db_session_reader_integration_test.go` — no `pgtype` anywhere in either file.
  `db_session_reader_by_vehicle_integration_test.go`'s T-Order2 case reads `EXPLAIN`
  plan text via plain `string` scanning, not any generated type.
  Since RM51 tier 1 (MAG-58, RM51-charging-derive-status-and-price-source):
  `entry_status_test.go` gained `promoteIfComplete`'s offline unit tests (design.md Test
  Contract A1-A6) — its promotion case, its two single-missing-field blocking cases, its
  two full-`DONE`-set blocking cases (proving the helper reuses the full
  `RequiredFieldsFor(StatusDone)` set), and its never-demote guard. `price_source_test.go`
  (new, package `charging` — `resolvePriceSource` is unexported) covers `resolvePriceSource`
  offline against Test Contract A7-A10: the three price/confirmation branches plus the
  precedence case. `db_promotion_price_source_integration_test.go` (new, package
  `charging_test`, mirrors `db_entry_status_integration_test.go`'s style and reuses its
  Group B helpers) covers Test Contract B1-B3 (the `price_source` `DEFAULT` mechanism, its
  `CHECK`, and that the `DEFAULT` is price-blind — asserted via the shared
  `assertPgErrorCode` helper, SQLSTATE `23514` not message text) and C1-C11 (the
  `price_source` write-path rule and the auto-promotion rule, both through
  `Writer`/`Reader`, including the re-promotion-on-reopen interaction).
