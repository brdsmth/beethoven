use async_trait::async_trait;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub enum EventType {
    ProjectCreated,

    AgentTemplateCreated,

    GoalCreated,
    GoalUpdated,

    AgentCreated,
    AgentStarted,
    AgentStopped,

    TaskCreated,
    TaskAssigned,
    TaskStarted,
    TaskBlocked,
    TaskCompleted,

    QuestionRaised,
    QuestionAnswered,

    DecisionRequested,
    DecisionResolved,

    WorkspaceCreated,
    WorkspaceDestroyed,

    WorktreeCreated,
    BranchCreated,

    TmuxSessionCreated,
    TmuxSessionDestroyed,

    PromptGenerated,

    ModelRunStarted,
    ModelRunCompleted,
    ModelRunFailed,

    FilesModified,
    DiffCaptured,

    ReviewStarted,
    ReviewApproved,
    ReviewRejected,

    PRCreated,
    PRMerged,

    InitiativeCreated,
    InitiativePlanned,
    InitiativeCompleted,

    RequirementCreated,
    RequirementUpdated,

    PlanCreated,
    EpicCreated,

    DecisionCreated,
    DecisionEscalated,

    PMPlanningStarted,
    PMPlanningCompleted,
    PMReviewStarted,
    PMReviewCompleted,

    RuntimeStarted,
    RuntimeStopped,
    TaskRecovered,
    AgentHeartbeat,
}

#[derive(Debug, thiserror::Error)]
#[error("unknown event type: {0}")]
pub struct UnknownEventType(String);

impl std::str::FromStr for EventType {
    type Err = UnknownEventType;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(match s {
            "ProjectCreated" => EventType::ProjectCreated,
            "AgentTemplateCreated" => EventType::AgentTemplateCreated,
            "GoalCreated" => EventType::GoalCreated,
            "GoalUpdated" => EventType::GoalUpdated,
            "AgentCreated" => EventType::AgentCreated,
            "AgentStarted" => EventType::AgentStarted,
            "AgentStopped" => EventType::AgentStopped,
            "TaskCreated" => EventType::TaskCreated,
            "TaskAssigned" => EventType::TaskAssigned,
            "TaskStarted" => EventType::TaskStarted,
            "TaskBlocked" => EventType::TaskBlocked,
            "TaskCompleted" => EventType::TaskCompleted,
            "QuestionRaised" => EventType::QuestionRaised,
            "QuestionAnswered" => EventType::QuestionAnswered,
            "DecisionRequested" => EventType::DecisionRequested,
            "DecisionResolved" => EventType::DecisionResolved,
            "WorkspaceCreated" => EventType::WorkspaceCreated,
            "WorkspaceDestroyed" => EventType::WorkspaceDestroyed,
            "WorktreeCreated" => EventType::WorktreeCreated,
            "BranchCreated" => EventType::BranchCreated,
            "TmuxSessionCreated" => EventType::TmuxSessionCreated,
            "TmuxSessionDestroyed" => EventType::TmuxSessionDestroyed,
            "PromptGenerated" => EventType::PromptGenerated,
            "ModelRunStarted" => EventType::ModelRunStarted,
            "ModelRunCompleted" => EventType::ModelRunCompleted,
            "ModelRunFailed" => EventType::ModelRunFailed,
            "FilesModified" => EventType::FilesModified,
            "DiffCaptured" => EventType::DiffCaptured,
            "ReviewStarted" => EventType::ReviewStarted,
            "ReviewApproved" => EventType::ReviewApproved,
            "ReviewRejected" => EventType::ReviewRejected,
            "PRCreated" => EventType::PRCreated,
            "PRMerged" => EventType::PRMerged,
            "InitiativeCreated" => EventType::InitiativeCreated,
            "InitiativePlanned" => EventType::InitiativePlanned,
            "InitiativeCompleted" => EventType::InitiativeCompleted,
            "RequirementCreated" => EventType::RequirementCreated,
            "RequirementUpdated" => EventType::RequirementUpdated,
            "PlanCreated" => EventType::PlanCreated,
            "EpicCreated" => EventType::EpicCreated,
            "DecisionCreated" => EventType::DecisionCreated,
            "DecisionEscalated" => EventType::DecisionEscalated,
            "PMPlanningStarted" => EventType::PMPlanningStarted,
            "PMPlanningCompleted" => EventType::PMPlanningCompleted,
            "PMReviewStarted" => EventType::PMReviewStarted,
            "PMReviewCompleted" => EventType::PMReviewCompleted,
            "RuntimeStarted" => EventType::RuntimeStarted,
            "RuntimeStopped" => EventType::RuntimeStopped,
            "TaskRecovered" => EventType::TaskRecovered,
            "AgentHeartbeat" => EventType::AgentHeartbeat,
            other => return Err(UnknownEventType(other.to_string())),
        })
    }
}

