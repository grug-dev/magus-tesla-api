## Context

`internal/gateway`'s `/external-charges` page lets a signed-in user log a manual Tesla
charge. Both writes it supports — create (`ExternalChargeCreate`) and inline row edit
(`ExternalChargeRowUpdate`) — call one shared parser, `parseExternalChargeForm`
(`internal/gateway/handlers/external_charges.go`), which builds a `charging.Entry` and a
`map[string]string` of field-keyed validation errors. The error map's keys are
`charging.Field` string values (`charging.FieldChargedOn == "charged_on"`), and the
create/edit templates already read `validationErrors["charged_on"]` into
`ui.FieldProps.Error` for the "Date" field — this is the red-label path roadmap D8 says
to reuse.

Tier 1 (`RM49-account-add-analysis-start-date`, archived) added
`account.Service.AnalysisStartDateFor(ctx, accountID) (time.Time, error)`, confirmed by
reading `internal/account/account.go`:

```go
// AnalysisStartDateFor returns the account's analysis start date: the first
// calendar day (America/Bogota) the platform analyzes this account's vehicle
// data. It never returns a zero time for a valid account — every account has
// exactly one settings row (RM42 D3/D4), and this column is NOT NULL. Mirrors
// LanguageFor/ThemeFor exactly: it only errors on an actual lookup failure
// (unknown accountID, DB error), never because of the stored value's shape.
AnalysisStartDateFor(ctx context.Context, accountID uuid.UUID) (time.Time, error)
```

This is one method on `account.Service` — the interface the gateway already depends on
as `Deps.Account` / `h.acct` (`internal/gateway/handlers/handlers.go`). No new `Deps`
field, no new import, no new module dependency: this tier is a pure consumer of a port
that already reaches the gateway.

Performance profile: **read-heavy** (`ai/architecture.md` §7). `AnalysisStartDateFor` is
backed by `PreferencesFor`, tier 1's single primary-key lookup on
`account.settings.account_id` — no new query, no new index, and this tier adds no
database object of its own (the `database` design gate does not apply here).

## Goals / Non-Goals

**Goals:**
- Reject a manual charge entry whose `charged_on` is before the account's analysis start
  date, on both the create and edit paths, server-side, with no way to bypass it from the
  browser.
- Reuse the existing `charged_on`-keyed validation-error map and red-label path exactly —
  no second error mechanism (roadmap D8).
- State the exact comparison and the boundary case precisely, since this roadmap ships no
  tests (D10) — this document is what a human (or a later test author) checks against.
- Give the date input a `min` attribute as a convenience, without ever treating it as the
  actual rule.

**Non-Goals:**
- Anything under `internal/account` or `internal/charging`. Tier 1 already shipped the
  storage and the read port; `internal/charging` is explicitly not touched (roadmap D5) —
  a future non-gateway caller of `charging.Writer` can still write a bad date.
- Comparing `StartedAt`. Only `Entry.ChargedOn` is checked (roadmap D6) — `StartedAt` is
  optional and unused by analytics.
- A settings-page field to change the date, or a write port for it (roadmap D7).
- Unit tests (roadmap D10). This design's "Rejection Contract" below stands in their
  place.

## Decisions

### D1 — Where the check runs: inside `parseExternalChargeForm`, after `charged_on` parses

The check is added inside `parseExternalChargeForm`, immediately after the existing
`charged_on` parse block, and **only when that parse already succeeded** (an empty or
malformed date already has its own error and short-circuits this one):

```go
chargedOnStr := c.PostForm("charged_on")
var chargedOn time.Time
if chargedOnStr == "" {
    errs["charged_on"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorDateRequired)
} else {
    var parseErr error
    chargedOn, parseErr = time.Parse("2006-01-02", chargedOnStr)
    if parseErr != nil {
        errs["charged_on"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorInvalidDateFormat)
    } else {
        // RM49 tier 2 (MAG-55): reject a charge dated before the account's
        // analysis start date. Only reached when charged_on itself parsed
        // cleanly. See design.md D1-D3.
        minDate, err := h.acct.AnalysisStartDateFor(c.Request.Context(), uid)
        if err != nil {
            log.Printf("gateway: AnalysisStartDateFor error for account %s: %v", uid, err)
            errs["_top"] = i18n.T(c.Request.Context(), i18n.KeyChargesErrorCouldNotValidateAnalysisStartDate)
        } else if chargedOn.Before(minDate) {
            errs["charged_on"] = fmt.Sprintf(
                i18n.T(c.Request.Context(), i18n.KeyChargesErrorDateBeforeAnalysisStart),
                minDate.Format("2006-01-02"),
            )
        }
    }
}
```

