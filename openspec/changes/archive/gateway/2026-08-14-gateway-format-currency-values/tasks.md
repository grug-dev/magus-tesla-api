# Tasks: gateway-format-currency-values

> Single module (`internal/gateway`), no DB, no new route, no new read path. Sub-task A
> (creating `format.go` by moving the existing helpers) is the only structural change and
> **blocks every other sub-task**, since B and C call into `format.go`'s `formatMoney`.
> Once A lands, **B, D and E can run in parallel** — disjoint files: B touches the
> `supercharger.go`/`charges.go` call sites, D touches the `Makefile`, E touches
> `AGENTS.md`. C (tests) depends on A+B; D.4's verification run naturally waits for B.
> F is the final gate and depends on everything.

## A. Create `format.go`; move the four existing helpers unchanged

- [x] A.1 Create `internal/gateway/handlers/format.go` (package `handlers`). Move
  `commaGroup` (currently `handlers.go:470`), `formatMoney` (currently
  `supercharger.go:294`), `formatKm` (currently `handlers.go:444`), and `formatKmRaw`
  (currently `history.go:424`) into it **verbatim** — same signatures, same bodies, same doc
  comments. Do NOT change any function's behavior, name, or signature (design.md D1).
- [x] A.2 Remove the moved function bodies from `handlers.go`, `supercharger.go`, and
  `history.go` (their old locations) — leave every *call site* in those files untouched; Go
  resolves unqualified same-package calls regardless of which file declares the function, so
  no call site needs an edit for the move itself.
- [x] A.3 Run `go build ./...` (or `make build`) to confirm the move alone compiles clean with
  zero behavior change before touching any call site.

_depends_on: none_

## B. Fix the 3 broken call sites

- [x] B.1 `internal/gateway/handlers/supercharger.go:274` (`buildSuperchargerRows`, the
  session `CostLabel`) — replace `fmt.Sprintf("%.2f %s", *s.TotalCost, *s.Currency)` with
  `formatMoney(*s.TotalCost, *s.Currency)`.
- [x] B.2 `internal/gateway/handlers/charges.go:540` (`chargeEntryVMFromEntry`, `PriceLabel`)
  — replace `fmt.Sprintf("%.2f %s", e.Price, e.Currency)` with
  `formatMoney(e.Price, e.Currency)`.
- [x] B.3 `internal/gateway/handlers/charges.go:480` (`chargeEntryVMFromEntry`,
  `CostPerKWhLabel`) — replace `fmt.Sprintf("%.2f %s/kWh", *v, e.Currency)` with
  `formatMoney(*v, e.Currency) + "/kWh"` (or an equivalent that produces the identical
  `"1,200.00 COP/kWh"` string — keep the `/kWh` suffix appended after the money format, not
  folded into `formatMoney`'s own currency argument).
- [x] B.4 **Do NOT touch** `internal/gateway/handlers/charges.go:550-551`
  (`RawEnergyKWh`/`RawPrice`, built with `strconv.FormatFloat(..., 'f', 2, 64)`) — these MUST
  stay unformatted/machine-parseable for the inline edit form's `<input value>` (design.md
  "Critical constraint"). If you find yourself tempted to make these consistent with the
  newly-comma-grouped display labels, stop — that breaks the edit form's submit round-trip.

_depends_on: A_

## C. Unit tests (design.md D5 — included)

- [x] C.1 Add `internal/gateway/handlers/format_test.go` with a table-driven `TestFormatMoney`
  covering at minimum: `0 -> "0.00 COP"`, `-1500.5 -> "-1,500.50 COP"`, `999 -> "999.00 COP"`,
  `1000 -> "1,000.00 COP"`, `58000 -> "58,000.00 COP"`.
