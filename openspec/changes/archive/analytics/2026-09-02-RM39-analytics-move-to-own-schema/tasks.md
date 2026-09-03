> **Scope.** Additive, non-breaking migration (tier 2 of roadmap `RM39-schema-per-module`,
> MAG-31). Moves `vehicle_metrics`, `vehicle_metric_watermarks`, `charge_gaps` into a new
> `analytics` Postgres schema via one goose migration (`CREATE SCHEMA IF NOT EXISTS analytics` +
> `ALTER TABLE … SET SCHEMA analytics` per table). No table, column, index, or constraint is
> renamed — D5a/D5b/D5c (the rename decisions) do not touch this module. Every `query.sql` table
> reference becomes schema-qualified (forced by sqlc, design.md D2); `sqlc.yaml` gains a
> `gen.go.rename` block so `ChargeGap`, `VehicleMetric`, `VehicleMetricWatermark` keep their
> exact Go names (design.md D3) — **verified by diffing `models.go`, not trusted from the
> config**. goose itself is unchanged (D4): no `db-reset`, `make migration-guard` stays
> required. `vehicle_metric_watermarks.source`'s stored string vocabulary and its CHECK
> constraint are DATA and are NOT touched (design.md D-source-values). D9's raw-SQL-in-tests
> debt is schema-qualified in this tier (design.md's Test Contract point 3), including one
> discovery beyond the roadmap's own estimate — see T2b below.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of T2 (disjoint files) and MAY run
>   in parallel with it.
> - T2 (`query.sql` schema-qualification) has no dependencies. Independent of T1.
> - T2b (test-file raw SQL, D9) has no dependencies on T1/T2 (disjoint files: `_test.go` vs
>   migration/`query.sql`) and MAY run in parallel with them.
> - T3 (`sqlc.yaml` rename block + `make sqlc` + `models.go` verification) depends on **both**
>   T1 and T2.
> - T4 (docs: `internal/analytics/AGENTS.md`) depends on T1 only, parallel-ok with T2/T2b/T3.
> - T5 (verification) depends on T1–T4.
>
> **Leader-integrated step:** run `make sqlc` after T1 and T2 both land (T3.2). Do not hand-edit
> `internal/analytics/db/models.go` or `db/query.sql.go` — both are sqlc-generated. Do not run
> `make migrate-up`, `make db-setup`, or any test suite from this dispatch.

## T1. Goose migration (`internal/analytics/db/migrations/`) — no dependencies, parallel-ok with T2/T2b

- [x] T1.1 Create `internal/analytics/db/migrations/20260902000002_move_analytics_to_own_schema.sql`
      (next free chronological timestamp: the latest existing filename across ALL modules'
      migration directories is `internal/account/db/migrations/20260902000001_move_account_to_own_schema.sql`
      (tier 1's own migration) and analytics's own latest is `20260901000001_...`;
      `20260902000002` collides with neither — confirmed immediately before creating the file)
      with the exact DDL from `design.md` D1:

      ```sql
      -- +goose Up
      CREATE SCHEMA IF NOT EXISTS analytics;

      ALTER TABLE vehicle_metrics           SET SCHEMA analytics;
      ALTER TABLE vehicle_metric_watermarks SET SCHEMA analytics;
      ALTER TABLE charge_gaps               SET SCHEMA analytics;

      -- +goose Down
      ALTER TABLE analytics.charge_gaps               SET SCHEMA public;
      ALTER TABLE analytics.vehicle_metric_watermarks SET SCHEMA public;
      ALTER TABLE analytics.vehicle_metrics           SET SCHEMA public;

      DROP SCHEMA IF EXISTS analytics;
      ```

      Acceptance: `make migrate-up` (or the owner's `goose up`) applies the migration cleanly;
      `to_regclass('analytics.vehicle_metrics')`, `to_regclass('analytics.vehicle_metric_watermarks')`,
      `to_regclass('analytics.charge_gaps')` all return non-NULL; the same three names under
      `public.` all return NULL (design.md Test Contract point 2). `goose down` (one step)
      reverses fully.

## T2. Schema-qualify `query.sql` (`internal/analytics/db/query.sql`) — no dependencies, parallel-ok with T1/T2b

- [x] T2.1 Qualify every table reference with `analytics.` across all 10 `-- name:` blocks —
      `FROM vehicle_metrics`, `FROM vehicle_metric_watermarks`, `FROM charge_gaps`, `INSERT INTO
      vehicle_metrics`, `INSERT INTO vehicle_metric_watermarks`, `INSERT INTO charge_gaps`,
      `DELETE FROM vehicle_metrics`, `DELETE FROM charge_gaps` (design.md D2). No `EXISTS`
      subqueries exist in this file. `RETURNING`/comment mentions of `vehicle_snapshots` /
      `supercharger_sessions` (other modules' tables, referenced only in prose comments here, not
      SQL) are untouched.
      Acceptance: a grep for a bare `\bFROM vehicle_metrics\b` / `\bFROM vehicle_metric_watermarks\b`
      / `\bFROM charge_gaps\b` / `\bINTO vehicle_metrics\b` / `\bINTO vehicle_metric_watermarks\b`
      / `\bINTO charge_gaps\b` (word-boundaried) in the file returns zero matches; confirmed.

## T2b. Schema-qualify raw SQL in `_test.go` files (D9) — no dependencies, parallel-ok with T1/T2

- [x] T2b.1 Find every hand-written SQL statement in this module's `_test.go` files that
      references `vehicle_metrics`, `vehicle_metric_watermarks`, or `charge_gaps` (analytics's
      own tables — NOT `vehicle_snapshots`/`supercharger_sessions`/`charge_sessions`, which
      belong to `telemetry`/`charging` and stay bare since those modules haven't moved schema).
      The roadmap's own grep (`grep -rnE '"[^"]*\b(FROM|INTO|UPDATE|JOIN)[[:space:]]+[a-z_]+'
      --include='*_test.go' internal/analytics`) only catches double-quoted strings on a single
      line and undercounts: it misses backtick-delimited multi-line SQL (a common pattern in
      this module's fixture helpers) entirely. A broader sweep
      (`grep -rn 'vehicle_metrics\b\|vehicle_metric_watermarks\b\|charge_gaps\b'
      internal/analytics/*_test.go`, manually filtered to statement lines) found **18**
      statements across 3 files, not the roadmap's ~5 estimate:
      - `db_gap_writer_integration_test.go`: 4 (lines 57, 67, 77, 98 pre-edit)
      - `db_integration_test.go`: 7 (lines 94, 95, 599, 626, 776, 1108, 1133 pre-edit — 3 of
        these are backtick multi-line statements the narrower grep pattern would have missed)
      - `db_watermark_migration_integration_test.go`: 7 (lines 96, 126, 131, 150, 156, 196, 197
        pre-edit — this whole FILE was absent from the roadmap's own "known statements" list)
      Acceptance: every one of the 18 qualified with `analytics.`; the broader sweep re-run
      after editing shows zero remaining bare reference to any of the three analytics tables in
      any `_test.go` file (excluding comments and other-module table names).
- [x] T2b.2 `db_watermark_migration_integration_test.go`'s `newAnalyticsMigrationProvider`
      replays historic migration `20260828000001`'s own bare Up/Down SQL directly via
      `goose.Provider.ApplyVersion`, OUT OF chronological order — after T1's schema-move
      migration has already applied (design.md D-watermark-replay). Editing the historic
      migration file is forbidden (D9: historic migrations stay bare for their NORMAL forward
      application). Fix: the goose provider's own `sql.Open` DSN gains
      `search_path=public,analytics` via a new `withSearchPath` helper — a test-connection-only
      change, verified against Context7's pgx documentation
      (`postgres://host/db?search_path=myschema,public` is documented pgx behavior).
      Acceptance: no historic migration file is touched (`git diff
      internal/analytics/db/migrations/20260828000001_*.sql` is empty); the new helper is scoped
      to this one file.

## T3. `sqlc.yaml` rename block + regeneration + verification (`sqlc.yaml`, `internal/analytics/db/`) — depends on T1 AND T2

- [x] T3.1 Add a `rename:` map under the analytics entry's existing `gen.go` block in the root
      `sqlc.yaml` (NOT the top-level `overrides:` block):
      ```yaml
      gen:
        go:
          package: "analyticsdb"
          out: "internal/analytics/db"
          sql_package: "pgx/v5"
          emit_json_tags: false
          emit_interface: false
          rename:
            analytics_charge_gap:               "ChargeGap"
            analytics_vehicle_metric:            "VehicleMetric"
            analytics_vehicle_metric_watermark:  "VehicleMetricWatermark"
          overrides:
            # ...(existing uuid override, unchanged)
      ```
      Key form is the singularized `<schema>_<table>` (design.md D3). Do not touch the
      `account`, `telemetry`, or `charging` entries in this same file.
- [x] T3.2 Run `make sqlc` (leader-integrated step). Regenerates `internal/analytics/db/models.go`,
      `db.go`, and `query.sql.go`.
- [x] T3.3 **Diff `internal/analytics/db/models.go` against its pre-change version and confirm
      zero change to any struct name, field name, or field type.** This is the mandatory
      verification step design.md requires. Report the diff output (or its absence) in the
      final report. Acceptance: `git diff internal/analytics/db/models.go` shows no diff at all.
- [x] T3.4 Confirm by inspection that `internal/analytics/db/query.sql.go` compiles against the
      new `analyticsdb` package (no hand edits) and that every generated query function's Go
      signature is unchanged. Acceptance: `go build ./internal/analytics/...` succeeds in
      isolation.

## T4. Docs (`internal/analytics/AGENTS.md` + any other doc naming these tables) — depends on T1, parallel-ok with T2/T2b/T3

- [x] T4.1 Update `internal/analytics/AGENTS.md`'s "Data ownership" section to state that this
      module's data lives in the `analytics` Postgres schema (tables `vehicle_metrics`,
      `vehicle_metric_watermarks`, `charge_gaps`), and explicitly note that
      `vehicle_metric_watermarks.source`'s stored string values and CHECK constraint are data
      naming other modules' tables by convention, untouched by this migration.
- [x] T4.2 Grep the repo (`grep -rln` for `vehicle_metrics`, `vehicle_metric_watermarks`,
      `charge_gaps`, scoped to prose/docs — `docs/`, `ai/`, root `README.md`, `kkpa/context/`)
      for any doc that names these tables and would now read as stale by omitting the schema.
      Report which files were checked and which, if any, needed an edit — do not edit
      speculatively. Found: `README.md`, `ai/architecture.md`, `docs/battery-consumed-graph.md`,
      and several `kkpa/context/` files reference these tables by name, but none assert a
      specific schema (`public.` or otherwise) — all are unaffected prose/code references, no
      edit needed, matching tier 1's own T4.2 conclusion for `account`'s tables.

## T5. Verification — depends on T1–T4

- [x] T5.1 `go build ./...`, `go vet ./...`, `gofmt -l` pass repo-wide (Claude-run).
- [x] T5.2 Boundary check: `internal/analytics` still imports only `internal/telemetry`,
      `internal/charging`, `internal/account`'s public ports (unchanged); no file outside
      `internal/analytics` (and the granted `sqlc.yaml` analytics entry +
      `openspec/changes/RM39-analytics-move-to-own-schema/` artifacts folder) was touched.
- [x] T5.3 Confirm no other module's `sqlc.yaml` entry, migrations directory, or `query.sql` was
      touched — this tier is scoped to the analytics entry only.
- [x] T5.4 State in the final report the exact catalog-verification queries from design.md's
      Test Contract point 2, so the owner can paste them after `make migrate-up`.
- [x] T5.5 Report the exact test-suite commands the owner must run to confirm this tier's zero
      assertion-level regression (`go test ./internal/analytics/... -run
      TestMigration_WatermarkSourceVocabulary`, and the full `go test ./internal/analytics/...`
      / `go test ./...` / `make test-with-db`) — this tier does not execute them
      (`Test-Execution-Policy`); the owner's run is what turns it from
      `awaiting-user-verification` into `done`.
- [x] T5.6 `openspec validate RM39-analytics-move-to-own-schema --strict` passes and every
      checkbox above reflects real completion.
