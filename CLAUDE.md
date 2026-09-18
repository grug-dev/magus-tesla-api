# magus-tesla-api — Project Instructions for Claude

## Builds & local checks — PROJECT OVERRIDE

**This project overrides the global `## Builds` rule in `~/.claude/CLAUDE.md`.** For
`magus-tesla-api` **only**, Claude MAY run the Go build/verify and codegen commands directly
(the owner has authorized it); it need not hand them back to the user. Allowed without asking:

- `go build ./...`, `go vet ./...`, `gofmt -l`
- `make build`, `make vet`, `make bins`, `make lint` (golangci-lint; config `.golangci.yml`)
- `make ui-guard`, `make i18n-guard`, `make money-guard`, `make tz-guard`, `make migration-boundary-guard`, `make boundary-guard`, `make theme-guard`, `make vehicleref-guard`, `make tenancy-guard`, `make naming-guard`, `make archive-guard`, `make logdir-guard`, `make delta-guard` — the standalone guards
- `sqlc generate` / `make sqlc`, `go mod tidy` / `make tidy`

**Claude does NOT run the test suite — the owner does.** Never run `go test ./...`,
`make test`, `make test-with-db`, or `make check`. Claude writes tests and asks; you run
them and report the results. This is the `Test-Execution-Policy` below, and it applies
inside and outside the pipeline.

Why the line falls there: everything on the allowed list is a **cheap deterministic
signal** — it fails fast, prints a few lines, and needs no human. `go vet` in particular
compiles `_test.go` files, so it catches signature drift and API mistakes in tests nobody
executed. `make lint` is the same kind of signal and catches three classes `go vet` cannot
see: an ignored error return (`errcheck`), dead code (`unused`), and a leaked resource
(`bodyclose`/`rowserrcheck`/`sqlclosecheck`). Skipping a signal like that doesn't save
anything; it converts it into a round-trip that costs more than the output it replaced.

**`make lint` and the `make *-guard` targets are different jobs — keep them apart.**
`.golangci.yml` holds Go-correctness rules an off-the-shelf linter already implements; the
guards hold rules specific to this project (i18n, boundaries, time zone, units, themes). Never
hand-roll a grep guard for something golangci-lint already checks, and never move a project
rule into `.golangci.yml`.

`make check` is owner-only *only* because it ends in `test` — its other phases
(`build vet lint ui-guard i18n-guard money-guard tz-guard migration-boundary-guard boundary-guard theme-guard vehicleref-guard tenancy-guard naming-guard archive-guard logdir-guard delta-guard`) are all on the allowed list and Claude runs
them individually. So excluding `check` costs no guard coverage.

Consequence: when Claude has written tests but not run them, the honest state is **awaiting
your verification** — not "done". No task, commit message, or summary may claim tests pass
on Claude's say-so; a passing suite is recorded as *your* report.

Still gated (ask / require explicit request first): anything that mutates or drops data —
`make db-reset`, `make migrate-down`, manual `DROP`/`DELETE`. Applying migrations forward
(`make migrate-up`, `make db-setup`) is fine when the user asks for setup. The global build
prohibition remains in force for every **other** project.

## Project Goal

Go modular monolith — the **Agentic Modular Monolith** (see [`ai/architecture.md`](ai/architecture.md)) —
that monitors Tesla vehicles via the Tesla Fleet API. It is **multi-tenant**: it serves
multiple users, each connecting their own Tesla account, with per-user tokens persisted in
a database (the `internal/account/` module). The goal is to turn live and historical
vehicle data into per-user dashboards and automation. Built with strict package separation
so new Tesla API capabilities slot in without touching existing code.

