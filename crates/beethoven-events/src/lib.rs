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

    AgentSpawned,
    AgentArchived,
    AgentDelegatedTask,

    TaskCreated,
    TaskApproved,
    TaskAssigned,
    TaskStarted,
    TaskBlocked,
    TaskCompleted,

    QuestionRaised,
    QuestionAnswered,

    DecisionResolved,

    WorkspaceCreated,
    WorkspaceDestroyed,

    WorktreeCreated,
    BranchCreated,

    TmuxSessionCreated,
    TmuxSessionDestroyed,

    PromptGenerated,

    RunStarted,
    RunCompleted,
    RunFailed,

    FilesModified,
    DiffCaptured,

    ReviewStarted,
    ReviewApproved,
    ReviewRejected,

    PRCreated,
    PRMerged,

    DecisionCreated,
    DecisionEscalated,

    TaskPlanningStarted,
    TaskPlanningCompleted,

    RuntimeStarted,
    RuntimeStopped,
    RuntimeRestarted,
    TaskRecovered,
    AgentHeartbeat,

    ProjectPaused,
    ProjectResumed,
    ProjectArchived,

    TaskRetried,
    TaskCancelled,
    TaskReassigned,

    ReviewChangesRequested,

    AgentPaused,
    AgentResumed,

    AgentMarkedSlow,
    AgentMarkedStalled,

    RepositoryPathChanged,
    DefaultBranchChanged,
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
            "AgentSpawned" => EventType::AgentSpawned,
            "AgentArchived" => EventType::AgentArchived,
            "AgentDelegatedTask" => EventType::AgentDelegatedTask,
            "TaskCreated" => EventType::TaskCreated,
            "TaskApproved" => EventType::TaskApproved,
            "TaskAssigned" => EventType::TaskAssigned,
            "TaskStarted" => EventType::TaskStarted,
            "TaskBlocked" => EventType::TaskBlocked,
            "TaskCompleted" => EventType::TaskCompleted,
            "QuestionRaised" => EventType::QuestionRaised,
            "QuestionAnswered" => EventType::QuestionAnswered,
            "DecisionResolved" => EventType::DecisionResolved,
            "WorkspaceCreated" => EventType::WorkspaceCreated,
            "WorkspaceDestroyed" => EventType::WorkspaceDestroyed,
            "WorktreeCreated" => EventType::WorktreeCreated,
            "BranchCreated" => EventType::BranchCreated,
            "TmuxSessionCreated" => EventType::TmuxSessionCreated,
            "TmuxSessionDestroyed" => EventType::TmuxSessionDestroyed,
            "PromptGenerated" => EventType::PromptGenerated,
            "RunStarted" => EventType::RunStarted,
            "RunCompleted" => EventType::RunCompleted,
            "RunFailed" => EventType::RunFailed,
            "FilesModified" => EventType::FilesModified,
            "DiffCaptured" => EventType::DiffCaptured,
            "ReviewStarted" => EventType::ReviewStarted,
            "ReviewApproved" => EventType::ReviewApproved,
            "ReviewRejected" => EventType::ReviewRejected,
            "PRCreated" => EventType::PRCreated,
            "PRMerged" => EventType::PRMerged,
            "DecisionCreated" => EventType::DecisionCreated,
            "DecisionEscalated" => EventType::DecisionEscalated,
            "TaskPlanningStarted" => EventType::TaskPlanningStarted,
            "TaskPlanningCompleted" => EventType::TaskPlanningCompleted,
            "RuntimeStarted" => EventType::RuntimeStarted,
            "RuntimeStopped" => EventType::RuntimeStopped,
            "RuntimeRestarted" => EventType::RuntimeRestarted,
            "TaskRecovered" => EventType::TaskRecovered,
            "AgentHeartbeat" => EventType::AgentHeartbeat,
            "ProjectPaused" => EventType::ProjectPaused,
            "ProjectResumed" => EventType::ProjectResumed,
            "ProjectArchived" => EventType::ProjectArchived,
            "TaskRetried" => EventType::TaskRetried,
            "TaskCancelled" => EventType::TaskCancelled,
            "TaskReassigned" => EventType::TaskReassigned,
            "ReviewChangesRequested" => EventType::ReviewChangesRequested,
            "AgentPaused" => EventType::AgentPaused,
            "AgentResumed" => EventType::AgentResumed,
            "AgentMarkedSlow" => EventType::AgentMarkedSlow,
            "AgentMarkedStalled" => EventType::AgentMarkedStalled,
            "RepositoryPathChanged" => EventType::RepositoryPathChanged,
            "DefaultBranchChanged" => EventType::DefaultBranchChanged,
            other => return Err(UnknownEventType(other.to_string())),
        })
    }
}