impl EventType {
    pub fn as_str(&self) -> &'static str {
        match self {
            EventType::ProjectCreated => "ProjectCreated",
            EventType::AgentTemplateCreated => "AgentTemplateCreated",
            EventType::GoalCreated => "GoalCreated",
            EventType::GoalUpdated => "GoalUpdated",
            EventType::AgentCreated => "AgentCreated",
            EventType::AgentStarted => "AgentStarted",
            EventType::AgentStopped => "AgentStopped",
            EventType::TaskCreated => "TaskCreated",
            EventType::TaskAssigned => "TaskAssigned",
            EventType::TaskStarted => "TaskStarted",
            EventType::TaskBlocked => "TaskBlocked",
            EventType::TaskCompleted => "TaskCompleted",
            EventType::QuestionRaised => "QuestionRaised",
            EventType::QuestionAnswered => "QuestionAnswered",
            EventType::DecisionRequested => "DecisionRequested",
            EventType::DecisionResolved => "DecisionResolved",
            EventType::WorkspaceCreated => "WorkspaceCreated",
            EventType::WorkspaceDestroyed => "WorkspaceDestroyed",
            EventType::WorktreeCreated => "WorktreeCreated",
            EventType::BranchCreated => "BranchCreated",
            EventType::TmuxSessionCreated => "TmuxSessionCreated",
            EventType::TmuxSessionDestroyed => "TmuxSessionDestroyed",
            EventType::PromptGenerated => "PromptGenerated",
            EventType::ModelRunStarted => "ModelRunStarted",
            EventType::ModelRunCompleted => "ModelRunCompleted",
            EventType::ModelRunFailed => "ModelRunFailed",
            EventType::FilesModified => "FilesModified",
            EventType::DiffCaptured => "DiffCaptured",
            EventType::ReviewStarted => "ReviewStarted",
            EventType::ReviewApproved => "ReviewApproved",
            EventType::ReviewRejected => "ReviewRejected",
            EventType::PRCreated => "PRCreated",
            EventType::PRMerged => "PRMerged",
            EventType::InitiativeCreated => "InitiativeCreated",
            EventType::InitiativePlanned => "InitiativePlanned",
            EventType::InitiativeCompleted => "InitiativeCompleted",
            EventType::RequirementCreated => "RequirementCreated",
            EventType::RequirementUpdated => "RequirementUpdated",
            EventType::PlanCreated => "PlanCreated",
            EventType::EpicCreated => "EpicCreated",
            EventType::DecisionCreated => "DecisionCreated",
            EventType::DecisionEscalated => "DecisionEscalated",
            EventType::PMPlanningStarted => "PMPlanningStarted",
            EventType::PMPlanningCompleted => "PMPlanningCompleted",
            EventType::PMReviewStarted => "PMReviewStarted",
            EventType::PMReviewCompleted => "PMReviewCompleted",
            EventType::RuntimeStarted => "RuntimeStarted",
            EventType::RuntimeStopped => "RuntimeStopped",
            EventType::TaskRecovered => "TaskRecovered",
            EventType::AgentHeartbeat => "AgentHeartbeat",
        }
    }
}

/// Every event belongs to Beethoven itself (installation-level lifecycle,
/// e.g. project/template creation) or to exactly one project.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub enum EventScope {
    Global,
    Project(String),
}

