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



## 1. **Security** — encrypt Tesla tokens at rest

### PROPOSAL

Encrypt Tesla tokens at rest (tesla_tokens.access_token / refresh_token) — app-level column encryption (AES-GCM with a key from the environment) or pgcrypto.

 kept tokens plaintext to stay scoped; acceptable on a locked-down local Postgres.

### ORIGIN
add-account-module (Tier 1) design.md Open Question

## 2. **telemetry** — adaptive polling / charge-session detection

### PROPOSAL

Boost sampling to minutes-level only while a vehicle is charging or driving (per the telemetry module's `AGENTS.md` §Polling Strategy), layering on the nightly-snapshot foundation. Deferred: the nightly anchor shipped first; adaptive polling adds API cost and scheduler complexity that only pays off once nightly data shows charge/drive patterns worth finer sampling. Trigger: when accumulated nightly data justifies charge-session detection or higher-resolution sampling.

### ORIGIN

RM1-nightly-vehicle-telemetry roadmap — "Future work (recorded, not tiers)" (descoped at the 2026-07-10 grill-me interview).

## 3. **telemetry** — availability & sleep-behavior metrics

### PROPOSAL

Derive availability and sleep-behavior metrics from the `poll_attempts` table (attempt reasons: `asleep-timeout`, `unauthorized`, `api-error`, …). Deferred: needs weeks of accumulated `poll_attempts` history before the metrics are statistically meaningful. Trigger: after several weeks of nightly poll attempts have been recorded.

### ORIGIN

RM1-nightly-vehicle-telemetry roadmap — "Future work (recorded, not tiers)".