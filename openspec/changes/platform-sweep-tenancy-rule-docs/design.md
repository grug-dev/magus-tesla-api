# Design — platform-sweep-tenancy-rule-docs

## Context

MAG-63 reversed the platform's tenancy rule across six prior steps. This step is a documentation
and dead-code sweep, not a schema change. Every decision below was checked against the current
repository state — the source ticket's own list turned out to be partly wrong, so "verified" here
means re-derived from a real `grep`/read, not copied from the ticket text.

**No database object changes in this change** — no table, column, index, constraint, view, or
migration. The project's DB design gate (`openspec/config.yaml` rules.design) therefore does not
fire. Recording that explicitly, per the project's own rule that "I did not think about it" and
"it is unaffected" are different findings — this is the second one.

## D1 — Port deletion list: the source ticket's list was wrong

The ticket named three methods for deletion. Verified against the real interface
(`internal/telemetry/telemetry.go`) and a repo-wide `grep`:

| Method | Ticket says | Verified state | Action |
|---|---|---|---|
| `SuperchargerHistoryByAccount` | delete | does not exist as a Go symbol anywhere in the repo | nothing to do |
| `SuperchargerHistoryByVehicle` | delete | no caller outside `internal/telemetry` (interface, impl, logging decorator, tests) | **delete** |
| `SuperchargerHistoryByVehicleBetween` | delete | no caller outside `internal/telemetry` | **delete** |
| `SuperchargerHistoryByVehicleUpdatedSince` | **delete** | **LIVE** — `internal/app/processor.go:223`, the nightly Supercharger mirror's only read | **keep** — deleting it breaks the mirror |

The ticket's list is self-contradictory: it asks to delete the one method the nightly mirror
actually depends on. The interview corrected this before any artifact was written.

Deleting the two dead methods touches, all in `internal/telemetry` except the last:

- `telemetry.go` — remove both method signatures and their doc comments from the
  `SuperchargerHistoryReader` interface.
- `reader.go` — remove both implementations.
- `query_log.go` — remove both logging-decorator methods.
- `db/query.sql` — remove both `-- name:` blocks; regenerate `db/query.sql.go` via
  `sqlc generate` (`make sqlc`).
- `db_supercharger_integration_test.go` (covers `SuperchargerHistoryByVehicle`) and
  `db_supercharger_between_integration_test.go` (covers `...Between`) — delete outright.
  `db_supercharger_vehicle_updated_since_integration_test.go` is untouched — it exercises only
  the kept method.
- **Correction, found during implementation:** an earlier version of this design also claimed
  `db_supercharger_battery_pct_integration_test.go` was untouched. That was wrong. The file
  calls `SuperchargerHistoryByVehicle` at four sites as its read-back mechanism (it verifies the
  battery-% trio, not the deleted method itself). This change swaps those four calls to
  `SuperchargerHistoryByVehicleUpdatedSince`, with every assertion unchanged.
- `query_log_test.go` — remove the two deleted methods' cases; keep the rest.
- `internal/app/processor_test.go` — the fake `SuperchargerHistoryReader` must drop the same two
  methods in the **same wave** as the interface shrinks. A fake that still implements a wider
  interface than the real one still compiles today (Go structural typing does not require an
  exact match to satisfy a narrower interface) — so this is not a hard compile-order dependency
  in the direction one might expect. It matters the other way: once the real interface narrows,
  the fake may keep extra methods and still satisfy it. The true reason to land it in the same
  wave is repository hygiene — a fake implementing dead methods is exactly the kind of drift this
  whole change exists to prevent — not a build break. (Recorded honestly rather than overstating
  the dependency as a compile failure it is not.)

## D2 — Spec targets: exactly two requirements, checked against the running code

Verified by reading both specs in full and cross-checking against the implementation:

- `openspec/specs/analytics/spec.md:96`, "Multi-Tenant Scoping on Every Underlying Read" —
  **stale**. It says the capability passes the given account identifier to every underlying read
  (telemetry, Supercharger, manual entries, vehicle lookup). Checked against
  `internal/analytics/reader.go:134-161` (`RecentEfficiency`, the capability this requirement
  describes): the account identifier reaches exactly **one** call,
  `carTypeFor(ctx, r.account, accountID, teslaID)` — a car-type lookup. The telemetry,
  Supercharger, and manual-entry reads (`SnapshotsByVehicleSince`, `ListSessionsByVehicle`,
  `ListEntriesByVehicle`) all take `teslaID` alone. **Removed** — see the `specs/analytics/spec.md`
  delta in this change.
