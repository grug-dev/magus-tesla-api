## Context

`/charges` is the gateway page for **manually entered** charge records — the charges the user
typed in themselves, which is every charge that did **not** happen on a Tesla Supercharger.
Its sibling page `/supercharger-stats` shows the Supercharger side. Both pages read the same
`charging` module, so "charges" alone does not separate them.

The sidebar label was already changed to **External** / **Externas**. This change makes the
URL and the gateway's own symbols agree with that label.

The whole surface is inside `internal/gateway/`. The page reads `charging.Reader`,
`charging.Writer` and `analytics` through their existing ports; none of those ports, their
method names, or the `manual_charge_entries` table are touched. The rename therefore cannot
cross a module boundary.

Current surface, measured (not estimated): 8 routes, 10 files, 8 handler methods, 9 templ
components, 4 view-model types, 8 DOM id families, 1 CSRF key, 1 i18n key identifier.

## Goals / Non-Goals

**Goals:**

- `/external-charges` and `/ui/external-charges/*` serve exactly what `/charges` and
  `/ui/charges/*` served — same handlers, same behaviour, same status codes.
- Every gateway-side name a reader meets on the way from the URL to the template says
  `external charge`, so the URL, the file name, the handler, the component and the DOM id all
  agree. A grep for `external_charge` returns this page and nothing else.
- The `charging` module keeps its name, so a grep for `charging` still returns the domain.

**Non-Goals:**

- No behaviour change. Not one validation rule, status code, swap target, htmx header, or
  rendered value differs before and after.
- No redirect from the old routes.
- No rename of the 86 `charges_*` i18n keys.
- No change to the page's visible title copy.
- No change to the `charging` module, the database, or any port.

## Decisions

### D1 — `external-charges`, plural, hyphenated

The page lists many records, so the noun is plural — matching `/settings` and
`/supercharger-stats`, and unlike the single-view `/dashboard`.

The route is a **noun phrase**, not the bare label. "External" alone is an adjective: `/external`
does not say what it lists, and the Spanish label proves the missing noun — "Externas" is
feminine plural because it agrees with "cargas". `/external-charges` also keeps the word
`charges` in the URL, so the URL still points a reader at the `charging` module.

*Rejected:* `/external` (adjective, says nothing), `/manual-charges` (describes how the row got
there, not what it is — and the row is still "manual" if the user typed in a Supercharger
session), keeping `/charges` (leaves the URL disagreeing with the menu the user just renamed).

### D2 — the gateway names follow the URL; the module names do not

Renaming only the URL would leave `/external-charges` served by `ChargePage` in `charges.go`
rendering `#charges-list`. Every future reader — human or agent — would have to learn that
mapping before making any change to the page, and nothing in the code would state it.

The line is drawn at the **module boundary**: names that describe *this page* follow the URL;
names that come from the *domain* do not. `ChargeEntryVM` becomes `ExternalChargeEntryVM`
because it is a gateway view model of one row on this page. `charging.Entry` stays, because the
module owns every charge, not just the external ones.

*Rejected:* renaming `charging` too — it would touch four modules and a table for a page's URL.

### D3 — the 86 `charges_*` i18n keys stay

An i18n key is not reachable from a URL and is never seen by a user. Renaming
`charges_form.*`, `charges_error.*` and `charges_list.*` would add 172 edits (86 key strings +
86 `KeyCharges*` identifiers) that change no observable thing. Only `nav.manual_records` is
renamed, because that name is now factually wrong: the label it holds says "External".

### D4 — no redirect from `/charges`

The page is behind authentication, is reached only from the sidebar, and has no external
caller or indexed URL. A permanent redirect would be cached by browsers forever to pay for a
bookmark that may not exist. If a stale bookmark turns up, adding the redirect later is one
line.

### D5 — the CSRF session key is renamed with the rest

`csrf_manualcharge` becomes `csrf_externalcharge`. The key is a session-map string, so the old
name would otherwise survive as the only `manualcharge` token left in the module — the exact
kind of orphan name this change exists to remove.

## Risks / Trade-offs

- **A form open in a browser across the deploy fails its next submit** (D5 changes the session
  key, so the stored token is read back as empty). → The failure is the normal CSRF branch: a
  403 and a reload, no data loss. It affects only sessions mid-form at deploy time.

- **A stale bookmark or an open tab on `/charges` gets a 404** (D4). → Accepted; the sidebar
  link is correct from the first page load. Reversible with one route line.

- **A missed reference compiles but breaks at runtime** — an htmx URL or a DOM id is a string,
  so the Go compiler cannot catch a stale one. → Two deterministic checks close this:
  `go build ./... && go vet ./...` (which also compiles `_test.go`) for the Go side, and a
  repo-wide grep asserting that no `/charges`, `/ui/charges`, `charges-list`, `charges-content`,
  `charges-create`, `charges-window` or `charge-row-` literal survives outside
  `openspec/changes/archive/`. The grep is the real gate; the compiler is not enough here.

- **`templ generate` must run** or the `_templ.go` files keep serving the old URLs while the
  `.templ` sources look correct. → It is an explicit task step, and the grep above would catch
  a skipped run because the generated files still hold the old literals.

- **The knowledge base goes stale silently** — `kkpa-context-fetch` presents the KB as
  authoritative, so a guide still saying `/charges` would make a future agent trust it instead
  of reading the code. → The KB files are listed as named tasks in this change, not left as
  follow-up.

## Migration Plan

No data migration. Deploy is a normal binary replacement; the new routes exist the moment the
process starts. Rollback is `git revert` of the single commit — nothing persists across the
change except the session CSRF key, which self-heals on the next page render.

## Open Questions

- The page's visible title is still "Registro de cargas" / "Charge log" while the menu says
  "Externas" / "External". Aligning that copy is user-facing wording and is deliberately left
  out of this change.
