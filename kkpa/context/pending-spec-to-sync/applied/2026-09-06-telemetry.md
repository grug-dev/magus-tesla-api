# Sync proposal — telemetry

> Staged by `kkpa-context-curate from-spec`. This is a **draft** of KB edits derived from one
> approved OpenSpec capability spec. Review/edit the blocks below, then run
> `/kkpa-context-curate apply-sync` to write them into the real KB. Nothing here touches the
> canonical KB until applied. This file is self-contained — it embeds the proposed content, so it
> stays valid even after the OpenSpec change folder is archived/moved.

Target guide: `architecture/telemetry-ingest-only.md`
Source spec:  `openspec/specs/telemetry/spec.md`
Generated:    2026-09-06
Status: APPLIED 2026-09-06

Derived from the 2 RM44 requirements the main spec now carries (MAG-48):
**Change-Detecting Supercharger-History Upsert** (tier 2) and
**Supercharger History Account-Wide Updated-Since Read Port** (tier 4).

**This draft REPLACES the earlier tier-2-only proposal, which was staged but never applied.**
It is derived from the current merged main spec, so it covers both tiers. Tier 1's logging
requirements were applied on 2026-09-05 and are not repeated here.

Same target guide the tier-1 proposal used, so this extends it rather than creating a second
entry. Its sibling proposals are `charge-session-log.md` and `charging.md` — the same roadmap,
one layer down, in `internal/charging`.

No `## Component map` block is proposed — `spec.md` carries behaviour, not file paths, so the
live Component map is preserved untouched. `--with-filemap` was not passed.

---

## [guide] ## Conventions & gotchas — APPEND

- **`updated_at` on `telemetry.supercharger_history` means "this row's data changed", not
  "the nightly sync ran".** An unchanged re-sync leaves it untouched. A re-sync that writes at
  least one different mirrored value advances it. Before MAG-48 every pass advanced it on
  every row, which made the signal useless to the consumer that reads it.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **The comparison covers exactly the columns the sync refreshes on a conflict — no more, no
  less.** Everything else is excluded on purpose: columns a human sets by hand, and columns
  written once at first capture and never refreshed. The spec requires exclusion to be an
  explicit, visible decision, never the accidental result of a name missing from a
  hand-written list.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A column the sync never refreshes must never enter the comparison.** If it did, and the
  vendor later reported a different value, the mismatch could never resolve — the sync does not
  write that column — so `updated_at` would advance every night forever. This is the trap the
  requirement's last scenario pins, and it is why the exclusion list is the complement of the
  refresh set rather than a short list of "obvious" columns.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A vehicle re-registration DOES advance `updated_at`.** A row with no vehicle identity,
  later resolved to a registered vehicle, counts as a real change. Excluding the vehicle
  identity from the comparison would silently break that recovery path.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **A human-entered value on the row neither blocks change detection nor counts as a change.**
  A verified row still reports "unchanged" on an unchanged re-sync, and the hand-entered value
  is left exactly as it was.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **Guard when you change this:** `internal/telemetry/db_change_detection_schema_test.go`
  reads the table's live columns and fails if any column is in neither the refresh set nor the
  exclusion list. Its failure message names the column and says which list to add it to. Add a
  column to the table and this test tells you what to decide.
  _Source: spec telemetry — Requirement: Change-Detecting Supercharger-History Upsert._

- **There are TWO updated-since read ports for Supercharger sessions, and the difference is
  load-bearing.** The per-vehicle port filters on the vehicle identifier. The account-wide port
  takes no vehicle at all. Only the account-wide one can return a session whose vehicle is not
  currently registered, because a per-vehicle filter can never match a row with no vehicle
  identity. A caller that must recover such a session once its vehicle re-registers has to use
  the account-wide port. Picking the per-vehicle one there loses rows, silently.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **The account-wide port returns oldest-first by `updated_at`, and an empty result is not an
  error.** Nothing updated in the window returns an empty collection with no error, exactly like
  every other read port on this module.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **A session updated at exactly the requested instant is included.** The bound is inclusive at
  both ports. A caller that treats it as exclusive will skip a row on every boundary.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

- **No caller reaches these rows any other way.** Both updated-since ports are the only route to
  this data for another module; nothing outside `internal/telemetry` imports
  `internal/telemetry/db`. `make boundary-guard` enforces it.
  _Source: spec telemetry — Requirement: Supercharger History Account-Wide Updated-Since Read Port._

## [index] ## Glossary & routing — entities — ADD ROWS

| `supercharger history change detection` | when a nightly sync counts as a change to a `telemetry.supercharger_history` row (`updated_at`) | entity | `architecture/telemetry-ingest-only.md` |
| `why does updated_at change every night` | the MAG-48 symptom, at the telemetry layer | entity | `architecture/telemetry-ingest-only.md` |
| `account-wide updated-since read` | `telemetry.SuperchargerHistoryReader.SuperchargerHistoryByAccountUpdatedSince` — the only updated-since port that returns sessions with no registered vehicle | entity | `architecture/telemetry-ingest-only.md` |
| `orphaned supercharger session` | a session whose vehicle is not currently registered; reachable only through the account-wide updated-since port | entity | `architecture/telemetry-ingest-only.md` |