Magus (the project owner's own Tesla) is the first test vehicle, but the platform is **not**
hardwired to a single car or user.

The stack is **Go + htmx** (htmx web layer is planned, not yet built).

---

## Coding Conventions — Read Before Writing Code

Technical coding conventions live in assistant-neutral files under [`ai/`](ai/) (kept
separate from the human-facing Tesla docs in `docs/`). These are the source of truth —
this file only points at them.

- **Before adding, moving, or removing any code** (modules, boundaries, structure), read [`ai/architecture.md`](ai/architecture.md) — the "Agentic Modular Monolith" structure and boundary rules.
- **Before writing or editing any Go code**, read [`ai/go-conventions.md`](ai/go-conventions.md) and follow it exactly.
- **Before writing or editing htmx markup**, read [`ai/htmx-conventions.md`](ai/htmx-conventions.md) (Templ engine).
- **Before wiring htmx to the Go gateway**, read [`ai/htmx-go-integration.md`](ai/htmx-go-integration.md).
- **For any requirement spanning multiple modules**, follow the lead-orchestrator protocol in [`ai/agentic-workflow.md`](ai/agentic-workflow.md).

(The htmx conventions are decided but the web layer isn't built yet. Note: the multi-tenant
design **is live** — `cmd/web` (`internal/account` + `internal/gateway` + `internal/tesla` +
`internal/googleauth`) is the running server and already scopes data per user. The `vehicle`
package was replaced by the `tesla` adapter. `internal/config`/`auth`/`server` are no longer a
smoke test: `internal/config` is shared config reading used by both `cmd/web` and `cmd/setup`;
`internal/auth` and `internal/server` are used only by `cmd/setup`, the standalone one-time
OAuth-capture tool, not the running server. Treat `ai/architecture.md` as the target structure.)

### Non-negotiables (full detail in `ai/go-conventions.md`)

These are always in effect. Do not violate them even if you haven't opened the conventions file:

- **AI-efficiency is a first-class design criterion** — when proposing, designing, or reviewing code, weigh how cheaply an AI assistant can *understand* the codebase and *implement* a change (token cost + round-trips) alongside correctness and performance, and state that rationale for non-trivial choices. Favor: **closed, small vocabularies** an agent looks up instead of re-inventing (the `ui/` kit, semantic theme tokens, one gold-standard slice to mirror); **change-locality** — structure so a change touches few files (module boundaries, single-source mappings/adapters); **discoverability** in the docs an agent already loads (`CLAUDE.md`, `AGENTS.md`, `ai/*.md`) over facts it must grep to rediscover; and **deterministic signals over human round-trips** — typed Props, `make` guards/gates, and codegen that fail fast and cheap. **But do not over-abstract**: indirection costs tokens to resolve, so add a wrapper/layer only where it buys change-locality on a *volatile or repeated* surface — never hide stable, self-describing things (theme tokens, Tailwind utilities, trivial one-offs) behind a lookup. Over-abstraction is *anti*-AI-efficiency; keep the code, the layers, and the docs themselves lean. Several rules below (modular packages, mirror-the-gold-standard, docs-track-change) are instances of this principle.
- **You write the tests; the owner runs them** — never `go test ./...`, `make test`, `make test-with-db`, or `make check`, in any session, pipeline or not. `go build ./...`, `go vet ./...`, `gofmt -l` and the standalone guards (`make ui-guard` / `i18n-guard` / `money-guard` / `tz-guard`) are yours to run (vet compiles `_test.go`, so it catches signature drift in tests nobody executed). Tests you wrote but did not run are **awaiting the owner's verification**, never "done", and a passing suite is reported by the owner and recorded as theirs — never claimed as your own. Full rule: §"Builds & local checks" above; testing detail in `ai/go-conventions.md` §Testing.
- **Modular packages are a hard requirement** — every Tesla API concern gets its own package under `internal/`; one concern per package; `cmd/` stays thin (zero business logic).
- **Display units are kilometres, °C and PSI; conversion happens once, on write, never on read** — every persisted unit-bearing column and domain field is stored in the platform display unit under a matching suffix (`_km`, `_kmh`, `_c`, `_psi`, `_kwh`, `_kw`, `_v`, `_a`, `_pct`). Two exemptions only: `internal/tesla`'s vendor DTOs (stay in the Fleet API's native units — miles, bar) and monetary amounts (no suffix; paired with a `currency` column instead). Full rule: `ai/go-conventions.md`.
- **The default time zone is `America/Bogota`, obtained only through `internal/clock`** — never a raw `time.Now()`, a hardcoded zone name, or a hand-rolled UTC-midnight truncation. Two exemptions only: the `pgtype.Date` UTC-midnight storage encoding (unchanged) and the gateway's per-user `browser_tz` cookie, which still wins for a signed-in user. `make tz-guard` enforces this repo-wide (escape hatch: `// tz:allow: <reason>`). Full rule: `ai/go-conventions.md`.
- **Boundaries are sacred** (detail in `ai/architecture.md`): no HTML outside `internal/gateway/`; no module reads another module's DB/internals; cross-module data flows only through public Go interfaces; the gateway calls interfaces, never a database.
- **Every user-facing label is bilingual — both `ES` and `EN`, always** (full rule + the how-to in `internal/gateway/AGENTS.md` §i18n). The app is ES-default/EN bilingual: any user-visible string added or edited in `internal/gateway/` resolves through `i18n.T(ctx, key)` against the single catalogue `internal/gateway/i18n/catalog.go`, with both languages non-empty. A hardcoded English (or Spanish-only) string is **incomplete work**, exactly like an undocumented structural change. Two guards enforce it — `TestCatalog_AllKeysHaveBothLanguages` (every known key has both languages) and `make i18n-guard` (no string bypasses `i18n.T` at all), both wired into `make check` — but the rule binds whether or not a guard happens to catch you first.
- **Docs track structural change** — any change that adds, removes, renames, or re-scopes a module (or otherwise alters the project's structure or a module's public surface) MUST update the affected docs **in the same change**, never as a follow-up: the root `README.md` "Project Structure" tree and "Architecture" table, `cmd/README.md` when a runnable changes, and the module's own `README.md` where one exists. **This includes the knowledge base under `kkpa/context/`** — before calling the change done, grep `kkpa/context/` for the module name and fix any guide whose consumer map, file map, or title the change invalidated. The KB is the easiest doc to forget and the most expensive to leave wrong: `kkpa-context-fetch` presents it as authoritative, so a stale guide makes an agent trust it *instead of* reading the code. (Precedent: RM38/RM40 removed every gateway→telemetry call but left the KB still listing four gateway consumers and a deleted `Deps.TelemetryReader` field.) A change that leaves structure docs stale is **incomplete**. **The one folder this sweep must never touch is `openspec/changes/archive/` — see the next bullet.**
- **`openspec/changes/archive/` is immutable — never edit an archived file** — an archived change is a snapshot of what was proposed and decided *at that time*, and it is the project's only audit trail of that reasoning. Editing one rewrites history: the file then looks current while describing a decision nobody made. Adding a newly archived change folder is fine, and so is moving one unchanged under `archive/<module>/` (this repo groups by module; the CLI drops archives at the archive root) — **modifying or deleting a file already in there is not.** When an archived doc is stale or wrong, the live spec in `openspec/specs/` is what gets corrected; leave the archive as it stands. This bullet exists because the previous one sends you sweeping every doc a change invalidated, and a grep for a module name *will* hit the archive — that hit is not yours to fix. `make archive-guard` enforces it (baseline: the merge-base with `main`, so it covers the whole branch plus uncommitted work; escape hatch: `ARCHIVE_GUARD_ALLOW=1`, for something that is not a rewrite of the record, with the reason in the commit message).

