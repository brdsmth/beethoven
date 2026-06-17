use beethoven_workspaces::{TmuxSession, WorkspaceState};
use std::path::Path;
use std::time::Duration;

#[derive(Debug, thiserror::Error)]
pub enum AgentsError {
    #[error("workspace error: {0}")]
    Workspace(#[from] beethoven_workspaces::WorkspacesError),
}

pub struct ModelTaskRequest<'a> {
    pub workspace: &'a WorkspaceState,
    pub prompt_path: &'a Path,
}

#[derive(Debug, Clone)]
pub struct ModelOutcome {
    pub summary: String,
    pub raw_output: String,
}

/// Behind this trait sits whatever actually does the coding work inside a
/// task's workspace. Beethoven orchestrates the workspace and prompt; the
/// runtime decides what command runs inside the real tmux session.
pub trait ModelRuntime {
    fn execute_task(&self, request: &ModelTaskRequest) -> Result<ModelOutcome, AgentsError>;
}

/// STUB standing in for a real `claude --dangerously-skip-permissions`
/// launch. Runs a single deterministic command through the *real* tmux
/// session -- appending the generated prompt into the worktree -- so the
/// rest of the pipeline (git status/diff, review) has genuine on-disk
/// changes to operate on. Swapping in the real invocation later is a
/// one-line change to `command` below.
pub struct ClaudeCodeRuntime;

impl ModelRuntime for ClaudeCodeRuntime {
    fn execute_task(&self, request: &ModelTaskRequest) -> Result<ModelOutcome, AgentsError> {
        let session = TmuxSession {
            name: request.workspace.tmux_session.clone(),
        };
        let command = format!("cat '{}' >> NOTES.md", request.prompt_path.display());
        let output = session.run_and_wait(&command, Duration::from_secs(10))?;
        Ok(ModelOutcome {
            summary: "stub: appended the generated prompt to NOTES.md via tmux".to_string(),
            raw_output: output,
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use beethoven_workspaces::{WorkspaceManager, WorkspaceRequest};
    use std::process::Command;

    fn run_git(dir: &Path, args: &[&str]) {
        let status = Command::new("git").arg("-C").arg(dir).args(args).status().unwrap();
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
    fn claude_code_runtime_stub_writes_the_prompt_into_the_real_worktree() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let mgr = WorkspaceManager::new(dir.path().join("workspaces"));
        let state = mgr
            .create(&WorkspaceRequest {
                project_slug: "sample",
                agent_label: "backend",
                task_id: "task-model",
                repository_path: &repo.to_string_lossy(),
                default_branch: "main",
                branch: "task/model-test",
            })
            .unwrap();

        let prompt_path = dir.path().join("prompt.md");
        std::fs::write(&prompt_path, "Task:\nDo the thing\n").unwrap();

        let outcome = ClaudeCodeRuntime
            .execute_task(&ModelTaskRequest {
                workspace: &state,
                prompt_path: &prompt_path,
            })
            .unwrap();
        assert!(outcome.summary.contains("stub"));

        let notes = std::fs::read_to_string(Path::new(&state.git_worktree).join("NOTES.md")).unwrap();
        assert!(notes.contains("Do the thing"));

        mgr.destroy(&state).unwrap();
    }
}