Because `parseExternalChargeForm` is the single shared parser for both create and edit,
this one edit covers both paths (roadmap tier prompt: "reuse the existing... map"; no
duplicate logic in `ExternalChargeRowUpdate`).

**Rejected alternative — check in the handler, after `parseExternalChargeForm` returns.**
Would need `uid` and `entry.ChargedOn` again at two call sites (`ExternalChargeCreate`,
`ExternalChargeRowUpdate`) instead of one, duplicating the read and the comparison.
Putting it inside the shared parser is one edit instead of two, and keeps every
`charged_on` rule (required, format, now this one) in the same place a future reader
already looks (AI-efficiency: change-locality).

### D2 — The exact comparison, and the boundary case

`chargedOn` comes from `time.Parse("2006-01-02", chargedOnStr)` — Go's `time.Parse`
with no zone in the layout returns a `time.Time` at UTC, midnight, for that calendar
day. `minDate` comes from `AnalysisStartDateFor`, which returns
`Settings.AnalysisStartDate` — itself `row.AnalysisStartDate.Time`, the `.Time` field of
a `pgtype.Date` read from a Postgres `DATE` column. `pgtype.Date`'s storage encoding is
UTC midnight of the calendar day (`ai/go-conventions.md`'s standing `pgtype.Date`
UTC-midnight exemption to the `internal/clock` time-zone rule) — the same shape
`chargedOn` is already in.

**Both values are therefore UTC-midnight `time.Time` values representing a calendar
day, with no wall-clock component to normalize.** A plain `chargedOn.Before(minDate)` is
correct with no additional conversion:

- `chargedOn.Before(minDate)` → **true** when `charged_on`'s calendar day is strictly
  earlier than the analysis start date → **rejected**.
- `chargedOn.Before(minDate)` → **false** when the two calendar days are equal (Go's
  `Before` is a strict `<`, so equal times compare false) → **accepted**. This is the
  boundary case the roadmap requires explicitly: **an entry dated exactly ON the
  analysis start date is accepted.**
- `chargedOn.Before(minDate)` → **false** when `charged_on` is any later day →
  **accepted**.

### D3 — Lookup failure fails closed, through the existing `_top` slot

