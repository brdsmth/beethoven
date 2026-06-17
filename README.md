# Beethoven

A personal agentic software organization. See [`docs/architecture.md`](docs/architecture.md)
for the full design. This is the Phase 1 (CLI) core runtime scaffold: a real,
event-sourced Goal → Task → Agent → Inbox model with a working CLI, but with
agent planning/execution as deterministic stubs rather than a real model
runtime.

## Build

```bash
cargo build
```

The binary is `beethoven` (from the `beethoven-cli` crate). All state lives
under `~/.beethoven/` (SQLite event log + per-task workspace directories).
Every CLI invocation is a fresh process that rebuilds in-memory state by
replaying the event log, so state survives restarts by construction.

## Usage

Create a goal. Lines in the description starting with `- ` are turned into
discrete tasks by the planner; otherwise a single default task is created.

```bash
cargo run -- goal create "Build login page" --description="- Implement the login form
- What auth provider should we use?
- Write the landing page copy"
```

Plan the goal (runs the stub Project Manager planner, creates tasks):

```bash
cargo run -- goal plan <goal-id>
```

Run an orchestration tick (assigns ready tasks to idle agents, executes
assigned tasks — completing them, escalating a question, or spawning a
subtask, depending on the stub heuristics):

```bash
cargo run -- run
```

Check overall state:

```bash
cargo run -- status
```

List open inbox items (questions/decisions escalated by agents) and answer
one, which unblocks its task for the next `run`:

```bash
cargo run -- inbox
cargo run -- inbox answer <inbox-id> "Use OAuth via Google + GitHub"
```

Print the full event log (audit trail / replay source of truth):

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
