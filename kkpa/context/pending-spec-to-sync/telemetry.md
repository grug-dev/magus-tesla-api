# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-06
Status: PENDING REVIEW

Derived from the 2 requirements added by `RM44-telemetry-add-query-logging` (MAG-48, roadmap
RM44 tier 1): **Query And Fleet-API Argument Logging** and **Structural Exclusion Of
Credentials And Raw Payloads From Logs**.

No `## Component map` block is proposed — `spec.md` carries behaviour, not file paths, so the
live Component map is preserved untouched. `--with-filemap` was not passed.

---

<!--
HOW APPLY-SYNC READS THIS FILE — block grammar (apply each near-verbatim):

  ## [guide] ## <Section heading> — APPEND
      → append the bullets/lines below to that section of the Target guide.
  ## [guide] ## <Section heading> — REPLACE
      → replace that section's body with the content below.
  ## [index] ## <Table heading> — ADD ROWS
      → append the table rows below to that INDEX.md table, skipping any row whose first cell
        already exists.
-->

## [guide] ## Glossary — REPLACE

- **Known as:** `telemetry module`, `vehicle snapshots`, `supercharger history`, `nightly collection`, `telemetry schema`, `who reads telemetry`, `telemetry vs analytics`, `can the gateway read telemetry`, `ingest module`, `poll run`, `run summary`, `telemetry query logging`, `fleet api logging`
- **Internal name:** `internal/telemetry` — ports `telemetry.Reader`, `telemetry.SuperchargerHistoryReader` (reads), `telemetry.Collector` (write), `telemetry.RunWriter` (run summary write) — tables (all in schema `telemetry`) `vehicle_snapshots`, `supercharger_history`, `poll_attempts`, `poll_runs`

## [guide] ## Conventions & gotchas — APPEND

- **Every live query and every Fleet API request logs its arguments** — each call through the
  module's persistence write seam, its snapshot-read port, its Supercharger-history read port
  and its poll-run write port emits one log line carrying the account and vehicle identity, the
  date filters supplied, and — for a method returning a collection or an optional record — how
  many records came back. Prefixes are `telemetry query:` and `fleet api:`, greppable.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **A Supercharger-history read logs the limit the query ACTUALLY runs with, not the caller's
  placeholder** — a caller passing "no limit" must appear in the log as the concrete resolved
  value. Logging the placeholder would make a full-history read indistinguishable from a
  bounded one, which is the exact blindness that let MAG-48 run unnoticed for months.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **An account-wide Supercharger-history read states explicitly that it applied no date bound** —
  absence of a filter must be visible in the log, not inferred from a missing field.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging._
- **No log line may ever contain a vendor access credential** — every Fleet API method takes
  credentials as an argument, so a naive "log the arguments" leaks a live bearer token. The
  guarantee is structural, and a test asserts it.
  _Source: spec telemetry — Requirement: Structural Exclusion Of Credentials And Raw Payloads From Logs._
- **A raw external-API payload is logged as a byte size, never as content** — record the size
  where it is useful; never the blob. A test asserts the content never appears.
  _Source: spec telemetry — Requirement: Structural Exclusion Of Credentials And Raw Payloads From Logs._
- **The instrumentation is an explicit, non-embedding decorator per port** — a method added to a
  decorated interface later must become a compile error rather than a silently unlogged call.
  Embedding would satisfy the type by promotion and defeat that.
  _Source: spec telemetry — Requirement: Query And Fleet-API Argument Logging (implementation shape carried by roadmap decision D7/D8)._

## [index] ## Architecture topics — ADD ROWS

| `telemetry query logging` (every live query + Fleet API call logs its arguments; credentials and raw payloads never logged) | `architecture/telemetry-ingest-only.md` |
| `fleet api logging` | synonym of `telemetry query logging` → `architecture/telemetry-ingest-only.md` |
| `why is my query not logged` | the four decorated ports + the callCounter seam → `architecture/telemetry-ingest-only.md` |
