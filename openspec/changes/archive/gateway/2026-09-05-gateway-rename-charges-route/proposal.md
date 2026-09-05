## Why

The sidebar entry for this page was renamed to **External** / **Externas**, but the page still
lives at `/charges` and its whole gateway-side vocabulary still says `charge`/`charges`. The
URL no longer says what the page shows, and it is indistinguishable from the sibling
Supercharger page — both are "charges". A visitor reading `/charges` cannot tell it holds only
the charges the user typed in by hand.

The name is also ambiguous for an agent: `charges` names both this page and the whole
`charging` module, so a grep for "charges" returns the page and the domain mixed together.
Naming the page for what it actually lists — charges made **outside** the Supercharger network —
removes the ambiguity in the URL and in the gateway's own symbols.

## What Changes

- **BREAKING (URL):** the page and its seven fragment routes move.

  | Before | After |
  |---|---|
  | `GET /charges` | `GET /external-charges` |
  | `GET /ui/charges` | `GET /ui/external-charges` |
  | `GET /ui/charges/list` | `GET /ui/external-charges/list` |
  | `GET /ui/charges/row/:id` | `GET /ui/external-charges/row/:id` |
  | `GET /ui/charges/row/:id/edit` | `GET /ui/external-charges/row/:id/edit` |
  | `POST /ui/charges/create` | `POST /ui/external-charges/create` |
  | `PUT /ui/charges/row/:id` | `PUT /ui/external-charges/row/:id` |
  | `DELETE /ui/charges/row/:id` | `DELETE /ui/external-charges/row/:id` |

  No redirect from the old paths is added — this is a signed-in internal page with no external
  callers and no search-engine surface.

- The gateway's own names follow the URL: 10 file renames, 8 handler methods, 9 templ
  components, 4 view-model types, 11 DOM ids, and the CSRF session key.
- `nav.manual_records` becomes `nav.external_charges`. Its ES/EN values are already
  "Externas"/"External" and do not change.
- The page's visible heading follows the label too: `charges_page.title` changes from
  "Registro de cargas" / "Charge log" to **"Cargas externas" / "External charges"**. The key
  identifier itself stays `KeyChargesPageTitle` — only the two values change. The sidebar keeps
  the shorter "Externas" / "External"; the page heading carries the full noun phrase.

**Explicitly NOT changed:**

- The `charging` module, `charging.Entry`, the `manual_charge_entries` table, and every port
  the page calls. The module owns *all* charging; this page is one view over part of it.
- The 86 `charges_*` i18n keys and their `KeyCharges*` Go identifiers. They are invisible to
  the user and to the URL, so renaming them would double the diff and buy nothing.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `gateway`: every requirement that names a `/charges` route, a `/ui/charges*` fragment route,
  one of the page's DOM ids, or the page by its old display name "Charge log" now names its
  `external-charges` / "External charges" equivalent. The requirement **Charge Log Page** is
  renamed to **External Charges Page**. No behaviour, status code, validation rule, or scenario
  outcome changes — only the identifiers and the page's display name.

## Impact

**Code — `internal/gateway/` only.**

- `gateway.go` — the 8 route registrations.
- `handlers/` — `charges.go`, `charges_range.go`, `charges_tiles.go` renamed to
  `external_charges*.go`; the 8 exported handler methods renamed; `csrfManualChargeKey` /
  `"csrf_manualcharge"` renamed.
- `templates/pages/charges.templ` and the five `fragments/charge*` files renamed; 9 templ
  components and 4 view-model types renamed; 11 DOM ids renamed.
- `templates/layouts/nav.go` — `Href` and the `Active` comparison.
- `i18n/catalog.go` — one key identifier, one key string, and the `charges_page.title`
  ES/EN value pair.
- 4 `handlers/*_test.go` files renamed and updated, plus `lang_test.go` and
  `templates/ui/nav_shell_test.go`.

**Deliberate side effect.** Renaming the CSRF session key invalidates any charge form left
open in a browser across the deploy. The next submit fails the CSRF check once and the user
reloads. Accepted: the alternative is carrying a dead key name forever.

**No impact.** No database change, no migration, no `sqlc` input, no change to any module port,
and nothing outside `internal/gateway/`. `MIGRATIONS_DIRS`, `db-setup`/`db-reset`, and every
`make` guard are unaffected — verified, not assumed.

**Docs updated in this same change** (project rule): `internal/gateway/AGENTS.md`,
`internal/charging/AGENTS.md`, the `handlers/charges.go` path comments in
`internal/analytics/analytics.go` and `internal/charging/charging.go`, and the knowledge base —
`kkpa/context/INDEX.md`, `input-port/charging/charges.md` (renamed to `external-charges.md`),
`workflows/manual-charge-crud.md`, `architecture/charge-record-mutation.md`, and the two
`use-case/charging/` files.
