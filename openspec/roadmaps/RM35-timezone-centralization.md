# RM35 — Timezone Centralization (America/Bogota)

Source ticket: MAG-20 — https://linear.app/magus-monitor/issue/MAG-20/datedate-time-time-zone

## Intention

Every date and datetime the app *creates* resolves through one package, `internal/clock`,
whose default zone is `America/Bogota`. No module hand-rolls a "now" or a "start of day"
again, and a `make tz-guard` check keeps it that way.

The ticket's acceptance criterion "the app does not use UTC" is scoped by **D1** below —
it means *the default zone is never UTC*, not *the string `time.UTC` disappears from the
codebase*. Those are different claims, and only the first one is correct here.

## Findings that shaped this roadmap (read before touching anything)

The app does **not** use UTC by accident. Before this change it already had three
deliberate, separately-documented time zones:

| Where | What decides the zone | Recorded as |
|---|---|---|
| Poller / telemetry / analytics day buckets | `config.PollerTimezone` env, default `"Local"` → `telemetry.Config.Location`, `app.processor.loc` | design D2, D6, D18, D-B12 |
| Gateway pages | the `browser_tz` cookie, per user | MAG-7, `internal/gateway/handlers/tz.go` |
| `DATE` columns | `time.UTC` midnight — a **storage encoding** for `pgtype.Date`, not a zone | design D-B7 "Representation vs. zone" |

The third one is the trap. `pgtype.Date` expects UTC midnight by contract, so "remove UTC"
applied literally there corrupts day attribution across telemetry, analytics and charging.
It is explicitly out of scope (D1).

## Decisions (binding — settled with the user before any artifact was written)

**D1 — Scope: defaults only. No migrations, no backfill.** `America/Bogota` becomes the
default wherever a zone is currently *missing or falls back*. Two things are explicitly
**unchanged**: the `DATE`/`pgtype.Date` UTC-midnight storage encoding, and the per-user
`browser_tz` cookie, which still wins for a logged-in user's own pages. Consequence: zero
migration files, zero rows rewritten, and the whole roadmap is revertible by `git revert`.

Rejected — *"remove UTC everywhere, including the DATE columns"*: it rewrites four
migration folders, forces a backfill and a `sqlc` regeneration, and breaks the
`pgtype.Date` contract. Rejected — *"drop the browser_tz cookie so everyone sees
Bogota"*: that undoes MAG-7 and gives every non-Colombian user wrong day boundaries.

**D2 — The package is `internal/clock`, not `internal/util`.** One concern per package,
named for the concern. It imports **stdlib `time` only** — nothing else, ever — so no
module can create an import cycle through it, and its own `AGENTS.md` forbids adding
non-time helpers.

The ticket proposed "a new shared util module". That instinct is right about the *need*
and wrong about the *name*: `ai/architecture.md:72` names the `shared`/`common` dump as an
anti-pattern by name. A package called `util` states nothing about what may not go in it;
`clock` does. Rejected — *extending `internal/config`*: `config` is a `cmd/`-level
concern today (only `cmd/*` imports it), so making `telemetry`/`analytics`/`gateway`/`app`
import it inverts the dependency direction and gives `config` a second job.

**D3 — Docs mirror the units rule.** The full convention goes in
`ai/go-conventions.md` §Coding Rules, immediately next to the existing `_km` / `_c` /
`_psi` display-units rule — the same shape of cross-cutting value convention, and a file
already in the pipeline's base Doc-Pack, so every worker and reviewer re-reads it on every
dispatch. `CLAUDE.md` gets a one-line non-negotiable pointing at it.

`AGENTS.md` (what opencode reads) gets a **new** pointer to `ai/go-conventions.md` — it
has none today. That gap is why the rule cannot simply live in `CLAUDE.md`.

Rejected — *duplicating the full rule into both root files*: this repo has already
demonstrated the failure mode, `CLAUDE.md` is five weeks newer than `AGENTS.md`. Rejected
— *per-module copies*: seven copies of one rule, and four modules have no `AGENTS.md`.

**D4 — Sweep: defaults + dedupe, behavior-preserving.** Three things per adopting tier:
the zone fallback becomes `clock.Zone()`, raw `time.Now()` becomes `clock.Now()`, and the
**three copy-pasted UTC-midnight day-truncators** collapse into one `clock.CalendarDay`:

- `internal/analytics/consumed.go:79` — `calendarDay(t)`
- `internal/gateway/handlers/history.go:61` — `startOfDay(t)`
- `internal/telemetry/service.go:559` — `dateOnly(t, loc)` — the odd one out: it is genuinely
  zone-aware, converting into `loc` *before* extracting the date, so it can return a different
  calendar day than the other two. Tier 1's `CalendarDay` reconciles all three (tier-1 design D7).

> **Corrected 2026-08-30, after tier 1's worker read the source.** An earlier draft of this
> roadmap counted *four* truncators and listed `gateway/handlers/supercharger.go:58` among them.
> That line is `startOfMonth`, a **month**-level function that is not a duplicate of `startOfDay`
> and does not collapse into `CalendarDay`. `startOfDay` is defined exactly once in the gateway
> (`history.go:61`), so **tier 6 changes one definition, not two**.

Two further UTC-midnight functions are deliberately **left alone**, and tier 7's `tz-guard` must
carry a `// tz:allow:` for each: `gateway/handlers/supercharger.go:57` `startOfMonth` (month
granularity, not a day truncator) and `gateway/handlers/tz.go:79` `startOfDayIn` (already
zone-aware, and carries a documented DST limitation from MAG-7 that must survive).

