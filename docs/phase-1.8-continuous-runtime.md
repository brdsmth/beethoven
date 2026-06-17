# Beethoven Phase 1.8: Continuous Runtime

## Objective

Transform Beethoven from a manually-triggered workflow engine into a continuously operating software organization.

Current:

Human -> beethoven run -> One orchestration cycle -> Exit

Target:

Human
-> beethoven start
-> Beethoven Daemon
-> Continuous Planning
-> Continuous Execution
-> Continuous Review
-> Continuous Escalation

The organization remains alive even when no user is interacting with it.

---

## Why This Phase Exists

Phase 1.7 introduces:

- Real PM
- Real workers
- Real reviewers
- Real model execution

The major remaining problem:

beethoven run

requires human intervention.

The PM should continuously manage work.

Workers should continuously look for work.

Reviews should happen automatically.

Only decisions should require human attention.

---

## Guiding Principle

Humans manage goals.

Beethoven manages execution.

---

## Scope

### In Scope

- Long-running runtime
- Scheduler
- Worker polling
- Automatic task pickup
- Automatic review
- Automatic escalation
- Runtime monitoring
- Runtime recovery

### Out of Scope

- Tauri UI
- HTTP API
- Organizational memory
- Multi-machine execution

---

## New Runtime Architecture

Current:

CLI -> Runtime -> Exit

Target:

CLI
-> Daemon
    -> Scheduler
    -> PM Loop
    -> Worker Loop
    -> Review Loop
    -> Inbox Loop

---

## Daemon Process

New command:

beethoven start

Starts:

- Scheduler
- PM loop
- Worker loop
- Review loop
- Inbox processor

Runs indefinitely.

---

## Runtime Lifecycle

States:

- Starting
- Running
- Stopping
- Stopped
- Recovering
- Degraded
- Failed

---

## Scheduler

Responsibilities:

- discover ready work
- dispatch work
- trigger reviews
- monitor progress

Flow:

Task Ready
-> Assign Worker
-> Execute
-> Review
-> Complete

---

## PM Loop

Runs continuously.

Responsibilities:

- monitor initiatives
- create new tasks
- identify blockers
- manage decisions
- monitor dependencies

Suggested interval:

30 seconds

---

## Worker Loop

Runs continuously.

Responsibilities:

- discover assigned tasks
- create workspaces
- launch model runtime
- track execution

Suggested interval:

10 seconds

---

## Review Loop

Runs continuously.

Responsibilities:

- discover completed executions
- launch review agents
- approve or reject work
- escalate questions

Suggested interval:

15 seconds

---

## Inbox Loop

Runs continuously.

Responsibilities:

- detect resolved decisions
- unblock tasks
- notify PM

Flow:

Decision Answered
-> Task Unblocked
-> Execution Resumes

---

## Concurrency Model

Multiple workers should execute simultaneously.

Implementation should be Tokio-based.

Examples:

- Punch Backend
- Punch Frontend
- Sailboat Backend
- Beethoven QA

may all execute concurrently.

---

## Runtime Recovery

Critical requirement.

Crash
-> Restart
-> Replay Events
-> Restore State
-> Resume Execution

No task loss.

No inbox loss.

No event loss.

---

## Heartbeats

Every active agent emits heartbeats.

Event:

AgentHeartbeat

Fields:

- agent_id
- project_id
- task_id
- timestamp

Used for monitoring and recovery.

---

## New Commands

### Start Runtime

beethoven start

### Stop Runtime

beethoven stop

### Runtime Status

beethoven runtime status

### Runtime Logs

beethoven logs

---

## Required Events

- RuntimeStarted
- RuntimeStopped
- SchedulerStarted
- SchedulerStopped
- AgentHeartbeat
- TaskQueued
- TaskDispatched
- ExecutionQueued
- ReviewQueued
- TaskResumed
- TaskRecovered

---

## Required Tables

### runtime_state

- id
- status
- started_at
- updated_at

### heartbeats

- id
- agent_id
- project_id
- task_id
- timestamp

---

## Failure Handling

Failures:

- Model Failure
- Workspace Failure
- Git Failure
- tmux Failure
- Review Failure

Every failure:

1. Emits an event
2. Updates status
3. Retries if appropriate
4. Escalates if necessary

---

## Runtime Policies

Configurable:

- Max Concurrent Tasks
- Max Concurrent Agents
- Heartbeat Interval
- Retry Count
- Retry Delay

---

## Acceptance Criteria

1. Runtime starts.
2. Runtime remains active.
3. PM continuously monitors projects.
4. Workers automatically pick up tasks.
5. Reviews happen automatically.
6. Decisions automatically unblock work.
7. State survives restart.
8. Active work resumes after restart.
9. Multiple projects run simultaneously.
10. Human no longer needs to invoke beethoven run.

---

## End State

Beethoven transitions from:

Workflow Engine

to:

Operating Software Organization

Work continuously flows through:

Initiative
-> Planning
-> Execution
-> Review
-> Completion

with human involvement only when decisions are required.