`AnalysisStartDateFor` "only errors on an actual lookup failure ... never because of the
stored value's shape" (its own doc comment) — every account is guaranteed exactly one
settings row (`RM42` D3/D4, restated by the account spec's "Settings Row Guaranteed At
Account Creation" requirement), so this branch is not expected to fire in normal
operation. On the rare DB-level failure, the handler logs it and sets `errs["_top"]` —
the same "top of form" slot `resolveSelectedVehicle`'s own failure path already uses
(`i18n.KeyChargesErrorSelectVehicle`, a few lines above in the same function) — rather
than a 500 that would discard the user's other typed values. This **fails closed**: any
error here means `len(errs) > 0`, so `ok` is false and nothing is written, exactly as a
`charged_on`-keyed rejection would.

**Rejected alternative — skip the check on a lookup error (fail open).** Would let a
transient DB hiccup silently bypass the one rule this whole tier exists to add. Failing
closed costs the user a retry on that error path; failing open costs the entire feature
its guarantee.

### D4 — `MinChargedOn`: computed once per page render, threaded to both templates

`ExternalChargesPageData` (`internal/gateway/templates/fragments/external_charges_vm.go`)
gains one field:

```go
// MinChargedOn is the account's analysis start date, pre-formatted "YYYY-MM-DD" by
// the handler, for the charged_on date input's min attribute (roadmap D8) — a
// convenience only. Empty when the lookup failed (rare); the server-side check in
// parseExternalChargeForm is the actual rule and runs regardless of this value.
MinChargedOn string
```

`buildExternalChargesPage` computes it **once, at the top of the function, before the
`teslaIDFilter == 0` early return** — the value only needs `uid`, not a resolved vehicle,
so it must not depend on one; the create form renders (and needs a `min`) even in the
no-vehicle-selected empty state:

```go
func (h *Handler) buildExternalChargesPage(ctx context.Context, uid uuid.UUID, csrfToken string, teslaIDFilter int64, today, start, end time.Time) fragments.ExternalChargesPageData {
    minChargedOn := ""
    if minDate, err := h.acct.AnalysisStartDateFor(ctx, uid); err != nil {
        log.Printf("gateway: AnalysisStartDateFor error for account %s: %v", uid, err)
    } else {
        minChargedOn = minDate.Format("2006-01-02")
    }

    if teslaIDFilter == 0 {
        return fragments.ExternalChargesPageData{
            CSRFToken:      csrfToken,
            EmptyState:     true,
            NoFilterChrome: true,
            MinChargedOn:   minChargedOn,
        }
    }
    // ... unchanged ...
    return fragments.ExternalChargesPageData{
        // ... unchanged fields ...
        MinChargedOn: minChargedOn,
    }
}
```

`ExternalChargeRowUpdate` computes its own `minChargedOn` once, the same way it already
computes `windowStartStr`/`windowEndStr` once and reuses them across both its error
branches ("Computed once and reused by every branch below" — the function's existing
comment):

```go
minChargedOn := ""
if minDate, err := h.acct.AnalysisStartDateFor(c.Request.Context(), uid); err != nil {
    log.Printf("gateway: AnalysisStartDateFor error for account %s, id %s: %v", uid, id, err)
} else {
    minChargedOn = minDate.Format("2006-01-02")
}
```

**`fragments.ExternalChargeRowEdit` gains a sixth parameter**, `minChargedOn string`,
appended after `windowStartStr, windowEndStr` (mirroring how those two were themselves
appended to the function's earlier signature):

```go
templ ExternalChargeRowEdit(vm ExternalChargeEntryVM, csrfToken string, validationErrors map[string]string, windowStartStr, windowEndStr, minChargedOn string) {
```

All three call sites pass it:
- `internal/gateway/handlers/external_charges.go:431` (422 branch) →
  `fragments.ExternalChargeRowEdit(vm, csrfToken, validationErrors, windowStartStr, windowEndStr, minChargedOn)`
- `internal/gateway/handlers/external_charges.go:456` (500 branch) → same, with the
  `_top`-keyed error map.
- `internal/gateway/templates/fragments/external_charges_list.templ:69` →
  `@ExternalChargeRowEdit(vm, d.CSRFToken, nil, d.WindowStartStr, d.WindowEndStr, d.MinChargedOn)`.

**Rejected alternative — put `MinChargedOn` on `ExternalChargeEntryVM` (per-row) instead
of threading it as a page-level parameter.** `MinChargedOn` is one value for the whole
page/account, not per-row — repeating it on every row VM would be redundant storage for
no benefit. The existing `windowStartStr`/`windowEndStr` pair already establishes the
"page-level value threaded as an extra function parameter" pattern for
`ExternalChargeRowEdit`; mirroring it is cheaper for a future reader than inventing a
second shape (AI-efficiency: one closed vocabulary, not two).

**Empty-string degradation.** When `MinChargedOn`/`minChargedOn` is `""` (the rare
lookup-failure case), the template still renders `min=""` — an HTML5 date input treats
an empty or invalid `min` as no constraint at all, so this degrades to exactly
"convenience not applied," never to a broken or blocking input. No conditional Attrs
construction is needed for this case (AI-efficiency: no helper function earns its keep
for a browser-native no-op).

### D5 — Template wiring: one `Attrs` entry on the existing `ui.Input`, no new component

Both templates already render `charged_on` through `ui.Input` with no `Attrs` today:

```templ
@ui.Field(ui.FieldProps{Label: i18n.T(ctx, i18n.KeyChargesFormDate), Error: validationErrors["charged_on"]}) {
    @ui.Input(ui.InputProps{Type: "date", Name: "charged_on", Value: d.DefaultChargedOn, Required: true})
}
```

`ui.InputProps.Attrs` (`templ.Attributes`) already exists precisely for "min/max/step/
placeholder/etc." (its own doc comment) — the energy and price fields two lines below
already use it (`Attrs: templ.Attributes{"step": "0.001", "min": "0.001"}`). This tier
adds one key, no new prop, no `ui/` kit change:

```templ
@ui.Input(ui.InputProps{Type: "date", Name: "charged_on", Value: d.DefaultChargedOn, Required: true, Attrs: templ.Attributes{"min": d.MinChargedOn}})
```

and the row-edit template's mirror, sourced from the new `minChargedOn` parameter:

```templ
@ui.Input(ui.InputProps{Type: "date", Name: "charged_on", Value: vm.RawChargedOn, Required: true, Attrs: templ.Attributes{"min": minChargedOn}})
```

### D6 — i18n: two new keys, both mirroring an existing pattern exactly

Added to `internal/gateway/i18n/catalog.go`, `charges_error` group:

```go
// KeyChargesErrorDateBeforeAnalysisStart carries a single %s verb for the account's
// analysis start date, formatted YYYY-MM-DD by the handler — mirrors
// KeyChargesErrorInProgressExists's exact shape.
KeyChargesErrorDateBeforeAnalysisStart Key = "charges_error.date_before_analysis_start"

// KeyChargesErrorCouldNotValidateAnalysisStartDate is the design.md D3 lookup-failure
// message, mirroring KeyChargesErrorCouldNotValidateVehicleOwnership's shape.
KeyChargesErrorCouldNotValidateAnalysisStartDate Key = "charges_error.could_not_validate_analysis_start_date"
```

```go
KeyChargesErrorDateBeforeAnalysisStart:            {ES: "La fecha no puede ser anterior al %s, el inicio del análisis de tu cuenta.", EN: "Date cannot be before %s, when your account's analysis starts."},
KeyChargesErrorCouldNotValidateAnalysisStartDate:  {ES: "no se pudo validar la fecha de inicio del análisis", EN: "could not validate analysis start date"},
```

Exact wording may be refined during implementation as long as both languages stay
non-empty and the `%s` verb is preserved in the first key — `TestCatalog_
AllKeysHaveBothLanguages` and `make i18n-guard` are the enforcement, not this document's
literal strings.

## Rejection Contract (stands in for a test's expected values — roadmap D10)

No `_test.go` work ships in this tier. This table is what a human check, or a future
test author, verifies by hand:

| `analysis_start_date` (Bogota) | Submitted `charged_on` | Result | `errs["charged_on"]` |
|---|---|---|---|
| `2026-01-01` | `2025-12-31` | **Rejected**, HTTP 422 | `KeyChargesErrorDateBeforeAnalysisStart` formatted with `2026-01-01` |
| `2026-01-01` | `2026-01-01` | **Accepted** (boundary case) | none |
| `2026-01-01` | `2026-01-02` | **Accepted** | none |
| `2026-01-01` | `` (empty) | **Rejected**, HTTP 422 | `KeyChargesErrorDateRequired` (pre-existing rule; this tier's check never runs) |
| `2026-01-01` | `not-a-date` | **Rejected**, HTTP 422 | `KeyChargesErrorInvalidDateFormat` (pre-existing rule; this tier's check never runs) |
| lookup fails (DB error) | `2026-01-01` | **Rejected**, HTTP 422 | none (`errs["_top"]` set instead — see D3) |

Additional, non-tabular assertions:

- The rejection applies identically on `ExternalChargeCreate` (create) and
  `ExternalChargeRowUpdate` (inline edit) — both call `parseExternalChargeForm`, so one
  code path covers both (D1).
- `charging.Writer.Create` / `.Update` is never called when this check rejects — the
  existing `if !ok2 { ... return }` short-circuit in both handlers already guarantees
  this for every `charged_on`-keyed (and every other) validation error; this tier adds
  no new short-circuit.
- On a **fresh page load** (`GET /external-charges`), the create form's date input
  carries `min="2026-01-01"` in this example (D4/D5) — never enforced by the browser
  alone; the table above is what actually governs.
- `StartedAt`/`EndedAt` are never compared against the analysis start date (roadmap D6) —
  a `started_at` before the limit is stored unchanged if `charged_on` itself is on/after
  the limit.

## Reverse-Direction Check (CLAUDE.md "Workflow & architectural decisions" rule)

This tier changes no database schema, no codegen input, and no module layout — it adds
one Go-level read call, one struct field, one template attribute, and two catalogue
entries, all inside `internal/gateway`. Checked and found not applicable:

- `MIGRATIONS_DIRS`, `db-setup`/`db-reset`, `sqlc.yaml` — no migration, no schema change,
  no sqlc query touched by this tier. Nothing to verify.
- `make migration-guard` — no new migration file exists for this tier to collide with.
- `make boundary-guard` — unaffected; this tier does not import `internal/telemetry` and
  introduces no new cross-module import (`internal/account` was already imported).
- `make tz-guard` — the new code calls `AnalysisStartDateFor`, an existing `account.
  Service` method, and formats its returned `time.Time` with `.Format("2006-01-02")` —
  no raw `time.Now()`, no hardcoded zone string, no UTC-midnight hand-roll is introduced.
  Passes with no escape hatch needed.
- `make i18n-guard` / `TestCatalog_AllKeysHaveBothLanguages` — both new keys are added
  through `i18n.T(...)` calls and the catalogue map, exactly like every existing key; no
  hardcoded string is introduced.
- `make ui-guard` — no raw DaisyUI component class is introduced; the one new markup
  change is an `Attrs` entry on an existing `ui.Input` call, which is exactly the
  sanctioned use of that prop.

## Test Contract commands (owner-run, per Test-Execution-Policy)

No new `_test.go` file ships in this tier (roadmap D10). The owner may still run the
full suite to confirm nothing broke:

```bash
go build ./...
go vet ./...
gofmt -l .
make ui-guard
make i18n-guard
make templ
make css
go test ./...
make test-with-db
```
