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
    pub repository_path: String,
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

    pub fn create(&self, task_id: &str, repository_path: &str) -> Result<WorkspaceState, WorkspacesError> {
        let root = self.path_for(task_id);
        for sub in ["repo", "memory", "logs", "artifacts", "prompts"] {
            std::fs::create_dir_all(root.join(sub))?;
        }

        let state = WorkspaceState {
            task_id: task_id.to_string(),
            created_at: Utc::now(),
            repository_path: repository_path.to_string(),
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn create_builds_the_expected_layout_and_state_file() {
        let dir = tempfile::tempdir().unwrap();
        let mgr = WorkspaceManager::new(dir.path());

        let state = mgr.create("task-1", "/repo/path").unwrap();

        let root = mgr.path_for("task-1");
        for sub in ["repo", "memory", "logs", "artifacts", "prompts"] {
            assert!(root.join(sub).is_dir(), "missing {sub} dir");
        }
        assert!(root.join("state.json").is_file());
        assert_eq!(state.task_id, "task-1");
    }

    #[test]
    fn destroy_removes_the_workspace_directory() {
        let dir = tempfile::tempdir().unwrap();
        let mgr = WorkspaceManager::new(dir.path());

        mgr.create("task-2", "/repo/path").unwrap();
        assert!(mgr.path_for("task-2").exists());

        mgr.destroy("task-2").unwrap();
        assert!(!mgr.path_for("task-2").exists());
    }

    #[test]
    fn destroy_on_a_workspace_that_was_never_created_is_a_no_op() {
        let dir = tempfile::tempdir().unwrap();
        let mgr = WorkspaceManager::new(dir.path());

        assert!(mgr.destroy("never-created").is_ok());
    }
}
