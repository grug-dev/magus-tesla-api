> **Scope.** Additive, non-breaking migration (tier 1 of roadmap `RM39-schema-per-module`, MAG-31).
> Moves `accounts`, `tesla_tokens`, `vehicles` into a new `account` Postgres schema via one goose
> migration (`CREATE SCHEMA IF NOT EXISTS account` + `ALTER TABLE … SET SCHEMA account` per
> table). No table, column, index, or constraint is renamed — D5a/D5b/D5c (the rename decisions)
> do not touch this module. Every `query.sql` table reference becomes schema-qualified (forced by
> sqlc, design.md D2); `sqlc.yaml` gains a `gen.go.rename` block so `Account`, `TeslaToken`,
> `Vehicle` keep their exact Go names (design.md D3) — **verified by diffing `models.go`, not
> trusted from the config**, since a wrong rename key fails silently at exit 0. goose itself is
> unchanged (D4): no `db-reset`, `make migration-guard` stays required.
>
> **Dependencies / parallelism:**
> - T1 (goose migration) has no dependencies. Independent of T2 (disjoint files: a new migration
>   file vs. `query.sql`) and MAY run in parallel with it.
> - T2 (`query.sql` schema-qualification) has no dependencies. Independent of T1 and MAY run in
>   parallel with it. (T2 does not need T1's migration to exist to *edit* the SQL text — but see
>   the leader-integrated step below: `make sqlc` cannot succeed until both T1 and T2 have landed.)
> - T3 (`sqlc.yaml` rename block + `make sqlc` + `models.go` verification) depends on **both** T1
>   and T2 — sqlc parses the migration directory (T1) AND the queries (T2) to generate code; it
>   cannot run correctly with only one of the two in place.
> - T4 (docs: `internal/account/AGENTS.md` + any other doc naming these tables) depends on T1 only
>   (it needs the schema name to exist in the design) and MAY run in parallel with T2/T3.
> - T5 (verification) depends on T1–T4.
>
> **Leader-integrated step:** run `make sqlc` after T1 and T2 both land (this is T3.2 below). Do
> not hand-edit `internal/account/db/models.go` or `db/query.sql.go` — both are sqlc-generated.
> Do not run `make migrate-up`, `make db-setup`, or any test suite from this artifact-only
> dispatch — those are implementation-phase and owner-run steps respectively.

## T1. Goose migration (`internal/account/db/migrations/`) — no dependencies, parallel-ok with T2

