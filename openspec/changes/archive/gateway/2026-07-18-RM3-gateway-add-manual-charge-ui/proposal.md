## Why

Tier 1 (`RM3-manualcharge-add-entries`) built the isolated `internal/manualcharge` backend
module and its `Writer`/`Reader` ports. Users can now create, update, and delete manual
charge entries in the database, but there is no UI — the data is inaccessible without
a direct database client. Tier 2 closes that gap: it adds a **standalone "Charge log" page**
(`/charges`) to the gateway, with an htmx/Templ form for creating entries and an inline-edit
table for managing them.

The raw user request (2026-07-18):

> "ok, let's go for a manual entry approach. Looks like this might be a big roadmap since it
> touches front-end pages with HTMX. I want this approach lives in its own package/module
> isolated of the other ones. It does not consumes any tesla's fleet api... separate the
> task/roadmap tiers by frontend and backend changes..."

The leader conducted a Step-2 user interview (2026-07-18) that resolved all open design
decisions (D3–D5 below). A separate grill-me pass is not required for this tier; the
interview served that function (per binding decision D5 in the dispatch).

## What Changes

**Module: `internal/gateway/`** — the only module that produces HTML (ai/architecture.md §2).
This change extends the gateway with:

- A standalone `/charges` page (Charge log) linked from the dashboard nav.
- htmx fragment routes under `/ui/charges/...` for the list, create, inline-edit, and delete
  flows.
- Injection of `manualcharge.Writer` and `manualcharge.Reader` into `gateway.Deps`,
  `handlers.Deps`, and `handlers.Handler`, and their corresponding wiring in `cmd/web`.
- A nav link in the dashboard/layout templates pointing to `/charges`.
- An amendment to `internal/gateway/AGENTS.md` documenting the D4 boundary rule change
  (the gateway may now call `manualcharge.Writer` on explicit form POSTs; see design.md).

This change also touches `cmd/web` (wiring the new ports) — this is the companion entry
point that assembles the gateway; it has no business logic and remains thin.

**Breaking:** No. This adds new routes and new `Deps` fields. Existing routes, handlers,
templates, and the `account`/`telemetry`/`tesla` integrations are untouched. The new `Deps`
fields (`ManualChargeWriter`, `ManualChargeReader`) extend the struct; callers that do not
supply them fail at compile time in `cmd/web` (caught before any deploy), which is the
correct behavior for mandatory dependencies.

**Modules affected:**

- `internal/gateway/` — the primary target.
- `cmd/web/` — wiring only; granted by the leader for explicit cross-file wiring.
- No changes to `internal/manualcharge`, `internal/account`, `internal/tesla`, or
  `internal/telemetry`.

**No new database objects.** This change adds zero new tables, columns, indexes, migrations,
or sqlc queries. All persistence is owned by `internal/manualcharge` (tier 1). The gateway
calls the tier-1 ports; it never touches a database directly. The database design gate is
therefore not triggered.

## Read Paths Affected

- **Per-vehicle charge list** — `manualcharge.Reader.ListEntriesByVehicle(ctx, accountID,
  teslaID, limit)`, served by the index `(account_id, tesla_id, charged_on DESC)` (designed
  in tier-1 design.md D3). Called once per `/charges` page render and once per
  `/ui/charges/list` fragment refresh. This is the hot read path for the new page.

- **Account-wide charge list** — `manualcharge.Reader.ListEntriesByAccount(ctx, accountID,
  limit)`, served by `(account_id, charged_on DESC)` (tier-1 D3). Called when no vehicle
  filter is active.

- **Registered vehicles** — `account.Service.RegisteredVehicles(ctx, uid)`, already used by
  the dashboard, called once per `/charges` page load to populate the vehicle picker and
  to validate ownership before any write. Not a new read path — reuses existing index on
  the account registry.

These read paths are all index-covered and design-gated in tier 1; no new index work is
needed here.

## Capabilities

### Modified Capabilities

- `gateway` — the existing capability gains a new page (`/charges`), new htmx fragment
  routes, and new `Deps` fields. The "read-only at request time" rule is **amended** to
  permit user-initiated writes via `manualcharge.Writer` on explicit CSRF-protected form
  POSTs (see design.md D4 boundary-rule amendment). The amendment is documented in
  `internal/gateway/AGENTS.md` during implementation.

### Consumed Capabilities (no change to their specs)

- `manual-charge-log` — the tier-1 backend. This tier consumes its `Writer` and `Reader`
  ports; their spec (`openspec/specs/manual-charge-log/spec.md`) is not modified.
