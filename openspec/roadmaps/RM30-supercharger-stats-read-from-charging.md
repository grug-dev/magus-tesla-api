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
| `[x]` | `RM30-charging-add-session-read-port` | `charging` | Add a `Session` domain type and a `SessionReader` port (`ListSessionsByVehicleBetween`) over `charge_sessions`, plus the sqlc query and `NewSessionReader`. No schema change — the existing `idx_charge_sessions_vehicle_stop` serves it. | — | Add a read port to `internal/charging` exposing `charge_sessions`: a `Session` domain type (full row, including the charging-owned battery-% columns) and `SessionReader.ListSessionsByVehicleBetween(ctx, accountID, teslaID, from, to)`, mirroring `Reader.ListEntriesByVehicleBetween`'s inclusive-bounds / no-limit / non-nil-empty-slice semantics. Window on `charge_stop_date_time` so `idx_charge_sessions_vehicle_stop` serves it with no sort. No new DB object. |
| `[ ]` | `RM30-gateway-read-supercharger-stats-from-charging` | `gateway` | Swap `Deps.SuperchargerReader` from `telemetry.SuperchargerReader` to `charging.SessionReader`; **replace `?months=N` with the mandated `?start=&end=` contract**; drop the in-memory month filter for the bounded window; remove the Country / Billing-type table columns and their i18n keys; document the per-endpoint window cap in `internal/gateway/AGENTS.md` and `README.md`. | 1 | Rewire `internal/gateway`'s supercharger-stats slice onto `charging.SessionReader`. Replace the single limit-based read + in-memory `ChargeStartDateTime >= since` filter with one `ListSessionsByVehicleBetween(from=since, to=now)` call. Remove `CountryCode` and `BillingType` from `SuperchargerRowVM`, the fragment template, and the i18n catalogue (both ES and EN). `internal/gateway` must no longer import `internal/telemetry` for the supercharger path. **Ordering flips:** `telemetry.SuperchargerSessionsByVehicle` returned newest-first (`charge_start_date_time DESC`); `charging.SessionReader` returns oldest-first (`charge_stop_date_time ASC`, which is what lets the index serve it). The handler must reverse for display if the table is to stay newest-first — do not "fix" the port to DESC. **API contract change (owner, 2026-08-27):** `?months=N` is a violation of the mandated date-filter convention (`internal/gateway/AGENTS.md` §"HTTP date-filter convention": *"a closed vocabulary of one: `?start=&end=`"*). Replace it with `?start=YYYY-MM-DD&end=YYYY-MM-DD`, reusing the `parseHistoryRange` pattern — same 400 rules, same empty-state-on-400 behaviour, no preset selector on a malformed request. Keep 3/6/12-month preset BUTTONS, but have them emit absolute `?start=&end=` hrefs exactly as the history selector already does. Default window when both params are absent: 6 months (preserves current behaviour). Cap: 400 days (see Decision 6). **Doc tasks (tier 2 only):** amend `internal/gateway/AGENTS.md` §"HTTP date-filter convention" so the cap is stated as **per-endpoint, set by data density** (history 90d / supercharger 400d) instead of a flat 90; add the project-wide rule to `ai/architecture.md` §"Concrete patterns already in use" pointing at that section as the full handler contract; and document both in the root `README.md` (owner request, Decision 7). Keep the existing bold emphasis on the "every future date-filtered endpoint" bullet — it is load-bearing. |

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
   sessions that straddle a month boundary. **Confirmed 2026-08-27:** this is not a new
   call — `telemetry.SuperchargerSessionsByVehicleBetween`, the existing dated sibling
   read, already filters on `charge_stop_date_time` for the same D12 reason. The two
   modules' dated session reads therefore behave identically.
4. **The KB is updated at archive time** via `/kkpa-context-curate from-spec`, per
   `~/.claude/rules/openspec-archive-kb-sync.md` — a staged, human-gated proposal, not an
   in-branch edit.

5. **`/ui/supercharger-stats` moves from `?months=N` to `?start=&end=`** (owner,
   2026-08-27). This is **compliance with an existing rule, not a new standard**:
   `internal/gateway/AGENTS.md` §"HTTP date-filter convention" already mandates
   `?start=YYYY-MM-DD&end=YYYY-MM-DD` for *every* date-filtered gateway endpoint and
   explicitly forbids a count parameter — supercharger-stats simply never complied.
   Consequence for tier 1: the read port's bounds become **whole calendar days with an
   inclusive end day**, not exact instants (reversing the worker's original D5), matching
   `telemetry.SuperchargerSessionsByVehicleBetween`.
6. **Window caps become endpoint-specific; they are set by data density.** The convention's
   90-day cap (`historyRangeMaxDays`) exists to stop unbounded range scans over
   `telemetry` snapshots, which are **dense** — many rows per day. `charge_sessions` is
   **sparse** — a few rows per month — so a 400-day supercharger window is a materially
   smaller read than history's 90-day one. History keeps 90; supercharger-stats gets 400
   (covers a 12-month preset plus leap-year slack). The default window stays
   endpoint-specific, as the convention already allows.
   *Note:* the "global" cap was never globally enforced — it is one constant used by one
   helper that exactly one endpoint calls. Making it per-endpoint is an honest change, not
   a refactor.
7. **The convention and its per-endpoint caps get documented in the root `README.md`**
   (owner request), in addition to amending `internal/gateway/AGENTS.md`. Both are tier 2
   tasks.

## Future work

See `openspec/roadmaps/backlog.md` item 17 — the `gateway` → `telemetry.Reader` /
`Snapshot` question raised by MAG-30's AC2.