impl EventScope {
    pub fn project_id(&self) -> Option<&str> {
        match self {
            EventScope::Global => None,
            EventScope::Project(id) => Some(id),
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Event {
    pub id: String,
    pub scope: EventScope,
    pub event_type: EventType,
    pub actor_id: Option<String>,
    pub payload: Value,
    pub timestamp: DateTime<Utc>,
}

impl Event {
    pub fn new(
        scope: EventScope,
        event_type: EventType,
        actor_id: Option<impl Into<String>>,
        payload: Value,
    ) -> Self {
        Self {
            id: Uuid::new_v4().to_string(),
            scope,
            event_type,
            actor_id: actor_id.map(Into::into),
            payload,
            timestamp: Utc::now(),
        }
    }

    pub fn global(event_type: EventType, actor_id: impl Into<String>, payload: Value) -> Self {
        Self::new(EventScope::Global, event_type, Some(actor_id), payload)
    }

    pub fn for_project(
        project_id: impl Into<String>,
        event_type: EventType,
        actor_id: impl Into<String>,
        payload: Value,
    ) -> Self {
        Self::new(
            EventScope::Project(project_id.into()),
            event_type,
            Some(actor_id),
            payload,
        )
    }
}

#[derive(Debug, thiserror::Error)]
pub enum EventStoreError {
    #[error("event store backend error: {0}")]
    Backend(String),
}

#[async_trait]
pub trait EventStore: Send + Sync {
    async fn append(&self, event: Event) -> Result<(), EventStoreError>;
    async fn list_all(&self) -> Result<Vec<Event>, EventStoreError>;
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::str::FromStr;

    const ALL_EVENT_TYPES: [EventType; 50] = [
        EventType::ProjectCreated,
        EventType::AgentTemplateCreated,
        EventType::GoalCreated,
        EventType::GoalUpdated,
        EventType::AgentCreated,
        EventType::AgentStarted,
        EventType::AgentStopped,
        EventType::TaskCreated,
        EventType::TaskAssigned,
        EventType::TaskStarted,
        EventType::TaskBlocked,
        EventType::TaskCompleted,
        EventType::QuestionRaised,
        EventType::QuestionAnswered,
        EventType::DecisionRequested,
        EventType::DecisionResolved,
        EventType::WorkspaceCreated,
        EventType::WorkspaceDestroyed,
        EventType::WorktreeCreated,
        EventType::BranchCreated,
        EventType::TmuxSessionCreated,
        EventType::TmuxSessionDestroyed,
        EventType::PromptGenerated,
        EventType::ModelRunStarted,
        EventType::ModelRunCompleted,
        EventType::ModelRunFailed,
        EventType::FilesModified,
        EventType::DiffCaptured,
        EventType::ReviewStarted,
        EventType::ReviewApproved,
        EventType::ReviewRejected,
        EventType::PRCreated,
        EventType::PRMerged,
        EventType::InitiativeCreated,
        EventType::InitiativePlanned,
        EventType::InitiativeCompleted,
        EventType::RequirementCreated,
        EventType::RequirementUpdated,
        EventType::PlanCreated,
        EventType::EpicCreated,
        EventType::DecisionCreated,
        EventType::DecisionEscalated,
        EventType::PMPlanningStarted,
        EventType::PMPlanningCompleted,
        EventType::PMReviewStarted,
        EventType::PMReviewCompleted,
        EventType::RuntimeStarted,
        EventType::RuntimeStopped,
        EventType::TaskRecovered,
        EventType::AgentHeartbeat,
    ];

    #[test]
    fn event_type_round_trips_through_as_str_and_from_str() {
        for event_type in ALL_EVENT_TYPES {
            assert_eq!(EventType::from_str(event_type.as_str()).unwrap(), event_type);
        }
    }

    #[test]
    fn unknown_event_type_string_is_rejected() {
        assert!(EventType::from_str("NotARealEvent").is_err());
    }

    #[test]
    fn new_events_get_unique_ids() {
        let a = Event::global(EventType::ProjectCreated, "human", serde_json::json!({}));
        let b = Event::global(EventType::ProjectCreated, "human", serde_json::json!({}));
        assert_ne!(a.id, b.id);
        assert_eq!(a.actor_id, Some("human".to_string()));
        assert_eq!(a.scope, EventScope::Global);
    }

    #[test]
    fn for_project_sets_project_scope() {
        let e = Event::for_project("p1", EventType::GoalCreated, "human", serde_json::json!({}));
        assert_eq!(e.scope.project_id(), Some("p1"));
    }
}
