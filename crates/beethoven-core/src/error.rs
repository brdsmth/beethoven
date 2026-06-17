use beethoven_events::EventStoreError;
use beethoven_workspaces::WorkspacesError;

#[derive(Debug, thiserror::Error)]
pub enum CoreError {
    #[error("event store error: {0}")]
    EventStore(#[from] EventStoreError),
    #[error("workspace error: {0}")]
    Workspace(#[from] WorkspacesError),
    #[error("goal not found: {0}")]
    GoalNotFound(String),
    #[error("task not found: {0}")]
    TaskNotFound(String),
    #[error("inbox item not found: {0}")]
    InboxItemNotFound(String),
    #[error("inbox item already resolved: {0}")]
    InboxItemAlreadyResolved(String),
}
