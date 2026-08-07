# Design — platform-unit-of-measure-convention

## Context

Three documents currently carry unit guidance, and they do not agree with where the project is
going:

| Location | Says today | Problem |
|---|---|---|
| `ai/go-conventions.md:44-47` | every miles field MUST have a `<Field>Km()` companion; km is "always derived, never stored" | directly forbids what RM7 does |
| `CLAUDE.md` non-negotiables | same rule, abbreviated ("Never a JSON-tagged km field") | loaded on every request, so the stale rule has the widest reach |
| `internal/telemetry/AGENTS.md:107-115` | "units are stored API-native (miles)… km is NEVER a stored column" | module-local, fixed by RM7 tier 2 |

Nothing states the positive rule — which units the platform standardises on, or that a column name
must carry its unit. The audit (see `proposal.md`) confirms the schema already follows the rule
everywhere except `vehicle_snapshots`, so what is missing is the written contract, not the data.

## Goals / Non-Goals

**Goals:**
- One normative source for the platform's unit rules, reachable as a spec and mirrored into the
  docs an agent loads without being asked.
- Make the rule actionable at the moment it matters: when someone designs a migration or a domain
  struct.
- Name the exceptions explicitly, so "exception" never has to be inferred.

**Non-Goals:**
- Changing any table, column, value, or Go type. The audit found nothing to fix.
- Restating the rule in modules that persist nothing (`battery`, `gateway`).
- Re-deciding RM7. This change generalises RM7's decision; it does not revisit it.

## Decisions

### D1 — The rule, stated once

> **Persisted values are stored in display units, named with a unit suffix, converted on write.**
>
> - Distance and range: **kilometres** (`_km`). Speed/rate: **km/h** (`_kmh`).
> - Temperature: **degrees Celsius** (`_c`).
> - Pressure: **PSI** (`_psi`).
> - Energy **kWh** (`_kwh`), power **kW** (`_kw`), voltage **V** (`_v`), current **A** (`_a`),
>   proportions **percent** (`_pct`).
> - Every persisted unit-bearing column, and the domain field it maps to, carries the suffix.
> - Conversion happens **once, on the write path**. No read path converts.
> - Formatting — rounding, thousands separators, symbols — is presentation and belongs to the
>   gateway. The stored value is always a plain unformatted number.

### D2 — Two explicit exceptions, both named in the rule

**Vendor adapter DTOs.** `internal/tesla`'s `...Tesla` structs mirror the Fleet API payload
field-for-field in the API's native units (miles, bar) and keep the `Km()`/`Kmh()`/`PSI()`
companions. That is not a violation — it is the boundary where conversion is *available*, and
after RM7 it is the only place in the repo any conversion factor exists. A DTO that silently
converted would stop mirroring the vendor, which `ai/architecture.md` §6 forbids.

**Monetary amounts.** `manual_charge_entries.price` and `supercharger_sessions.total_cost` take no
suffix, because their unit is not fixed at schema-design time — it is a value in a sibling
`currency` column ('COP', 'USD'). A suffix would have to encode one currency and would be wrong
for every other. The rule instead requires the pairing: a monetary column MUST be accompanied by a
currency column. Naming these exempt is what turns them from silent debt into a documented,
deliberate shape.

**Rejected:** renaming them to `price_amount` / `total_cost_amount`. It makes the rule
exceptionless in wording only — `_amount` names no unit — and costs two migrations across two
modules, forcing a roadmap for a purely cosmetic gain.

### D3 — Where the rule goes, and why each location earns its copy

| Location | Why |
|---|---|
| `openspec/specs/unit-of-measure/spec.md` | Normative source of record; the thing a delta spec can modify later. |
| `CLAUDE.md` non-negotiables | Loaded on every request. Highest reach, so it carries the compressed rule and points at the detail. |
| `ai/go-conventions.md` §Coding Rules | Replaces the stale companion-method bullet at its own location, so the contradiction is removed rather than layered over. |
| `ai/go-conventions.md` §Persistence | Where an agent reads while designing a migration — the column-naming half of the rule belongs at the point of use, next to the existing `account_id`-leading-index rule. |
| `internal/{account,manualcharge,telemetry}/AGENTS.md` | The three table-owning modules. A pipeline worker gets its own module's `AGENTS.md` plus the base doc pack, so this is the module-local reminder at the point of use. |
| `internal/tesla/AGENTS.md` | The exception has to be documented *in the exception*, or a future agent "fixing" the adapter to store km would be following the rule and breaking the architecture. |

`battery` and `gateway` are deliberately skipped. Neither owns a schema nor persists anything;
the storage rule is inert there, and every extra copy is a copy that can drift. This is the
AI-efficiency principle applied to the docs themselves — the rule warns against bloat in the very
files meant to save tokens.

### D4 — This change takes over the doc edits from RM7 tier 2

`telemetry-store-display-units` tasks 6.2 and 6.3 currently claim `ai/go-conventions.md:44-47` and
the `CLAUDE.md` bullet. Two authored-but-unapplied changes editing the same lines is a conflict
waiting to happen and splits ownership of one rule across two changes. Since both are still
artifacts-only, the cheap fix is now: strip those two tasks, leave RM7 tier 2 with only
`internal/telemetry/AGENTS.md` (genuinely module-local), and amend RM7's RD12 to point here.

### D5 — Documenting the audit result, not just the rule

The audit table in `proposal.md` is part of the deliverable. Without it, the next person asking
"are the other tables compliant?" re-runs the same investigation. Task 4.2 puts a condensed
version in `ai/go-conventions.md` §Persistence so the answer survives outside the archived change.

## Risks / Trade-offs

- **[The spec asserts a state RM7 has not delivered yet]** → `vehicle_snapshots` still stores
  miles/bar until RM7 tier 4. The rule is normative and forward-looking, and the one known
  exception is tracked by an active roadmap, not unknown debt. Mitigated by archiving this change
  after RM7 (task 5.3) so `openspec/specs/` never contradicts the code.
- **[Five copies of the rule can drift]** → Accepted and bounded: one normative spec plus four
  short module pointers that reference the rule rather than restating it in full. Task 4.5
  requires each `AGENTS.md` edit to be a pointer, not a duplicate of the whole rule.
- **[A future non-metric unit has no home]** → If the platform ever needs a unit not in D1's list,
  the spec is the place to add it. Noted so the list is read as extensible, not exhaustive.

## Migration Plan

None. Documentation and spec only — nothing to deploy, nothing to roll back beyond reverting the
edits.

## Open Questions

None.