- [x] C.2 In `internal/gateway/handlers/charges_test.go`, add or extend the existing
  `chargeEntryVMFromEntry` test to assert `PriceLabel` and `CostPerKWhLabel` render
  comma-grouped for an amount ≥ 1000 (e.g. price `12500` -> `"12,500.00 COP"`; a cost-per-kWh
  computation landing at `1200` -> `"1,200.00 COP/kWh"`). The existing
  `charges_test.go:868` assertion (`CostPerKWhLabel != ""`) stays valid but is not sufficient
  on its own — add the exact-string assertion alongside it, do not remove the existing check.
- [x] C.3 In `internal/gateway/handlers/supercharger_test.go`, add or extend a test asserting
  the session `CostLabel` (from `buildSuperchargerRows`) renders comma-grouped for an amount ≥
  1000 (e.g. `TotalCost = 58000, Currency = "COP"` -> `CostLabel == "58,000.00 COP"`). The
  existing `supercharger_test.go:272` `CostLines` assertion (already `"58,000.00 COP"`) stays
  as-is — this task covers the previously-untested `CostLabel` field on the row, not the tile.
- [x] C.4 Add a test (in `charges_test.go` or a dedicated case) asserting `RawEnergyKWh` and
  `RawPrice` are NOT comma-grouped — e.g. an entry with `EnergyAddedKWh = 1200.5` yields
  `RawEnergyKWh == "1200.50"`, never `"1,200.50"` — pinning the negative requirement from
  design.md's "Critical constraint" so a future change cannot silently regress it.

_depends_on: A, B_

## D. `money-guard` Makefile target (design.md D4)

- [x] D.1 Add a `money-guard` target to the `Makefile`, mirroring `ui-guard`/`i18n-guard`'s
  grep-based shape: fail when `internal/gateway/handlers/*.go` (excluding `_test.go` and
  excluding `format.go` itself) contains a `fmt.Sprintf("%.[0-9]+f %s` pattern (a decimal verb
  immediately followed by a `%s` currency placeholder) not marked with a trailing
  `// money:allow: <reason>` comment on the same line.
- [x] D.2 Add `money-guard` to the `.PHONY` list (Makefile line ~62, alongside
  `ui-guard i18n-guard`).
- [x] D.3 Add `money-guard` to the `check` target's prerequisite list: `check: build vet
  ui-guard i18n-guard money-guard test`, and update `check`'s `##` help text to mention it.
- [x] D.4 Run `make money-guard` against the repo AFTER task B lands and confirm it passes
  clean (no remaining `%.Nf %s`-shaped currency `Sprintf` calls in `handlers/*.go`).

_depends_on: A_ (does not depend on B — the guard target itself can be written in parallel with
B; D.4's verification run naturally waits for B to land)

## E. Docs sweep (change-locality check)

- [x] E.1 If `internal/gateway/AGENTS.md`'s "How to add or modify a page" recipe or any other
  section references where formatting helpers live (e.g. a mention of `commaGroup`/`formatKm`
  living in `handlers.go`), update the reference to point at `format.go`. If no such reference
  exists, no edit is needed — confirm this instead of skipping it.
- [x] E.2 No root `README.md` "Project Structure"/"Architecture" table edit — no module was
  added, removed, renamed, or re-scoped, and no new route or `Deps` field was introduced.

_depends_on: A_

## F. Verification gate

- [x] F.1 `make check` (build + vet + ui-guard + i18n-guard + money-guard + test) passes.
- [x] F.2 Manually confirm (reading the rendered test assertions from C.2/C.3, or via
  `openspec/specs/gateway/spec.md` once synced) that all 4 monetary render sites — Supercharger
  `CostLines` tiles, Supercharger session `CostLabel`, Charge log `PriceLabel`, Charge log
  `CostPerKWhLabel` — now produce comma-grouped output.
- [x] F.3 Confirm `RawEnergyKWh`/`RawPrice` remain unformatted (C.4's test passing is
  sufficient; no manual step beyond that).

_depends_on: A, B, C, D, E_
