use beethoven_agents::AgentRole;
use beethoven_core::{
    AgentStatus, ArtifactKind, DecisionStatus, InboxKind, InboxStatus, ProjectStatus, ReviewStatus, RunStatus,
    ScoreBandCount, TaskStatus,
};
use chrono::{DateTime, Utc};
use serde::Serialize;

#[derive(Debug, Clone, Serialize, Default)]
pub struct TaskCountsDto {
    pub draft: usize,
    pub ready: usize,
    pub assigned: usize,
    pub in_progress: usize,
    pub review: usize,
    pub approved: usize,
    pub done: usize,
    pub blocked: usize,
    pub cancelled: usize,
}

/// A compact reference to another task -- enough to link to and label it
/// without pulling in its whole subtree. Backs `TaskDto.dependencies`/
/// `dependents`.
#[derive(Debug, Clone, Serialize)]
pub struct TaskRefDto {
    pub id: String,
    pub title: String,
    pub status: TaskStatus,
    /// Why this dependency edge exists, traced back to the `PlanningSummary`
    /// that generated it. Always `None` today -- the stub planner never
    /// generates dependencies -- but the lookup exists for when one does.
    pub dependency_reason: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
pub struct TaskDto {
    pub id: String,
    pub project_id: String,
    pub parent_task_id: Option<String>,
    pub title: String,
    pub description: String,
    pub status: TaskStatus,
    pub assigned_agent_id: Option<String>,
    pub assigned_agent_name: Option<String>,
    pub attempt: u32,
    pub branch: Option<String>,
    pub pr_url: Option<String>,
    pub blocked_reason: Option<String>,
    /// Individual, independently-checkable outcomes (Phase 2.5B) -- a list,
    /// not a blob of text.
    pub success_criteria: Vec<String>,
    /// Why these specific success criteria were chosen, traced back to the
    /// `PlanningSummary` that generated them -- `None` for a task a human
    /// created directly, or one whose criteria came back empty.
    pub success_criteria_reason: Option<String>,
    pub target_score: f32,
    /// Why this specific quality bar was chosen (Phase 2.5C), traced back
    /// to the `PlanningSummary` that generated it -- `None` for a task a
    /// human created directly.
    pub target_score_reason: Option<String>,
    /// Tasks this one depends on -- it cannot become executable until every
    /// one of these is `done`.
    pub dependencies: Vec<TaskRefDto>,
    /// Tasks that depend on this one.
    pub dependents: Vec<TaskRefDto>,
    /// `RuntimeState::is_executable` computed fresh at read time: `Ready`,
    /// a leaf, its project `Active`, every dependency `Done`, and no
    /// unresolved `Decision` against it directly. Never persisted.
    pub is_executable: bool,
    /// The planning Run that generated this task, if any -- `None` for a
    /// task a human created directly via `create_task`.
    pub created_by_planning_run_id: Option<String>,
    /// Why this task was generated, traced back to the same `PlanningSummary`
    /// as `created_by_planning_run_id`.
    pub creation_reason: Option<String>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// One task plus its children, recursively -- the single planning
/// primitive's tree, replacing the old Initiative(epics(tasks))/Goal(tasks)
/// nesting with one shape at every level.
#[derive(Debug, Clone, Serialize)]
pub struct TaskNodeDto {
    pub task: TaskDto,
    pub children: Vec<TaskNodeDto>,
}

#[derive(Debug, Clone, Serialize)]
pub struct DecisionDto {
    pub id: String,
    pub project_id: String,
    pub task_id: Option<String>,
    pub question: String,
    pub context: String,
    pub answer: Option<String>,
    pub status: DecisionStatus,
    /// Why this decision was escalated, traced back to the `PlanningSummary`
    /// that raised it -- `None` if it wasn't planner-raised.
    pub creation_reason: Option<String>,
    pub created_at: DateTime<Utc>,
}

/// One planning run (a specialized `Run`, not a new domain entity), joined
/// to its `PlanningSummary` artifact -- the audit trail entry for one
/// `plan_task` call. `sequence` is this run's 1-based position among its
/// project's planning runs, oldest first ("Planning Run #N").
#[derive(Debug, Clone, Serialize)]
pub struct PlanningRunDto {
    pub run_id: String,
    pub sequence: usize,
    pub planned_task_id: String,
    pub planned_task_title: String,
    pub task_count: usize,
    pub average_task_length: f64,
    pub leaf_task_count: usize,
    pub dependency_count: usize,
    pub decision_count: usize,
    pub average_dependency_depth: f64,
    pub planning_duration_ms: i64,
    /// Phase 2.5B coverage metrics, observability-only.
    pub tasks_with_criteria: usize,
    pub average_criteria_per_task: f64,
    pub criteria_coverage_percent: f64,
    /// Phase 2.5C quality metrics, observability-only.
    pub average_target_score: f32,
    pub highest_target_score: f32,
    pub lowest_target_score: f32,
    pub score_distribution: Vec<ScoreBandCount>,
    pub summary: String,
    pub started_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ProjectSummaryDto {
    pub id: String,
    pub name: String,
    pub slug: String,
    pub status: ProjectStatus,
    pub task_counts: TaskCountsDto,
    pub open_inbox_count: usize,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ProjectDetailDto {
    pub id: String,
    pub name: String,
    pub slug: String,
    pub repository_path: String,
    pub default_branch: String,
    pub status: ProjectStatus,
    pub tasks: Vec<TaskNodeDto>,
    pub decisions: Vec<DecisionDto>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct AgentDto {
    pub id: String,
    pub project_id: String,
    pub template_id: String,
    pub role: AgentRole,
    pub role_description: String,
    pub parent_agent_id: Option<String>,
    pub name: String,
    pub status: AgentStatus,
    pub current_task_id: Option<String>,
    pub current_task_title: Option<String>,
    pub last_heartbeat: Option<DateTime<Utc>>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentHealthStatus {
    Healthy,
    Slow,
    Stalled,
}

/// One in-scope agent's live health, computed fresh from `now() -
/// last_heartbeat` every time this is derived -- never persisted (see
/// `Runtime::sync_agent_health_events`, which only ever emits audit-trail
/// markers, not the source of truth this reads).
#[derive(Debug, Clone, Serialize)]
pub struct AgentHealthDto {
    pub agent_id: String,
    pub agent_name: String,
    pub role: AgentRole,
    pub role_description: String,
    pub project_id: String,
    pub project_slug: String,
    pub current_task_id: Option<String>,
    pub current_task_title: Option<String>,
    pub last_heartbeat: Option<DateTime<Utc>>,
    pub health: AgentHealthStatus,
}

#[derive(Debug, Clone, Serialize)]
pub struct InboxItemDto {
    pub id: String,
    pub project_id: String,
    pub kind: InboxKind,
    pub status: InboxStatus,
    pub prompt: String,
    pub task_id: Option<String>,
    pub task_title: Option<String>,
    pub agent_id: Option<String>,
    pub agent_name: Option<String>,
    pub decision_id: Option<String>,
    pub decision_question: Option<String>,
    pub response: Option<String>,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize)]
pub struct TimelineEventDto {
    pub id: String,
    pub project_id: Option<String>,
    pub event_type: String,
    pub actor_id: Option<String>,
    pub payload: serde_json::Value,
    pub timestamp: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct RuntimeStatusDto {
    pub daemon_running: bool,
    pub pid: Option<u32>,
    pub stale_pidfile: bool,
    pub last_started: Option<DateTime<Utc>>,
    pub last_stopped: Option<DateTime<Utc>>,
    pub active_agent_count: usize,
    pub active_task_count: usize,
    pub open_inbox_count: usize,
    /// Non-Lead agents currently `Assigned`/`Executing` -- the scheduler-
    /// centric "Workers Active" stat, narrower than `active_agent_count`
    /// (which also counts a busy Lead).
    pub worker_count: usize,
    /// Tasks the scheduler would release right now across every `Active`
    /// project -- `RuntimeState::is_executable`, not merely `Ready`.
    pub queue_depth: usize,
    pub ready_task_count: usize,
    pub blocked_task_count: usize,
    /// Tasks whose most recent `Run` failed -- distinct from `Blocked`,
    /// which also covers tasks blocked by a raised question or a low
    /// review score, not just an outright run failure.
    pub failed_task_count: usize,
}

#[derive(Debug, Clone, Serialize)]
pub struct ActiveExecutionDto {
    pub task_id: String,
    pub task_title: String,
    pub project_id: String,
    pub project_slug: String,
    pub agent_id: String,
    pub agent_name: String,
    pub agent_role: AgentRole,
    pub started_at: DateTime<Utc>,
}

/// The Mission Control header's progress banner -- the most recently
/// created open root task (the old Initiative/Goal header, unified) and how
/// far its subtree has progressed.
#[derive(Debug, Clone, Serialize)]
pub struct GoalHeaderDto {
    pub project_id: String,
    pub project_name: String,
    pub active_task_id: Option<String>,
    pub active_label: Option<String>,
    pub percent_complete: f64,
}

/// A root task's descendant subtree, bucketed by status -- the old
/// Initiative/Epic-specific stage map, generalized to any task tree.
#[derive(Debug, Clone, Serialize, Default)]
pub struct WorkflowMapDto {
    pub draft_count: usize,
    pub ready_count: usize,
    pub executing_count: usize,
    pub review_count: usize,
    pub done_count: usize,
    pub blocked_count: usize,
}

/// A cross-project decision list row (CEO Mode's Decisions page), with
/// enough project/task context to act on it without drilling in.
#[derive(Debug, Clone, Serialize)]
pub struct DecisionSummaryDto {
    pub id: String,
    pub project_id: String,
    pub project_slug: String,
    pub task_id: Option<String>,
    pub task_title: Option<String>,
    pub question: String,
    pub context: String,
    pub status: DecisionStatus,
    /// Why this decision was escalated, traced back to the `PlanningSummary`
    /// that raised it -- `None` if it wasn't planner-raised.
    pub creation_reason: Option<String>,
    pub created_at: DateTime<Utc>,
}

/// One task's active on-disk workspace -- path, branch, and tmux session --
/// derived the same deterministic way `Runtime::cleanup_orphaned_workspace`
/// reconstructs them, with no new persisted state.
#[derive(Debug, Clone, Serialize)]
pub struct WorkspaceDto {
    pub task_id: String,
    pub task_title: String,
    pub project_id: String,
    pub project_slug: String,
    pub agent_id: String,
    pub agent_name: String,
    pub branch: String,
    pub git_worktree: String,
    pub tmux_session: String,
}

/// One recent `Artifact{kind: Diff}`, joined to its run's task/project
/// (Operator Mode's Diffs page).
#[derive(Debug, Clone, Serialize)]
pub struct DiffSummaryDto {
    pub id: String,
    pub task_id: String,
    pub task_title: String,
    pub project_id: String,
    pub project_slug: String,
    pub content: String,
    pub created_at: DateTime<Utc>,
}

/// 1:1 mirror of `beethoven_agents::RuntimeProfile` -- read-only, no new
/// persistence (Phase 1.9c scope decision: Runtime Settings is display-only).
#[derive(Debug, Clone, Serialize)]
pub struct RuntimeProfileDto {
    pub id: String,
    pub runtime_id: String,
    pub model: String,
}

/// An `AgentTemplate`, joined to the `RuntimeProfile` its
/// `runtime_profile_id` resolves to (Project Settings' read-only Runtime
/// subsection and Operator Mode's Agent Configuration panel).
#[derive(Debug, Clone, Serialize)]
pub struct AgentTemplateDto {
    pub id: String,
    pub role: AgentRole,
    pub label: String,
    pub prompt: String,
    pub permissions: Vec<String>,
    pub tools: Vec<String>,
    pub runtime_profile: Option<RuntimeProfileDto>,
    pub created_at: DateTime<Utc>,
}

/// One recent `Artifact{kind: Prompt}`, joined to its run's task/project
/// (Operator Mode's Prompts page, the spec's "PromptViewer").
#[derive(Debug, Clone, Serialize)]
pub struct PromptSummaryDto {
    pub id: String,
    pub run_id: String,
    pub task_id: String,
    pub task_title: String,
    pub project_id: String,
    pub project_slug: String,
    pub content: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct RunDto {
    pub id: String,
    pub agent_id: String,
    pub attempt: u32,
    pub provider: String,
    pub status: RunStatus,
    pub summary: Option<String>,
    pub prompt: Option<String>,
    pub response: Option<String>,
    pub started_at: DateTime<Utc>,
    pub completed_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Serialize)]
pub struct ArtifactDto {
    pub id: String,
    pub kind: ArtifactKind,
    pub content: String,
    pub created_at: DateTime<Utc>,
}

/// One `Review` of a task's run -- the evaluation that drives the
/// score-vs-`target_score` retry decision (Beethoven's loop, with no
/// separate Loop entity).
#[derive(Debug, Clone, Serialize)]
pub struct ReviewDto {
    pub id: String,
    pub status: ReviewStatus,
    pub score: f32,
    pub summary: String,
    pub recommendations: Vec<String>,
    pub created_at: DateTime<Utc>,
}

/// One task's full execution history: the task itself, every run, review,
/// and captured artifact, and the timeline slice that mentions it -- backs
/// the shared Task Detail panel's "View Diff"/"View Logs" actions.
#[derive(Debug, Clone, Serialize)]
pub struct TaskDetailDto {
    pub task: TaskDto,
    pub runs: Vec<RunDto>,
    pub reviews: Vec<ReviewDto>,
    pub artifacts: Vec<ArtifactDto>,
    pub timeline: Vec<TimelineEventDto>,
}