- [ ] T1.1 Create `internal/account/db/migrations/20260902000001_move_account_to_own_schema.sql`
      (next free chronological timestamp: the latest existing filename across ALL modules'
      migration directories is `internal/telemetry/db/migrations/20260830000002_add_poll_runs.sql`
      and account's own latest is `20260830000001_...`; `20260902000001` collides with neither —
      confirm this is still true immediately before creating the file, since sibling tiers/changes
      may land migrations in the meantime) with the exact DDL from `design.md` D1:

      ```sql
      -- +goose Up
      -- RM39 tier 1 (account-move-to-own-schema, MAG-31): move this module's three tables into a
      -- dedicated `account` Postgres schema, additive to existing history (roadmap D1). No table,
      -- column, index, or constraint is renamed or altered — this migration is pure namespacing so
      -- the modular-monolith boundary (ai/architecture.md §2 "no cross-module database leaks")
      -- becomes visible in the database catalog, not just enforced by Go import guards. `ALTER
      -- TABLE … SET SCHEMA` is catalog-only (see design.md's index plan). Because migrations run
      -- as the app role (Makefile db-setup exports PGUSER=$(APP_ROLE)), CREATE SCHEMA here makes
      -- that role the schema owner — no GRANT needed.

      CREATE SCHEMA IF NOT EXISTS account;

      ALTER TABLE accounts     SET SCHEMA account;
      ALTER TABLE tesla_tokens SET SCHEMA account;
      ALTER TABLE vehicles     SET SCHEMA account;

      -- +goose Down
      -- Reverse in the OPPOSITE order of Up: move every table back to public first, then drop the
      -- now-empty schema.
      ALTER TABLE account.vehicles     SET SCHEMA public;
      ALTER TABLE account.tesla_tokens SET SCHEMA public;
      ALTER TABLE account.accounts     SET SCHEMA public;

      DROP SCHEMA IF EXISTS account;
      ```

      Acceptance: `make migrate-up` (or the owner's `goose up`) applies the migration cleanly;
      `to_regclass('account.accounts')`, `to_regclass('account.tesla_tokens')`,
      `to_regclass('account.vehicles')` all return non-NULL; the same three names under `public.`
      all return NULL (design.md Test Contract point 2). `goose down` (one step) reverses fully —
      all three tables resolve under `public.` again and `account` no longer appears in
      `pg_namespace`.

## T2. Schema-qualify `query.sql` (`internal/account/db/query.sql`) — no dependencies, parallel-ok with T1

- [ ] T2.1 Qualify every table reference with `account.` — `FROM accounts`, `FROM tesla_tokens`,
      `FROM vehicles`, `INSERT INTO accounts`, `INSERT INTO tesla_tokens`, `INSERT INTO vehicles`,
      `UPDATE accounts`, `UPDATE tesla_tokens`, `UPDATE vehicles` — **including the two `EXISTS
      (SELECT 1 FROM accounts a WHERE …)` subqueries** inside `GetLatestTeslaTokenByAccount`,
      `GetLatestTeslaTokenByAccountForUpdate`, `ListVehiclesByAccount`, and `ListAllVehicles`
      (design.md D2 — those are table references too and sqlc resolves them the same way). Do
      **not** qualify `RETURNING *` clauses (they reference whatever table the preceding
      `INSERT`/`UPDATE` already names) or the alias `a` used inside the `EXISTS` subqueries after
      its first qualified appearance (e.g. `a.id = tesla_tokens.account_id` stays as-is except the
      `FROM accounts a` → `FROM account.accounts a` at the alias's introduction).
      Acceptance: every one of the 10 `-- name:` blocks in `query.sql` that references
      `accounts`/`tesla_tokens`/`vehicles` has each bare reference replaced with its
      `account.`-qualified form; a grep for a bare `\bFROM accounts\b` / `\bFROM tesla_tokens\b` /
      `\bFROM vehicles\b` / `\bINTO accounts\b` / `\bINTO tesla_tokens\b` / `\bINTO vehicles\b` /
      `\bUPDATE accounts\b` / `\bUPDATE tesla_tokens\b` / `\bUPDATE vehicles\b` (word-boundaried,
      case-sensitive) in the file returns zero matches.

## T3. `sqlc.yaml` rename block + regeneration + verification (`sqlc.yaml`, `internal/account/db/`) — depends on T1 AND T2

- [ ] T3.1 Add a `rename:` map under the account entry's existing `gen.go` block in the root
      `sqlc.yaml` (NOT the top-level `overrides:` block — design.md D3 confirms sqlc ignores
      renames placed there):
      ```yaml
      gen:
        go:
          package: "accountdb"
          out: "internal/account/db"
          sql_package: "pgx/v5"
          emit_json_tags: false
          emit_interface: false
          rename:
            account_account:     "Account"
            account_tesla_token: "TeslaToken"
            account_vehicle:     "Vehicle"
          overrides:
            # ...(existing uuid override, unchanged)
      ```
      Key form is the singularized `<schema>_<table>` (design.md D3): `accounts` → `account`,
      `tesla_tokens` → `tesla_token`, `vehicles` → `vehicle`, each prefixed with schema `account`.
      Do not touch the `telemetry`, `charging`, or `analytics` entries in this same file — they
      are out of scope for this tier.
- [ ] T3.2 Run `make sqlc` (leader-integrated step — requires both T1's migration and T2's
      schema-qualified queries to already be in place). This regenerates
      `internal/account/db/models.go`, `db.go`, and `query.sql.go`.
- [ ] T3.3 **Diff `internal/account/db/models.go` against its pre-change version and confirm zero
      change to any struct name, field name, or field type** — compare against the exact byte-form
      quoted in `design.md`'s Test Contract point 1 (`Account`, `TeslaToken`, `Vehicle` struct
      bodies). This is the mandatory verification step design.md requires: a wrong `rename` key
      form fails silently at exit 0, so `sqlc generate`'s own success is not evidence of
      correctness — the diff is. Report the diff output (or its absence) in the final report.
      Acceptance: `git diff internal/account/db/models.go` shows either no diff, or a diff limited
      to comments/whitespace/version-string churn — zero diff in any `type X struct { ... }` body.
      If the diff shows a renamed struct (e.g. `AccountAccount`, `AccountTeslaToken`,
      `AccountVehicle`), the rename key form was wrong — fix T3.1 and re-run T3.2/T3.3 before
      proceeding.
- [ ] T3.4 Confirm by inspection that `internal/account/db/query.sql.go` compiles against the new
      `accountdb` package (no hand edits) and that every generated query function's Go signature
      (parameter/return types) is unchanged from before this migration — the schema move changes
      only the SQL text embedded as string constants, never a Go-visible type.
      Acceptance: `go build ./internal/account/...` succeeds in isolation.

## T4. Docs (`internal/account/AGENTS.md` + any other doc naming these tables) — depends on T1, parallel-ok with T2/T3

- [ ] T4.1 Update `internal/account/AGENTS.md`'s "Boundaries" section to state that this module's
      data lives in the `account` Postgres schema (tables `accounts`, `tesla_tokens`, `vehicles`),
      not just `internal/account/db/`. Keep the addition to one or two sentences, consistent with
      the file's existing terse style (CLAUDE.md's docs-track-change rule — this is a structural
      change to the module landing in the same change, not a follow-up).
