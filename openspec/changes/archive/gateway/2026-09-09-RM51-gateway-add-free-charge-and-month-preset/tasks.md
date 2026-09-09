# Tasks — RM51-gateway-add-free-charge-and-month-preset

Ownership legend: **[module: gateway worker]** — inside `internal/gateway/` (plus the KB path
explicitly granted below) only. **[leader]** — outside the module sandbox. **[owner]** — the
human. No design gate on this tier (design.md's banner) — no task below is blocked on a design
confirmation.

## Ordering constraints

- **The checkbox work (Groups 1–2) and the preset work (Group 3) touch disjoint files, except
  `internal/gateway/i18n/catalog.go`, which both add one key to.** They may be implemented in
  parallel by separate agents; only the catalog.go edit needs a moment of care (two additive key
  insertions — a straightforward merge, not a real conflict, since neither task edits the other's
  key).
- **Group 2 depends on Group 1** — the create form and edit row templates reference
  `ui.Checkbox`, which must exist first, or `templ generate` fails to compile.
- **Group 3 has no dependency on Groups 1–2 at all.** It may start immediately, in parallel with
  Group 1.
- **Group 4 (tests) depends on Groups 1–3 being done** — `go vet` needs the real types and
  functions to compile the test files against (`ai/go-conventions.md` §Testing authoring order:
  pure/offline tests are written early, but they still cannot compile before the code they test
  exists). Expected values are already fixed in design.md §Test Contract — implement tests
  against that contract, not against whatever the code happens to produce.
- **Within Group 4, 4.1 and 4.2 are pure-function/offline tests (no DB, no `httptest`) and run
  first.** 4.3 uses `httptest` with `Deps` fakes — no real container, no `DATABASE_URL` gate, but
  it depends on 4.1/4.2's fixtures existing in the same test files in some cases, so it runs last
  within the group. **This tier has no container-backed (`DATABASE_URL`-gated) test at all** —
  every assertion is either a pure function or an `httptest` call against fakes, matching
  `internal/gateway`'s standing test shape (`AGENTS.md` §Testing).
- **Group 5 (docs) has no code dependency** — it may be done any time after Groups 1–3, but
  logically last so it describes what actually shipped.

---

## Group 1 — `ui.Checkbox` component (no dependency)

- [x] **1.1** **[module: gateway worker]** Create `internal/gateway/templates/ui/checkbox.templ`
  with `CheckboxProps{Name, Label, Checked, Class, Attrs}` and the `Checkbox` component, exactly
  as design.md §D-Checkbox specifies — a `<label class="label cursor-pointer justify-start
  gap-2">` wrapping a `<input type="checkbox" class="checkbox">` and a `<span class="label-text">`
  for the label text. Run `make templ` (regenerates `checkbox_templ.go`).
  `depends_on`: — · `parallel_ok`: with Group 3

## Group 2 — wire the "this charge was free" checkbox (depends on 1.1)

- [x] **2.1** **[module: gateway worker]** Edit
  `internal/gateway/templates/fragments/external_charges_vm.go`: add `PriceConfirmed bool` to
  `ExternalChargeFormValues` (design.md §D-Echo) and `RawPriceConfirmed bool` to
  `ExternalChargeEntryVM`, each placed next to its sibling `Raw*`/status fields, with a doc
  comment following the file's existing style. Update `ExternalChargeFormValues`'s own doc
  comment to note the one exception to "every field is the literal `c.PostForm(name)` string" —
  this one field is a parsed `bool` (checkbox presence, not a string), per design.md Context
  fact 4.
  `depends_on`: — · `parallel_ok`: with 2.3, 3.1, 3.2

- [x] **2.2** **[module: gateway worker]** Edit `internal/gateway/handlers/external_charges.go`:
  - `parseExternalChargeForm` — add `PriceConfirmed: c.PostForm("price_confirmed") != ""` to the
    `raw` literal (built FIRST, alongside every other field, design.md Context fact 3), and set
    `entry.PriceConfirmed = raw.PriceConfirmed` alongside the other `charging.Entry{...}` field
    assignments. **No validation branch** — a checkbox has only two wire states (design.md
    §D-Parse). Do NOT special-case `entry.Price > 0` here — `charging` already ignores the intent
    in that case (tier 1); duplicating the check here is out of scope and against the boundary
    rule.
  - `externalChargeEntryVMFromEntry` — add `RawPriceConfirmed: e.Price == 0 && e.PriceSource ==
    charging.PriceSourceUser` to the returned `fragments.ExternalChargeEntryVM{...}` literal
    (design.md §D-Echo's edit-row formula, verbatim).
  - `externalChargeEntryVMFromRawValues` — add `RawPriceConfirmed: raw.PriceConfirmed` to its
    returned literal.
  `depends_on`: 2.1 (needs the new struct fields to exist) · `parallel_ok`: with 2.3

- [x] **2.3** **[module: gateway worker]** Add `charges_form.price_confirmed` to
  `internal/gateway/i18n/catalog.go`: `Key = "charges_form.price_confirmed"`, `{ES: "Esta carga
  fue gratis", EN: "This charge was free"}` — same-line `ES`/`EN`, mirroring every existing
  `charges_form.*` entry.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.2, 3.1

- [x] **2.4** **[module: gateway worker]** Edit
  `internal/gateway/templates/fragments/external_charge_create_form.templ`: inside the existing
  price `ui.Field` block, add `@ui.Checkbox(ui.CheckboxProps{Name: "price_confirmed", Label:
  i18n.T(ctx, i18n.KeyChargesFormPriceConfirmed), Checked: d.FormValues.PriceConfirmed, Class:
  "mt-1"})` as a second child, right after the `ui.Input` (design.md §D-Checkbox — inside the
  SAME field, not a new grid cell). Mirror the identical change in
  `external_charge_row_edit.templ`'s price field, with `Checked: vm.RawPriceConfirmed`. Run `make
  templ && make css`.
  `depends_on`: 1.1 (component must exist), 2.1 (struct fields), 2.3 (i18n key) · `parallel_ok`:
  with 3.1, 3.2

## Group 3 — "last month" date preset (independent of Groups 1–2)

- [x] **3.1** **[module: gateway worker]** Add `charges_range.last_month` to
  `internal/gateway/i18n/catalog.go`: `Key = "charges_range.last_month"`, `{ES: "Mes pasado", EN:
  "Last month"}` — same-line, mirroring `KeyChargesRangeLast7Days`/`KeyChargesRangeThisMonth`.
  `depends_on`: — · `parallel_ok`: with 2.1, 2.2, 2.3, 3.2

- [x] **3.2** **[module: gateway worker]** Edit
  `internal/gateway/handlers/external_charges_range.go`'s `buildExternalChargesPresets`: add
  `prevStart := startOfMonth(today).AddDate(0, -1, 0)` and `prevEnd := endOfMonth(prevStart)`,
  and append a third `fragments.RangePreset` entry using `i18n.KeyChargesRangeLastMonth`, exactly
  as design.md §D-Order specifies — **appended after** the existing two entries, same
  exact-match `Active` rule, no reordering of "Last 7 days"/"This month". Do **not** touch
  `buildSuperchargerPresets` (`supercharger.go`) — RD8.
  `depends_on`: 3.1 (i18n key) · `parallel_ok`: with Group 1, Group 2

## Group 4 — tests, against design.md §Test Contract (depends on Groups 1–3)

- [x] **4.1** **[module: gateway worker, offline/pure]** In
  `internal/gateway/handlers/external_charges_test.go`, add cases **A1–A2** (checkbox parsing:
  checked → `true`, absent → `false`) and **B1–B4** (httptest-level: unconditional pass-through
  regardless of price, 422 checked-echo on both create and edit forms) exactly as design.md
  §Test Contract Groups A and B specify. Assert the `checked` HTML attribute on the
  `price_confirmed` input — never markup order, class strings, or copy text
  (`AGENTS.md` §"Do not test what the page looks like").
  `depends_on`: 2.1, 2.2, 2.4 · `parallel_ok`: with 4.2

- [x] **4.2** **[module: gateway worker, offline/pure]** In
  `internal/gateway/handlers/external_charges_range_test.go` (new or extended), add cases
  **C1–C5** (the "last month" window for `2026-09-09` and the January-crossing case
  `2027-01-15`, the `Active` exact-match rule, the 3-entry order) and **D1**
  (`buildSuperchargerPresets` still returns exactly 2 entries), exactly as design.md §Test
  Contract Groups C and D specify.
  `depends_on`: 3.2 · `parallel_ok`: with 4.1

- [x] **4.3** **[module: gateway worker, httptest]** In
  `internal/gateway/handlers/external_charges_single_edit_test.go` (or alongside 4.1's file, per
  the existing test-file convention), add cases **B5–B7**: the edit row's checked state on a
  normal (non-error) open, for a `Price == 0 && PriceSourceUser` entry (checked), a `Price == 0 &&
  PriceSourceUnconfirmed` entry (unchecked), and a `Price > 0` entry regardless of provenance
  (unchecked) — design.md §Test Contract Group B, verbatim.
  `depends_on`: 4.1 (shares fixtures/helpers with the create/edit test cases) · `parallel_ok`: —

## Group 5 — docs (any time after Groups 1–3; logically last)

- [x] **5.1** **[module: gateway worker]** Update
  `kkpa/context/input-port/charging/external-charges.md`: the form field set now includes the
  "this charge was free" checkbox (both forms), and the date-range preset selector now offers
  three presets, not two — update the line that currently says the selector holds "last 7 days"/
  "this month" only. Do **not** touch `openspec/changes/archive/` (`CLAUDE.md`'s archive-immutable
  rule).
  `depends_on`: Groups 1–3 done (describes the shipped shape) · `parallel_ok`: with 5.2 (n/a — no
  5.2 in this tier)

## Owner verification (not automatable here)

None. Unlike tier 1, this change touches no database — there is no post-migration backfill to
eyeball. The owner's manual step is the standard one from `CLAUDE.md`: run the suite
(`go test ./...` / `make test`) and report results — see below.

## Signals a worker may run unprompted (`CLAUDE.md` §"Builds & local checks")

`go build ./...`, `go vet ./...`, `gofmt -l`, `make templ`, `make css`, `make i18n-guard`,
`make ui-guard`. **Never** `go test ./...`, `make test`, `make test-with-db`, or `make check` —
those are the owner's to run. Exact commands to hand back once implementation is done:

```
go test ./internal/gateway/...
make check
```