- `openspec/specs/analytics/spec.md:265`, "Multi-Tenant Scoping On Every Underlying Read"
  (capitalized "On") — **already correct**. States the vehicle-only rule this platform now
  follows, for the capability's other derivation (per-day consumption). No change.
- `openspec/specs/charge-session-log/spec.md:889`, "Supercharger Mirror Synchronization Is
  Bounded By An Account Watermark" — **stale**. `internal/charging/db/migrations/
  20260912000003_rekey_mirror_watermarks_on_tesla_id.sql` dropped `mirror_watermarks.account_id`
  and its `UNIQUE (account_id)` constraint, replacing them with `tesla_id BIGINT NOT NULL` and
  `UNIQUE (tesla_id)`. `internal/charging/charging.go`'s `MirrorWatermarkStore` port
  (`MirrorWatermark(ctx, teslaID)`, `AdvanceMirrorWatermark(ctx, teslaID, observed)`) confirms the
  cursor is per vehicle. **Rewritten** — see the `specs/charge-session-log/spec.md` delta.

**Checked and found nothing to change** (the ticket asked for telemetry and charging spec edits;
that instruction was wrong):

- `openspec/specs/telemetry/spec.md` — no account-scoping requirement exists. Its account-level
  headings (`Collection Spans All Accounts And Vehicles`, `Account-Level Attempt And Outcome
  Counts`, `Poll Account Election`) describe **polling**, which stays legitimately account-level —
  a car is polled on behalf of one elected account, and that fact is unrelated to how data is
  keyed once captured.
- `openspec/specs/charging/spec.md` — no such requirement.
- `openspec/specs/manual-charge-log/spec.md:256`, "Multi-tenant isolation" — already corrected by
  MAG-68; its own text says so ("CHANGED from its prior revision, under which the write-path
  proof was an account identifier").
- `openspec/specs/gateway/spec.md:947`, "Tenant Isolation" — out of scope for this change.

## D3 — Third stale sentence, found beyond the interview's original scope: APPROVED and fixed

While verifying D2, a **third** stale sentence turned up, inside the "Recent Energy-Per-Kilometre
Derivation" requirement itself (`openspec/specs/analytics/spec.md:9-19`, not the requirement named
in D2):

> "The capability SHALL scope every underlying read to the given account, even when the vehicle
> identifier alone would suffice, as defense-in-depth tenant isolation."

This was false for the same reason D2's removed requirement was false: `RecentEfficiency`'s
account identifier reaches only the `carTypeFor` car-type lookup, not "every underlying read."

The interview's original binding scope was "exactly TWO requirements, no more," and this sentence
sits inside a **third** requirement, so the first version of this design flagged it for the user
instead of fixing it. **The user has since reviewed the finding and approved the fix; the leader
independently re-verified it against `internal/analytics/reader.go:134-161` and confirmed the same
four calls this design already found: three take the vehicle identifier alone
(`SnapshotsByVehicleSince`, `ListSessionsByVehicle`, `ListEntriesByVehicle`) and one,
`carTypeFor(ctx, r.account, accountID, teslaID)`, takes both the account and the vehicle.** This
design is now accepted, and the requirement is corrected in this change's `specs/analytics/spec.md`
delta (a `MODIFIED Requirements` block).

**The fix is precise, not a removal.** `RecentEfficiency` still takes an `accountID` parameter and
still uses it — the corrected requirement does not say "no account identifier is used." It says:
reads are scoped by the vehicle identifier; the account identifier is used only to resolve the
vehicle's car type, never as read scoping. Overstating this as "no account identifier" would be a
new inaccuracy in the opposite direction.

**Declined, on purpose: the dead `design.md` pointer inside `reader.go`'s doc comment.**
`RecentEfficiency`'s own doc comment reads `// RecentEfficiency implements Reader (design.md
"Public Surface", D4 accountID scoping).` — a pointer into an archived change's `design.md` that a
later reader cannot resolve, naming a decision (`D4 accountID scoping`) whose premise this very
change corrects. This change does **not** edit `internal/analytics/reader.go` — the user chose the
spec-only fix, not a code or comment change. Recording the decline here explicitly, so a reviewer
does not raise the stale comment as a missed finding: it was seen, and left alone on purpose.

## D4 — Three stale comments, left alone deliberately

`internal/charging/db/models.go:48`,
`internal/charging/db/migrations/20260906000002_add_mirror_watermarks.sql:61`, and
`internal/telemetry/db/migrations/20260906000001_add_supercharger_history_account_updated_idx.sql:3`
all still name `SuperchargerHistoryByAccountUpdatedSince` — a method that no longer exists
(replaced by `SuperchargerHistoryByVehicleUpdatedSince` when `RM57-telemetry-rekey-supercharger-
history-on-tesla-id` dropped `account_id` from `supercharger_history`).

`models.go:48` is sqlc-generated from a Postgres `COMMENT ON COLUMN` in a migration, so it can
only change by writing a new migration whose sole purpose is editing comment text. The project's
standing rule: a drifted table `COMMENT` is not a finding, and never justifies a migration to fix
comment text (`kkpa-module-worker` doc-pack precedent,
`feedback-stale-sqlc-table-comments-are-fine`). The two migration-file comments are inside
already-applied, frozen migrations — editing them would be editing history for no functional
gain. All three are left untouched, on purpose, so a reviewer does not flag them as a missed
sweep target.

## D5 — `make tenancy-guard`: detection rule

New standalone guard, mirroring the grep-based shape and escape-hatch convention of the project's
eight existing guards (`ui-guard`, `i18n-guard`, `money-guard`, `tz-guard`, `migration-guard`,
`boundary-guard`, `theme-guard`, `vehicleref-guard`, `archive-guard`).

**Rule:** fail if any `internal/<module>/db/query.sql` (or `.sql` file under a module's `db/`
directory) **outside `internal/account`** contains `account_id` as a **whole word** — a
word-boundary match, not a substring match. Grep shape:
`grep -nE '\baccount_id\b' internal/<module>/db/**/*.sql` for every module directory except
`internal/account`, excluding lines matching `created_by_account_id` or `polled_by_account_id`.

**Why word-boundary, and why those two exclusions matter:** a substring match
(`grep 'account_id'`) would also match `created_by_account_id` and `polled_by_account_id` — the
two attribute columns MAG-63 deliberately kept. Those columns record **who acted** (which
account's token paid for a poll, which account created a manual entry), not what the car did —
they are not a tenancy filter, and the guard must never fire on them. A naive substring grep would
make the guard permanently red and force every module to carry an escape-hatch comment on a
column that is correct by design. The guard therefore matches `account_id` as a whole word and
then excludes any line also containing `created_by_account_id` or `polled_by_account_id` before
failing.

**Escape hatch:** a trailing `// tenancy:allow: <reason>` comment on the same line, mirroring
`boundary-guard`/`money-guard`/`tz-guard`.

