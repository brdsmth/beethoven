use async_trait::async_trait;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use uuid::Uuid;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "PascalCase")]
pub enum EventType {
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

    PRCreated,
    PRMerged,
}

#[derive(Debug, thiserror::Error)]
#[error("unknown event type: {0}")]
pub struct UnknownEventType(String);

impl std::str::FromStr for EventType {
    type Err = UnknownEventType;

    fn from_str(s: &str) -> Result<Self, Self::Err> {
        Ok(match s {
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
            "PRCreated" => EventType::PRCreated,
            "PRMerged" => EventType::PRMerged,
            other => return Err(UnknownEventType(other.to_string())),
        })
    }
}

impl EventType {
    pub fn as_str(&self) -> &'static str {
        match self {
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
            EventType::PRCreated => "PRCreated",
            EventType::PRMerged => "PRMerged",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Event {
    pub id: String,
    pub event_type: EventType,
    pub actor_id: String,
    pub payload: Value,
    pub timestamp: DateTime<Utc>,
}

impl Event {
    pub fn new(event_type: EventType, actor_id: impl Into<String>, payload: Value) -> Self {
        Self {
            id: Uuid::new_v4().to_string(),
            event_type,
            actor_id: actor_id.into(),
            payload,
            timestamp: Utc::now(),
        }
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

    const ALL_EVENT_TYPES: [EventType; 18] = [
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
        EventType::PRCreated,
        EventType::PRMerged,
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
        let a = Event::new(EventType::GoalCreated, "human", serde_json::json!({}));
        let b = Event::new(EventType::GoalCreated, "human", serde_json::json!({}));
        assert_ne!(a.id, b.id);
        assert_eq!(a.actor_id, "human");
    }
}
