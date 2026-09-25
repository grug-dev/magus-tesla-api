> **Scope.** Adds the eighth "km per gallon" tile to `/vehicle-stats`, per
> roadmap D1, D6–D10 (and D5, D11–D13). This dispatch wrote the OpenSpec
> artifacts only; the tasks below are for the implementation dispatch that
> follows. No database object is touched by this tier — tier 1
> (`internal/reference`) is already built and archived.
>
> **Sandbox note.** T8 and T11 touch files OUTSIDE `internal/gateway/`
> (`cmd/web/main.go` and the root `README.md`). The leader must explicitly
> grant those paths to whichever worker implements this tier, the same way
> it grants the OpenSpec change folder.
>
> **Dependencies / parallelism:**
> - T1 (`i18n/catalog.go`) has no dependencies. MAY start immediately.
> - T2 (`handlers/format.go`) has no dependencies. MAY run in parallel with T1.
> - T3 (`gateway.go` + `handlers.go` Deps plumbing) has no dependencies. MAY
>   run in parallel with T1/T2.
> - T4 (`templates/fragments/vehicle_stats_vm.go`) has no dependencies. MAY
>   run in parallel with T1/T2/T3.
> - T5 (`handlers/vehicle_stats_tiles.go`) depends on **T2** (needs
>   `formatKmPerGallon`) and **T4** (needs the `KmPerGallon` field).
> - T6 (`handlers/vehicle_stats.go`) depends on **T3** (needs
>   `h.referenceReader`) and **T5** (needs the new `sumVehicleStatsMonths`
>   signature).
> - T7 (`templates/fragments/vehicle_stats.templ`) depends on **T1** (the
>   i18n key) and **T4** (the field). MAY run in parallel with T6.
> - T8 (`cmd/web/main.go`, LEADER-GRANTED path) depends on **T3** (the `Deps`
>   field must exist first). MAY run in parallel with T5/T6/T7.
> - T9 (`internal/gateway/AGENTS.md`) depends on **T3**. MAY run in parallel
>   with T5/T6/T7/T8.
> - T10 (KB guides) depends on **T6** and **T7** (describes the final
>   shape) — do last.
> - T11 (root `README.md`, LEADER-GRANTED path) depends on **T3**. MAY run
>   any time after T3, but do it alongside T10 so both docs sweeps land
>   together.
>
> **Wave placement:** T1, T2, T3, T4 first (all four independent, parallel,
> disjoint files). T5 next (needs T2+T4). T6, T7, T8, T9 next (T6 needs
> T3+T5; T7 needs T1+T4; T8, T9 need only T3 — all four may run together,
> disjoint files). T10, T11 last.

## T1. i18n key — `internal/gateway/i18n/catalog.go` — no dependencies

Decisions: design.md "New i18n key"

- [x] T1.1 Add `KeyVehicleStatsKmPerGallon Key = "vehicle_stats.km_per_gallon"`
      to the `Key` constant block, next to `KeyVehicleStatsCostPerKm` /
      `KeyVehicleStatsRangeFull`.
- [x] T1.2 Add its one `catalog` map entry on the same line pattern as its
      neighbours: `KeyVehicleStatsKmPerGallon: {ES: "Km por galón
      (equivalente en gasolina)", EN: "Km per gallon (gasoline
      equivalent)"},`.
      Acceptance: `go build ./internal/gateway/i18n/...` passes.
      `TestCatalog_AllKeysHaveBothLanguages` will cover this key once the
      suite runs (not run by Claude — see Test-Execution-Policy).

## T2. `formatKmPerGallon` — `internal/gateway/handlers/format.go` — no dependencies

Decisions: design.md "New formatter"

- [x] T2.1 Add `formatKmPerGallon(kmPerGallon float64) string`, next to
      `formatKmPerPct`, exactly as specified in design.md (one decimal,
      `" km/gal"` suffix, via `strconv.FormatFloat`).
      Acceptance: `go build ./internal/gateway/...` and
      `go vet ./internal/gateway/...` pass. `gofmt -l
      internal/gateway/handlers/format.go` prints nothing.

## T3. `Deps.ReferenceReader` plumbing — `gateway.go` + `handlers.go` — no dependencies

Decisions: design.md "Verified against the code" #7