**Measured fact — the guard passes today with zero violations.** Checked directly:

- `internal/analytics/db/query.sql` — one hit, and it is inside a SQL comment, not a filter.
- `internal/charging/db/query.sql` — only `created_by_account_id` and comment mentions.
- `internal/telemetry/db/query.sql` — only `polled_by_account_id`, on an `INSERT` column list.

So this guard adds no red build and fixes nothing by itself — its only job is to keep the query
layer clean going forward, catching a future regression before review does.

**Wired into `make check`**, joining the other nine phases (`build vet ui-guard i18n-guard
money-guard tz-guard migration-guard boundary-guard theme-guard vehicleref-guard archive-guard`
becomes ten with `tenancy-guard` added). `CLAUDE.md`'s allowed-commands list and `make check`
explanation gain the new guard, per the project's "workflow decisions are documented with their
steps" rule.

## D6 — Docs rewrite content

Both `ai/go-conventions.md` (~lines 263-266) and `ai/architecture.md` (~lines 315-319) carry the
same single-rule statement today: "`account_id` is the leading index column on every multi-tenant
table." Both are replaced with the same three-rule table:

| Rule | Applies when | Tables |
|---|---|---|
| Key on `tesla_id` | `tesla_id` is `NOT NULL` | `telemetry.vehicle_snapshots`, `analytics.vehicle_metrics`, `analytics.vehicle_metric_watermarks`, `analytics.charge_gaps` |
| Key on `vin` | `tesla_id` is nullable — the row can exist before the vehicle is known | `telemetry.supercharger_history`, `charging.supercharger_sessions` |
| Keep `account_id`, demoted to an attribute | the row records who acted, not what the car did | `charging.manual_charge_entries`, `telemetry.poll_attempts` |

Plus the index-redundancy rule carried over from MAG-63: do not keep an index just because the
old table had one. A `UNIQUE (a, b)` constraint already builds a btree on `(a, b)` that serves
equality on `a`, point lookups on `(a, b)`, range scans on `b` within one `a`, and
`ORDER BY b DESC` when `a` is pinned. A separate `(a, b DESC)` index is redundant. Justify every
index against a query that exists today.

