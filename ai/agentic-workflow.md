# Agentic Modular Monolith — AI Development Workflow

How AI coding assistants build and maintain this project. This is a **dev-time** workflow:
it governs *how the code gets written*, and adds **zero runtime code**. Pairs with the
structural rules in [`architecture.md`](./architecture.md). Referenced from `CLAUDE.md`.

---

## The core idea

**The module boundary is the unit of both code isolation *and* agent isolation.** The same
walls that stop `internal/battery/` from touching `internal/tesla/`'s database also scope
the AI agent that works on each module. This is Anthropic's **orchestrator-workers** agent
pattern, with one added constraint: **a worker's scope == a module boundary.**

There is **no runtime agent** — no `agent.go`, no compiled LLM component. "Agent" here
always means a *coding* agent (instructions/config that scope an assistant), never a
shipped feature.

---

## Two layers of instructions

1. **Global rules** — the repo-root `AGENTS.md` / `CLAUDE.md`. Apply to **every** module:
   the boundary rules, Go conventions, htmx conventions, this workflow.
2. **Per-module rules** — a co-located `internal/<module>/AGENTS.md` in each module. Holds
   what is specific to that module: its responsibility, its public interface, what it may
   and may not import, its DTO conventions, where its data lives, and testing notes.

`AGENTS.md` is the cross-assistant standard (read by Claude Code, OpenCode, Cursor, and
others) and is **hierarchical**: an assistant working inside `internal/battery/` merges the
**global** root file with the **nearest** module file. That merge *is* the "global +
scoped" model — no custom machinery required.

> Per-module `AGENTS.md` files are created **when the module is built**, not up front. Do
> not scaffold empty module folders just to hold one.

---

## The lead-orchestrator protocol

For a requirement that spans multiple modules:

1. **The main assistant session plays the lead** — no separate "orchestrator" artifact to
   build. The lead is a *role*, described here so any assistant can play it.
2. **Decompose by module.** The lead splits the requirement into per-module slices along
   the boundaries in [`architecture.md`](./architecture.md).
3. **Delegate one worker per affected module.** Each worker (a subagent, if the assistant
   supports them; otherwise the same assistant switching "hats") does its slice **inside
   that module's boundary only**, following the module's `AGENTS.md`.
4. **Respect the sandbox.** A worker assigned to `battery` may read/edit `battery` and call
   other modules only through their **public interfaces** — it must not edit another
   module's internals or query its database.
5. **The lead integrates** — wires the modules together through interfaces and reconciles
   the slices into the finished feature.

*Why: keeping each worker inside one module means changes stay boundary-respecting by
construction, and the same isolation that makes the code maintainable makes the AI work
reviewable module-by-module.*

---

## The pipeline — roles, state files, and the review gate {#pipeline}

The lead-orchestrator protocol above is executed as a **dev harness pipeline** with three
roles and durable state, so a change survives session interruptions (rate limits,
crashes, context resets). Assistants with subagent support dispatch real subagents;
others switch "hats" — the roles and state contract are identical either way. (For
Claude Code, the automation lives in the user-global `kkpa-dev-harness-pipeline` skill;
this section is the assistant-neutral contract of record.)

### Roles

- **Leader** — the main session. Orchestrates the state machine: resolves the owning
  module, runs all user-facing interviews (subagents never talk to the user), dispatches
  workers and the reviewer, triages review findings, integrates across modules. Never
  implements module code itself.
- **Worker** — scoped to ONE module (the sandbox in step 4 above). Reads the doc pack
  (this folder's `ai/*.md` + the module's `AGENTS.md`, creating the latter if missing),
  creates OpenSpec artifacts and implements assigned sub-tasks, updating tasks.md and its
  own progress entries live. Its **identity** is the `Agent-Name:` header declared in the
  module's `AGENTS.md` (e.g. `account`; fallback: the module folder name) — dispatch
  prompts address the worker by that name and every progress.json `agent` field records
  it, so the state file reads as "the `account` agent did this". The reviewer's identity
  is `<agent-name>-reviewer`.
- **Reviewer** — reviews the module's completed change cold, against a leader-written
  review brief, before archive. The only role allowed to set `reviewer-approved`.
  On `changes-requested`, the leader triages and fixes, then signals the **same**
  reviewer to re-verify.

### Pipeline config & the doc-pack guarantee

The project declares its pipeline config in a `## Pipeline config (kkpa-dev-harness-pipeline)`
block in the repo-root instruction file (here: `CLAUDE.md`): **`Modules-Root`** — the
folder whose direct children are the modules (`internal/`; workers are sandboxed to one
child each) — and **`Doc-Pack`** — the module-agnostic **base** docs every worker and
reviewer must read. Each module may extend the base through a `## Doc-Pack (module)`
section in its own `AGENTS.md` (e.g. the gateway declares the htmx docs); module packs
are **additive-only** — they add docs, never remove or override base ones. The resolved
doc pack for a dispatch = base Doc-Pack + the module's Doc-Pack section + the module's
`AGENTS.md` itself, deduped by the leader.

Loading is guaranteed in layers: (1) the assistant harness injects the CLAUDE.md/AGENTS.md
hierarchy into every subagent automatically; (2) the worker/reviewer role definitions
order "Read the doc pack before any write"; (3) every leader dispatch prompt lists the
paths and requires the subagent's final report to name the doc-pack files it read — no
acknowledgment, no acceptance; (4) the reviewer reads the same pack independently and
validates convention compliance, catching any worker that skipped it.

### State files (the progress.json contract)

- `openspec/changes/<change>/progress.json` (`kind: "change"`) — per-change state:
  change status lifecycle `proposed → in-progress → applied → verified → in-review →
  changes-requested → reviewer-approved → archived`; `tasks[]` mirroring tasks.md
  sub-tasks with `depends_on`, `parallel_ok`, `status`, `agent`, `model`, timestamps;
  a `review` block (`brief`, `rounds[]` of findings with severity and leader triage);
  an append-only `events[]` leader log. Timestamps ISO-8601 UTC.
- `openspec/roadmaps/<feature>.md` + `<feature>.progress.json` (`kind: "roadmap"`) —
  a multi-module feature: one module-prefixed change per tier (`id`, `change`, `module`,
  `depends_on`, `optional`, `status`, assistant/model, timestamps, `tokens`, `notes`),
  implemented one tier at a time. (Format proven by
  `openspec/roadmaps/archive/multi-tenant-vehicle-access.progress.json`.)

progress.json **complements** tasks.md, never replaces it: tasks.md wins on
implementation done-ness, progress.json wins on review state.

### Write ownership (anti-gaming — absolute)

1. The leader owns top-level `status`, task creation (append-only), `review.brief`,
   finding triage, `events`, and roadmap tier statuses.
2. A worker writes only its own task entries (status/agent/model/timestamps/notes).
3. The reviewer alone owns `review.status`/`rounds` and the value `reviewer-approved` —
   the leader never sets it, and archiving requires it.
4. Tasks, acceptance criteria, and findings are append-only: no agent may edit, weaken,
   or delete them to make work look complete. A truthful `blocked` beats a false `done`.
