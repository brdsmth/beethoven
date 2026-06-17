use beethoven_agents::AgentKind;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

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
    pub goal_id: String,
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

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Agent {
    pub id: String,
    pub kind: AgentKind,
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
    pub kind: InboxKind,
    pub task_id: Option<String>,
    pub agent_id: Option<String>,
    pub prompt: String,
    pub status: InboxStatus,
    pub response: Option<String>,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}