`ai/architecture.md` additionally gains the tenancy-proof rule that the docs never stated
explicitly, even though the code (`internal/vehicleref`, archived
`platform-add-vehicle-authorization-seam`) already implements it: **the gateway authorizes the
vehicle; modules below it do not check tenancy.** Today `WHERE account_id = $1` used to be the
tenant boundary in SQL. Removing it removes that net. The replacement is the gateway resolving and
authorizing the caller's vehicles (`internal/gateway/handlers/handlers.go`'s `authorizeVehicle`),
then calling modules with the vehicle only. `internal/vehicleref` makes it a compile-time check —
`Ref`'s unexported field means a handler that skips the check has no `Ref` to pass to a port that
requires one, so the mistake fails to compile. `make vehicleref-guard` enforces the call sites.

## D7 — KB reconciliation: split rule and the one confirmed finding

Split rule (binding, from the standing KB-sync convention): component-map, file-map, and path
facts are corrected **directly** in the guide; glossary, gotcha, and `INDEX.md` entries are
**staged** under `kkpa/context/pending-spec-to-sync/` via `kkpa-context-curate`, for the user's
review — never applied directly.

**Confirmed finding, apply directly:** `kkpa/context/architecture/nightly-cycle.md`'s "Table
effects" table, the `vehicle_snapshots` row, still names the write's on-conflict target as
`(account_id, tesla_id, captured_date)`. Verified against
`internal/telemetry/db/migrations/20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`: the
real constraint, after that migration, is `vehicle_snapshots_tesla_date_unique UNIQUE (tesla_id,
captured_date)`. This is a structural fact (what the write path actually conflicts on) — applied
directly, not staged.

**Checked, found current:** the port map in the same file (line 71) already says
`SuperchargerHistoryByVehicleUpdatedSince` and already notes the table dropped `account_id`
(RM57 D7) — no correction needed there. `kkpa/context/INDEX.md` was grepped for
`account_id`/`tenanc`/`leading index`/`SuperchargerHistoryByAccount` and returned no stale hits —
its existing entries (`vehicle ownership proof`, `can a module check tenancy itself`,
`polled_by_account_id`, `session vehicle keying`) already describe the current rule. A sweep that
finds nothing to stage is an acceptable outcome, not a task failure — it means an earlier change
already did this reconciliation for `INDEX.md`.

The remaining four guides (`entities/vehicle-metrics/guide.md`,
`architecture/telemetry-ingest-only.md`, `architecture/schema-per-module.md`,
`workflows/supercharger-stats-read.md`) showed no stale `account_id` reference in this pass, but
the task list still asks the implementer to re-check them once the port deletion (D1) and doc
rewrite (D6) land, since a guide can reference a symbol or rule this proposal changes even where
today's snapshot looked clean.

## D8 — `telemetry.vehicle_snapshots.account_id` stays: dropping it is `MAG-83`'s job, not this change's

The user asked once to add "drop `account_id` from `telemetry.vehicle_snapshots`" to this
change, then chose to leave it out. Recording the decision and the reason here, since the
question already came back once and will likely come back again.

**The column is dead today.** It has been nullable since MAG-65, nothing writes to it, nothing
reads it, and no index covers it. The live table carries only two constraints:
`vehicle_snapshots_pkey (id)` and `vehicle_snapshots_tesla_date_unique (tesla_id, captured_date)`
(the second, from `20260911000002_rekey_vehicle_snapshots_on_tesla_id.sql`, is what D7's KB
finding corrects a stale reference to).

**It is NOT dropped in this change.** `MAG-83` —
https://linear.app/magus-monitor/issue/MAG-83/squash-migrations-to-one-baseline-per-module-each-with-its-own-goose
— owns it. That ticket squashes each module's migrations to one baseline per module, and drops
the column simply by leaving it out of the new baseline. Its own "Done when" list already
includes "`telemetry.vehicle_snapshots.account_id` is gone."

**Dropping it here would add two edits that `MAG-83` then deletes anyway:**

1. A new `internal/telemetry/db/migrations/` migration with a bare `DROP COLUMN account_id`.
2. An edit to `internal/analytics/db/migrations/20260908000002_add_tpms_pressure_columns.sql:82`,
   whose backfill joins on `WHERE vm.account_id = vs.account_id`. Without that edit, the join
   would fail on every fresh database and every disposable test container provisioned after the
   drop, since `MIGRATIONS_DIRS` runs `internal/telemetry`'s migrations before
   `internal/analytics`'s — the column would already be gone by the time that backfill runs.

Both edits are throwaway work under a squash that is already scheduled to remove the column a
different way. This change adds neither migration and does not touch the analytics migration.

**Consequence for this change, restated:** no database object of any kind changes here — no
table, column, index, constraint, view, or migration (see Context above and the proposal's
Impact section). This decision is the concrete reason the `vehicle_snapshots.account_id` case in
particular stays out, on top of the general no-DB-changes scope this change already committed to.
