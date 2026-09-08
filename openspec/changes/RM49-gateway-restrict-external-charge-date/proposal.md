Source: MAG-55 — https://linear.app/magus-monitor/issue/MAG-55/external-charges
Roadmap: openspec/roadmaps/RM49-analysis-start-date.md
Tier: 2 of 2 (gateway; tier 1, `RM49-account-add-analysis-start-date`, module
`account`, is already archived — this tier depends on it and consumes its new port
method).

## Why

MAG-55 wants the `/external-charges` page to reject a manual charge entry dated before
the account's analysis start date. A charge from before that day is not part of the
platform's analytics model, so saving one only adds noise the app can never use.

Tier 1 gave every account a stored, explicit start date:
`account.settings.analysis_start_date`, read through the new port method
`account.Service.AnalysisStartDateFor(ctx, accountID) (time.Time, error)`. This tier is
the only consumer of that method. It adds the rejection rule to the one place charge
entries are written: `internal/gateway`'s `/external-charges` create and edit forms.

## What Changes

- **Server-side rejection in `parseExternalChargeForm`** (`internal/gateway/handlers/
  external_charges.go`) — shared by both the create form (`ExternalChargeCreate`) and the
  inline row-edit form (`ExternalChargeRowUpdate`), since both call this one parser. After
  `charged_on` parses into a valid date, the handler reads the account's analysis start
  date via `h.acct.AnalysisStartDateFor(ctx, uid)` — **no new `Deps` field**, since
  `account.Service` already includes this method and `h.acct` is already wired
  (`Deps.Account`). If `charged_on` is before that date, the existing `charged_on`-keyed
  validation-error map gets the new error — the exact same red-label path every other
  field error already uses. An entry dated exactly ON the start date is accepted.
- **A `min` attribute on the date input** (both the create form and the row-edit form) —
  a convenience only; the server check above is the actual rule and runs regardless. A
  new `ExternalChargesPageData.MinChargedOn` field carries the formatted date into both
  templates.
- **Two new i18n catalogue keys**, both ES and EN non-empty: the rejection message
  (carrying the formatted date, mirroring the existing `KeyChargesErrorInProgressExists`
  pattern) and a lookup-failure message for the rare case the account's start date cannot
  be read.
- **`internal/charging` is untouched** — roadmap D5, deliberate. A future non-gateway
  caller of `charging.Writer` can still save a bad date; that is accepted.
- **Docs**: the `/external-charges` page-detail KB guide
  (`kkpa/context/input-port/charging/external-charges.md`) gets a new section describing
  the rule, mirroring its existing "one IN_PROGRESS entry per (vehicle, charged_on)"
  section. `kkpa/context/INDEX.md`'s existing "analysis start date" row is checked and
  updated if this tier's consumer isn't already reflected there.
- **No unit tests** (roadmap D10). This proposal's `design.md` states the exact
  comparison and the boundary case in place of a test's expected values.

**Not breaking.** No exported Go signature changes — `account.Service` is read through an
interface method that already exists (added by tier 1). `ExternalChargesPageData` gains
one field, which is source-additive for any existing caller.

**Affected modules:** `internal/gateway` (implements this tier). `internal/account` is a
read-only dependency (already the case before this tier — no new dependency).
`internal/charging` is explicitly NOT touched (roadmap D5).

**Read path affected:** every `/external-charges` page render and every create/edit
submission now makes one extra call to `account.Service.AnalysisStartDateFor`, which is a
single primary-key lookup on `account.settings.account_id` (tier 1's design, no new
index) — no new database object, no new query, well within the platform's read-heavy
performance profile.

## Capabilities

### New Capabilities

(none — this proposal extends the existing `gateway` capability only)

### Modified Capabilities

- `gateway`: adds a new requirement, "External Charge Date Restricted To Analysis
  Start," describing the server-side rejection on both the create and edit paths, the
  `min` attribute convenience, and the boundary behavior. The existing "Create Charge
  Entry" and "Inline Row Editing" requirements are unchanged in substance — this
  proposal adds one more validation rule to the shared parser both already describe as
  "validates and rejects with a field-level error."

## Impact

- `internal/gateway` — `handlers/external_charges.go` (the new check inside
  `parseExternalChargeForm`, the `MinChargedOn` computation inside
  `buildExternalChargesPage`, and the extra parameter threaded through
  `ExternalChargeRowUpdate`'s two error-render call sites),
  `templates/fragments/external_charges_vm.go` (new `MinChargedOn` field),
  `templates/fragments/external_charge_create_form.templ`,
  `templates/fragments/external_charge_row_edit.templ`,
  `templates/fragments/external_charges_list.templ` (its one call site of
  `ExternalChargeRowEdit` gains the new argument), `i18n/catalog.go` (two new keys).
- `internal/account` — **not touched.** This tier only calls the port method tier 1
  already shipped.
- `internal/charging` — **not touched** (roadmap D5, deliberate).