- **Only `web` and `poller` may mount the named-log folder** (`/var/log/magus`, the host's `MAGUS_LOGS_DIR`). That folder is owned by exactly one user on the host. A container running as a *different* user either cannot write to it — which crash-loops the service — or forces the permissions loose enough that the files stop being readable by the host user without `sudo`, which defeats the point of naming them. `caddy` was added once and took the site down twice: it could not create its file, and once it could, the file needed `sudo` to read. Any other service's log goes to Docker's driver and is read with `docker compose ... logs <service>`. `make logdir-guard` enforces it. This rule exists because no static check catches it — `docker compose config` passes either way, and the failure appears only when a container starts.

- **Workflow & architectural decisions are documented with their steps** — when a change introduces or alters *how to build, run, generate, deploy, upgrade, or create something* in this project (a new toolchain or dependency, a new required command or `make` target, a new codegen step, a changed setup/upgrade/deploy procedure, or any architectural decision that affects the developer/agent workflow), the **same change** MUST document it where a human or an agent will look: the root `README.md` (its "Making a change" / run instructions), the relevant `docs/` file (e.g. `docs/0-set-up/deployment.md`), the affected `ai/*.md` convention doc, and the touched module's `AGENTS.md` / `README.md`. State **what changed, why, and the exact steps/commands** to do it — and to upgrade or reverse it where relevant. **The reverse direction counts too:** when a change alters the database schema, the module layout, or a codegen input, **verify the existing `make` targets and guards still hold** — `db-setup`/`db-reset` role-and-ownership assumptions, `MIGRATIONS_DIRS` order, every guard, and `sqlc`. Record in the change that you checked and what you found; "I did not think about the Makefile" is not the same as "the Makefile is unaffected", and only the second one is a finding. A decision that lives only in chat, code, or a commit message — with no doc a future contributor can follow — is **incomplete**.

## Pipeline config (kkpa-dev-harness-pipeline)

Declarative config read by the dev-harness-pipeline at dispatch time (contract of record:
[`ai/agentic-workflow.md`](ai/agentic-workflow.md) §pipeline). These files are
**module-agnostic** — general instructions that apply to every module; the per-module
layer is each module's own `AGENTS.md`, added to the pack by the leader per dispatch.

- **Modules-Root:** `internal/` — the only folder whose direct children are the monolith's
  modules. Pipeline module resolution considers only these; each worker is sandboxed to
  exactly one child of this folder (plus explicitly granted paths).
- **Work counter:** `openspec/.work-counter` — the monotonic next-number file the pipeline
  reads and bumps to number both `ft/CH<N>-…` (standalone change) and `ft/RM<N>-…` (roadmap)
  branches. **One shared counter**, so a `CH3` and an `RM3` can never both exist. This is
  the skill's own default as of v1.0.0 — not a project override — and the project's
  migration off the retired `.ch-counter` / `.rm-counter` is complete: both are gone, and
  `.work-counter` is authoritative. Monotonic rules: missing ⇒ `1`; numbers are never
  reused or decremented; archiving never touches it.
- **Doc-Pack (base — every worker AND reviewer, all modules):** `CLAUDE.md`,
  `ai/architecture.md`, `ai/go-conventions.md`. Lean on purpose — every dispatch
  re-reads it in full. Module-specific docs are declared per module in the
  `## Doc-Pack (module)` section of `internal/<module>/AGENTS.md` — additive to this
  base, never replacing it (e.g. the htmx docs live in the gateway module's pack).
  **This list is declared here and nowhere else.** A module's own Doc-Pack section names
  only its additions and must never restate the base — four of them once did, and two had
  drifted into naming the root `AGENTS.md` and the reviewer-only `ai/agentic-workflow.md`,
  which would have added ~3,400 tokens to every worker dispatch that believed them.
  **The root `AGENTS.md` is deliberately NOT in this pack** — it carries mission and
  product vision, not implementation rules.
  **Measured after MAG-38 (2026-09-11):** the base pack is ~11,900 tokens and is now the
  floor on every dispatch. Only `gateway` still has a module file larger than it
  (~13,400); every other module's is under 5,500, and six are under 2,000. **The next
  worthwhile reduction is in this base pack, not in the module files** — for a small
  module like `clock` it is already 94% of the dispatch cost. Page- and column-level
  detail belongs in `kkpa/context/`, which is fetched on demand; a rule belongs in an
  `AGENTS.md` only when an agent could break it *without ever thinking about the
  concept*, and no build, `go vet`, or guard would catch the break.
- **Doc-Pack (reviewer):** `ai/agentic-workflow.md` — the leader-protocol/contract doc,
  added to reviewer dispatches only; workers never receive it.
- **Context-Checkpoint-At:** `40` — context-window % that trips the auto-checkpoint;
  checkpoint + `/clear` + resume beats pushing a long context further, and progress.json
  makes a fresh session nearly free. On a change of three or more waves, prefer a `/clear`
  + resume at every wave boundary regardless of the percentage.
- **Test-Execution-Policy:** `Claude writes tests but never runs the suite — never go test
  ./..., make test, make test-with-db or make check. It MAY run go build ./..., go vet
  ./..., gofmt -l, make lint, make build, make vet, make bins, and the standalone guards make
  ui-guard / make i18n-guard / make money-guard /  make tz-guard / make migration-boundary-guard / make boundary-guard / make naming-guard / make archive-guard / make logdir-guard / make delta-guard. The owner runs the suite and reports
  results; work that is complete but unexecuted is awaiting-user-verification, never done,
  and a passing suite is recorded as the owner's report, never claimed by the assistant.`
- **Design-Gates:** `database` — design areas whose artifacts require the user's explicit
  confirmation before Apply (design + rationale + index plan shown to the user, iterated
  until confirmed). `database` is built-in and always on; listing it here is for
  visibility — additional areas may be appended later.
- **Performance-Profile:** `read-heavy — read performance is mandatory over write
  performance; writes are mostly done by pollers at midnight, so denormalizing, indexing
  aggressively, and precomputing for reads is acceptable — never at the cost of the
  modular-monolith boundaries or module data ownership.`

---

## Tesla API Exploration (`tesla-exploration` capability)

The `cmd/explore-tesla-api` runnable + the `Raw*` methods in `internal/tesla/raw.go` exist to
inspect the **raw** Fleet API JSON on demand (see `cmd/explore-tesla-api/README.md`). Two standing
rules govern this capability:

- **Never write tests for it.** The `tesla-exploration` capability — `internal/tesla/raw.go` and
  everything under `cmd/explore-tesla-api/` — **must not** have `_test.go` files, and no test in
  the repo may call its `Raw*` methods or the command. Its calls hit the live, **paid** Fleet API
  and wake the car; keeping it test-free is what guarantees `go test ./...` never incurs that cost.
  Do not add tests here even when asked to "add tests" broadly — this module is the explicit
  exception.
- **Keep it in sync with the `tesla` adapter.** Whenever a new Fleet API call is added to
  `internal/tesla` (a new typed method in `vehicles.go`), you **must** also: (1) add its raw
  sibling in `internal/tesla/raw.go` following the existing `Raw*` pattern (reuse `get`/`post`,
  return `json.RawMessage`, keep it OFF the `VehicleService` interface), and (2) surface it in
  `cmd/explore-tesla-api/main.go` so the explorer keeps full coverage of the adapter. Update
  `cmd/explore-tesla-api/README.md` accordingly.

---

## Session Start Protocol

At the start of every session, before writing any code:

1. Remind the user of the current open roadmap items (see openspec/roadmaps/backlog.md).
2. Ask which one they want to work on, or if they have something else in mind.
3. If they are unsure, open the brainstorm section and suggest 2–3 ideas based on what's already built.
4. Agree on the goal for the session before starting.

---

## Token Behavior

Each Tesla connection uses two tokens, **per user**:

- **access token** — expires every 8 hours; sent as `Authorization: Bearer` on every Fleet API call.
- **refresh token** — expires every 3 months, single-use; exchanged for a new access token.

**Multi-tenant model (target):** tokens are stored **per user in a database**, owned by the
`internal/account/` module (see [`ai/architecture.md`](ai/architecture.md) §5). The `tesla`
adapter is stateless — it receives the access token to use as `tesla.Credentials`. Token
**refresh belongs to the `account` module**, not the adapter.


- Full token explanation: see `docs/layer2-user-vehicle-access.md` → "Understanding the two tokens".

---

## Security — Files That Must Never Be Committed

| File | Why |
|---|---|
| `.env` | Contains CLIENT_ID, CLIENT_SECRET, ACCESS_TOKEN, REFRESH_TOKEN |
| `private-key.pem` | EC private key for vehicle command signing |
| `public-key.pem` | Already excluded as `*.pem` — copied to Netlify separately |

---

## External Services

| Service | URL | Purpose |
|---|---|---|
| Tesla Fleet API | `https://fleet-api.prd.na.vn.cloud.tesla.com` | Vehicle data and commands |
| Tesla Auth | `https://auth.tesla.com/oauth2/v3` | OAuth 2.0 tokens |
| Netlify — magus-monitor | `https://magus-monitor.netlify.app` | Hosts the EC public key at `/.well-known/appspecific/com.tesla.3p.public-key.pem` |

To redeploy the public key: `netlify deploy --dir=magus-public-key-netlify --prod`

---

## Key Reference

Tesla API setup walkthrough, split by authorization layer (each step references the package/class that implements it). Read before touching auth or setup code:
- `docs/layer1-app-registration.md` — Layer 1 (Steps 1–5): registering the "Magus Monitor" app (Client ID/Secret, EC keys, public-key hosting, partner-account registration).
- `docs/layer2-user-vehicle-access.md` — Layer 2 (Steps 6–8): OAuth login, the two tokens, and fetching vehicle data.
- `docs/post-registration-setup.md` — short index pointing to both.


---

## Brainstorm: What Data Can We Get From Tesla?

Use this section when the user is unsure what to build next. All of these are available
with the current `vehicle_device_data` scope — no new permissions needed.

### Data available right now

| Category | Fields | Ideas |
|---|---|---|
| **Battery** | Level %, estimated range, charging state, charge rate, time to full, charge limit | Range anxiety alert (notify below X%), daily charge report, charge session log |
| **Location** | GPS lat/lng, heading, speed, shift state | Trip detector (driving = speed != null), home/away detection, geo-fence alert |
| **Climate** | Inside/outside temp, climate on/off, driver temp setting | Comfort report, "car is hot" alert when parked in sun |
| **Vehicle state** | Locked, odometer, software version, sentry mode on/off | Mileage tracker, software update notifier, sentry alert |
| **Drive state** | Speed, heading, shift state | Detect when the vehicle is in motion vs parked |

### Concrete ideas to explore with the user

2. **Daily digest** — every morning: battery level, overnight charge added, current range, where vehicle is parked. Needs `cmd/poller` + `internal/store`.
3. **Battery health log** — record battery level vs odometer over time. Graph degradation. Needs `internal/store`.
4. **Charge session detector** — detect start/end of charging (state changes to/from "Charging"), log kWh added and duration. Needs `cmd/poller`.
5. **Range anxiety guard** — send a notification (Slack, email, push) when battery drops below a configurable threshold. Simple to add to `cmd/poller`.
6. **Geo-fence home alert** — detect when the vehicle leaves or arrives at home coordinates. Needs stored home location + `cmd/poller`.
7. **Software update notifier** — compare `car_version` to last known value, alert on change.
8. **Sentry mode monitor** — log when sentry mode turns on/off.
