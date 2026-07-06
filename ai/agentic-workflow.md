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
