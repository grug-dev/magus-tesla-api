> **Scope.** Additive, non-breaking migration (tier 3 of roadmap `RM39-schema-per-module`,
> MAG-31). Moves `charge_sessions`, `manual_charge_entries` into a new `charging` Postgres
> schema via one goose migration (`CREATE SCHEMA IF NOT EXISTS charging` +
> `ALTER TABLE … SET SCHEMA charging` per table), AND renames `charge_sessions` →
> `supercharger_sessions` (design.md D5b) in the mandatory D7 statement order. The db model
> `ChargeSession` becomes `SuperchargerSession` (D5c — NOT frozen, unlike `ManualChargeEntry`).
> The 2 sqlc query names `MirrorChargeSession`/`VerifyChargeSession` become
> `MirrorSuperchargerSession`/`VerifySuperchargerSession`. The index
> **EVERY** catalog object carrying the old table name is renamed (roadmap **D16**,
> owner-confirmed, expanded from four to nine after a catalog query found five auto-named
> column CHECKs): the index, the named CHECK, the primary key, the unique constraint, and
> `charge_sessions_{battery_pct_source,start_battery_pct,end_battery_pct,start_battery_pct_est,end_battery_pct_est}_check`
> — see design.md "Rename scope (D16)". The completeness criterion is the catalog, not this
> list: no name beginning `charge_sessions` may survive. D9's
> raw-SQL-in-tests debt is schema/name-qualified (design.md's Test Contract point 4) —
> confirmed 32 statements across 10 files, matching the roadmap's own re-measured figure. D12
> checked and NOT present in this module. **D8's `vehicle_metric_watermarks` CHECK
> rewrite + DELETE is NOT PART OF THIS CHANGE AT ALL** — roadmap **D15** (owner-confirmed)
> split it into its own analytics tier 3b, `RM39-analytics-fix-watermark-vocabulary`.
> design.md retains the analysis as hand-off context only. No task here implements it, and no
> worker on this change may write to `internal/analytics/` or to `vehicle_metric_watermarks`.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of T2 (disjoint files) and MAY run
>   in parallel with it.
> - T2 (`query.sql` schema-qualification + the 2 query-name renames) has no dependencies.
>   Independent of T1.
> - T2b (test-file raw SQL, D9, including the T-Order2 EXPLAIN-text update) has no
>   dependencies on T1/T2 (disjoint files: `_test.go` vs migration/`query.sql`) and MAY run in
>   parallel with them.
> - T3 (`sqlc.yaml` rename block + `make sqlc` + `models.go` verification) depends on **both**
>   T1 and T2.
> - T3b (fix the 2 renamed-query call sites in `session_writer.go`/`session_verifier.go`)
>   depends on T3 (needs the regenerated function/params names to compile against).
> - T4 (docs: `internal/charging/AGENTS.md`, `charging.go`'s RM31 D8 comment, KB sweep)
>   depends on T1 only, parallel-ok with T2/T2b/T3/T3b.
> - The watermark CHECK+DELETE is **not a task here**. It is roadmap tier 3b
>   (`RM39-analytics-fix-watermark-vocabulary`), a separate change that `depends_on` this one.
>   See "Hand-off to tier 3b" at the end of this file.
> - T5 (verification) depends on T1–T4.
>
> **Leader-integrated step:** run `make sqlc` after T1 and T2 both land (T3.2). Do not
> hand-edit `internal/charging/db/models.go` or `db/query.sql.go` — both are sqlc-generated.
> Do not run `make migrate-up`, `make db-setup`, or any test suite from this dispatch.

## T1. Goose migration (`internal/charging/db/migrations/`) — no dependencies, parallel-ok with T2/T2b

- [x] T1.1 Create
      `internal/charging/db/migrations/20260902000003_move_charging_to_own_schema.sql`
      (next free chronological timestamp — the latest existing filename across ALL modules'
      migration directories is `internal/analytics/db/migrations/20260902000002_...` (tier
      2's own migration); `20260902000003` collides with neither that nor this module's own
      latest, `20260829000002_add_entry_status.sql` — re-confirm by listing every module's
      `db/migrations/` directory immediately before creating the file, since a later tier may
      have claimed a number in between) with the exact DDL from `design.md` D1/D7 (Up: CREATE
      SCHEMA → 2× SET SCHEMA → RENAME TO → ALTER INDEX RENAME → ALTER TABLE RENAME
      CONSTRAINT; Down: the exact reverse order).

      Acceptance: `make migrate-up` (or the owner's `goose up`) applies the migration
      cleanly; `to_regclass('charging.supercharger_sessions')`,
      `to_regclass('charging.manual_charge_entries')` both return non-NULL;
      `to_regclass('public.charge_sessions')`, `to_regclass('public.manual_charge_entries')`,
      `to_regclass('charging.charge_sessions')` all return NULL (design.md Test Contract
      points 2–3). ALSO assert the D16 rename landed for EVERY object, not a fixed list:
      `SELECT conname FROM pg_constraint WHERE conrelid = 'charging.supercharger_sessions'::regclass`
      returns `supercharger_sessions_pkey`,
      `supercharger_sessions_account_session_unique` and
      `supercharger_sessions_pct_source_required`, plus the five auto-named column CHECKs
      (`supercharger_sessions_battery_pct_source_check`,
      `supercharger_sessions_start_battery_pct_check`,
      `supercharger_sessions_end_battery_pct_check`, and both `_est` siblings) — and NO name
      starting `charge_sessions`. The catalog is the completeness criterion, not a list.
      `goose down` (one step) reverses fully, including every name reverting.

## T2. Schema-qualify `query.sql` + rename 2 query names (`internal/charging/db/query.sql`) — no dependencies, parallel-ok with T1/T2b

- [x] T2.1 Qualify every table reference with `charging.` across all 10 `-- name:` blocks:
      `manual_charge_entries` → `charging.manual_charge_entries` (7 references — `CreateEntry`,
      `UpdateEntry`, `DeleteEntry`, `ListEntriesByVehicle`, `ListEntriesByAccount`,
      `ListEntriesByVehicleBetween`, `ListEntriesByVehicleUpdatedSince`); `charge_sessions` →
      `charging.supercharger_sessions` (6 references — `MirrorChargeSession`,
      `ListSessionsByVehicleBetween`, `LockSessionForVerification`, `VerifyChargeSession`,
      `ListSessionsByVehicleUpdatedSince`, `ListSessionsByVehicle`) — design.md D2 (13 total).
      Acceptance: a word-boundaried grep for bare `\bFROM charge_sessions\b` /
      `\bINTO charge_sessions\b` / `\bUPDATE charge_sessions\b` /
      `\bFROM manual_charge_entries\b` / `\bINTO manual_charge_entries\b` /
      `\bUPDATE manual_charge_entries\b` in the file returns zero matches; confirmed.
- [x] T2.2 Rename the two `-- name:` lines that embed the old table name (design.md
      D5c-query): `MirrorChargeSession` → `MirrorSuperchargerSession`,
      `VerifyChargeSession` → `VerifySuperchargerSession`. Do NOT rename
      `LockSessionForVerification` (no "ChargeSession" in its name — only its table
      reference needs T2.1's qualification). Update the doc comments above each renamed
      query that reference the OLD query name in prose (e.g. "the same shape
      VerifyChargeSession's @battery_pct_source already uses" inside `CreateEntry`'s comment)
      so they name the new query. Acceptance: `grep -c 'name: MirrorChargeSession\|name:
      VerifyChargeSession' internal/charging/db/query.sql` is 0; `grep -c 'name:
      MirrorSuperchargerSession\|name: VerifySuperchargerSession'` is 2.

## T2b. Schema-qualify raw SQL in `_test.go` files (D9) — no dependencies, parallel-ok with T1/T2

- [x] T2b.1 Find every hand-written SQL statement in this module's `_test.go` files that
      references `charge_sessions` or `manual_charge_entries` (this module's own tables —
      NOT `supercharger_sessions` when it appears bare in `db_backfill_integration_test.go`,
      which is telemetry's OWN table, still in `public`, and must stay bare). Use the
      quote-agnostic pattern (design.md D9, matching the roadmap's own corrected method):
      ```
      grep -rnE '(FROM|INTO|UPDATE|JOIN)[[:space:]]+(charge_sessions|manual_charge_entries)\b' \
        --include='*_test.go' internal/charging
      ```
      Confirmed by this change's own design phase: **32 statements across 10 files** —
      `db_entry_status_integration_test.go`, `db_session_integration_test.go`,
      `db_integration_test.go`, `db_inferred_capacity_sessions_integration_test.go`,
      `db_session_reader_updated_since_integration_test.go`,
      `db_session_reader_integration_test.go`, `db_session_verifier_integration_test.go`,
      `db_session_reader_by_vehicle_integration_test.go`,
      `db_inferred_capacity_entries_integration_test.go`, `db_backfill_integration_test.go`.
      Qualify every `manual_charge_entries` reference to `charging.manual_charge_entries` and
      every `charge_sessions` reference to `charging.supercharger_sessions` (schema AND
      name). Acceptance: re-running the grep above after editing returns zero matches; a
      separate check confirms `db_backfill_integration_test.go`'s bare `supercharger_sessions`
      references (telemetry's table) are UNCHANGED.
- [x] T2b.2 `db_session_reader_by_vehicle_integration_test.go`'s T-Order2 case asserts literal
      `EXPLAIN` plan text containing `"Index Scan Backward using
      idx_charge_sessions_vehicle_stop"` (design.md D9's "one additional consequence"). Update
      the expected substring to `"Index Scan Backward using
      idx_supercharger_sessions_vehicle_stop"` — this is a genuine value change (the index
      itself is renamed by T1), not a mechanical qualification. Acceptance: the two `t.Errorf`/
      `strings.Contains` occurrences at (pre-edit) lines 324–325 both reference the new index
      name; grep for the OLD index name anywhere in this test file returns zero matches.

## T3. `sqlc.yaml` rename block + regeneration + verification (`sqlc.yaml`, `internal/charging/db/`) — depends on T1 AND T2

- [x] T3.1 Add a `rename:` map under the charging entry's existing `gen.go` block in the root
      `sqlc.yaml` (NOT the top-level `overrides:` block):
      ```yaml
      gen:
        go:
          package: "chargingdb"
          out: "internal/charging/db"
          sql_package: "pgx/v5"
          emit_json_tags: false
          emit_interface: false
          rename:
            charging_manual_charge_entry:  "ManualChargeEntry"
            charging_supercharger_session: "SuperchargerSession"
          overrides:
            # ...(existing uuid override, unchanged)
      ```
      Key form is the singularized `<schema>_<table>` (design.md D3/D5c) — note
      `charging_supercharger_session` is a NEW key (the table's post-rename name), not a
      preservation of `charging_charge_session`. Do not touch the `account`, `telemetry`, or
      `analytics` entries in this same file.
- [x] T3.2 Run `make sqlc` (leader-integrated step). Regenerates
      `internal/charging/db/models.go`, `db.go`, and `query.sql.go`.
- [x] T3.3 **Diff `internal/charging/db/models.go` against its pre-change version and confirm
      the EXACT expected shape from design.md's Test Contract point 1**: `ManualChargeEntry`
      byte-identical; `ChargeSession` replaced by `SuperchargerSession` with an identical
      field list/order/types (only the type identifier changes). This is the mandatory
      verification step design.md requires — a wrong `rename` key fails silently at exit 0.
      Report the diff output in the final report.
- [x] T3.4 Confirm by inspection that `internal/charging/db/query.sql.go` compiles against the
      new `chargingdb` package (no hand edits), that `MirrorSuperchargerSession`/
      `VerifySuperchargerSession` (and their `*Params` types) exist with the expected shape,
      and that `LockSessionForVerification`'s signature is unchanged except for its table
      reference. Acceptance: `go build ./internal/charging/...` fails ONLY at the T3b call
      sites (expected, until T3b lands) or succeeds if T3b is done in the same pass.

## T3b. Fix the 2 renamed-query call sites — depends on T3

- [x] T3b.1 `internal/charging/session_writer.go`: update `qtx.MirrorChargeSession(ctx,
      chargingdb.MirrorChargeSessionParams{...})` to `qtx.MirrorSuperchargerSession(ctx,
      chargingdb.MirrorSuperchargerSessionParams{...})`. Update the doc comment referencing
      "One transaction, one MirrorChargeSession call per entry" to name the new query.
- [x] T3b.2 `internal/charging/session_verifier.go`: update `q.VerifyChargeSession(ctx,
      chargingdb.VerifyChargeSessionParams{...})` to `q.VerifySuperchargerSession(ctx,
      chargingdb.VerifySuperchargerSessionParams{...})`. Update the doc comments referencing
      "Call VerifyChargeSession" to name the new query.
- [x] T3b.3 `internal/charging/session_reader.go`: `rowToSession` takes a
      `chargingdb.ChargeSession` parameter — update to `chargingdb.SuperchargerSession`.
      Acceptance: `go build ./internal/charging/...` and `go vet ./internal/charging/...`
      both succeed.

## T4. Docs (`internal/charging/AGENTS.md`, `charging.go`, KB sweep) — depends on T1, parallel-ok with T2/T2b/T3/T3b

- [x] T4.1 Update `internal/charging/AGENTS.md`'s Data Ownership section: state that this
      module's data lives in the `charging` Postgres schema (tables
      `manual_charge_entries`, `supercharger_sessions`), and that `charge_sessions` was
      renamed to `supercharger_sessions` (D5b) with the Go type following (`ChargeSession` →
      `SuperchargerSession`, D5c). Update every place the doc names the table
      `charge_sessions` or the type `ChargeSession` (the §Public Interface code blocks, the
      §Data Ownership prose, the index/constraint names in the "Column-by-column" list).
- [x] T4.2 Update `internal/charging/charging.go`'s `SuperchargerSessionAnalyticsReader` doc
      comment (the RM31 D8 "deliberate divergence" note, ~line 409). The comment currently
      states `"Supercharger" is the owner's deliberate divergence from this module's Session*
      family ... chosen for call-site readability`. Once the table itself is named
      `supercharger_sessions`, this is no longer a divergence — it agrees with the table.
      Rewrite the note to say so (design.md's proposal.md "Why the rename" section has the
      exact framing: "D5b makes the table agree with a name the module already chose — it
      removes a divergence rather than creating one").
- [x] T4.3 Grep the repo (`grep -rln` for `charge_sessions`, `ChargeSession`, scoped to
      `docs/`, `ai/`, root `README.md`, `kkpa/context/`) for any doc that names the table/type
      and would now read as stale. Report which files were checked and which need an edit —
      do not edit speculatively.

      Found (this design phase's own grep, for the next worker to act on):
      - `kkpa/context/INDEX.md` — 5 lines naming `charge_sessions` (supercharger stats,
        Supercharger session, charge session log, watermark source, session inferred
        capacity rows).
      - `kkpa/context/architecture/charge-record-mutation.md` — multiple `charge_sessions`/
        `VerifyChargeSession`/`MirrorChargeSession` mentions.
      - `kkpa/context/architecture/nightly-cycle.md` — multiple `charge_sessions`/
        `MirrorChargeSession` mentions, including the watermark-source-label note at
        (pre-edit) line 101.
      - `kkpa/context/architecture/telemetry-ingest-only.md` — already carries a
        **PENDING** banner (top of file) explicitly assigning this file's `charge_sessions`
        → `charging.supercharger_sessions` half of the update to "RM39 tier 3" — this
        change's own doc pass should resolve that banner's charging-side half. The
        `telemetry.supercharger_sessions` → `supercharger_history` half (D5a) stays PENDING
        for tier 4 — do not touch those mentions.
      - `kkpa/context/use-case/charging/delete-manual-charge.md`,
        `verify-session-battery.md`, `update-manual-charge.md` — `charge_sessions` table
        name and `charge_sessions_pct_source_required` constraint name mentioned.
      - `kkpa/context/workflows/manual-charge-crud.md`,
        `workflows/supercharger-stats-read.md` — multiple `charge_sessions` mentions,
        including `idx_charge_sessions_vehicle_stop` in `supercharger-stats-read.md` line 94.
      - `kkpa/context/entities/vehicle-metrics/guide.md` — `charge_sessions` mentioned as
        analytics' Supercharger read source AND as the watermark vocabulary value; this
        file's watermark-vocabulary line (pre-edit line 61) belongs to tier 3b (D15) —
        LEAVE IT UNEDITED and record that you did, since the correct end-state string is the
        one tier 3b writes. Edit only the charging-side facts in this file.
      - **Out of this module's sandbox, found but NOT this change's to edit:**
        `internal/analytics/db_integration_test.go:496` has a comment mentioning
        `charge_sessions_pct_source_required` — analytics' own file; handed off to tier 3b
        (see "Hand-off to tier 3b").
      Acceptance: every file in this list is either edited (charging-side facts only) or
      explicitly left with a reason recorded (deferred to tier 3b, or telemetry-side
      D5a scope), matching this design phase's own findings — do not silently skip one.

- [x] T4.4 Retire stale `charge_sessions` vocabulary from comments and test helper names in
      `internal/charging/` (leader follow-up, found during leader verification: 13 comments +
      5 helper identifiers still carrying names D16 renamed or that no longer exist under
      those names).
      - Renamed 5 test helper identifiers and every call site: `cleanupChargeSessions`,
        `countChargeSessions`, `fetchChargeSession`, `fetchChargeSessionID`, and
        `type chargeSessionRow` (all in `db_session_integration_test.go` /
        `db_session_verifier_integration_test.go`, called from 6 more `_test.go` files).
        `cleanupChargeSessions` could not become a bare `cleanupSuperchargerSessions` — that
        name already exists in `db_backfill_integration_test.go` for telemetry's own seed
        table — so the charging-table helper (and its family) is
        `cleanupChargingSuperchargerSessions`, `countSuperchargerSessions`,
        `fetchSuperchargerSession`, `fetchSuperchargerSessionID`,
        `type superchargerSessionRow`.
      - Fixed stale comments: `db/query.sql` (4 — `idx_charge_sessions_vehicle_stop` →
        `idx_supercharger_sessions_vehicle_stop`, `charge_sessions_pct_source_required` →
        `supercharger_sessions_pct_source_required`), `db_session_integration_test.go` (4),
        `db_session_verifier_integration_test.go` (1),
        `db_inferred_capacity_sessions_integration_test.go` (3),
        `db_backfill_integration_test.go` (1) — `MirrorChargeSession`/`VerifyChargeSession`
        and the renamed constraint names, all updated to the D16/T2 names.
      - Re-ran `make sqlc` after fixing `query.sql`'s comments so `query.sql.go`'s
        sqlc-derived doc comments pick up the fix; `models.go` diffed byte-identical
        (unaffected, since it derives from unchanged migrations, not from `query.sql`).
      - Did NOT touch `internal/charging/db/models.go` (its two stale comments are
        sqlc-generated from the historic `20260823000001` migration's `COMMENT ON`
        statements — D1/D9 forbid editing that migration; left for the owner to decide),
        any historic migration, or anything deferred to tier 3b.
      Acceptance: `go build ./...`, `go vet ./...` pass repo-wide; `gofmt -l
      internal/charging/` empty; repo-wide grep for every renamed identifier/comment string
      (excluding `db/migrations/` and the two forbidden `models.go` lines) returns zero
      matches.

- [x] T4.5 Sweep the remaining `charge_sessions`/`ChargeSession` mentions in
      `internal/charging/` by rule, not by list (leader follow-up: T4.4's brief only
      covered catalog-object/query names, missing ~20 prose mentions naming the table
      itself). Rule applied: a comment describing the table's CURRENT identity uses
      `supercharger_sessions`; a comment narrating HISTORY (an event tied to a name at
      the time, an earlier/rejected draft, an immutable migration filename, or the
      rename itself) keeps the old name; a reference to the Go type
      `chargingdb.ChargeSession` is simply wrong (renamed to `SuperchargerSession`).
      21 renames applied across 8 files: `charging.go` (7 — L47, 283, 295, 346, 392, 460,
      470), `db_session_integration_test.go` (6 — L6 left, see below; L56, 68, 76, 81,
      104, 125 renamed), `db_session_verifier_integration_test.go` (3 — L10 Go-type fix,
      L44, 333), `service.go` (1 — L628 Go-type fix), `session_reader.go` (1 — L119),
      `db_inferred_capacity_entries_integration_test.go` (1 — L94),
      `db_inferred_capacity_sessions_integration_test.go` (1 — L2),
      `db_backfill_integration_test.go` (1 — L27).
      Left as historical, by rule (not by list): `charging.go` L414 (already resolved in
      T4.2 — the rename-itself sentence); `db_session_integration_test.go` L6 ("no reader
      port on charge_sessions in this tier" — an explicit tier-scoped historical fact,
      same shape as AGENTS.md's "gained `charge_sessions` in RM29 tier 6"); `session_writer.go`
      L98/L108 ("added by RM30 ... this module's first reader for charge_sessions" — an
      event dated to a tier, table named as it was then); `db_backfill_integration_test.go`
      L2-3 (bound to the immutable migration filename right next to it, describing what
      that specific historic migration shipped) and L43 (the migration filename constant
      itself, untouched); `db/query.sql` L178 ("An earlier draft carried WHERE
      charge_sessions.tesla_id ..." — explicitly narrates a REJECTED draft of the query
      that never shipped, never the query as it stands today — no `make sqlc` re-run
      needed since `query.sql` itself was not edited this pass).
      Not touched (per constraint): `db/models.go`, any historic migration,
      `internal/charging/AGENTS.md`'s historical rename sentences, anything deferred to
      tier 3b.
      Acceptance: `go build ./...`, `go vet ./...` pass repo-wide; `gofmt -l
      internal/charging/` empty; repo-wide grep for `charge_sessions`/`ChargeSession`
      (excluding `db/migrations/`, `db/models.go`, and the historical mentions listed
      above) returns zero matches.

## Hand-off to tier 3b — NOT a task in this change

The watermark cleanup this change's analysis uncovered is **roadmap tier 3b**,
`RM39-analytics-fix-watermark-vocabulary`, owned by `internal/analytics` and sequenced after
this tier (roadmap D15, owner-confirmed). Nothing below is checked off here; it is written down
so tier 3b starts from a finished hand-off rather than a rediscovery:

- design.md's "D8 — Boundary" section holds the full Up/Down migration SQL, the reasoning for
  the DELETE, and the era-ambiguity argument.
- `internal/analytics/recalculate.go:44` — `sourceChargeSessions = "charge_sessions"` must
  become `"supercharger_sessions"`.
- `internal/analytics/db_integration_test.go:496` — a comment mentioning
  `charge_sessions_pct_source_required`; now `supercharger_sessions_pct_source_required`.
- `kkpa/context/entities/vehicle-metrics/guide.md`'s watermark-vocabulary line (see T4.x) is
  left to tier 3b for the same reason: the correct end-state string is the one tier 3b writes.

**No worker on this change may edit any of these.** They are listed as evidence for the
reviewer that the split was deliberate, not an omission.

## T5. Verification — depends on T1–T4

- [x] T5.1 `go build ./...`, `go vet ./...`, `gofmt -l` pass repo-wide (Claude-run).
- [x] T5.2 Boundary check: `internal/charging` still imports only what
      `internal/charging/AGENTS.md`'s Allowed Imports section already permits; no file
      outside `internal/charging` (and the granted `sqlc.yaml` charging entry +
      `openspec/changes/RM39-charging-move-to-own-schema/` artifacts folder) was touched by
      this change's own tasks (T1–T4). The tier-3b hand-off is documentation only — it
      touches no file.
- [x] T5.3 Confirm no other module's `sqlc.yaml` entry, migrations directory, or `query.sql`
      was touched by T1–T4 — this tier's own sandbox is scoped to the charging entry only.
- [x] T5.4 State in the final report the exact catalog-verification queries from design.md's
      Test Contract points 2–3, so the owner can paste them after `make migrate-up`.
- [x] T5.5 Report the exact test-suite commands the owner must run to confirm this tier's
      assertion-level state (`go test ./internal/charging/...`, and the full `go test
      ./...` / `make test-with-db`) — this tier does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns it from
      `awaiting-user-verification` into `done`.
- [x] T5.6 `openspec validate RM39-charging-move-to-own-schema --strict` passes and every
      checkbox above (T1–T4; the tier-3b hand-off is not a checkbox) reflects real completion.