That duplication is what the ticket's AC #2 ("centralized in one place") is actually
pointing at. Everything except the documented default change is behavior-preserving, so
the existing suites are the regression net.

`cmd/*` is **out of scope**: it is the composition root and already calls
`time.LoadLocation` explicitly and on purpose.

**D5 — `make tz-guard`, wired into `make check`, shipped LAST.** A grep guard mirroring
`money-guard`'s shape, with a `// tz:allow: <reason>` escape hatch for the legitimate
uses: the `pgtype.Date` storage encoding (D1) and the `cmd/` composition roots.

It lands in **tier 7**, not tier 1. Wired in at tier 1 it would fail on every module not
yet migrated, and the only ways out would be planting `tz:allow` markers across all five
modules from tier 1 — which is exactly the one-big-cross-module-change that
`openspec/config.yaml` forbids — or leaving `make check` red for six tiers.

Per `CLAUDE.md`'s AI-efficiency rule this guard is the point of the whole roadmap:
without it the convention decays the next time an agent types `time.Now()`, and nothing
catches it until a human notices.

**D6 — Unit tests: tier 1 only; every other tier repairs what it breaks.** New `_test.go`
coverage is written for `internal/clock` alone — pure functions, no DB, no container, and
the one package all five other tiers depend on. Tiers 2–6 add no new tests but **must**
repair existing tests they break. This narrows the owner's standing "no unit tests"
default by explicit decision, on the grounds that an off-by-one in `clock.CalendarDay`
would shift day attribution across telemetry, analytics and charging simultaneously.

## Tiers

Status legend: `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[x]` | `RM35-clock-add-bogota-time-package` | `clock` *(new)* | Create `internal/clock`: `Zone()`, `Now()`, `CalendarDay(t)`, `LoadOrDefault(name)`. Stdlib `time` import only. Its `AGENTS.md` (`Agent-Name: clock`, empty module Doc-Pack, "time ONLY — never add unrelated helpers"). Unit tests (D6). Docs per D3: full rule in `ai/go-conventions.md` §Coding Rules, one-line non-negotiable in `CLAUDE.md`, new `ai/go-conventions.md` pointer in `AGENTS.md`. Update root `README.md` structure tree + architecture table (CLAUDE.md docs-track-change rule). | — | Create `internal/clock` per RM35 D2/D3/D6. Zero deps beyond stdlib `time`. Adopt it nowhere — tiers 2–6 do that. |
| `[x]` | `RM35-config-adopt-clock` | `config` | `PollerTimezone` default `"Local"` → `clock` default (`America/Bogota`), via `clock.LoadOrDefault`. `internal/config/config.go:87-89`. | 1 | Change the `POLLER_TIMEZONE` fallback per RM35 D1/D4. One file. |
| `[x]` | `RM35-telemetry-adopt-clock` | `telemetry` | `service.go:107-112` `location()` fallback `time.Local` → `clock.Zone()`; `service.go:102` `now()` → `clock.Now()`; `service.go:561` `dateOnly` → `clock.CalendarDay`. Keep the `Config.Clock` / `Config.Location` test-injection seams. | 1 | Adopt `clock` per RM35 D4. Behavior-preserving except the fallback. Repair broken tests (D6). |
| `[x]` | `RM35-analytics-adopt-clock` | `analytics` | `consumed.go:80-82` `calendarDay` → `clock.CalendarDay`; `recalculate.go:268` `time.Now()` → `clock.Now()`. Module still owns no `*time.Location` (D-B12 holds). | 1 | Adopt `clock` per RM35 D4. Repair broken tests (D6). |
| `[x]` | `RM35-app-adopt-clock` | `app` | `scheduler.go:32` `nil loc` fallback `time.Local` → `clock.Zone()`; `processor.go:190` `time.Now().In(p.loc)` → `clock` equivalents. Preserve the D6/D18 "poller's own zone" semantics. | 1 | Adopt `clock` per RM35 D4. Repair broken tests (D6). |
| `[~]` | `RM35-gateway-adopt-clock` | `gateway` | `tz.go` — the four UTC fallbacks in `browserLocation` / `browserLocationFromHeader` become `clock.Zone()`; cookie still wins (D1). `history.go:61` `startOfDay` → `clock.CalendarDay` (ONE definition — `supercharger.go:57` is `startOfMonth`, a different function; leave it). `handlers.go:391,461,729,924` `time.Now()` → `clock.Now()`. Keep `startOfDayIn` and its documented DST limitation. | 1 | Adopt `clock` per RM35 D4. The `browser_tz` cookie still wins — only the fallback changes. Repair broken tests (D6). |
| `[ ]` | `RM35-platform-add-tz-guard` | `platform` | `make tz-guard` mirroring `money-guard`'s grep shape + `// tz:allow: <reason>` escape hatch; add to `.PHONY` and to `make check`. Known legitimate escapes: the `pgtype.Date` UTC-midnight encoding (D1), `cmd/*` composition roots, `supercharger.go:57` `startOfMonth`, `tz.go:79` `startOfDayIn`. Document the target in `README.md` "Making a change" and in `CLAUDE.md`'s allowed-commands list (workflow-decisions rule). | 2,3,4,5,6 | Add the guard per RM35 D5 — LAST, once every module is migrated. |

## Future work

None deferred. Nothing was descoped into `openspec/roadmaps/backlog.md` for this roadmap:
the two rejected alternatives in D1 were rejected as wrong, not postponed.
