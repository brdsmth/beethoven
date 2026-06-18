# Beethoven

A personal agentic software organization. See [`docs/architecture.md`](docs/architecture.md)
for the full design, [`docs/phase-1.5-real-execution-plan.md`](docs/phase-1.5-real-execution-plan.md)
for the real-execution plan,
[`docs/phase-1.6-project-manager-runtime.md`](docs/phase-1.6-project-manager-runtime.md)
for the project-management domain model,
[`docs/phase-1.7-real-model-runtime.md`](docs/phase-1.7-real-model-runtime.md)
for the real-model-runtime plan, and
[`docs/phase-1.8-continuous-runtime.md`](docs/phase-1.8-continuous-runtime.md)
for the continuous-runtime plan. On top of Phase 1.6's Project → Initiative →
Requirement → Plan → Epic hierarchy and Project → Goal → Task hierarchy
(both terminating in the same `Task`/execution/review pipeline) and Phase
1.7a's real model invocation and Phase 1.8's continuous runtime, this is
**Phase 1.9a**: a read-only Tauri + React desktop UI, Mission Control,
giving the continuously-running organization a visual home (see
[Mission Control (desktop UI)](#mission-control-desktop-ui)). PM planning
and review are still deterministic heuristics — replacing those with
model-driven behavior is Phase 1.7b (see [Stub behavior](#stub-behavior)).
A few pieces of the spec were deliberately simplified or skipped — see
[Continuous runtime](#continuous-runtime) for what and why.

## Build

```bash
cargo build
```

The binary is `beethoven` (from the `beethoven-cli` crate). All state lives
under `~/.beethoven/` (SQLite event log + per-project workspace directories +
an active-project pointer file). Every CLI invocation is a fresh process that
rebuilds in-memory state by replaying the event log, so state survives
restarts by construction.

Override the state directory with `--home <path>` or the `BEETHOVEN_HOME`
environment variable (flag wins if both are set) — useful for tests, scratch
runs, or multiple isolated instances:

```bash
beethoven --home /tmp/scratch project create demo --repo ~/workspace/demo
BEETHOVEN_HOME=/tmp/scratch beethoven project list
```

`run` invokes the real `claude` CLI for every task it executes, so a logged-in
Claude Code install is required and each run incurs real API cost. Use
`--home`/`BEETHOVEN_HOME` to point at a scratch directory and a throwaway git
repo when experimenting, to avoid polluting real project state.

## Usage

### Projects

Projects are the root organizational boundary: goals/tasks/inbox
items/workspaces/events all belong to exactly one project, and each project
gets its own 6 agent instances (PM + Backend/Frontend/QA/Research/DevOps),
auto-bootstrapped from 6 shared global agent templates.

```bash
cargo run -- project create sample --repo ~/workspace/sample --default-branch main
cargo run -- project list      # "*" marks the active project
cargo run -- project show sample
cargo run -- project use sample # sets ~/.beethoven/current-project
cargo run -- project current
```

The first project you ever create becomes active automatically. Any command
below that needs a project takes `--project <slug>`, falling back to the
active project when omitted.

### Goals and tasks

Create a goal. Lines in the description starting with `- ` are turned into
discrete tasks by the planner; otherwise a single default task is created.

```bash
cargo run -- goal create "Build login page" --project sample --description="- Implement the login form
- What auth provider should we use?
- Write the landing page copy"
```

Plan the goal (runs the stub Project Manager planner, creates tasks — no
`--project` needed, it's derived from the goal):

```bash
cargo run -- goal plan <goal-id>
```

Run an orchestration tick for a project (assigns ready tasks to that
project's idle agent instances, then executes each assigned task — up to
`--max-concurrent` of them at once, default 4):

```bash
cargo run -- run --project sample
```

For each assigned task this drives the real execution pipeline:

1. Create a real `git worktree` on a fresh `task/<slug>` branch and a real
   `tmux` session rooted at it.
2. Render the structured task prompt via `PromptBuilder::worker`, write it to
   `<workspace>/prompts/prompt.md`, and emit it as a `PromptGenerated` event.
3. Invoke the model runtime (`ClaudeCodeRuntime`, the default `ModelRuntime`
   — see below) inside that tmux session, piping the prompt over stdin to
   `claude --dangerously-skip-permissions --output-format json -p` and
   capturing its response/stderr/exit code to files under the workspace's
   `artifacts/` directory. The run is bracketed by `ModelRunStarted` and
   `ModelRunCompleted`/`ModelRunFailed` events; the response is stored as a
   `ModelArtifact`.
4. Capture real `git status`/`git diff` output from the worktree as
   artifacts.
5. Run the task through review, branching on the outcome: **Approve** (open
   a PR, complete the task), **Needs Changes** (bump the attempt, retry),
   or **Blocked**/**Question** (escalate to the inbox, reusing the same
   Worker → Review → PM → Inbox → Human flow as execution-time questions).
6. Tear down the tmux session and worktree (branch included) regardless of
   outcome — the diff/status are preserved as event-sourced artifacts, but
   nothing about the workspace itself persists past the tick, since there's
   no real PR/remote to push the branch to yet.

The printed report includes `needs_changes` and `escalated` counts alongside
`completed`/`assigned`/`subtasks_spawned`.

### Continuous runtime

Instead of invoking `run` by hand on a loop, `start` runs the same tick
continuously, across every project, until interrupted:

```bash
cargo run -- start --interval 10 --max-concurrent 4 --max-attempts 3
```

On startup it first recovers from any crash: any task left assigned to an
agent without ever reaching a terminal status (the process died sometime
between `TaskStarted` and the next event, most commonly mid-model-call) is
reset to `Ready` with a bumped attempt, its agent freed, and any worktree/tmux
session the crashed attempt left behind torn down — reconstructed
deterministically from the project/task/agent kind, since none of that needs
to have been persisted separately. This is recorded as a `TaskRecovered`
event per task, *unless* the task had already hit `--max-attempts`, in which
case it's escalated to the inbox instead of recovered again.

The daemon then loops: tick every active project, sleep for `--interval`
seconds, repeat, until interrupted via Ctrl-C or `beethoven stop` from
another shell (SIGTERM) — either stops the loop after its current tick
finishes (no in-flight tick is interrupted) and records
`RuntimeStarted`/`RuntimeStopped` events around the session. `start` refuses
to run if a daemon is already running (tracked via a pidfile,
`<home>/daemon.pid`); `stop` reads that pidfile and waits for the process to
actually exit.

**Real concurrency, no locks.** Within a tick, every project's ready tasks
are assigned and started first (fast, sequential), then *every* prepared
task's real model execution — across every active project, not just one —
runs concurrently via `tokio::task::spawn_blocking`, bounded to
`--max-concurrent` in flight at once. `Runtime` never needs `Arc<Mutex<...>>`
for this: it only ever holds `&mut self` during the fast assign/start/finish
steps, never while a real model call is in flight, since each prepared
task's data is fully owned and moved into its own blocking task. A
workspace/git/tmux/IO failure while starting one task escalates just that
task (`TaskBlocked` + `QuestionRaised`, same as a model failure) instead of
aborting every other task's progress in the same tick.

**Retry cap.** An *automatic* retry (a Needs-Changes review outcome, or
crash recovery) that would push a task's attempt count past `--max-attempts`
escalates to the inbox instead of retrying again — a task can no longer loop
through Needs-Changes forever. This does not cap a human resolving a
question/decision, which always unblocks the task.

```bash
cargo run -- runtime status   # daemon liveness, last start/stop, per-project/agent activity
cargo run -- logs --project sample --limit 50
cargo run -- stop
```

What Phase 1.8 still simplifies relative to the spec, deliberately: the
Scheduler/PM/Worker/Review/Inbox loops (different suggested intervals) stay
collapsed into the one tick above rather than becoming 4 independently-timed
Tokio loops — splitting them apart would add coordination complexity for no
behavioral gain here, and would force `Runtime` into shared-mutable-state
mode for *every* command, not just the daemon. There's no formal
`Starting`/`Running`/`Stopping`/`Recovering`/`Degraded`/`Failed` lifecycle
state machine either — `runtime status` answers the same questions
pragmatically (pidfile liveness, last `RuntimeStarted`/`RuntimeStopped`,
per-agent last heartbeat) without it, and `Degraded`/`Failed` would need
failure-detection heuristics nothing here asks for yet. `logs` is the event
log tailed/filtered, not a separate logging subsystem. The spec's
`SchedulerStarted`/`SchedulerStopped`/`TaskQueued`/`TaskDispatched`/
`ExecutionQueued`/`ReviewQueued`/`TaskResumed` events were skipped — each
existed to mark entry into one of the 4 separate loops/queues above, so
there's no distinct transition left for them to mark.

Check a project's state:

```bash
cargo run -- status --project sample
```

List a project's open inbox items (questions/decisions escalated by agents)
and answer one (no `--project` needed, it's derived from the inbox item),
which unblocks its task for the next `run`:

```bash
cargo run -- inbox --project sample
cargo run -- inbox answer <inbox-id> "Use OAuth via Google + GitHub"
```

Print the full event log across every project (audit trail / replay source
of truth):

```bash
cargo run -- timeline
```

### Initiatives, requirements, plans, and decisions

A second top-level hierarchy alongside Goals: an Initiative starts in
discovery, gathers Requirements, and is planned into Epics (each becomes one
`Task`, flowing through the same `run` pipeline as goal-derived tasks) and
Decisions (escalated to the inbox for a human to resolve).

```bash
cargo run -- initiative create "Build Gmail Agent" --project sample \
  --description "Routes inbound email to the right team"
cargo run -- requirement create <initiative-id> "Support PDF attachments"
cargo run -- requirement create <initiative-id> "What auth provider should we use?"
cargo run -- initiative plan <initiative-id>
```

Planning runs the deterministic PM planner (`InitiativePlanningService`): a
requirement containing `?` becomes a Decision, every other requirement
becomes an Epic with one `Implement: <epic title>` task. Run the normal
orchestration tick to execute the epics' tasks, then resolve any escalated
decisions:

```bash
cargo run -- run --project sample
cargo run -- decision --project sample             # list open decisions
cargo run -- decision resolve <decision-id> "Use OAuth via Google + GitHub"
```

The initiative automatically transitions Discovery → Planned → Completed:
it's marked Completed once every epic-derived task has reached `Done` and
every decision has been resolved — checked after each task review approval
and after each decision resolution, never polled.

```bash
cargo run -- initiative show <initiative-id>   # requirements/epics/decisions + status
cargo run -- initiative list --project sample
```

## Mission Control (desktop UI)

A Tauri 2 + React desktop app (`apps/desktop`) that gives the runtime a
visual home. It reads the same SQLite-backed event log the CLI does
(`beethoven_db::SqliteEventStore` + `beethoven_core::RuntimeState::replay`,
via the `beethoven-tauri` crate's command/derivation layer) — it is a new
*reader* of existing state, not a new source of truth. No `Runtime`/
`ModelRuntime` runs in the desktop process in this phase, since every
command is a pure read.

```bash
cd apps/desktop
npm install
npm run tauri dev    # reads the same ~/.beethoven (or BEETHOVEN_HOME) the CLI uses
```

Realtime updates come from Tauri's own event bridge, not a literal
websocket: a background task polls the event log every 1.5s and emits one
`state-changed` event on change, which the frontend uses to invalidate and
refetch every TanStack Query — so the UI updates on its own (within ~1.5s)
whenever `beethoven run`/`start` or any CLI command appends events, no
manual refresh needed.

**Implemented (Phase 1.9a, read-only):**

- Runtime status (daemon liveness, last start/stop, active agent/task/inbox
  counts)
- Projects (list + detail: initiatives/epics/decisions, goals/tasks)
- Agents (per-project org tree + detail: status, current task, last
  heartbeat, recent activity)
- Timeline (full event history, filterable by project/agent/event
  type/date range)
- Inbox (decisions/questions/approvals/alerts, rendered read-only)
- Active execution (every in-progress task, its agent, and elapsed runtime)
- Realtime updates (no manual refresh)

**Deliberately deferred to Phase 1.9b** (so the UI is proven as a pure
observability layer before any mutation path exists): starting/stopping the
runtime, answering inbox items, pausing/resuming a project, and retrying a
task. Inbox action buttons (Approve/Reject/Respond/Answer/Acknowledge) are
rendered visibly disabled rather than omitted, so 1.9b only has to wire up
`onClick`, not redesign the cards.

## Stub behavior

What's real:

- `beethoven-git`/`beethoven-workspaces`: real `git worktree add`/`remove`,
  real branch creation, a real `tmux` session per task (`new-session`,
  `send-keys`, `capture-pane`, `kill-session`), and real
  `git status --porcelain` / `git diff --cached` capture.
- The prompt contract: every task gets a structured prompt
  (project/task/repository/branch/requirements) built by `PromptBuilder` and
  rendered to disk before execution.
- **The model invocation** (`ClaudeCodeRuntime`, implementing the
  `ModelRuntime` trait in `beethoven-agents`): a real
  `claude --dangerously-skip-permissions --output-format json -p` call,
  prompted over stdin and run inside the task's real tmux session. Its JSON
  envelope (`is_error`, `result`, `total_cost_usd`, and an optional
  `structured_output` field when a JSON schema is supplied) is parsed
  directly — never prose — into an `ExecutionResult`. `ModelRuntime` is
  injected into `Runtime::load`, so tests substitute a stub implementation
  without touching orchestration code. See
  [`docs/phase-1.7-real-model-runtime.md`](docs/phase-1.7-real-model-runtime.md).
- The review workflow's control flow: Approve/Needs Changes/Blocked/Question
  each drive genuinely different event sequences and task-state transitions
  (`TaskStatus::Review` → `Approved`/`Done`, or back to `Ready` with a bumped
  attempt, or `Blocked` with an inbox escalation).

What's still stubbed (deterministic heuristics standing in for real model
judgment calls, per `beethoven-agents`) — replacing these is Phase 1.7b:

- **Planning** (`PlanningService`) splits `- ` bullet lines in a goal's
  description into tasks, falling back to one task named after the goal.
- **Initiative planning** (`InitiativePlanningService`) turns each
  requirement containing `?` into a Decision and every other requirement
  into an Epic, falling back to one epic named after the initiative when
  there are no requirements yet — a deterministic stand-in for a real PM
  model, by design (see
  [`docs/phase-1.6-project-manager-runtime.md`](docs/phase-1.6-project-manager-runtime.md)).
  `PromptBuilder::pm_planning` and the `PM_PLANNING_JSON_SCHEMA`/
  `PmPlanResponse` structured-output types already exist for when this
  becomes model-driven.
- **The execution decision** (`ExecutionService`) — whether a task completes,
  raises a question, or spawns a subtask — is a deterministic heuristic on
  the task title and attempt number, not a model judgment call. Phase 1.7a
  deliberately leaves this gate unchanged: only the model invocation *behind*
  a `Completed` outcome is now real.
- **The review decision** (`ReviewService`) is a keyword heuristic on the
  task title and whether the diff is empty (e.g. "risky"/"migration" titles
  are always Blocked, "clarify" titles always raise a question, an empty
  diff always Needs Changes, everything else Approved) — not a real review
  agent reading the diff. `PromptBuilder::review` and the
  `REVIEW_JSON_SCHEMA`/`ReviewResponse` structured-output types already
  exist for when this becomes model-driven.
- **PR creation** (`beethoven_git::open_pr`) records a fake `stub://pr/...`
  URL as an event; there's no real GitHub integration yet.

Because the review heuristic is keyword-based and ignores the human's inbox
answer, a task titled with "risky"/"migration"/"clarify" will re-escalate
identically on every retry until the title or the heuristic changes.
