## ADDED Requirements

### Requirement: External Charge Date Restricted To Analysis Start

The gateway SHALL reject a manual charge entry whose `charged_on` date is before the
account's analysis start date, on both the create form (`POST
/ui/external-charges/create`) and the inline row-edit form (`PUT
/ui/external-charges/row/:id`). The gateway SHALL read the account's analysis start date
through the `account` module's public interface
(`account.Service.AnalysisStartDateFor`), never by any other means. The rejection SHALL
be reported through the same `charged_on`-keyed validation-error mechanism the page
already uses for every other `charged_on` validation rule (a required date, a malformed
date). An entry dated exactly ON the analysis start date SHALL be accepted. The
comparison SHALL consider only `charged_on` — an entry's optional `started_at` value
SHALL NOT be compared against the analysis start date.

The date input on both the create form and the row-edit form SHALL carry an HTML `min`
attribute set to the account's analysis start date, as a convenience only. This
attribute SHALL NOT be the enforcement mechanism — the server-side rejection above SHALL
apply regardless of the submitted value, including a submission that bypasses or
overrides the `min` attribute in the browser.

#### Scenario: Create form rejects a charge dated before the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2025-12-31` and every
  other field valid
- **THEN** the handler does NOT call `charging.Writer.Create`
- **AND** the create form fragment is re-rendered at HTTP 422
- **AND** an error message naming the analysis start date is shown alongside the
  `charged_on` field
- **AND** the other submitted values are pre-filled in the re-rendered form

#### Scenario: Create form accepts a charge dated exactly on the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2026-01-01` and every
  other field valid
- **THEN** the handler calls `charging.Writer.Create`
- **AND** the new entry appears in the updated charge list

#### Scenario: Create form accepts a charge dated after the analysis start date

- **GIVEN** a signed-in user whose account's analysis start date is `2026-01-01`
- **WHEN** they submit the create form with `charged_on` set to `2026-03-15` and every
  other field valid
- **THEN** the handler calls `charging.Writer.Create`

#### Scenario: Inline row edit rejects a charge dated before the analysis start date

- **GIVEN** a signed-in user editing an existing entry, whose account's analysis start
  date is `2026-01-01`
- **WHEN** they submit the edit form with `charged_on` changed to `2025-06-01`
- **THEN** the handler does NOT call `charging.Writer.Update`
- **AND** the row-edit fragment is re-rendered at HTTP 422 with the same
  `charged_on`-keyed error the create form uses
- **AND** the other submitted values on that row are preserved

#### Scenario: The rejection reads the analysis start date through the account module's public interface

- **GIVEN** a create or edit submission whose `charged_on` needs to be checked against
  the analysis start date
- **WHEN** the gateway performs the check
- **THEN** it calls `account.Service.AnalysisStartDateFor(ctx, accountID)`
- **AND** it does not read `internal/account`'s tables or internals by any other means

#### Scenario: internal/charging is not involved in the rejection

- **GIVEN** the analysis-start-date rejection rule
- **WHEN** a manual charge entry is validated
- **THEN** the rejection is decided and enforced entirely inside `internal/gateway`
- **AND** `internal/charging`'s `Writer`/`Reader` interfaces and validation rules are
  unchanged by this rule

#### Scenario: started_at is not compared against the analysis start date

- **GIVEN** a signed-in user submitting a create or edit form where `charged_on` is on or
  after the account's analysis start date, but the optional `started_at` value is before
  it
- **WHEN** the form is submitted
- **THEN** the submission is NOT rejected for this reason
- **AND** the entry is persisted with the submitted `started_at` value unchanged

#### Scenario: The date input carries a min attribute reflecting the analysis start date

- **GIVEN** a signed-in user opens the External charges page, whose account's analysis
  start date is `2026-01-01`
- **WHEN** the create form's `charged_on` date input is rendered
- **THEN** the input carries `min="2026-01-01"`
- **AND** the same `min` attribute is present on the `charged_on` input of a row opened
  for inline editing

#### Scenario: The min attribute is a convenience, not the enforcement

- **GIVEN** a signed-in user whose browser is made to submit a `charged_on` value earlier
  than the date input's `min` attribute (e.g. via a modified request, bypassing the
  browser's own constraint validation)
- **WHEN** the server receives the submission
- **THEN** the server-side rejection above still applies
- **AND** the entry is still not persisted
