use beethoven_agents::AgentKind;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ProjectStatus {
    Active,
    Archived,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Project {
    pub id: String,
    pub name: String,
    pub slug: String,
    pub repository_path: PathBuf,
    pub default_branch: String,
    pub status: ProjectStatus,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Global, metadata-only definition of an agent role. Never executes
/// directly -- projects instantiate `AgentInstance`s from these.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentTemplate {
    pub id: String,
    pub kind: AgentKind,
    pub role: String,
    pub prompt: String,
    pub permissions: Vec<String>,
    pub tools: Vec<String>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum GoalStatus {
    Open,
    Planned,
    Completed,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Goal {
    pub id: String,
    pub project_id: String,
    pub title: String,
    pub description: String,
    pub status: GoalStatus,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TaskStatus {
    Draft,
    Ready,
    Assigned,
    InProgress,
    Review,
    Approved,
    Done,
    Blocked,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Task {
    pub id: String,
    pub project_id: String,
    pub goal_id: Option<String>,
    pub epic_id: Option<String>,
    pub parent_task_id: Option<String>,
    pub title: String,
    pub description: String,
    pub status: TaskStatus,
    pub assigned_agent_id: Option<String>,
    pub branch: Option<String>,
    pub pr_url: Option<String>,
    pub attempt: u32,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentStatus {
    Created,
    Idle,
    Assigned,
    Planning,
    Executing,
    Review,
    Completed,
    Blocked,
    Waiting,
    Escalated,
}

/// A project-scoped, running agent. Owns memory/task-queue/workspace
/// isolation; instantiated from an `AgentTemplate` when its project is created.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentInstance {
    pub id: String,
    pub project_id: String,
    pub template_id: String,
    pub kind: AgentKind,
    pub name: String,
    pub status: AgentStatus,
    pub current_task_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InboxKind {
    Decision,
    Approval,
    Question,
    Alert,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InboxStatus {
    Open,
    Resolved,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct InboxItem {
    pub id: String,
    pub project_id: String,
    pub kind: InboxKind,
    pub task_id: Option<String>,
    pub agent_id: Option<String>,
    pub decision_id: Option<String>,
    pub prompt: String,
    pub status: InboxStatus,
    pub response: Option<String>,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ExecutionRunStatus {
    Running,
    Completed,
    Failed,
}

/// One invocation of a `ModelRuntime` against a task's workspace.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ExecutionRun {
    pub id: String,
    pub task_id: String,
    pub agent_id: String,
    pub status: ExecutionRunStatus,
    pub summary: Option<String>,
    pub started_at: DateTime<Utc>,
    pub completed_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ArtifactKind {
    Diff,
    FilesModified,
}

/// Output captured from a task's workspace -- a git diff or a files-changed
/// summary -- recorded for the timeline since the workspace itself is
/// destroyed at the end of every attempt.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Artifact {
    pub id: String,
    pub task_id: String,
    pub kind: ArtifactKind,
    pub content: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReviewStatus {
    Pending,
    Approved,
    NeedsChanges,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Review {
    pub id: String,
    pub task_id: String,
    pub reviewer_agent_id: String,
    pub status: ReviewStatus,
    pub notes: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InitiativeStatus {
    Discovery,
    Planned,
    Completed,
}

/// Top-level objective within a project -- the PM's unit of project
/// management, one layer above the deterministic Goal/Task model.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Initiative {
    pub id: String,
    pub project_id: String,
    pub title: String,
    pub description: String,
    pub status: InitiativeStatus,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// A business or technical requirement gathered during an initiative's
/// discovery phase, before planning turns it into epics/tasks or a decision.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Requirement {
    pub id: String,
    pub project_id: String,
    pub initiative_id: String,
    pub description: String,
    pub created_at: DateTime<Utc>,
}

/// The generated execution strategy produced by planning an initiative.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Plan {
    pub id: String,
    pub project_id: String,
    pub initiative_id: String,
    pub summary: String,
    pub created_at: DateTime<Utc>,
}

/// A large work stream under an initiative's plan; each epic deterministically
/// produces one task today (see `beethoven_agents::InitiativePlanningService`).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Epic {
    pub id: String,
    pub project_id: String,
    pub initiative_id: String,
    pub title: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DecisionStatus {
    Open,
    Resolved,
}

/// Tracks an architectural or product decision raised during planning. The
/// PM owns escalation: every open decision is mirrored as an `InboxItem` so a
/// human resolves it, per the Worker -> PM -> Decision -> Inbox -> Human flow.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Decision {
    pub id: String,
    pub project_id: String,
    pub initiative_id: String,
    pub title: String,
    pub context: String,
    pub status: DecisionStatus,
    pub created_at: DateTime<Utc>,
}
