# Backlog — deferred & cross-cutting work

Items that are **not** part of a single lifecycle roadmap tier — security hardening, tech debt,
and cross-cutting improvements. Each becomes its own OpenSpec change **when picked up**;
until then, this file is the durable record of **what to do next**.


## How to use this file

- Add a new subsection on [Pending to be picked up](#pending-to-be-picked-up) when a decision defers real work (with a clear **trigger** so it isn't forgotten). Use the following template:

Architecture is a valid MODULE-NAME when it is cross-cutting (e.g., `security`, `logging`, `metrics`, `deployment`) or a new module that doesn't yet exist.

```
## {{NUMBER}}. {{MODULE-NAME}} — {{TITLE}}

### PROPOSAL

{{Description of the work/proposal, why it was deferred, and what triggers it to be picked up.}}

### ORIGIN

{{Where the proposal came from (tier design.md, discussion, etc.)}}

```

- When you pick an item up, run the skill `/kkpa-dev-harness-pipeline:propose` to generate a new OpenSpec change (proposal.md, design.md, specs/<domain>/spec.md, tasks.md) and move the item into that change's design.md.




# Pending to be picked up


## 1. tesla — Percent-encode `dx/charging/history` date query params

### PROPOSAL

`internal/tesla` `Client.ChargingHistory` (in `vehicles.go`) builds the request URL by
concatenating the optional `startTime`/`endTime` query params as raw strings, WITHOUT
percent-encoding. Date/time values (e.g. `2026-06-28T10:24:41-05:00`) contain `:` and `+`,
which are URL-significant — an unencoded value can produce a malformed query and a failed or
wrong-window API call.

**Currently dormant / harmless:** no caller passes date params — the nightly collector calls
`ChargingHistory(creds, ChargingHistoryParams{})` (full fetch, no filter), so the affected
code path never runs.

**TRIGGER — fix this FIRST when** a date-windowed / incremental charging-history backfill is
added (i.e. the collector starts passing `StartTime`/`EndTime` to avoid re-fetching the whole
history every night). Encode both params with `url.QueryEscape` (or build the query via
`url.Values`) before that feature ships.

### ORIGIN

`RM2-tesla-add-charging-history` (RM2 tier 1) review finding **R1**, deferred through
`RM2-telemetry-add-charging-stats` (tier 2, which chose full-fetch nightly so no date params
are passed). Recorded in the archived RM2 progress.json.

Note: the former "CHARGING STATS" backlog item shipped as roadmap **RM2-charging-stats** (both
tiers archived 2026-07-16) — see `openspec/roadmaps/archive/RM2-charging-stats/`.



# BRAINSTORMING


## 1. **Security** — encrypt Tesla tokens at rest

### PROPOSAL

Encrypt Tesla tokens at rest (tesla_tokens.access_token / refresh_token) — app-level column encryption (AES-GCM with a key from the environment) or pgcrypto.

 kept tokens plaintext to stay scoped; acceptable on a locked-down local Postgres.

### ORIGIN
add-account-module (Tier 1) design.md Open Question
