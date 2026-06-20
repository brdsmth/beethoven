use beethoven_events::EventStoreError;
use beethoven_workspaces::WorkspacesError;

#[derive(Debug, thiserror::Error)]
pub enum CoreError {
    #[error("event store error: {0}")]
    EventStore(#[from] EventStoreError),
    #[error("workspace error: {0}")]
    Workspace(#[from] WorkspacesError),
    #[error("git error: {0}")]
    Git(#[from] beethoven_git::GitError),
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("project not found: {0}")]
    ProjectNotFound(String),
    #[error("task not found: {0}")]
    TaskNotFound(String),
    #[error("inbox item not found: {0}")]
    InboxItemNotFound(String),
    #[error("inbox item already resolved: {0}")]
    InboxItemAlreadyResolved(String),
    #[error("decision not found or already resolved: {0}")]
    DecisionNotFound(String),
    #[error("agent not found: {0}")]
    AgentNotFound(String),
    #[error("invalid state: {0}")]
    InvalidState(String),
}
