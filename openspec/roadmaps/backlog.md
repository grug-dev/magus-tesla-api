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


## CHARGING STATS

### PROPOSAL

I'd like to start fetching nightly charging stats for vehicles. By using the same
EXISTING poller as the `vehicle_data` endpoint, we can get a nightly snapshot of charging events and
store them in a separate table. This would allow us to do analytics on charging behavior, energy usage, etc. etc.

Also, if there is a way to get the charging history from Tesla's API, we can backfill the table with historical data. 

Wondering if this is a good idea, or if there are any gotchas with the Tesla API that would make this difficult.

I'd like to be able to query the battery level before and after charging events, as well as the energy used during the charge. This would allow us to calculate efficiency and other metrics.

ANy other ideas for what to track in the charging stats table? You can do that after exploring the Tesla API and seeing what data is available.



# BRAINSTORMING


## 1. **Security** — encrypt Tesla tokens at rest

### PROPOSAL

Encrypt Tesla tokens at rest (tesla_tokens.access_token / refresh_token) — app-level column encryption (AES-GCM with a key from the environment) or pgcrypto.

 kept tokens plaintext to stay scoped; acceptable on a locked-down local Postgres.

### ORIGIN
add-account-module (Tier 1) design.md Open Question
