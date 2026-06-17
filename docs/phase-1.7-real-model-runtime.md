# Beethoven Phase 1.7: Real Model Runtime

## Objective

Replace deterministic planning, execution, and review heuristics with real model-driven behavior.

At the end of Phase 1.7:

- PM planning is model-driven
- Worker execution is model-driven
- Review is model-driven
- Beethoven orchestrates work
- Models perform reasoning

---

## Current State

Phase 1.6 delivers:

- Projects
- Initiatives
- Requirements
- Plans
- Epics
- Tasks
- Decisions
- Workspaces
- Git integration
- tmux integration
- Event sourcing

Current limitation:

Planning = Stub
Execution = Stub
Review = Stub

---

## Guiding Principle

Beethoven is not an LLM framework.

Beethoven is a software organization runtime.

Models are interchangeable workers.

---

## Scope

### In Scope

- ModelRuntime abstraction
- Claude Code integration
- PM planning runtime
- Worker runtime
- Review runtime
- Prompt generation
- Execution artifacts

### Out of Scope

- Tauri UI
- HTTP API
- Agent memory
- Multi-machine execution

---

## Core Requirement

Create a provider abstraction before integrating Claude.

Never couple Beethoven directly to a specific model provider.

---

## Model Runtime Interface

```rust
pub trait ModelRuntime {
    fn execute(
        &self,
        request: ExecutionRequest,
    ) -> Result<ExecutionResult>;
}
```

---

## Execution Request

```rust
pub struct ExecutionRequest {
    pub project_id: ProjectId,
    pub agent_id: AgentInstanceId,
    pub task_id: Option<TaskId>,
    pub prompt: String,
    pub workspace_path: PathBuf,
}
```

---

## Execution Result

```rust
pub struct ExecutionResult {
    pub summary: String,
    pub files_modified: Vec<PathBuf>,
    pub status: ExecutionStatus,
}
```

---

## Runtime Providers

### Phase 1

Implement:

- ClaudeCodeRuntime

### Future

Support:

- ClaudeCodeRuntime
- CodexRuntime
- OpenAIRuntime
- LocalRuntime

without changing orchestration code.

---

## PM Runtime

Replace:

Initiative -> Stub Planner

with:

Initiative -> PM Agent -> Requirements -> Plan -> Epics -> Tasks

The PM model generates:

- requirements
- plans
- epics
- initial tasks

---

## PM Planning Prompt

Inputs:

- Project
- Initiative
- Requirements
- Repository Context

Outputs:

Structured JSON only.

Example:

```json
{
  "requirements": [],
  "epics": [],
  "tasks": []
}
```

No freeform parsing.

---

## Worker Runtime

Replace:

Task -> Heuristic

with:

Task -> Worker Agent -> Claude Code

Worker receives:

- project
- task
- epic
- requirements
- repository path
- branch

---

## Claude Execution Strategy

Launch Claude Code inside the task workspace.

Example:

```bash
claude --dangerously-skip-permissions
```

Beethoven owns:

- workspace
- prompt
- lifecycle
- logging

Claude owns:

- coding

---

## Review Runtime

Replace:

Keyword Review

with:

Review Agent

Inputs:

- task
- diff
- files changed
- execution summary

Outputs:

```json
{
  "decision": "approve",
  "notes": ""
}
```

or

```json
{
  "decision": "changes_requested",
  "notes": ""
}
```

or

```json
{
  "decision": "question",
  "notes": ""
}
```

---

## Structured Outputs

All model responses must use structured outputs.

Never parse prose.

---

## Prompt Builder

Create dedicated prompt generators.

Responsibilities:

- PM prompts
- Worker prompts
- Review prompts

Never inline prompts throughout orchestration code.

---

## New Tables

### model_runs

- id
- agent_id
- project_id
- task_id
- provider
- status
- started_at
- completed_at

### model_artifacts

- id
- model_run_id
- artifact_type
- path
- created_at

---

## New Events

- ModelRunStarted
- ModelRunCompleted
- ModelRunFailed
- PromptGenerated
- PlanningGenerated
- ReviewGenerated

---

## Error Handling

Model failures must not crash orchestration.

Possible outcomes:

- Retry
- Escalate
- Failed

Every failure generates an event.

---

## Observability

For every model run store:

- prompt
- response
- runtime
- status
- token metadata when available

Store everything as artifacts.

---

## Acceptance Criteria

PM

1. Initiative submitted
2. PM generates requirements
3. PM generates plan
4. PM generates epics
5. PM generates tasks

Worker

6. Worker receives task
7. Claude modifies code
8. Git diff captured

Review

9. Review agent evaluates diff
10. Review decision recorded

System

11. All executions recorded as events
12. All outputs stored as artifacts
13. Deterministic planning removed
14. Deterministic review removed

---

## End State

Beethoven becomes a Software Organization Runtime.

The PM reasons.

Workers code.

Reviewers review.

Beethoven orchestrates.
