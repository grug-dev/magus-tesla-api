# Backlog — deferred & cross-cutting work

Items that are **not** part of a single lifecycle roadmap tier — security hardening, tech debt,
and cross-cutting improvements. Each becomes its own OpenSpec change **when picked up**; until
then this is the durable "don't forget" list. For the ordered feature lifecycle, see
[`multi-tenant-vehicle-access.md`](./archive/multi-tenant-vehicle-access.md).

| # | Item | Why deferred | Trigger — do it before… | Origin |
|---|------|--------------|--------------------------|--------|
| B1 | **Encrypt Tesla tokens at rest** (`tesla_tokens.access_token` / `refresh_token`) — app-level column encryption (AES-GCM with a key from the environment) or `pgcrypto`. | Tier 1 kept tokens plaintext to stay scoped; acceptable on a locked-down local Postgres. | **Any non-local / production deployment.** Plaintext secrets must not leave a dev machine. | `add-account-module` (Tier 1) design.md Open Questions; noted in `docs/deployment.md`. |



# PEnding to be pickup

- [] **`cmd/poller`** — scheduled runner that periodically calls `the module that fetch vehicle data` logic and appends that data to the database. Foundation for all historical data features.


## How to use this file

- Add a row when a decision defers real work (with a clear **trigger** so it isn't forgotten).
- When you pick an item up, run the normal OpenSpec flow (`grill-me` → `openspec-propose` → …),
  then remove the row (its history lives in the archived change).
