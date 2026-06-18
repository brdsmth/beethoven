mod tmux;

pub use tmux::TmuxSession;

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::path::{Path, PathBuf};
use std::time::Duration;

#[derive(Debug, thiserror::Error)]
pub enum WorkspacesError {
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("serialization error: {0}")]
    Serde(#[from] serde_json::Error),
    #[error("git error: {0}")]
    Git(#[from] beethoven_git::GitError),
    #[error("tmux error: {0}")]
    Tmux(String),
    #[error("timed out waiting for tmux session {0}")]
    Timeout(String),
}

fn sanitize_for_tmux(input: &str) -> String {
    let mut out = String::new();
    let mut last_was_underscore = true;
    for ch in input.to_lowercase().chars() {
        if ch.is_ascii_alphanumeric() {
            out.push(ch);
            last_was_underscore = false;
        } else if !last_was_underscore {
            out.push('_');
            last_was_underscore = true;
        }
    }
    while out.ends_with('_') {
        out.pop();
    }
    out
}

/// `beethoven_<project>_<agent>_<task>`. Colons (per the original naming
/// scheme `beethoven:<project>:<agent>:<task>`) are silently mangled by tmux,
/// so this is the on-disk/real session name; render the colon form only for
/// human-facing display if needed.
pub fn tmux_session_name(project_slug: &str, agent_label: &str, task_id: &str) -> String {
    let short_task = &task_id[..task_id.len().min(8)];
    format!(
        "beethoven_{}_{}_{}",
        sanitize_for_tmux(project_slug),
        sanitize_for_tmux(agent_label),
        sanitize_for_tmux(short_task)
    )
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct WorkspaceState {
    pub task_id: String,
    pub created_at: DateTime<Utc>,
    pub repository_path: String,
    pub branch: String,
    pub tmux_session: String,
    pub git_worktree: String,
    /// The worktree's `.git` link as `git worktree add` originally wrote it,
    /// captured so it can be restored if a task's model run replaces it
    /// (e.g. by running its own `git init`).
    pub git_link: String,
}

/// Everything needed to stand up a real workspace: a git worktree on a fresh
/// branch, plus a tmux session rooted at it.
pub struct WorkspaceRequest<'a> {
    pub project_slug: &'a str,
    pub agent_label: &'a str,
    pub task_id: &'a str,
    pub repository_path: &'a str,
    pub default_branch: &'a str,
    pub branch: &'a str,
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

    /// Creates the on-disk layout, a real `git worktree` checked out on
    /// `request.branch`, and a real tmux session rooted at that worktree.
    pub fn create(&self, request: &WorkspaceRequest) -> Result<WorkspaceState, WorkspacesError> {
        let root = self.path_for(request.task_id);
        for sub in ["memory", "logs", "artifacts", "prompts"] {
            std::fs::create_dir_all(root.join(sub))?;
        }

        let worktree_path = root.join("repo");
        beethoven_git::create_worktree(
            Path::new(request.repository_path),
            &worktree_path,
            request.branch,
            request.default_branch,
        )?;
        let git_link = beethoven_git::read_worktree_git_link(&worktree_path)?;

        let session_name = tmux_session_name(request.project_slug, request.agent_label, request.task_id);
        TmuxSession::create(&session_name, &worktree_path)?;

        let state = WorkspaceState {
            task_id: request.task_id.to_string(),
            created_at: Utc::now(),
            repository_path: request.repository_path.to_string(),
            branch: request.branch.to_string(),
            tmux_session: session_name,
            git_worktree: worktree_path.to_string_lossy().into_owned(),
            git_link,
        };
        self.write_state(&root, &state)?;
        Ok(state)
    }

    /// Kills the tmux session and removes the worktree + branch, then deletes
    /// the workspace directory. Safe to call even if creation never finished.
    pub fn destroy(&self, state: &WorkspaceState) -> Result<(), WorkspacesError> {
        TmuxSession { name: state.tmux_session.clone() }.kill()?;
        let _ = beethoven_git::remove_worktree(
            Path::new(&state.repository_path),
            Path::new(&state.git_worktree),
            &state.branch,
        );

        let root = self.path_for(&state.task_id);
        if root.exists() {
            std::fs::remove_dir_all(root)?;
        }
        Ok(())
    }

    /// Sends `command` into the workspace's tmux session and waits for it to
    /// finish (sentinel-based, see [`TmuxSession::run_and_wait`]).
    pub fn run_in_session(&self, state: &WorkspaceState, command: &str, timeout: Duration) -> Result<String, WorkspacesError> {
        TmuxSession { name: state.tmux_session.clone() }.run_and_wait(command, timeout)
    }

    /// Restores the worktree's `.git` link if the task's model run replaced
    /// or deleted it, so git status/diff always reflect this worktree's real
    /// history regardless of what ran inside it.
    pub fn restore_git_link(&self, state: &WorkspaceState) -> Result<(), WorkspacesError> {
        beethoven_git::restore_worktree_git_link(Path::new(&state.git_worktree), &state.git_link)?;
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

    fn run_git(dir: &Path, args: &[&str]) {
        let status = std::process::Command::new("git").arg("-C").arg(dir).args(args).status().unwrap();
        assert!(status.success(), "git {args:?} failed");
    }

    fn init_repo(path: &Path) {
        std::fs::create_dir_all(path).unwrap();
        run_git(path, &["init", "-q", "-b", "main"]);
        run_git(path, &["config", "user.email", "test@example.com"]);
        run_git(path, &["config", "user.name", "Test"]);
        std::fs::write(path.join("README.md"), "scratch repo\n").unwrap();
        run_git(path, &["add", "-A"]);
        run_git(path, &["commit", "-q", "-m", "init"]);
    }

    #[test]
    fn create_builds_a_real_worktree_branch_and_tmux_session() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let mgr = WorkspaceManager::new(dir.path().join("workspaces"));
        let state = mgr
            .create(&WorkspaceRequest {
                project_slug: "sample",
                agent_label: "backend",
                task_id: "task-1",
                repository_path: &repo.to_string_lossy(),
                default_branch: "main",
                branch: "task/demo",
            })
            .unwrap();

        let root = mgr.path_for("task-1");
        for sub in ["repo", "memory", "logs", "artifacts", "prompts"] {
            assert!(root.join(sub).is_dir(), "missing {sub} dir");
        }
        assert!(root.join("state.json").is_file());
        assert!(Path::new(&state.git_worktree).join("README.md").is_file());
        assert!(TmuxSession::list_active().unwrap().contains(&state.tmux_session));

        mgr.destroy(&state).unwrap();
    }

    #[test]
    fn destroy_kills_the_session_and_removes_the_worktree_and_directory() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let mgr = WorkspaceManager::new(dir.path().join("workspaces"));
        let state = mgr
            .create(&WorkspaceRequest {
                project_slug: "sample",
                agent_label: "backend",
                task_id: "task-2",
                repository_path: &repo.to_string_lossy(),
                default_branch: "main",
                branch: "task/demo2",
            })
            .unwrap();

        mgr.destroy(&state).unwrap();

        assert!(!mgr.path_for("task-2").exists());
        assert!(!Path::new(&state.git_worktree).exists());
        assert!(!TmuxSession::list_active().unwrap().contains(&state.tmux_session));
    }

    #[test]
    fn run_in_session_executes_a_real_command_in_the_worktree() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let mgr = WorkspaceManager::new(dir.path().join("workspaces"));
        let state = mgr
            .create(&WorkspaceRequest {
                project_slug: "sample",
                agent_label: "backend",
                task_id: "task-3",
                repository_path: &repo.to_string_lossy(),
                default_branch: "main",
                branch: "task/demo3",
            })
            .unwrap();

        mgr.run_in_session(&state, "echo hi-from-workspace > NOTES.md", Duration::from_secs(5)).unwrap();
        assert!(Path::new(&state.git_worktree).join("NOTES.md").is_file());

        mgr.destroy(&state).unwrap();
    }

    #[test]
    fn tmux_session_name_is_underscore_joined_and_sanitized() {
        assert_eq!(
            tmux_session_name("Sample!", "Backend Engineer", "0123456789abcdef"),
            "beethoven_sample_backend_engineer_01234567"
        );
    }
}
