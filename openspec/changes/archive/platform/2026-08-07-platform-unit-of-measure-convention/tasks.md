# Tasks — platform-unit-of-measure-convention

> Docs and spec only. **Zero migrations, zero Go changes, zero schema objects.**
>
> Groups 1–4 are independent of each other and can be done in any order or in parallel.
> Group 5 gates.
>
> No hard apply-order dependency on RM7 — but see task 5.3: archive this change only after
> RM7 tier 4, so the merged spec never describes a state the code has not reached.

## 1. Re-scope the stale convention in `ai/go-conventions.md`

- [x] 1.1 Rewrite the "**Miles → km conversion is mandatory**" bullet (`:44-47`) so it applies to
      **vendor adapter DTOs only**: `...Tesla` DTOs mirror the Fleet API's native units and expose
      `<Field>Km()` / `<Field>Kmh()` / `<Field>PSI()` companions, backed by named package-level
      constants (`milesToKm`, `barToPSI`), never inline factors; pointer fields stay nil-safe.
- [x] 1.2 In the same bullet, delete the clause that forbids storing the converted value
      ("Never add the km value as a JSON-tagged struct field… km is always derived, not
      unmarshalled") — it is the sentence that contradicts the platform rule.
- [x] 1.3 Add an adjacent bullet stating the persisted-type rule: persisted domain types and
      columns store **display units** (km, km/h, °C, PSI, kWh, kW, V, A, %) under unit-suffixed
      names, converted once on write, never on read; formatting stays in the gateway and the
      stored value is a plain unformatted number.
- [x] 1.4 State the two exemptions inline in that bullet: vendor adapter DTOs (rule 1.1) and
      monetary amounts (no suffix; must be paired with a currency column).

## 2. Add the column-naming rule where migrations are designed

- [x] 2.1 Add a bullet to `ai/go-conventions.md` §Persistence requiring every unit-bearing column
      to be named with its unit suffix (`_km`, `_kmh`, `_c`, `_psi`, `_kwh`, `_kw`, `_v`, `_a`,
      `_pct`), placed alongside the existing `account_id`-leading-index rule so it is read while
      designing a migration.
- [x] 2.2 State the negative half explicitly: identifiers, timestamps, dates, counts, names,
      states and flags take **no** suffix — so `captured_at`, `tesla_id` and
      `max_range_charge_counter` are not read as violations.

## 3. Update `CLAUDE.md`

- [x] 3.1 Replace the "**Miles → km conversion is mandatory**" non-negotiable bullet with the
      compressed platform rule: the project stores **kilometres, degrees Celsius and PSI**;
      unit-bearing columns and fields carry a unit suffix; conversion happens once on write and
      never on read; vendor DTOs and monetary amounts are the two exemptions.
- [x] 3.2 Keep the bullet short and point at `ai/go-conventions.md` for the full rule —
      `CLAUDE.md` is loaded on every request, so it carries the rule, not the rationale.
- [x] 3.3 Verify the rewritten bullet does not contradict any other `CLAUDE.md` section,
      particularly the Performance-Profile line that this rule follows from.

## 4. Spread to the module `AGENTS.md` files

- [x] 4.1 `internal/telemetry/AGENTS.md` — add the platform rule reference to its DTO/units
      section. Coordinate with RM7 tier 2, which rewrites the same section's module-local content;
      whichever lands second must leave both the module fact and the platform pointer intact.
- [x] 4.2 `internal/manualcharge/AGENTS.md` — add the rule plus the audit result for its table:
      `energy_added_kwh`, `start_battery_pct`, `end_battery_pct` are compliant, and `price` is an
      exempt monetary amount paired with `currency`.
- [x] 4.3 `internal/account/AGENTS.md` — add the rule and record that `accounts`, `tesla_tokens`
      and `vehicles` currently hold **no** unit-bearing columns, so the rule applies to future
      columns only.
- [x] 4.4 `internal/tesla/AGENTS.md` — document the module as **the exception**: its DTOs stay in
      the Fleet API's native units and it is the single home of every conversion factor. State
      why, so a future agent does not "fix" the adapter into storing km and break the
      vendor-mirroring rule (`ai/architecture.md` §6).
- [x] 4.5 Keep each `AGENTS.md` edit a short **pointer** to the rule plus that module's specific
      fact — not a full restatement. Five verbatim copies drift; that is the doc bloat the
      AI-efficiency rule warns against.
- [x] 4.6 Confirm `internal/battery/AGENTS.md` and `internal/gateway/AGENTS.md` are **not**
      touched: neither module owns a schema or persists anything. Record the reasoning in
      `progress.json` rather than editing them.

## 5. Hand-off, de-conflict, and gate

- [ ] 5.1 Remove tasks **6.2** and **6.3** from
      `openspec/changes/telemetry-store-display-units/tasks.md` — this change now owns
      `ai/go-conventions.md` and `CLAUDE.md`. RM7 tier 2 keeps task 6.1
      (`internal/telemetry/AGENTS.md`) and renumbers as needed.
- [ ] 5.2 Update RM7's Decision 12 and `RM7-store-display-units.progress.json` RD12 to record that
      the platform-wide doc rewrite moved to `platform-unit-of-measure-convention`, and adjust
      `telemetry-store-display-units`'s `proposal.md` Impact section to match.
- [x] 5.3 Do **not** archive this change until RM7 tier 4 is archived. Until then
      `vehicle_snapshots` still stores miles and bar, and merging the `unit-of-measure` spec into
      `openspec/specs/` would assert a compliance the code has not reached.
- [x] 5.4 Re-run the audit as a final check: confirm no migration anywhere under
      `internal/*/db/migrations/` adds a unit-bearing column without a suffix, and that the only
      unsuffixed numeric columns repo-wide are identifiers, timestamps, counts, and the two
      exempt monetary amounts.
- [x] 5.5 `openspec validate platform-unit-of-measure-convention --strict` passes.
- [x] 5.6 Confirm `git diff --stat` for this change touches only `.md` files — no `.sql`, no
      `.go`, no `sqlc.yaml`.
