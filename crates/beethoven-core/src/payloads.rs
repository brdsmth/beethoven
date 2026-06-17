use crate::models::GoalStatus;
use beethoven_agents::AgentKind;
use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize)]
pub struct GoalCreatedPayload {
    pub id: String,
    pub title: String,
    pub description: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct GoalUpdatedPayload {
    pub id: String,
    pub status: GoalStatus,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AgentCreatedPayload {
    pub id: String,
    pub kind: AgentKind,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AgentStartedPayload {
    pub id: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct TaskCreatedPayload {
    pub id: String,
    pub goal_id: String,
    pub parent_task_id: Option<String>,
    pub title: String,
    pub description: String,
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
pub struct DecisionRequestedPayload {
    pub inbox_id: String,
    pub task_id: Option<String>,
    pub agent_id: Option<String>,
    pub prompt: String,
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