impl EventType {
    pub fn as_str(&self) -> &'static str {
        match self {
            EventType::ProjectCreated => "ProjectCreated",
            EventType::AgentTemplateCreated => "AgentTemplateCreated",
            EventType::AgentSpawned => "AgentSpawned",
            EventType::AgentArchived => "AgentArchived",
            EventType::AgentDelegatedTask => "AgentDelegatedTask",
            EventType::TaskCreated => "TaskCreated",
            EventType::TaskApproved => "TaskApproved",
            EventType::TaskAssigned => "TaskAssigned",
            EventType::TaskStarted => "TaskStarted",
            EventType::TaskBlocked => "TaskBlocked",
            EventType::TaskCompleted => "TaskCompleted",
            EventType::QuestionRaised => "QuestionRaised",
            EventType::QuestionAnswered => "QuestionAnswered",
            EventType::DecisionResolved => "DecisionResolved",
            EventType::WorkspaceCreated => "WorkspaceCreated",
            EventType::WorkspaceDestroyed => "WorkspaceDestroyed",
            EventType::WorktreeCreated => "WorktreeCreated",
            EventType::BranchCreated => "BranchCreated",
            EventType::TmuxSessionCreated => "TmuxSessionCreated",
            EventType::TmuxSessionDestroyed => "TmuxSessionDestroyed",
            EventType::PromptGenerated => "PromptGenerated",
            EventType::RunStarted => "RunStarted",
            EventType::RunCompleted => "RunCompleted",
            EventType::RunFailed => "RunFailed",
            EventType::FilesModified => "FilesModified",
            EventType::DiffCaptured => "DiffCaptured",
            EventType::ReviewStarted => "ReviewStarted",
            EventType::ReviewApproved => "ReviewApproved",
            EventType::ReviewRejected => "ReviewRejected",
            EventType::PRCreated => "PRCreated",
            EventType::PRMerged => "PRMerged",
            EventType::DecisionCreated => "DecisionCreated",
            EventType::DecisionEscalated => "DecisionEscalated",
            EventType::TaskPlanningStarted => "TaskPlanningStarted",
            EventType::TaskPlanningCompleted => "TaskPlanningCompleted",
            EventType::RuntimeStarted => "RuntimeStarted",
            EventType::RuntimeStopped => "RuntimeStopped",
            EventType::RuntimeRestarted => "RuntimeRestarted",
            EventType::TaskRecovered => "TaskRecovered",
            EventType::AgentHeartbeat => "AgentHeartbeat",
            EventType::ProjectPaused => "ProjectPaused",
            EventType::ProjectResumed => "ProjectResumed",
            EventType::ProjectArchived => "ProjectArchived",
            EventType::TaskRetried => "TaskRetried",
            EventType::TaskCancelled => "TaskCancelled",
            EventType::TaskReassigned => "TaskReassigned",
            EventType::ReviewChangesRequested => "ReviewChangesRequested",
            EventType::AgentPaused => "AgentPaused",
            EventType::AgentResumed => "AgentResumed",
            EventType::AgentMarkedSlow => "AgentMarkedSlow",
            EventType::AgentMarkedStalled => "AgentMarkedStalled",
            EventType::RepositoryPathChanged => "RepositoryPathChanged",
            EventType::DefaultBranchChanged => "DefaultBranchChanged",
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

    const ALL_EVENT_TYPES: [EventType; 53] = [
        EventType::ProjectCreated,
        EventType::AgentTemplateCreated,
        EventType::AgentSpawned,
        EventType::AgentArchived,
        EventType::AgentDelegatedTask,
        EventType::TaskCreated,
        EventType::TaskApproved,
        EventType::TaskAssigned,
        EventType::TaskStarted,
        EventType::TaskBlocked,
        EventType::TaskCompleted,
        EventType::QuestionRaised,
        EventType::QuestionAnswered,
        EventType::DecisionResolved,
        EventType::WorkspaceCreated,
        EventType::WorkspaceDestroyed,
        EventType::WorktreeCreated,
        EventType::BranchCreated,
        EventType::TmuxSessionCreated,
        EventType::TmuxSessionDestroyed,
        EventType::PromptGenerated,
        EventType::RunStarted,
        EventType::RunCompleted,
        EventType::RunFailed,
        EventType::FilesModified,
        EventType::DiffCaptured,
        EventType::ReviewStarted,
        EventType::ReviewApproved,
        EventType::ReviewRejected,
        EventType::PRCreated,
        EventType::PRMerged,
        EventType::DecisionCreated,
        EventType::DecisionEscalated,
        EventType::TaskPlanningStarted,
        EventType::TaskPlanningCompleted,
        EventType::RuntimeStarted,
        EventType::RuntimeStopped,
        EventType::RuntimeRestarted,
        EventType::TaskRecovered,
        EventType::AgentHeartbeat,
        EventType::ProjectPaused,
        EventType::ProjectResumed,
        EventType::ProjectArchived,
        EventType::TaskRetried,
        EventType::TaskCancelled,
        EventType::TaskReassigned,
        EventType::ReviewChangesRequested,
        EventType::AgentPaused,
        EventType::AgentResumed,
        EventType::AgentMarkedSlow,
        EventType::AgentMarkedStalled,
        EventType::RepositoryPathChanged,
        EventType::DefaultBranchChanged,
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
        let e = Event::for_project("p1", EventType::TaskCreated, "human", serde_json::json!({}));
        assert_eq!(e.scope.project_id(), Some("p1"));
    }
}
