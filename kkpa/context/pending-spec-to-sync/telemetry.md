# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-03
Status: PENDING REVIEW

> **Origin:** RM39 tier 4 (`RM39-telemetry-move-to-own-schema`, MAG-31), archived 2026-09-03.
> It added two requirements to the live telemetry spec: *Module-Scoped Database Schema* and
> *Supercharger History Table Renamed*.
>
> **Note for the reviewer:** the archive wave already corrected this guide's factual claims in
> place (the PENDING banner, the module-role table and the internal-name table list), because
> `CLAUDE.md`'s docs rule requires structure docs to move in the same change. What is proposed
> below is the part that rule does *not* cover: the vocabulary and the durable rules a future
> agent needs so it does not re-derive them. Blocks are additive and safe to apply on top.

---

## [guide] ## Glossary — REPLACE

- **Known as:** `telemetry module`, `vehicle snapshots`, `supercharger history`, `nightly collection`, `telemetry schema`, `who reads telemetry`, `telemetry vs analytics`, `can the gateway read telemetry`, `ingest module`, `poll run`, `run summary`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerHistoryReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables (all in schema `telemetry`) `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`

## [guide] ## Conventions & gotchas — APPEND

- **All four telemetry tables live in the `telemetry` schema, never `public`.** Every query naming one must be schema-qualified; the module boundary is now checkable in the database catalog, not only by Go import guards. `goose_db_version` deliberately stays in `public` as the one shared ledger across modules.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema._
- **`telemetry.supercharger_history` and `charging.supercharger_sessions` are different tables.** The rename exists to retire that ambiguity: telemetry's is the raw vendor upsert, charging's is the dense human-correctable mirror. Never resolve either through a `search_path` — a bare `supercharger_sessions` reached through a path including `charging` finds the WRONG table and succeeds while reading the wrong rows.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema, Scenario: The charging module's identically-named table is untouched._
- **`supercharger_history` is UPSERTED, not append-only — do not reason about it like `vehicle_snapshots`.** Tesla settles fees after a session ends, so a row is never final on first insert. That difference is why the table is named `_history` and not `_snapshots`; treating both as append-only invites a query that double-counts.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **The port's half-renamed state is CLOSED — the vocabulary is now uniform.** The tier-4 spec deliberately left `SuperchargerReader`, its four `SuperchargerSessions*` methods and its constructor on the old names while they returned `[]SuperchargerHistory`, and told reviewers not to flag it. RM39 tier 5 finished the rename: the port is `SuperchargerHistoryReader`, its methods are `SuperchargerHistoryBy*`, its constructor is `NewSuperchargerHistoryReader`. Read the tier-4 spec's "SHALL NOT rename" clause as history, not as a live instruction.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **When renaming a table here, the completeness criterion is the CATALOG, never a hand-written list.** PostgreSQL auto-names one CHECK per inline column constraint and renames nothing automatically when a table is renamed; those names exist only in `pg_constraint`, where no grep reaches. The binding check is that a catalog query for any constraint or index named `<old_table>%` in this schema returns zero rows.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed, Scenario: No catalog object survives under the old table name._
- **A schema move preserves Go names; a table rename does not.** Moving a table into a schema changes sqlc's default struct name, so `sqlc.yaml` carries preserving `rename:` keys to hold `PollAttempt`, `PollRun` and `VehicleSnapshot` steady. The `SuperchargerHistory` rename is the deliberate exception. A `rename:` key that does not match is ignored **silently, at exit 0** — the `models.go` diff is the only detector.
  _Source: spec telemetry — Requirement: Supercharger History Table Renamed._
- **Raw SQL inside `_test.go` files is invisible to sqlc and to `go vet`.** No assistant-runnable signal catches an unqualified table name there — only the suite does. After any schema or table change in this module, re-run a grep over the test files as the acceptance step; a green build proves nothing about them.
  _Source: spec telemetry — Requirement: Module-Scoped Database Schema (namespacing-only guarantee across all module reads)._

## [index] ## Architecture topics — ADD ROWS

| `supercharger history` (`telemetry.supercharger_history` — the RAW vendor upsert; NOT charging's `supercharger_sessions` mirror) | `architecture/telemetry-ingest-only.md` |
| `telemetry schema` | the module's own Postgres schema — all four telemetry tables live there, never `public` → `architecture/telemetry-ingest-only.md` |
| `supercharger_history vs supercharger_sessions` | two different tables — telemetry's raw upsert vs charging's mirror → `architecture/telemetry-ingest-only.md` |
