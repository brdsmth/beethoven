# How a Goal Becomes Tasks, Dependencies, Success Criteria, and Target Scores

What the current planner actually does, with exact code locations. Written against the
state of the codebase as of 2026-06-20. This is a trace of *today's* deterministic stub
planner, not a design doc for a smarter one -- what's explicitly deferred to Phase 2.5+ is
called out inline below.

## The starting point: a Goal is just a root Task

There is no `Goal` type. A "goal" is a `Task` with `parent_task_id: None`, created the same
way as any other task:

```
Runtime::create_task(project_id, parent_task_id, title, description, success_criteria, target_score, draft)
```
`crates/beethoven-core/src/runtime.rs:571-602`

This is a **human-driven** call (CLI `beethoven task create`, or the Tauri `create_task`
command) -- not the planner. It emits one `TaskCreated` event:

```rust
json!({
    "id": id,
    "parent_task_id": parent_task_id,
    "title": title,
    "description": description,
    "status": if draft { TaskStatus::Draft } else { TaskStatus::Ready },
    "success_criteria": success_criteria,
    "target_score": target_score.unwrap_or(DEFAULT_TARGET_SCORE),
})
```
`crates/beethoven-core/src/runtime.rs:586-599`

So even before planning runs, a goal already has whatever `success_criteria`/`target_score`
the human passed in (or the default, see below). Planning only ever adds *children* under it.

---

## 1. Goal/Task → Tasks (decomposition)

**Decision lives in:** `PlanningService::plan` -- `crates/beethoven-agents/src/lib.rs:80-112`

Triggered by `Runtime::plan_task(task_id)` -- `crates/beethoven-core/src/runtime.rs:609`.

The algorithm, in full:

```rust
pub fn plan(title: &str, description: &str) -> PlanOutcome {
    let mut outcome = PlanOutcome::default();
    for item in description.lines().map(str::trim).filter_map(|line| {
        line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")).map(str::trim).filter(|t| !t.is_empty())
    }) {
        if item.contains('?') {
            outcome.decision_drafts.push(DecisionDraft { question: item.to_string(), context: item.to_string(), reason: /* see below */ });
        } else {
            outcome.task_drafts.push(TaskDraft { title: item.to_string(), description: String::new(), reason: /* see below */ });
        }
    }
    if outcome.task_drafts.is_empty() && outcome.decision_drafts.is_empty() {
        outcome.task_drafts.push(TaskDraft { title: format!("Implement: {title}"), description: String::new(), reason: /* see below */ });
    }
    outcome
}
```

In plain language: it is **string parsing, not planning**.

1. Split the parent task's `description` into lines.
2. A line prefixed with `- ` or `* ` is a candidate. Everything else (plain prose) is ignored entirely.
3. A candidate line containing `?` becomes a `DecisionDraft` (escalated to a human, not decomposed).
4. Every other candidate line becomes a `TaskDraft`, verbatim as the title, with an **empty** `description`.
5. If there were no bullet lines at all, exactly one fallback `TaskDraft` is generated: `"Implement: {title}"`.

Back in `Runtime::plan_task` (`crates/beethoven-core/src/runtime.rs:609-654`), each `TaskDraft`
becomes a real child `Task` via a `TaskCreated` event with `parent_task_id` set to the task
being planned and `status: Ready`:

```rust
json!({
    "id": id,
    "parent_task_id": task_id,
    "title": draft.title,
    "description": draft.description,   // always "" for planner-generated tasks
    "status": TaskStatus::Ready,
    "success_criteria": "",              // see section 3
    "target_score": DEFAULT_TARGET_SCORE, // see section 4
})
```
`crates/beethoven-core/src/runtime.rs:628-636`

**What's not implemented:** any actual judgment about scope, granularity, or what "the work"
is. A task is generated 1:1 per bullet line. There is no LLM call anywhere in this path.

---

## 2. Task → Dependencies

**Decision lives in:** nowhere, today. The planner generates zero dependencies.

This is recorded explicitly, not silently omitted -- `Runtime::plan_task` builds its
`PlanQualityMetrics` with:

```rust
// The stub planner never infers dependencies between the tasks
// it generates -- that's Phase 2.5's job. Kept at zero, honestly,
// rather than fabricated.
dependency_count: 0,
```
`crates/beethoven-core/src/runtime.rs:690-696`, and `planning_summary.dependencies: Vec::new()` at `runtime.rs:706`.

The *only* way a `TaskDependency` edge gets created today is the explicit, human/manual API:

```
Runtime::add_task_dependency(task_id, depends_on_task_id)
```
`crates/beethoven-core/src/runtime.rs:1711-1741`

It validates (in order) before writing anything to the event log:
1. `task_id != depends_on_task_id` (no self-dependency) -- `runtime.rs:1712-1714`
2. Both tasks exist -- `runtime.rs:1715-1720`
3. Both tasks are in the same project -- `runtime.rs:1721-1725`
4. The edge wouldn't close a cycle, via DFS in `RuntimeState::creates_cycle` (`crates/beethoven-core/src/state.rs:701-716`) -- `runtime.rs:1726-1730`

Only then does it emit `TaskDependencyCreated` (`runtime.rs:1733-1739`).

A dependency's effect on scheduling lives in `RuntimeState::is_executable`
(`crates/beethoven-core/src/state.rs:727-753`): a task is only executable once every task it
depends on is `Done` (lines 740-746). This check is **never persisted** -- it's recomputed
from current state on every scheduling pass, so a task automatically becomes executable the
instant its blocking dependency completes, with no extra event.

