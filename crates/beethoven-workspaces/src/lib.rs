use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};

#[derive(Debug, thiserror::Error)]
pub enum WorkspacesError {
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("serialization error: {0}")]
    Serde(#[from] serde_json::Error),
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkspaceState {
    pub task_id: String,
    pub created_at: DateTime<Utc>,
    /// Placeholders for the real runtime layers (tmux session, git worktree,
    /// model runtime) until those integrations land.
    pub tmux_session: String,
    pub git_worktree: String,
    pub model_runtime: String,
}

pub struct WorkspaceManager {
    base_dir: PathBuf,
}

impl WorkspaceManager {
    pub fn new(base_dir: impl Into<PathBuf>) -> Self {
        Self {
            base_dir: base_dir.into(),
        }
    }

    pub fn path_for(&self, task_id: &str) -> PathBuf {
        self.base_dir.join(task_id)
    }

    pub fn create(&self, task_id: &str) -> Result<WorkspaceState, WorkspacesError> {
        let root = self.path_for(task_id);
        for sub in ["repo", "memory", "logs", "artifacts", "prompts"] {
            std::fs::create_dir_all(root.join(sub))?;
        }

        let state = WorkspaceState {
            task_id: task_id.to_string(),
            created_at: Utc::now(),
            tmux_session: format!("beethoven-{task_id}"),
            git_worktree: root.join("repo").to_string_lossy().into_owned(),
            model_runtime: "stub".to_string(),
        };
        self.write_state(&root, &state)?;
        Ok(state)
    }

    pub fn destroy(&self, task_id: &str) -> Result<(), WorkspacesError> {
        let root = self.path_for(task_id);
        if root.exists() {
            std::fs::remove_dir_all(root)?;
        }
        Ok(())
    }

    fn write_state(&self, root: &Path, state: &WorkspaceState) -> Result<(), WorkspacesError> {
        let json = serde_json::to_string_pretty(state)?;
        std::fs::write(root.join("state.json"), json)?;
        Ok(())
    }
}
