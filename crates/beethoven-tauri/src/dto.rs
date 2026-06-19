use beethoven_agents::AgentKind;
use beethoven_core::{
    AgentStatus, ArtifactKind, DecisionStatus, GoalStatus, InboxKind, InboxStatus, InitiativeStatus, ModelRunStatus,
    ProjectStatus, TaskStatus,
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

#[derive(Debug, Clone, Serialize)]
pub struct TaskDto {
    pub id: String,
    pub project_id: String,
    pub goal_id: Option<String>,
    pub epic_id: Option<String>,
    pub title: String,
    pub description: String,
    pub status: TaskStatus,
    pub assigned_agent_id: Option<String>,
    pub assigned_agent_name: Option<String>,
    pub attempt: u32,
    pub branch: Option<String>,
    pub pr_url: Option<String>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct RequirementDto {
    pub id: String,
    pub description: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct EpicDto {
    pub id: String,
    pub title: String,
    pub tasks: Vec<TaskDto>,
}

#[derive(Debug, Clone, Serialize)]
pub struct DecisionDto {
    pub id: String,
    pub title: String,
    pub context: String,
    pub status: DecisionStatus,
}

#[derive(Debug, Clone, Serialize)]
pub struct InitiativeDto {
    pub id: String,
    pub title: String,
    pub description: String,
    pub status: InitiativeStatus,
    pub requirements: Vec<RequirementDto>,
    pub epics: Vec<EpicDto>,
    pub decisions: Vec<DecisionDto>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct GoalDto {
    pub id: String,
    pub title: String,
    pub description: String,
    pub status: GoalStatus,
    pub tasks: Vec<TaskDto>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
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
    pub status: ProjectStatus,
    pub initiatives: Vec<InitiativeDto>,
    pub goals: Vec<GoalDto>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Serialize)]
pub struct AgentDto {
    pub id: String,
    pub project_id: String,
    pub kind: AgentKind,
    pub name: String,
    pub status: AgentStatus,
    pub current_task_id: Option<String>,
    pub current_task_title: Option<String>,
    pub last_heartbeat: Option<DateTime<Utc>>,
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
    pub decision_title: Option<String>,
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
}

#[derive(Debug, Clone, Serialize)]
pub struct ActiveExecutionDto {
    pub task_id: String,
    pub task_title: String,
    pub project_id: String,
    pub project_slug: String,
    pub agent_id: String,
    pub agent_name: String,
    pub agent_kind: AgentKind,
    pub started_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum GoalHeaderSource {
    Initiative,
    Goal,
    None,
}

#[derive(Debug, Clone, Serialize)]
pub struct GoalHeaderDto {
    pub project_id: String,
    pub project_name: String,
    pub source: GoalHeaderSource,
    pub active_label: Option<String>,
    pub active_initiative_id: Option<String>,
    pub percent_complete: f64,
}

#[derive(Debug, Clone, Serialize, Default)]
pub struct WorkflowMapDto {
    pub initiative_count: usize,
    pub epic_count: usize,
    pub task_count: usize,
    pub execution_count: usize,
    pub review_count: usize,
    pub complete_count: usize,
}

/// A cross-project initiative list row (Operator/CEO Mode's Initiatives
/// page) -- counts rather than the full nested epic/task tree
/// `ProjectDetailDto`'s `InitiativeDto` carries, matching the existing
/// summary-vs-detail split `ProjectSummaryDto`/`ProjectDetailDto` already use.
#[derive(Debug, Clone, Serialize)]
pub struct InitiativeSummaryDto {
    pub id: String,
    pub project_id: String,
    pub project_slug: String,
    pub project_name: String,
    pub title: String,
    pub description: String,
    pub status: InitiativeStatus,
    pub requirement_count: usize,
    pub epic_count: usize,
    pub task_count: usize,
    pub open_decision_count: usize,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// A cross-project decision list row (CEO Mode's Decisions page), with
/// enough project/initiative context to act on it without drilling in.
#[derive(Debug, Clone, Serialize)]
pub struct DecisionSummaryDto {
    pub id: String,
    pub project_id: String,
    pub project_slug: String,
    pub initiative_id: String,
    pub initiative_title: String,
    pub title: String,
    pub context: String,
    pub status: DecisionStatus,
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

/// One recent `Artifact{kind: Diff}`, joined to its task/project (Operator
/// Mode's Diffs page).
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

#[derive(Debug, Clone, Serialize)]
pub struct ModelRunDto {
    pub id: String,
    pub agent_id: String,
    pub provider: String,
    pub status: ModelRunStatus,
    pub summary: Option<String>,
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

/// One task's full execution history: the task itself, every model run and
/// captured artifact, and the timeline slice that mentions it -- backs the
/// shared Task Detail panel's "View Diff"/"View Logs" actions.
#[derive(Debug, Clone, Serialize)]
pub struct TaskDetailDto {
    pub task: TaskDto,
    pub model_runs: Vec<ModelRunDto>,
    pub artifacts: Vec<ArtifactDto>,
    pub timeline: Vec<TimelineEventDto>,
}
