# Beethoven

A personal agentic software organization. See [`docs/architecture.md`](docs/architecture.md)
for the full design, [`docs/phase-1.5-real-execution-plan.md`](docs/phase-1.5-real-execution-plan.md)
for the real-execution plan, and
[`docs/phase-1.6-project-manager-runtime.md`](docs/phase-1.6-project-manager-runtime.md)
for the project-management domain model. This is Phase 1.6: on top of
Phase 1.5's real, event-sourced Project → Goal → Task → Agent → Inbox model
(working CLI, real `git worktree` + `tmux` execution mechanics, real `git
status`/`git diff` artifact capture, and a review workflow — Approve / Needs
Changes / Blocked / Question), there's now a second top-level hierarchy,
Project → Initiative → Requirement → Plan → Epic, that terminates in the
same `Task`/execution/review pipeline. A deterministic PM planner turns an
initiative's requirements into epics (one task each) and decisions
(auto-escalated to the inbox); the initiative completes once every epic's
task is done and every decision is resolved. Only the model invocation
itself and the planning/review *decisions* are stubbed out (see
[Stub behavior](#stub-behavior)) — Phase 1.6 deliberately does not add real
LLM planning, only the domain objects a future model-driven PM will operate
on.

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
project's idle agent instances, then executes each assigned task):

```bash
cargo run -- run --project sample
```

For each assigned task this drives the real execution pipeline:

1. Create a real `git worktree` on a fresh `task/<slug>` branch and a real
   `tmux` session rooted at it.
2. Render the structured task prompt and write it to
   `<workspace>/prompts/prompt.md`.
3. Invoke the model runtime (currently `ClaudeCodeRuntime`, a stub — see
   below) inside that tmux session.
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

## Stub behavior

What's real:

- `beethoven-git`/`beethoven-workspaces`: real `git worktree add`/`remove`,
  real branch creation, a real `tmux` session per task (`new-session`,
  `send-keys`, `capture-pane`, `kill-session`), and real
  `git status --porcelain` / `git diff --cached` capture.
- The prompt contract: every task gets a structured prompt
  (project/task/repository/branch/requirements) rendered to disk before
  execution.
- The review workflow's control flow: Approve/Needs Changes/Blocked/Question
  each drive genuinely different event sequences and task-state transitions
  (`TaskStatus::Review` → `Approved`/`Done`, or back to `Ready` with a bumped
  attempt, or `Blocked` with an inbox escalation).

What's still stubbed (deterministic heuristics standing in for real model
calls, per `beethoven-agents`):

- **Planning** (`PlanningService`) splits `- ` bullet lines in a goal's
  description into tasks, falling back to one task named after the goal.
- **Initiative planning** (`InitiativePlanningService`) turns each
  requirement containing `?` into a Decision and every other requirement
  into an Epic, falling back to one epic named after the initiative when
  there are no requirements yet — a deterministic stand-in for a real PM
  model, by design (see
  [`docs/phase-1.6-project-manager-runtime.md`](docs/phase-1.6-project-manager-runtime.md)).
- **The execution decision** (`ExecutionService`) — whether a task completes,
  raises a question, or spawns a subtask — is a deterministic heuristic on
  the task title and attempt number, not a model judgment call.
- **The model invocation itself** (`ClaudeCodeRuntime`) does not call a real
  model. It runs one deterministic command inside the task's real tmux
  session (`cat prompt.md >> NOTES.md`) so the rest of the pipeline has
  genuine on-disk changes to diff and review. Swapping in a real
  `claude --dangerously-skip-permissions` invocation is a one-line change to
  that command.
- **The review decision** (`ReviewService`) is a keyword heuristic on the
  task title and whether the diff is empty (e.g. "risky"/"migration" titles
  are always Blocked, "clarify" titles always raise a question, an empty
  diff always Needs Changes, everything else Approved) — not a real review
  agent reading the diff.
- **PR creation** (`beethoven_git::open_pr`) records a fake `stub://pr/...`
  URL as an event; there's no real GitHub integration yet.

Because the review heuristic is keyword-based and ignores the human's inbox
answer, a task titled with "risky"/"migration"/"clarify" will re-escalate
identically on every retry until the title or the heuristic changes.