- [ ] T4.2 Grep the repo (`grep -rn` for `accounts`, `tesla_tokens`, `vehicles` as bare
      identifiers, scoped to prose/docs — `docs/`, `ai/`, root `README.md`, `kkpa/context/`) for
      any doc that names these tables and would now read as stale by omitting the schema. Most
      references are expected to be either code (unaffected — Go identifiers don't change) or
      prose that doesn't assert a schema either way (no change needed). Report which files were
      checked and which, if any, needed an edit — do not edit speculatively.

## T5. Verification — depends on T1–T4

- [ ] T5.1 `go build ./...`, `go vet ./...`, `gofmt -l` pass repo-wide (Claude-run, per
      `Test-Execution-Policy` — these are cheap deterministic signals, not the owner-only test
      suite).
- [ ] T5.2 Boundary check: `internal/account` still does not import any other feature module; no
      file outside `internal/account` (and the granted `openspec/changes/RM39-account-move-to-own-schema/`
      artifacts folder) was touched by this tier.
- [ ] T5.3 Confirm no other module's `sqlc.yaml` entry, migrations directory, or `query.sql` was
      touched — this tier is scoped to the account entry only.
- [ ] T5.4 State in the final report the exact catalog-verification queries from design.md's Test
      Contract point 2, so the owner can paste them after `make migrate-up`:
      ```sql
      SELECT to_regclass('account.accounts'), to_regclass('account.tesla_tokens'),
             to_regclass('account.vehicles');
      SELECT to_regclass('public.accounts'), to_regclass('public.tesla_tokens'),
             to_regclass('public.vehicles');
      ```
      Expected: the first query returns three non-NULL OIDs; the second returns three NULLs.
- [ ] T5.5 Report the exact test-suite commands the owner must run to confirm this tier's zero
      assertion-level regression (`go test ./internal/account/... -run
      TestUpsertFromOAuth_Idempotent`, `go test ./internal/account/... -run
      TestLanguagePreference_RoundTrip`, and the full `go test ./...` / `make test-with-db`) — this
      tier does not execute them (`Test-Execution-Policy`); the owner's run is what turns it from
      `awaiting-user-verification` into `done`.
- [ ] T5.6 `openspec validate RM39-account-move-to-own-schema --strict` passes and every checkbox
      above reflects real completion.