There is also a `reason: Option<String>` field threaded through `TaskRefDto.dependency_reason`
(`crates/beethoven-tauri/src/dto.rs`) and a lookup helper, `dependency_reason()` in
`crates/beethoven-tauri/src/derive.rs`, that traces a dependency edge back to whichever
`PlanningSummary` artifact recorded a reason for it. It always returns `None` today, because
nothing populates `PlanningSummary.dependencies` yet -- the plumbing exists, the planner
intelligence to use it doesn't.

---

## 3. Task → Success Criteria

**Decision lives in two places, both hardcoded, never computed:**

- **Root task (human-created):** whatever string the human passed to `create_task`, or `""`
  if they passed nothing. `crates/beethoven-core/src/runtime.rs:577,596` -- it's a pass-through, not a decision.
- **Planner-generated child task:** always the literal empty string `""`.
  `crates/beethoven-core/src/runtime.rs:634` (the `TaskCreated` payload) and `runtime.rs:648` (mirrored into the `PlannedTaskExplanation`).

The *reason* recorded for this (Phase 2.4.5's explainability layer) is itself hardcoded, not derived from anything about the task:

```rust
success_criteria_reason: "No explicit success criteria were specified in the planning \
    input for this task."
```
`crates/beethoven-core/src/runtime.rs:649-651`

**What's not implemented:** the planner never derives success criteria from the bullet text
itself (e.g. turning "- Google OAuth works" into a structured criterion). It is purely a
constant.

---

## 4. Task → Target Score

**Decision lives in:** a single constant.

```rust
const DEFAULT_TARGET_SCORE: f32 = 70.0;
```
`crates/beethoven-core/src/runtime.rs:29`

- **Root task:** `target_score.unwrap_or(DEFAULT_TARGET_SCORE)` -- a human can override it via `create_task`'s optional argument; absent that, it's the constant. `runtime.rs:597`
- **Planner-generated child task:** always `DEFAULT_TARGET_SCORE`, unconditionally -- no override path exists for planner output. `runtime.rs:635` and mirrored at `runtime.rs:644`.

Same pattern for the recorded reason -- a constant string, not a computation:

```rust
target_score_reason: "Default quality bar applied -- the planning input specified no \
    explicit target score for this task."
```
`crates/beethoven-core/src/runtime.rs:645-647`

**Where the number actually matters** (this is the one place "target score" does real work,
as opposed to just being stored): `Runtime::finish_task`, comparing a real review's score
against it to decide retry vs. complete:

```rust
match ReviewService::review(&task.title, &diff_text) {
    ReviewOutcome::Scored(scored) if scored.score >= task.target_score => { /* ReviewApproved -> TaskCompleted */ }
    ReviewOutcome::Scored(scored) => {
        if task.attempt >= max_attempts { /* give up, escalate to inbox */ }
        else { /* ReviewRejected -> back to Ready for another attempt */ }
    }
    ...
}
```
`crates/beethoven-core/src/runtime.rs:1225-1226` (the comparison) and `runtime.rs:1264-1268` (the give-up path, quoting both `scored.score` and `task.target_score` in the escalation message).

`ReviewService::review` itself (`crates/beethoven-agents/src/review.rs`) is, like the
planner, a deterministic keyword heuristic today -- not a model call -- so in practice almost
every non-trivial diff clears the default 70 on the first attempt.

---

## Summary table

| Generated thing | Decided by | File:line | Real computation, or constant? |
|---|---|---|---|
| Child tasks | `PlanningService::plan` (bullet-line extraction) | `beethoven-agents/src/lib.rs:80-112` | String parsing |
| Decisions (from `?` lines) | `PlanningService::plan` | `beethoven-agents/src/lib.rs:85-92` | String parsing |
| Task dependencies | nobody (planner); `Runtime::add_task_dependency` (human/manual only) | `beethoven-core/src/runtime.rs:1711-1741` | Manual API call, validated (cycle/self-dep/cross-project) |
| Success criteria (planner-generated tasks) | hardcoded | `beethoven-core/src/runtime.rs:634,648` | Constant `""` |
| Success criteria (root task) | human input, pass-through | `beethoven-core/src/runtime.rs:577,596` | Pass-through, no decision |
| Target score (planner-generated tasks) | hardcoded | `beethoven-core/src/runtime.rs:29,635,644` | Constant `70.0` |
| Target score (root task) | human input, with default fallback | `beethoven-core/src/runtime.rs:578,597` | Pass-through or constant |
| "Why" each of the above happened | `PlanningSummary` artifact, built alongside the above in `Runtime::plan_task` | `beethoven-core/src/runtime.rs:609-755`, shapes in `beethoven-core/src/planning_summary.rs` | Templated strings, traceable via `creation_trace_for_task`/`creation_trace_for_decision`/`dependency_reason` in `beethoven-tauri/src/derive.rs` |

## What this means in practice

Nothing in this pipeline calls a model. `PlanningService::plan` and `ReviewService::review`
are both explicitly-labeled "stub" deterministic heuristics (see their own doc comments).
The entire goal -> tasks -> dependencies -> success criteria -> target scores chain today is:

```
Goal description's bullet lines  -->  1:1 child tasks (empty description)
Goal description's "?" lines     -->  1:1 Decisions, escalated to inbox
Dependencies                     -->  none, ever, unless a human calls add_task_dependency
Success criteria                 -->  "" for every planner-generated task
Target score                     -->  70.0 for every planner-generated task
```

Phase 2.5 (not implemented) is where any of the five right-hand sides above would become an
actual model-driven decision instead of a parse or a constant.
