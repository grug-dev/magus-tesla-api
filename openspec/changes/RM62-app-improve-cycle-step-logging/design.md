# Design — RM62-app-improve-cycle-step-logging

## Context

`recalculateAnalytics` (`internal/app/processor.go`) runs two halves per vehicle:
`Recalculator.Reconcile` (metrics), then charge-gap reconciliation. Today it logs
its gap window once, before the vehicle loop starts:

```go
logging.Note("Processor", "recalculateAnalytics", "gap reconciliation: %s → %s", start, end)
```

Every telemetry query that follows in the log comes from `Reconcile`, not from
gap work. The line names the wrong step for those queries. The window values
themselves (`start`, `end`) are correct — only where the line sits, and what it
implies it owns, is wrong.

The roadmap fixed the target shape before this change was written (RD2):

```
[Processor] [recalculateAnalytics] metrics reconciliation: vehicle 3744325961659064
[Reader] [SnapshotsByVehicleUpdatedSince] ...
[Reader] [SnapshotsByVehicleBetween] ...
[Reader] [SnapshotPrecedingDay] ...
[Processor] [recalculateAnalytics] gap reconciliation: vehicle 3744325961659064: 2026-08-17 -> 2026-09-15
[Reader] [ConsumedByDay] ...
[GapWriter] [ReconcileWindow] ...
```

## Decisions

### D1 — The pre-loop line is removed, not reduced

The target shape above has no line before the per-vehicle block. The first line
printed for `recalculateAnalytics` is already `metrics reconciliation: vehicle
<id>`. Keeping a shortened pre-loop line (e.g. "starting analytics recalculation")
would add a fourth line with no query information under it — pure noise, and it
duplicates what "metrics reconciliation: vehicle <id>" already states just one
line later. The window itself is still logged — the gap line below states it
per vehicle, which is the piece of information a reader actually needs when
looking at a specific vehicle's queries.

### D2 — Two log calls per vehicle, replacing one call per step

Inside the loop, right before `p.recalculator.Reconcile(ctx, v.TeslaID)`:

```go
logging.Note("Processor", "recalculateAnalytics", "metrics reconciliation: vehicle %d", v.TeslaID)
```

Right before the gap work starts (before `p.analyticsReader.ConsumedByDay`):

```go
logging.Note("Processor", "recalculateAnalytics", "gap reconciliation: vehicle %d: %s -> %s", v.TeslaID, start, end)
```

Both keep the existing message topics (`metrics reconciliation:`,
`gap reconciliation:`) exactly as today's doc comment describes them — the
function's own comment already says these topics exist so a reader can grep
the two halves apart, and the two per-vehicle error lines lower in the function
already use them. Changing the topic text would break that grep for anyone who
already relies on it.

### D3 — The cost is real and is accepted

Before this change: 1 log line total for the whole `recalculateAnalytics` call,
covering every vehicle.

After this change: 2 log lines per vehicle. For an N-vehicle account set, that
is 2N lines instead of 1. This is the entire point of the change — the roadmap
exists because today's single line hides which vehicle a query belongs to. The
cost is small: `logging.Note` is a single `fmt.Sprintf` + `log.Printf`, off the
gateway's read path, running once a night in the nightly batch.

### D4 — VS Code launch entry mirrors `cmd/web`'s `envFile` handling

Add one entry to `.vscode/launch.json`, modeled on the existing `cmd/web` entry:

```json
{
    "name": "Debug poller (cmd/poller --once)",
    "type": "go",
    "request": "launch",
    "mode": "auto",
    "program": "${workspaceFolder}/cmd/poller",
    "cwd": "${workspaceFolder}",
    "args": ["--once"],
    "envFile": "${workspaceFolder}/.env"
}
```

`envFile` is required for the same reason the `cmd/web` entry has it:
`config.Load()` reads `.env` directly, and VS Code does not load it on its own.
`cmd/poller` needs `DATABASE_URL` and a valid `POLLER_TIMEZONE`; both already
live in `.env` (`cmd/poller/main.go`'s own doc comment: both paths validate
`POLLER_TIMEZONE`, and an invalid value is fatal even for `--once`). No `env`
override block is needed — unlike `cmd/web`'s `MAGUS_DEV` flag, `cmd/poller` has
no equivalent dev-mode switch.

**Warning that belongs in the docs, not buried in a task:** running this launch
entry executes a real nightly cycle. It wakes the user's actual Tesla and makes
paid Fleet API calls, exactly like running `go run ./cmd/poller --once` from a
terminal. It is not a safe "explore the code" action.

### D5 — Doc placement for the new launch entry: `internal/app/AGENTS.md`

Three candidates: root `README.md`, `cmd/README.md`, `internal/app/AGENTS.md`.
Picked `internal/app/AGENTS.md`, in its existing "Testing notes" section, which
already says: *"The verification signal for the two uncovered steps is the
owner's own `go run ./cmd/poller --once`."* The new launch entry runs that exact
command under a debugger — adding one sentence there keeps the fact next to the
command it debugs, in the one file every `app` worker already reads before
touching this module (AI-efficiency: change-locality over a second file).
`cmd/README.md` already documents `--once` as a flag; it is not the natural home
for a VS Code-specific detail, and splitting the fact across two files buys
nothing. The paid-API warning (D4) goes in the same paragraph.

### D6 — No cross-module change

Nothing about `Processor.ProcessVehicleData`'s signature, return type, or
control flow changes. `recalculateAnalytics`'s two halves still run in the same
order, for the same reasons (see the function's own doc comment on why
`Reconcile` must precede gap work). Only where the `logging.Note` calls sit
changes.

## Tests excluded (roadmap Decision 1)

This change adds no test. The roadmap's own reasoning: the work is a logging
label move and a debugger config, and `internal/logging.Note`'s own tests
(`internal/logging/logging_test.go`) already verify the `[Type] [Method]
message` format the new calls reuse unchanged. There is no new formatting logic
to characterize.

What this leaves uncovered: nothing new. `recalculateAnalytics`'s internals were
already an accepted gap before this change (`internal/app/AGENTS.md`'s Testing
notes table: "pure relocations of `cmd/poller` code that was never tested
there"). Moving where two `logging.Note` calls sit does not change that. The
owner's `go run ./cmd/poller --once` (or the new VS Code entry) remains the
verification signal, exactly as it was before this change.

## Database

**None.** This change touches no table, column, index, constraint, view, or
migration. `internal/app` owns no data (`internal/app/AGENTS.md` "Data
ownership") and this change does not alter that.

## Knowledge base

Checked `kkpa/context/architecture/nightly-cycle.md` and every other KB file
that mentions `internal/app/processor.go` or `recalculateAnalytics`
(`kkpa/context/architecture/charge-record-mutation.md`,
`kkpa/context/entities/vehicle-metrics/guide.md`) for a fact this change makes
false. All of them describe the ORDER of the two halves (`Reconcile` before gap
work) and WHY that order matters — both unchanged by this change. None quotes
the removed pre-loop log line or describes it as current. No KB update is
needed.
