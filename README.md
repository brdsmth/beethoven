# Beethoven

A personal agentic software organization. See [`docs/architecture.md`](docs/architecture.md)
for the full design. This is the Phase 1 (CLI) core runtime scaffold: a real,
event-sourced Project → Goal → Task → Agent → Inbox model with a working CLI,
but with agent planning/execution as deterministic stubs rather than a real
model runtime.

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
cargo run -- project create punch --repo ~/workspace/punch --default-branch main
cargo run -- project list      # "*" marks the active project
cargo run -- project show punch
cargo run -- project use punch # sets ~/.beethoven/current-project
cargo run -- project current
```

The first project you ever create becomes active automatically. Any command
below that needs a project takes `--project <slug>`, falling back to the
active project when omitted.

### Goals and tasks

Create a goal. Lines in the description starting with `- ` are turned into
discrete tasks by the planner; otherwise a single default task is created.

```bash
cargo run -- goal create "Build login page" --project punch --description="- Implement the login form
- What auth provider should we use?
- Write the landing page copy"
```

Plan the goal (runs the stub Project Manager planner, creates tasks — no
`--project` needed, it's derived from the goal):

```bash
cargo run -- goal plan <goal-id>
```

Run an orchestration tick for a project (assigns ready tasks to that
project's idle agent instances, executes assigned tasks — completing them,
escalating a question, or spawning a subtask, depending on the stub
heuristics):

```bash
cargo run -- run --project punch
```

Check a project's state:

```bash
cargo run -- status --project punch
```

List a project's open inbox items (questions/decisions escalated by agents)
and answer one (no `--project` needed, it's derived from the inbox item),
which unblocks its task for the next `run`:

```bash
cargo run -- inbox --project punch
cargo run -- inbox answer <inbox-id> "Use OAuth via Google + GitHub"
```

Print the full event log across every project (audit trail / replay source
of truth):

```bash
cargo run -- timeline
```

## Stub behavior

`beethoven-agents` stands in for real model-driven planning/execution:

- Planning splits `- ` bullet lines in a goal's description into tasks.
- A task whose title contains `?` raises a question (escalates to the inbox)
  on its first attempt only.
- A task whose title starts with "Implement" spawns one subtask ("Write
  tests for ...") on its first attempt, then completes on retry.
- Everything else completes immediately.

`beethoven-workspaces` and `beethoven-git` similarly stand in for real
tmux/git-worktree/PR integrations: they create/destroy the on-disk workspace
layout and record branch/PR facts as events, without touching a real
repository or remote.