- [x] T3.1 In `internal/gateway/gateway.go`, add `import
      "github.com/cristianpena/magus-tesla-api/internal/reference"` and a
      new `Deps.ReferenceReader reference.Reader` field, with a doc comment
      mirroring `AnalyticsMonthlyReader`'s (injected from `cmd/web` via
      `reference.NewReader(pool)`; read by the Vehicle Stats page's new
      gasoline cost-parity tile only; NEVER import `internal/reference/db`).
      Pass it through in the `handlers.New(handlers.Deps{...})` literal
      inside `NewEngine`.
- [x] T3.2 In `internal/gateway/handlers/handlers.go`, add the same import,
      the same `Deps.ReferenceReader reference.Reader` field (mirrored doc
      comment), a `referenceReader reference.Reader` field on `Handler`, and
      the one assignment line in `New()` (`referenceReader:
      d.ReferenceReader,`).
      Acceptance: `go build ./internal/gateway/...` passes — this task adds
      a field nothing reads yet, so no other file should need to change.
      `go vet ./internal/gateway/...` and `gofmt -l internal/gateway/gateway.go
      internal/gateway/handlers/handlers.go` are both clean.

## T4. View-model field — `templates/fragments/vehicle_stats_vm.go` — no dependencies

Decisions: design.md D5

- [x] T4.1 Add `KmPerGallon string` to `VehicleStatsTiles`, with a doc
      comment stating: empty string (never an em dash) when no month in the
      period was eligible, per D3/D8/D9; carries no trend pair (D5); the
      gasoline price itself is never rendered (roadmap D5).
      Acceptance: `go build ./internal/gateway/...` passes (an unused
      struct field is not a compile error).

## T5. Roll-up math — `internal/gateway/handlers/vehicle_stats_tiles.go` — depends on T2, T4

Decisions: design.md D2, D3, D4

