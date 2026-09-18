Source: MAG-59 — https://linear.app/magus-monitor/issue/MAG-59/travel-progress-drive-the-updown-arrows-from-the-previous-day
Roadmap: openspec/roadmaps/RM66-travel-progress-trends.md
Tier: 1 of 3 (`platform`, cross-cutting — runs FIRST, per roadmap D-F; tiers 2-3
`analytics` and `gateway` depend on this one)

## Why

Roadmap decision **D-D** picks one name for every delta column in the database:
`_delta_calc` (Go: `DeltaCalc`). Decision **D-E** says the rule must live in two
places: prose in `ai/go-conventions.md`, and a `make` guard. Prose alone was
rejected — a guard is a deterministic signal, so a wrong name fails fast instead of
costing a human review round (`CLAUDE.md`'s AI-efficiency rule).

This tier runs first (D-F) so the guard exists **before** tier 2 renames the four
tyre-pressure columns. That ordering has one hard consequence: on the day this
guard lands, the renamed columns do not exist yet. Any pre-existing `_calc` name
that does not yet carry `_delta_calc` must WARN, not FAIL, or `make check` goes red
for work nobody has done yet.

**A correction to the roadmap's own scope.** The roadmap states the baseline holds
"exactly the four `tpms_pressure_*_psi_calc` names" and "must shrink to empty when
tier 2 lands." A full repo grep (below, and in design.md) found this is not
correct: there are **ten** pre-existing SQL columns and **fifteen** pre-existing Go
identifiers ending in `_calc`/`Calc` without `_delta_calc`/`DeltaCalc`, spread
across `analytics` AND `charging`. Tier 2 only renames four of the ten SQL columns
(and their eight matching Go names). The other six SQL names and seven Go names are
not day-over-day deltas of the same metric — `km_per_pct_calc` is a same-day rate,
`inferred_capacity_kwh_calc` is a same-row ratio — and this roadmap never touches
them. The baseline shrinks from 10/15 to 6/7 when tier 2 lands. It does not reach
empty. This proposal documents the true count and sizes the guard accordingly; see
design.md D-plat-6.

This proposal, its sibling artifacts (design.md, specs, tasks.md), and this tier's
implementation are produced as **separate dispatches** in this pipeline — unlike
the `RM35-platform-add-tz-guard` precedent tier, which combined both in one
dispatch. This dispatch produces artifacts only.

## What Changes

- **New naming rule in `ai/go-conventions.md`**, next to the existing column
  unit-suffix rule (~line 370): a column or Go field that stores a day-over-day
  change (today's value of a metric minus yesterday's value of the same metric)
  carries `_delta_calc` (Go: `DeltaCalc`), never a bare `_calc`/`Calc`. A bare
  `_calc`/`Calc` name stays valid for a derived value that is **not** itself a
  day-over-day difference (a rate, a same-row ratio, a verbatim copy).
- **New `make delta-guard` target**, mirroring `naming-guard`'s baseline shape
  (warn on a pre-rule legacy name, fail on a new one), with a
  `-- delta:allow: <reason>` (SQL) / `// delta:allow: <reason>` (Go) escape hatch
  for a genuinely new non-delta `_calc` name.
- **Baseline seeded with every name the repo-wide grep finds today** (design.md
  D-plat-6 has the full list) — not just the four tpms names the roadmap named.
- **Docs**: `README.md`'s `make check` composition line and `CLAUDE.md`'s
  allowed-commands / Test-Execution-Policy lists gain `make delta-guard`, per
  `CLAUDE.md`'s workflow-documentation rule (a new required `make` target).

**Not breaking.** No Go logic changes, no database object, no behavior change —
only a new Makefile target, prose in `ai/go-conventions.md`, and doc updates.

**Affected modules:** none in the runtime sense. This is `platform` — a
cross-cutting change touching `Makefile`, `README.md`, `CLAUDE.md`, and
`ai/go-conventions.md`. `openspec/config.yaml` names `platform` as the domain for
work not owned by one module.

## Capabilities

### New Capabilities

- `platform` (delta-guard) — a repo-wide, deterministic guard that keeps every
  day-over-day delta column and its Go field named `_delta_calc`/`DeltaCalc`,
  exactly as `ai/go-conventions.md` and roadmap `RM66` D-D require.

### Modified Capabilities

- `unit-of-measure` — the "Unit Suffix Naming" requirement is corrected: a
  unit-bearing column's suffix must be its name's last **unit-bearing** segment,
  not literally its last segment. `_calc` and `_delta_calc` are recognised trailing
  derivation markers that may follow the suffix. See "Spec conflict" in design.md.

## Impact

- `Makefile` — one new target, one `.PHONY` entry, one `check` dependency added.
- `README.md`, `CLAUDE.md`, `ai/go-conventions.md` — doc updates per `CLAUDE.md`'s
  workflow-decisions rule.
- `openspec/specs/unit-of-measure/spec.md` — one requirement corrected.

**No database object is created or changed by this tier.** It owns no table, runs
no query, and touches no migration. **No hot read path is affected**: the guard is
a `make`-time static grep over source text, never executed at runtime, so
`ai/architecture.md` §7's read-heavy performance profile is not engaged.
