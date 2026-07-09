# Multi-Tenant Vehicle Access — Lifecycle Roadmap

**Global intention:** stand up the platform end-to-end so that **any user can sign in with
Google, connect their own Tesla, and monitor their vehicles.** This roadmap is the ordered set
of OpenSpec changes that deliver that lifecycle — the durable source of truth for *what to
build next and in what order*.

Principle: **one change = one module's slice** (the Agentic Modular Monolith — a change is a
work-packet). Keep each change small, module-scoped, and independently reviewable.

## Tiers

| # | OpenSpec change | Domain | Delivers | Depends on |
|---|---|---|---|---|
| 0 | `document-tesla-adapter` *(optional)* | `tesla` | Retro-spec the already-built adapter (no code) | — |
| 1 | `add-account-module` | `account` | Persistence + service: tables, `UpsertFromOAuth`, `SaveTeslaTokens`, `AccessTokenFor` (refresh) | — |
| 2 | `add-web-gateway-foundation` | `gateway` | Web skeleton: `cmd/web` + `internal/gateway`, router, Templ base layout, session middleware | — |
| 3 | `add-google-login` | `gateway` (+`account`) | Google OAuth app login → session + `account.UpsertFromOAuth` | 1, 2 |
| 4 | `add-tesla-connect` | `gateway` (+`account`) | Tesla OAuth connect (reuses `internal/auth`) → `account.SaveTeslaTokens` | 1, 2 |
| 5 | `add-vehicle-dashboard` | `gateway` (+`account`,`tesla`) | htmx page listing the user's vehicles via `account.AccessTokenFor` → `tesla.ListVehicles` → Templ fragment | 1, 2, 4 |

**Execution order:** 1 (and optionally 0) → 2 → 3 → 4 → 5.

## Progress checklist

Quick human view. The **authoritative** record — status, which AI assistant + model ran each
tier, timestamps, token usage, notes — lives in
[`multi-tenant-vehicle-access.progress.json`](./multi-tenant-vehicle-access.progress.json)
beside this file. Tick a box when that tier is **archived**.

- [x] Tier 0 — `document-tesla-adapter` (tesla) *(optional)* — _archived_
- [x] Tier 1 — `add-account-module` (account) — _archived_
- [x] Tier 2 — `add-web-gateway-foundation` (gateway) — _archived_
- [x] Tier 3 — `add-google-login` (gateway) — _archived_
- [x] Tier 4 — `add-tesla-connect` (gateway) — _archived_
- [x] Tier 5 — `add-vehicle-dashboard` (gateway) — _archived_

## Resuming across sessions / assistants

A tier may be run in a later session — or by a **different AI code assistant** (Claude Code,
OpenCode, Cursor). Before starting a tier, **read the progress JSON** to see what's done and
who did it; after finishing, **update that tier's entry**. This is what lets state survive
token limits and hand-offs between tools.

## Just-in-time

Only `add-account-module` is proposed today. Every other tier is created **when its tier is
reached**, so each design reflects the code as it actually is at that moment.

## Per-tier workflow

Run this loop for each tier (the project's OpenSpec config requires proposals to be grilled
first):

1. **`grill-me`** — stress-test that tier's design (routes, session/auth strategy, failure cases).
2. **`openspec-propose`** — generate `proposal.md` + `design.md` + `specs/<domain>/spec.md` + `tasks.md`.
3. **`openspec validate <change> --strict`** — then an optional review.
4. **`/opsx:apply`** — the assistant implements; the **user** runs `go mod tidy`, `sqlc generate`,
   `goose`, `go build` / `go vet` / `go test` (builds/codegen are never run by the assistant).
5. **`/opsx:verify`** — confirm the implementation matches the artifacts.
6. **`/opsx:archive`** — merge the delta into `openspec/specs/<domain>/spec.md` (grows OpenSpec's
   durable context of the app). *(KB sync is skipped — no `kkpa/context/`.)*

## Updating progress

When a tier changes state:

1. Set its `status` in the progress JSON: `not-started → proposed → applied → verified → archived`.
2. Record `assistant` (e.g. "Claude Code", "OpenCode"), `model`, `started_at`, `completed_at`.
3. Optionally fill `tokens` and `notes`.
4. Tick the checklist box above once the tier is **archived**.

**Note on `tokens`:** an AI assistant cannot reliably self-measure its own token consumption
mid-run, so this field is **filled manually** from your CLI's usage/cost display (e.g. Claude
Code `/cost`). Leave it `null` to skip — it's for your learning, not required by the workflow.
