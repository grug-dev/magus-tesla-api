# RM30 — /supercharger-stats reads from `charging`, not `telemetry`

Source ticket: MAG-30 — https://linear.app/magus-monitor/issue/MAG-30/supercharger-stats-reads

**Intention:** `/supercharger-stats` renders from `charge_sessions` through a `charging`
read port, so the page reads the module that *owns* charge sessions instead of
`telemetry`'s raw collection buffer.

## Why this is a roadmap and not one change

It spans two modules — `charging` must grow a session read port (it has only
`SessionWriter` today), and `gateway` must swap onto it. `openspec/config.yaml`
§`rules.proposal` forbids one big cross-module change.

## Findings that shaped it (verified in code, 2026-08-27)

The ticket's premise was inaccurate in two places; the artifacts below reflect the code,
not the ticket wording:

- The page does **not** read Tesla's Fleet API. It reads the telemetry table
  `supercharger_sessions` via `telemetry.SuperchargerReader`.
- **`internal/app/processor.go`** — not `analytics` — mirrors sessions into
  `charge_sessions` via `charging.SessionWriter.MirrorSessions`, as a **verbatim copy with
  no calculations**. `analytics` reads `telemetry.SuperchargerReader` directly and never
  writes `charge_sessions`.

`charge_sessions` is a strict subset of `supercharger_sessions`: it has no `country_code`
or `billing_type`, both of which the page's session table currently displays.

## Tiers

Legend — `[ ]` pending (change not created) · `[~]` in progress (change created, not
archived) · `[x]` done (archived).

| Status | Change | Module | Scope | depends_on | Proposal prompt |
|---|---|---|---|---|---|
| `[~]` | `RM30-charging-add-session-read-port` | `charging` | Add a `Session` domain type and a `SessionReader` port (`ListSessionsByVehicleBetween`) over `charge_sessions`, plus the sqlc query and `NewSessionReader`. No schema change — the existing `idx_charge_sessions_vehicle_stop` serves it. | — | Add a read port to `internal/charging` exposing `charge_sessions`: a `Session` domain type (full row, including the charging-owned battery-% columns) and `SessionReader.ListSessionsByVehicleBetween(ctx, accountID, teslaID, from, to)`, mirroring `Reader.ListEntriesByVehicleBetween`'s inclusive-bounds / no-limit / non-nil-empty-slice semantics. Window on `charge_stop_date_time` so `idx_charge_sessions_vehicle_stop` serves it with no sort. No new DB object. |
| `[ ]` | `RM30-gateway-read-supercharger-stats-from-charging` | `gateway` | Swap `Deps.SuperchargerReader` from `telemetry.SuperchargerReader` to `charging.SessionReader`; drop the in-memory month filter for the bounded window; remove the Country / Billing-type table columns and their i18n keys. | 1 | Rewire `internal/gateway`'s supercharger-stats slice onto `charging.SessionReader`. Replace the single limit-based read + in-memory `ChargeStartDateTime >= since` filter with one `ListSessionsByVehicleBetween(from=since, to=now)` call. Remove `CountryCode` and `BillingType` from `SuperchargerRowVM`, the fragment template, and the i18n catalogue (both ES and EN). `internal/gateway` must no longer import `internal/telemetry` for the supercharger path. **Ordering flips:** `telemetry.SuperchargerSessionsByVehicle` returned newest-first (`charge_start_date_time DESC`); `charging.SessionReader` returns oldest-first (`charge_stop_date_time ASC`, which is what lets the index serve it). The handler must reverse for display if the table is to stay newest-first — do not "fix" the port to DESC. |

`cmd/web` wiring (injecting `charging.NewSessionReader(pool)` into `gateway.Deps`) is
leader-owned integration — it sits outside `internal/`, so it belongs to no tier.

## Decisions

1. **Country code + billing type are dropped from the page, not added to the table.**
   The user chose the minimal-scope option over a migration + backfill. Consequence: two
   visible table columns disappear; this is intended, not a regression to fix. It also
   means the change creates **no database object**, so the `database` design gate does not
   trip.
2. **`gateway` → `telemetry.Reader` / `Snapshot` stays.** The standing rule is *"the
   gateway reads the module that OWNS the domain, through its port, never its DB"* — not
   "gateway may never import telemetry". `telemetry` owns live vehicle snapshots and has
   no other owner, so the dashboard tiles and the history battery chart are correct as
   they are. Only the supercharger path is misfiled, because `charging` owns charge
   sessions. Follow-up ticket requested by the user — recorded as backlog item 17.
3. **The month window moves from `charge_start_date_time` to `charge_stop_date_time`.**
   The existing index leads on stop time, and roadmap D12 already established that a
   session belongs to the window containing its stop instant. Minor behavioural shift for
   sessions that straddle a month boundary. Flagged for review rather than escalated.
4. **The KB is updated at archive time** via `/kkpa-context-curate from-spec`, per
   `~/.claude/rules/openspec-archive-kb-sync.md` — a staged, human-gated proposal, not an
   in-branch edit.

## Future work

See `openspec/roadmaps/backlog.md` item 17 — the `gateway` → `telemetry.Reader` /
`Snapshot` question raised by MAG-30's AC2.
