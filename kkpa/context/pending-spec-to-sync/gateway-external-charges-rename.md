# Sync proposal — gateway (external-charges rename)

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `input-port/charging/external-charges.md`
Source spec:  `openspec/specs/gateway/spec.md`
Generated:    `2026-09-05`
Status: PENDING REVIEW

> **Filename note.** This proposal is deliberately NOT named `gateway.md`. An earlier, still
> unapplied `gateway.md` proposal (RM41, generated 2026-09-04, target
> `workflows/supercharger-stats-read.md`) is sitting in this folder at `PENDING REVIEW`, and
> `from-spec` overwrites by capability name. Writing here keeps both. Apply them in either order;
> they touch different guides and do not overlap.

> **Routing note.** The `gateway` capability spec is broad (70 requirements). This archive's delta
> is entirely the `/charges` → `/external-charges` page rename, which resolves to the existing
> input-port guide. This proposal extends that one guide. It creates no second entry and moves no
> file.

> **Origin.** `gateway-rename-charges-route` (CH44), archived 2026-09-05. The archive synced ~15
> modified requirements and 1 renamed (`Charge Log Page` → `External Charges Page`) into the
> capability spec.

> **This is a NARROW delta on purpose.** The change updated this guide by hand in the same commit
> (route, endpoint table, component map, symbol names, CSRF key — all already correct). The three
> bullets below are the things the spec now guarantees that the guide does **not** yet record, and
> each is a trap a future agent would otherwise walk into.

---

## [guide] ## Adapter-side conventions — APPEND

- **The page has THREE names, and they are deliberately different.** The sidebar says
  **External** / **Externas** (`i18n.KeyNavExternalCharges`, key `nav.external_charges`); the page
  heading says **External charges** / **Cargas externas** (`i18n.KeyChargesPageTitle`, key
  `charges_page.title`); the route is `/external-charges`. A sidebar entry sits beside its
  siblings under a section heading, so the adjective alone is unambiguous there; a page heading
  stands alone and carries the noun. Do not "fix" the sidebar to match the heading.
  _Source: spec gateway — Requirement: External Charges Page; Requirement: Navigation Items._
- **The i18n keys still say `charges_*`, not `external_charges_*` — this is deliberate, not
  debt.** All 86 keys for this page keep the `charges_form.*` / `charges_error.*` /
  `charges_list.*` / `charges_page.*` prefixes and their `KeyCharges*` Go identifiers. A key is
  never seen by a user and is not reachable from a URL, so renaming them would have added 172
  edits that change nothing observable. Grep `KeyCharges`, not `KeyExternalCharges`. The one
  exception is `nav.manual_records` → `nav.external_charges`, renamed because that name had become
  factually wrong.
  _Source: spec gateway — Requirement: External Charges Page._
- **There is NO redirect from the old `/charges` route — it 404s.** The page is behind
  authentication, is reached only from the sidebar, and had no external caller or indexed URL, so
  a permanent redirect would have been cached by browsers forever to serve a bookmark that may not
  exist. If a stale bookmark ever turns up, adding the redirect is one route line in
  `gateway.go`. Do not assume the old path still resolves when writing a test or a link.
  _Source: spec gateway — Requirement: External Charges Page._

---

<!--
NO [index] BLOCK ON PURPOSE. INDEX.md already routes this page — `External charges page`,
`Externas`, `charges page`, plus `Manual Records` and `Registros manuales` kept as former-label
aliases so a search on the old name still resolves. Nothing to add.

NO [guide] ## Component map BLOCK — spec.md carries behavior, not file paths, and the live
component map was already updated by hand in the same change.
-->
