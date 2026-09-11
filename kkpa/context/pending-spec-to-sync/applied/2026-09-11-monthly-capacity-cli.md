# Sync proposal — monthly-capacity-cli

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `workflows/vehicle-monthly-metrics.md`
Source spec:  `openspec/specs/monthly-capacity-cli/spec.md`
Generated:    `2026-09-11`
Status: APPLIED 2026-09-11

**Read this before applying — the proposal is deliberately small.** The guide was written by the
leader in the same change that created this capability, so most of the spec is already live. Only
the rules the guide does **not** yet state are proposed below. A near no-op here is the healthy
outcome: it means the docs rule worked.

**No `## Glossary` block.** The spec adds no new alias. Its wording (`monthly capacity tool`) is
already reachable through the `cmd/monthly-capacity` and `capacity backfill` rows.

**No `## Component map` block.** A spec carries behavior, not file paths, and `--with-filemap` was
not passed. The live Component map stays untouched at apply time.

**No `[index]` block.** Every routing row this capability needs already exists: `cmd/monthly-capacity`,
`capacity backfill`, `monthly capacity`. The `ADD ROWS` grammar would skip them anyway.

---

## [guide] ## Conventions & gotchas — APPEND

- **A bad month is rejected before the tool touches a database.** The month value is parsed and
  validated first. A month number that does not exist, or a value in the wrong shape, exits with a
  failure status and attempts no measurement. Keep this order if the flag handling is ever
  rewritten — validating after connecting turns a typo into a wasted connection.
  _Source: spec monthly-capacity-cli — Requirement: An Invalid Month Is Rejected Before Any Work Starts._
- **The tool reports four numbers, or it reports a failure — never both.** A successful run names
  the month it measured, how many vehicles it considered, how many got a measured capacity, and how
  many were left thin. Any failure — bad input, a database problem, a measurement error — reports
  the failure, exits non-zero, and never prints a success summary.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Reports What It Measured And Exits Accordingly._
- **The tool needs a database connection and nothing else.** It never calls the Tesla Fleet API, so
  it must never require a Tesla credential to start. This is why it uses `config.LoadDatabase()`
  and not the full `config.Load()`. Without a database connection it fails to start and says so.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Needs Only A Database Connection, Never A Tesla Credential._
- **The tool is local only. It is not in the production image.** It runs from a checkout against a
  database the operator can reach directly. Do not add it to the deployed container: it is an
  operator tool for re-running a month, not a service.
  _Source: spec monthly-capacity-cli — Requirement: The Tool Is Not Part Of The Deployed Production Image._