- [x] T5.1 Add the `monthKey` type and `monthKeyOf(t time.Time) monthKey`
      helper exactly as specified in design.md D2 (add `"time"` to the
      file's imports if not already present — it is not, today).
- [x] T5.2 Add the `gasolineAccumulator` type (`distanceKm`, `gallons`
      fields) with its `add` and `value` methods, exactly as specified in
      design.md D3.
- [x] T5.3 Add `gasoline gasolineAccumulator` to `vehicleStatsTotals`.
- [x] T5.4 Change `sumVehicleStatsMonths`'s signature to
      `sumVehicleStatsMonths(months []analytics.VehicleMonthlyMetrics,
      priceByMonth map[monthKey]float64) vehicleStatsTotals`. Inside its
      existing loop, assign the per-month cost to a local `cost` variable
      (reused for both `t.cost +=` and the new call), and add
      `if price, ok := priceByMonth[monthKeyOf(m.Period)]; ok {
      t.gasoline.add(m.AllDistanceKm, cost, price) }` — exactly the diff in
      design.md D3.
- [x] T5.5 In `buildVehicleStatsTiles`, set `tiles.KmPerGallon = ""` and then
      `if v := t.gasoline.value(); v > 0 { tiles.KmPerGallon =
      formatKmPerGallon(v) }`, placed next to the existing `RangeFull`
      assignment (same "compute once, no trend" shape).
      Acceptance: `go build ./internal/gateway/...` now fails exactly at the
      two `sumVehicleStatsMonths(...)` call sites in `vehicle_stats.go`
      (missing second argument) and nowhere else. Record the exact failing
      lines in the report — they are T6's targets. `go vet
      ./internal/gateway/handlers/...` and `gofmt -l
      internal/gateway/handlers/vehicle_stats_tiles.go` are otherwise clean.
      CONFIRMED: fails exactly at vehicle_stats.go:123 and :136, no other
      site.

## T6. Handler wiring — `internal/gateway/handlers/vehicle_stats.go` — depends on T3, T5

Decisions: design.md D1, D6

- [x] T6.1 Right after the existing `if len(months) == 0 { return v,
      http.StatusOK }` check and before the current
      `totals := sumVehicleStatsMonths(months)` line, insert the
      `priceByMonth` build block from design.md D6 — call
      `h.referenceReader.PricesForMonths(ctx, start, end)`, log via
      `logging.Note("Handler", "vehicleStatsViewFor", ...)` on error and
      leave the map empty, else populate it keyed by `monthKeyOf(p.Period)`.
- [x] T6.2 Update the current-period call to
      `totals := sumVehicleStatsMonths(months, priceByMonth)`.
- [x] T6.3 Update the previous-period call (inside the `if ps, pe, ok :=
      vehicleStatsPrevWindow(...)` block) to
      `p := sumVehicleStatsMonths(prevMonths, nil)` — no new price read for
      the previous period (design.md D5).
      Acceptance: `go build ./internal/gateway/...` compiles clean. `go vet
      ./internal/gateway/...` passes. `gofmt -l
      internal/gateway/handlers/vehicle_stats.go` prints nothing.

## T7. Template — `templates/fragments/vehicle_stats.templ` — depends on T1, T4

Decisions: design.md "Template — the eighth tile's placement"

- [x] T7.1 In `vehicleStatsTiles`, add the conditional fifth `ui.StatTile`
      call to the existing `grid-cols-2 md:grid-cols-4` ledger row, exactly
      as specified in design.md's template snippet — `if t.KmPerGallon !=
      "" { @ui.StatTile(...) }`, no `Trend`/`Desc`.
- [x] T7.2 Run `make templ` (regenerates `vehicle_stats_templ.go`).
      Acceptance: `go build ./internal/gateway/...` compiles clean.
      `make ui-guard` and `make i18n-guard` both pass (the new tile composes
      `ui.StatTile`, no raw DaisyUI class; its label goes through `i18n.T`).

## T8. `cmd/web` wiring — `cmd/web/main.go` — LEADER-GRANTED PATH — depends on T3

Decisions: design.md "Verified against the code" #7

- [x] T8.1 Add `"github.com/cristianpena/magus-tesla-api/internal/reference"`
      to the import block.
- [x] T8.2 Add `ReferenceReader: reference.NewReader(pool),` to the
      `gateway.Deps{...}` literal, with a one-line comment mirroring
      `AnalyticsMonthlyReader`'s ("The Vehicle Stats page's gasoline
      cost-parity tile reads through this port. Like `AnalyticsMonthlyReader`
      it reads one table and needs only the pool.").
      Acceptance: `go build ./...` compiles clean.

## T9. Module docs — `internal/gateway/AGENTS.md` — depends on T3

Decisions: none (docs-track-structural-change rule, `CLAUDE.md`)

- [x] T9.1 Under "Public interface", add a `Deps.ReferenceReader
      reference.Reader` bullet immediately after the `AnalyticsMonthlyReader`
      bullet, same shape: what it is, that it is wired from `cmd/web` via
      `reference.NewReader(pool)`, that it is read only by the Vehicle Stats
      page's new tile, and the "never import `internal/reference/db`" rule.
      Acceptance: `grep -n "ReferenceReader" internal/gateway/AGENTS.md`
      returns the new bullet.

## T10. KB guides — depends on T6, T7 (do last)

Decisions: design.md "Docs to update"

- [x] T10.1 In `kkpa/context/input-port/analytics/vehicle-stats.md`, add
      `vehicle_stats_tiles.go`'s new `monthKey`/`gasolineAccumulator`
      symbols and `internal/reference` to the "Front-end component map"
      table; add `KmPerGallon` to the ledger-tier row of the "Page
      structure" table; add a "Gotchas" bullet recording the D3 eligibility
      rule (a month with a resolved price and real distance still does not
      count if its charging cost is zero).
- [x] T10.2 In `kkpa/context/entities/fuel-price/guide.md`, add
      `internal/gateway`'s `/vehicle-stats` page as this port's first
      consumer under "Related KB" (link to the file T10.1 just updated).
      Acceptance: `grep -rn "km per gallon\|KmPerGallon" kkpa/context/`
      returns hits in both files.

## T11. Root `README.md` — LEADER-GRANTED PATH — depends on T3

Decisions: `CLAUDE.md` "Docs track structural change"

- [x] T11.1 In the "Dependency graph" ASCII diagram, add `reference` to the
      `gateway ────────────►`, `├─ handlers ──────►`, and
      `cmd/web ────────────►` lines, in the same position `analytics`
      already occupies on each (alphabetical-ish, matches existing
      ordering).
      Acceptance: `grep -n "reference" README.md` shows the three new edges
      alongside the existing module-table row (line ~262, unchanged by this
      task — tier 1 already added it).
