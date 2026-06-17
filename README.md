# Beethoven

A personal agentic software organization. See [`docs/architecture.md`](docs/architecture.md)
for the full design and [`docs/phase-1.5-real-execution-plan.md`](docs/phase-1.5-real-execution-plan.md)
for the real-execution plan. This is Phase 1.5: a real, event-sourced
Project → Goal → Task → Agent → Inbox model with a working CLI, real `git
worktree` + `tmux` execution mechanics, real `git status`/`git diff` artifact
capture, and a review workflow (Approve / Needs Changes / Blocked / Question)
— with only the model invocation itself and the planning/review *decisions*
still stubbed out (see [Stub behavior](#stub-behavior)).

## Build

```bash
cargo build
```

The binary is `beethoven` (from the `beethoven-cli` crate). All state lives
under `~/.beethoven/` (SQLite event log + per-project workspace directories +
an active-project pointer file). Every CLI invocation is a fresh process that
rebuilds in-memory state by replaying the event log, so state survives
restarts by construction.

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
