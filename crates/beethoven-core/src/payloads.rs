use crate::models::{deserialize_success_criteria, TaskStatus};
use beethoven_agents::AgentRole;
use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize)]
pub struct ProjectCreatedPayload {
    pub id: String,
    pub name: String,
    pub slug: String,
    pub repository_path: String,
    pub default_branch: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AgentTemplateCreatedPayload {
    pub id: String,
    pub role: AgentRole,
    pub label: String,
    pub prompt: String,
    pub permissions: Vec<String>,
    pub tools: Vec<String>,
    #[serde(default = "default_runtime_profile_id")]
    pub runtime_profile_id: String,
}

/// Replay default for event logs recorded before runtime profiles existed.
fn default_runtime_profile_id() -> String {
    "claude-default".to_string()
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AgentSpawnedPayload {
    pub id: String,
    pub template_id: String,
    pub role: AgentRole,
    pub role_description: String,
    pub parent_agent_id: Option<String>,
    pub name: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AgentArchivedPayload {
    pub id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskCreatedPayload {
    pub id: String,
    pub parent_task_id: Option<String>,
    pub title: String,
    pub description: String,
    pub status: TaskStatus,
    #[serde(deserialize_with = "deserialize_success_criteria")]
    pub success_criteria: Vec<String>,
    pub target_score: f32,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskApprovedPayload {
    pub task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskAssignedPayload {
    pub task_id: String,
    pub agent_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskStartedPayload {
    pub task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskBlockedPayload {
    pub task_id: String,
    pub reason: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskRecoveredPayload {
    pub task_id: String,
    pub agent_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskInterruptedPayload {
    pub task_id: String,
    pub agent_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskCompletedPayload {
    pub task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct QuestionRaisedPayload {
    pub inbox_id: String,
    pub task_id: String,
    pub agent_id: String,
    pub prompt: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct QuestionAnsweredPayload {
    pub inbox_id: String,
    pub response: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DecisionResolvedPayload {
    pub inbox_id: String,
    pub response: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct PrCreatedPayload {
    pub task_id: String,
    pub branch: String,
    pub url: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct BranchCreatedPayload {
    pub task_id: String,
    pub branch: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct PromptGeneratedPayload {
    pub run_id: String,
    pub task_id: String,
    pub prompt: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RunStartedPayload {
    pub id: String,
    pub agent_id: String,
    pub project_id: String,
    pub task_id: String,
    pub attempt: u32,
    pub provider: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RunCompletedPayload {
    pub id: String,
    pub summary: String,
    pub response: String,
    pub cost_usd: Option<f64>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RunFailedPayload {
    pub id: String,
    pub error: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct FilesModifiedPayload {
    pub run_id: String,
    pub modified: Vec<String>,
    pub added: Vec<String>,
    pub deleted: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DiffCapturedPayload {
    pub run_id: String,
    pub diff: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ReviewStartedPayload {
    pub review_id: String,
    pub task_id: String,
    pub reviewer_agent_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ReviewApprovedPayload {
    pub review_id: String,
    pub task_id: String,
    pub score: f32,
    pub summary: String,
    pub recommendations: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ReviewRejectedPayload {
    pub review_id: String,
    pub task_id: String,
    pub score: f32,
    pub summary: String,
    pub recommendations: Vec<String>,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DecisionCreatedPayload {
    pub id: String,
    pub task_id: Option<String>,
    pub question: String,
    pub context: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DecisionEscalatedPayload {
    pub decision_id: String,
    pub inbox_id: String,
    pub prompt: String,
}

/// Shared by `ProjectPaused`/`ProjectResumed`/`ProjectArchived` -- all three
/// just flip `Project.status`.
#[derive(Debug, Serialize, Deserialize)]
pub struct ProjectStatusChangedPayload {
    pub id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskRetriedPayload {
    pub task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskCancelledPayload {
    pub task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskReassignedPayload {
    pub task_id: String,
    pub old_agent_id: Option<String>,
    pub new_agent_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ReviewChangesRequestedPayload {
    pub task_id: String,
    pub reason: String,
}

/// Shared by `AgentPaused`/`AgentResumed` -- both just flip
/// `AgentInstance.status`.
#[derive(Debug, Serialize, Deserialize)]
pub struct AgentStatusChangedPayload {
    pub id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RepositoryPathChangedPayload {
    pub id: String,
    pub repository_path: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct DefaultBranchChangedPayload {
    pub id: String,
    pub default_branch: String,
}

/// Shared by `TaskDependencyCreated`/`TaskDependencyRemoved` -- both just
/// name one edge of the dependency graph.
#[derive(Debug, Serialize, Deserialize)]
pub struct TaskDependencyPayload {
    pub task_id: String,
    pub depends_on_task_id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct PlanningSummaryGeneratedPayload {
    pub run_id: String,
    pub content: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ProjectChatTurnRecordedPayload {
    pub id: String,
    pub question: String,
    pub answer: String,
    pub focus_task_id: Option<String>,
    pub suggested_actions: Vec<crate::project_chat::SuggestedAction>,
}
