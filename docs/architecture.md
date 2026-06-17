# Beethoven Architecture Specification (CLI-First, Tauri-Native)

## Vision

Beethoven is a personal agentic software organization.

The user defines goals.

A Project Manager agent plans work.

Specialized agents execute work.

Questions flow upward.

Decisions flow to the user.

Execution happens autonomously.

---

# Core Philosophy

Do not build:

```text
CLI
+
Tauri App
```

Build:

```text
Rust Runtime
├── CLI Interface
├── API Interface
└── Tauri Interface
```

The runtime is the product.

The interfaces are simply views into the runtime.

---

# Evolution Strategy

## Phase 1

CLI

Purpose:

Validate:

- Goal planning
- Task decomposition
- Agent delegation
- Workspace orchestration
- Event sourcing
- Human escalation

Architecture:

```text
Human
  │
  ▼
Beethoven CLI
  │
  ▼
Rust Runtime
```

---

## Phase 2

CLI + API

Purpose:

Introduce remote access.

Architecture:

```text
Human
  │
  ├── CLI
  │
  └── HTTP API
          │
          ▼
      Rust Runtime
```

---

## Phase 3

CLI + API + Tauri

Purpose:

Mission Control UI.

Architecture:

```text
React
   │
Tauri Commands
   │
Rust Runtime
```

No business logic should exist in React.

---

# High-Level Architecture

```text
Human
  │
  ▼
Mission Control
  │
  ▼
Project Manager Agent
  │
  ├── Backend Agent
  ├── Frontend Agent
  ├── QA Agent
  ├── Research Agent
  └── DevOps Agent
          │
          ▼
Workspace Runtime
          │
          ▼
tmux
git worktree
model runtime
```

---

# Repository Layout

```text
beethoven/

├── crates/
│
├── beethoven-core/
├── beethoven-events/
├── beethoven-agents/
├── beethoven-workspaces/
├── beethoven-git/
├── beethoven-db/
├── beethoven-api/
├── beethoven-cli/
└── beethoven-tauri/

├── apps/
│   └── desktop/
│
└── docs/
```

---

# Runtime Layers

## Core

Responsibilities:

- goals
- tasks
- orchestration
- state management

No UI dependencies.

---

## Agents

Responsibilities:

- planning
- delegation
- execution
- escalation

---

## Workspaces

Responsibilities:

- tmux
- git worktrees
- filesystem isolation

---

## Events

Responsibilities:

- event sourcing
- replay
- timeline

---

## Database

Responsibilities:

- persistence
- recovery

---

## Interfaces

Responsibilities:

- CLI
- API
- Tauri

No business logic.

---

# CLI

Binary:

```bash
beethoven
```

Examples:

```bash
beethoven run

beethoven status

beethoven inbox

beethoven timeline

beethoven goal create

beethoven goal plan
```

The CLI is a workflow interface.

Not a CRUD interface.

---

# API

Purpose:

Expose runtime externally.

Examples:

```http
GET /goals

GET /tasks

GET /agents

GET /timeline

POST /goals
```

API is a thin wrapper around runtime services.

---

# Tauri

Purpose:

Mission Control.

The Tauri app should never contain:

- planning logic
- delegation logic
- orchestration logic

All execution remains inside Rust.

---

# Agent Model

## Project Manager

Responsibilities:

- plan goals
- create tasks
- assign tasks
- aggregate questions
- escalate decisions

---

## Worker Agents

Responsibilities:

- execute tasks
- create subtasks
- report status

Cannot directly interrupt humans.

---

# Agent Lifecycle

```text
Created
 ↓
Idle
 ↓
Assigned
 ↓
Planning
 ↓
Executing
 ↓
Review
 ↓
Completed
```

Alternative states:

```text
Blocked
Waiting
Escalated
```

---

# Task Lifecycle

```text
Draft
 ↓
Ready
 ↓
Assigned
 ↓
In Progress
 ↓
Review
 ↓
Approved
 ↓
Done
```

---

# Workspace Architecture

Each agent receives:

```text
workspace/

├── repo/
├── memory/
├── logs/
├── artifacts/
├── prompts/
└── state.json
```

Runtime:

```text
Agent

├── tmux session
├── git worktree
├── model runtime
└── local memory
```

---

# Git Strategy

Every task receives a branch.

Example:

```text
task/implement-auth
task/create-dashboard
task/fix-tests
```

Flow:

```text
Task
 ↓
Branch
 ↓
PR
 ↓
Review
 ↓
Merge
```

---

# Event Sourcing

Everything becomes an event.

```rust
struct Event {
    id: String,
    event_type: String,
    actor_id: String,
    payload: Value,
    timestamp: DateTime<Utc>
}
```

Benefits:

- audit history
- replay
- debugging
- metrics

---

# Required Events

```text
GoalCreated
GoalUpdated

AgentCreated
AgentStarted
AgentStopped

TaskCreated
TaskAssigned
TaskStarted
TaskBlocked
TaskCompleted

QuestionRaised
QuestionAnswered

DecisionRequested
DecisionResolved

WorkspaceCreated
WorkspaceDestroyed

PRCreated
PRMerged
```

---

# Database

MVP:

```text
SQLite
+
SQLx
```

Location:

```text
~/.beethoven/
```

Structure:

```text
~/.beethoven/

├── beethoven.db
├── events/
├── workspaces/
├── artifacts/
└── logs/
```

---

# Realtime

Mission Control subscribes via websocket.

Examples:

```text
TASK_UPDATED

AGENT_UPDATED

INBOX_UPDATED

EVENT_CREATED
```

---

# Inbox Model

All human interaction happens here.

Types:

```text
Decision
Approval
Question
Alert
```

Flow:

```text
Worker
 ↓
Manager
 ↓
Inbox
 ↓
Human
 ↓
Manager
 ↓
Worker
```

---

# Recovery Requirements

After restart:

- goals survive
- tasks survive
- agents survive
- events survive
- inbox survives
- workspaces survive

State must be reconstructable from events.

---

# MVP Success Criteria

The system is successful when:

1. User creates goal.
2. PM generates plan.
3. PM creates tasks.
4. PM assigns tasks.
5. Agents execute work.
6. Agents create subtasks.
7. Questions escalate.
8. Human answers.
9. Work resumes.
10. Entire execution history is replayable.

---

# Guiding Principle

The runtime is the product.

The CLI is a view.

The API is a view.

The Tauri application is a view.

All three interfaces should operate on the exact same runtime, event system, workspace manager, and database.

Business logic should only exist once.
