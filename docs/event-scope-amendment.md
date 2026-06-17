# Beethoven Architecture Amendment: Event Scoping

## Problem

The Project specification introduced a contradiction:

- Projects are the primary organizational boundary.
- Events were defined as requiring a non-null project_id.
- Agents remain global.
- Project lifecycle events occur before a project exists.

This creates ambiguity for:

- AgentCreated
- AgentStarted
- AgentStopped
- ProjectCreated
- RuntimeStarted
- RuntimeStopped

These events do not naturally belong to a project.

---

## Solution

Introduce EventScope.

```rust
pub enum EventScope {
    Global,
    Project(ProjectId),
}
```

Updated event model:

```rust
pub struct Event {
    pub id: EventId,
    pub scope: EventScope,
    pub event_type: EventType,
    pub actor_id: Option<ActorId>,
    pub payload: Value,
    pub timestamp: DateTime<Utc>,
}
```

---

## Global Events

Examples:

- AgentCreated
- AgentStarted
- AgentStopped
- ProjectCreated
- ProjectArchived
- RuntimeStarted
- RuntimeStopped
- SettingsUpdated

These belong to Beethoven itself.

---

## Project Events

Examples:

- GoalCreated
- GoalPlanned
- TaskCreated
- TaskAssigned
- TaskBlocked
- TaskCompleted
- DecisionRequested
- DecisionResolved
- PRCreated
- PRMerged
- WorkspaceCreated
- WorkspaceDestroyed

These must reference a project.

Example:

```rust
Event {
    scope: EventScope::Project(project_id),
}
```

---

## Agent Bootstrap

```text
AgentCreated
scope = Global

AgentStarted
scope = Global
```

---

## Project Creation

```text
ProjectCreated
scope = Global
```

Payload:

```json
{
  "project_id": "punch",
  "name": "Punch"
}
```

Future events become project-scoped.

---

## Agent Model Clarification

Agents remain global:

- PM
- Backend
- Frontend
- QA
- Research

Agents are reusable capabilities.

Projects own work.

---

## Runtime Context

Example:

```bash
beethoven run punch
```

Runtime receives:

```rust
RunContext {
    project_id: ProjectId,
    repository_path: PathBuf,
}
```

Global agents execute inside the project context.

---

## Specification Change

Remove:

Every event must have a non-null project_id.

Replace with:

Every event must have a scope.

EventScope:
- Global
- Project(ProjectId)

Project-scoped events must reference a project.

Global events are allowed for runtime, agent, and project lifecycle operations.

---

## Benefits

1. No fake system project
2. Cleaner event sourcing
3. Simpler querying
4. Better multi-project support
5. Cleaner audit trails

---

## Recommendation

Adopt EventScope before additional event types are added to avoid future migrations.
