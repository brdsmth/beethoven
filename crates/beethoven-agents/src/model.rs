use beethoven_workspaces::{TmuxSession, WorkspaceState};
use serde::Deserialize;
use std::path::Path;
use std::process::{Command, Stdio};
use std::time::{Duration, Instant};

#[derive(Debug, thiserror::Error)]
pub enum AgentsError {
    #[error("workspace error: {0}")]
    Workspace(#[from] beethoven_workspaces::WorkspacesError),
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("malformed model response: {0}")]
    InvalidResponse(#[from] serde_json::Error),
    #[error("model process failed: {0}")]
    ProcessFailed(String),
    #[error("unknown runtime profile: {0}")]
    UnknownProfile(String),
    #[error("unknown runtime: {0}")]
    UnknownRuntime(String),
    #[error("planner produced no usable task or decision output")]
    NoPlannerOutput,
    #[error("chat call produced no usable answer")]
    NoChatOutput,
}

/// Everything a `ModelRuntime` needs to run one model invocation inside a
/// task's real workspace. `prompt_path` and `artifacts_dir` are always
/// absolute -- the tmux session's cwd is rooted at `workspace.git_worktree`,
/// a *sibling* of the artifacts directory, so relative paths typed inside
/// the session would not resolve to it.
pub struct ExecutionRequest<'a> {
    pub project_id: &'a str,
    pub agent_id: &'a str,
    pub task_id: Option<&'a str>,
    pub prompt_path: &'a Path,
    pub artifacts_dir: &'a Path,
    pub workspace: &'a WorkspaceState,
    pub json_schema: Option<&'a str>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ExecutionStatus {
    Completed,
    Failed,
}

#[derive(Debug, Clone)]
pub struct ExecutionResult {
    pub status: ExecutionStatus,
    pub summary: String,
    pub raw_response: String,
    pub structured_output: Option<serde_json::Value>,
    pub cost_usd: Option<f64>,
}

/// Behind this trait sits whatever actually does the reasoning/coding work.
/// Beethoven orchestrates the workspace and prompt; the runtime decides how
/// to get a response out of a model. Orchestration code must never assume a
/// specific provider.
pub trait ModelRuntime {
    fn provider(&self) -> &'static str;
    fn execute(&self, request: &ExecutionRequest, on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError>;

    /// A one-shot call with no task workspace behind it at all -- no git
    /// worktree, no tmux session. `cwd` is just where the prompt/response
    /// files for this one call live; callers must point it at an isolated,
    /// inert scratch directory they own (never a real repository), since
    /// unlike `execute` there is no disposable worktree containing whatever
    /// a `--dangerously-skip-permissions` invocation might do there.
    fn ask(&self, prompt: &str, cwd: &Path, json_schema: Option<&str>) -> Result<ExecutionResult, AgentsError>;
}

#[derive(Debug, Deserialize)]
struct ClaudeEnvelope {
    is_error: bool,
    result: Option<String>,
    total_cost_usd: Option<f64>,
    structured_output: Option<serde_json::Value>,
}

/// Builds the shell command that runs Claude Code inside the task's real
/// tmux session: the prompt is piped over stdin (never embedded as a CLI
/// argument) so prompt content -- partially derived from task titles a
/// human wrote -- can never be interpreted as shell syntax. Only the
/// Beethoven-controlled paths need to be shell-safe.
fn build_claude_command(
    prompt_path: &Path,
    response_path: &Path,
    stderr_path: &Path,
    exit_code_path: &Path,
    json_schema: Option<&str>,
) -> String {
    let mut cmd = format!(
        "cat '{}' | claude --dangerously-skip-permissions --output-format json -p",
        prompt_path.display()
    );
    if let Some(schema) = json_schema {
        cmd.push_str(&format!(" --json-schema '{schema}'"));
    }
    cmd.push_str(&format!(
        " > '{}' 2> '{}'; echo $? > '{}'",
        response_path.display(),
        stderr_path.display(),
        exit_code_path.display()
    ));
    cmd
}

/// Launches a real `claude --dangerously-skip-permissions` invocation inside
/// the task's tmux session. Claude's stdout/stderr/exit code are redirected
/// to files under `artifacts_dir` -- tmux pane capture is used purely as a
/// completion signal (via the sentinel in `run_and_wait`), never to retrieve
/// the response itself, since scrollback limits and line wrapping make it
/// unreliable for large JSON payloads.
pub struct ClaudeCodeRuntime {
    pub timeout: Duration,
    /// Timeout for `ask`'s workspace-free calls -- much shorter than
    /// `timeout`, since a chat answer involves no code-writing and
    /// shouldn't leave a human waiting anywhere near as long as a real
    /// task execution is allowed to run.
    pub chat_timeout: Duration,
}

impl Default for ClaudeCodeRuntime {
    fn default() -> Self {
        Self {
            timeout: Duration::from_secs(900),
            chat_timeout: Duration::from_secs(120),
        }
    }
}

/// Shared by `execute` (tmux-backed) and `ask` (workspace-free): both just
/// need to turn `claude`'s JSON envelope into an `ExecutionResult`.
fn parse_envelope_response(raw_response: String) -> Result<ExecutionResult, AgentsError> {
    let envelope: ClaudeEnvelope = serde_json::from_str(&raw_response)?;
    let summary = envelope.result.unwrap_or_default();
    Ok(ExecutionResult {
        status: if envelope.is_error {
            ExecutionStatus::Failed
        } else {
            ExecutionStatus::Completed
        },
        summary,
        raw_response,
        structured_output: envelope.structured_output,
        cost_usd: envelope.total_cost_usd,
    })
}

impl ModelRuntime for ClaudeCodeRuntime {
    fn provider(&self) -> &'static str {
        "claude-code"
    }

    fn execute(&self, request: &ExecutionRequest, on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        let response_path = request.artifacts_dir.join("response.json");
        let stderr_path = request.artifacts_dir.join("stderr.log");
        let exit_code_path = request.artifacts_dir.join("exit_code");

        let command = build_claude_command(
            request.prompt_path,
            &response_path,
            &stderr_path,
            &exit_code_path,
            request.json_schema,
        );

        let session = TmuxSession {
            name: request.workspace.tmux_session.clone(),
        };
        session.run_and_wait(&command, self.timeout, on_tick)?;

        let exit_code: i32 = std::fs::read_to_string(&exit_code_path)
            .ok()
            .and_then(|s| s.trim().parse().ok())
            .unwrap_or(-1);
        if exit_code != 0 {
            let stderr = std::fs::read_to_string(&stderr_path).unwrap_or_default();
            return Err(AgentsError::ProcessFailed(format!(
                "claude exited with status {exit_code}: {stderr}"
            )));
        }

        let raw_response = std::fs::read_to_string(&response_path)?;
        parse_envelope_response(raw_response)
    }

    /// No worktree, no tmux: writes the prompt straight into `cwd`, runs
    /// `claude` as a plain child process with stdout/stderr/exit-code
    /// redirected to files in the same directory (same shell command
    /// `execute` uses, just invoked directly instead of inside a tmux
    /// pane), and polls the child for completion instead of polling a tmux
    /// pane for a sentinel.
    fn ask(&self, prompt: &str, cwd: &Path, json_schema: Option<&str>) -> Result<ExecutionResult, AgentsError> {
        let prompt_path = cwd.join("prompt.md");
        let response_path = cwd.join("response.json");
        let stderr_path = cwd.join("stderr.log");
        let exit_code_path = cwd.join("exit_code");
        std::fs::write(&prompt_path, prompt)?;

        let command = build_claude_command(&prompt_path, &response_path, &stderr_path, &exit_code_path, json_schema);
        let mut child = Command::new("sh")
            .arg("-c")
            .arg(&command)
            .current_dir(cwd)
            .stdin(Stdio::null())
            .spawn()?;

        let start = Instant::now();
        loop {
            if child.try_wait()?.is_some() {
                break;
            }
            if start.elapsed() > self.chat_timeout {
                let _ = child.kill();
                return Err(AgentsError::ProcessFailed(format!(
                    "chat call timed out after {:?}",
                    self.chat_timeout
                )));
            }
            std::thread::sleep(Duration::from_millis(100));
        }

        let exit_code: i32 = std::fs::read_to_string(&exit_code_path)
            .ok()
            .and_then(|s| s.trim().parse().ok())
            .unwrap_or(-1);
        if exit_code != 0 {
            let stderr = std::fs::read_to_string(&stderr_path).unwrap_or_default();
            return Err(AgentsError::ProcessFailed(format!(
                "claude exited with status {exit_code}: {stderr}"
            )));
        }

        let raw_response = std::fs::read_to_string(&response_path)?;
        parse_envelope_response(raw_response)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn build_claude_command_pipes_the_prompt_over_stdin_and_redirects_all_three_streams() {
        let command = build_claude_command(
            Path::new("/work/prompts/prompt.md"),
            Path::new("/work/artifacts/response.json"),
            Path::new("/work/artifacts/stderr.log"),
            Path::new("/work/artifacts/exit_code"),
            None,
        );
        assert_eq!(
            command,
            "cat '/work/prompts/prompt.md' | claude --dangerously-skip-permissions --output-format json -p > '/work/artifacts/response.json' 2> '/work/artifacts/stderr.log'; echo $? > '/work/artifacts/exit_code'"
        );
    }

    #[test]
    fn build_claude_command_includes_the_json_schema_flag_when_provided() {
        let command = build_claude_command(
            Path::new("/work/prompts/prompt.md"),
            Path::new("/work/artifacts/response.json"),
            Path::new("/work/artifacts/stderr.log"),
            Path::new("/work/artifacts/exit_code"),
            Some(r#"{"type":"object"}"#),
        );
        assert!(command.contains(r#"--json-schema '{"type":"object"}'"#));
    }

    /// Opt-in only (`cargo test -- --ignored`): makes one real, billed
    /// `claude` call through a real tmux session + git worktree. Requires a
    /// logged-in `claude` CLI on PATH. Not part of the default suite since
    /// `cargo test --workspace` must stay fast, free, and network-independent.
    #[test]
    #[ignore]
    fn claude_code_runtime_executes_a_real_prompt_and_parses_the_envelope() {
        use beethoven_workspaces::{WorkspaceManager, WorkspaceRequest};
        use std::process::Command;

        fn run_git(dir: &Path, args: &[&str]) {
            let status = Command::new("git").arg("-C").arg(dir).args(args).status().unwrap();
            assert!(status.success(), "git {args:?} failed");
        }

        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        std::fs::create_dir_all(&repo).unwrap();
        run_git(&repo, &["init", "-q", "-b", "main"]);
        run_git(&repo, &["config", "user.email", "test@example.com"]);
        run_git(&repo, &["config", "user.name", "Test"]);
        std::fs::write(repo.join("README.md"), "scratch repo\n").unwrap();
        run_git(&repo, &["add", "-A"]);
        run_git(&repo, &["commit", "-q", "-m", "init"]);

        let mgr = WorkspaceManager::new(dir.path().join("workspaces"));
        let state = mgr
            .create(&WorkspaceRequest {
                project_slug: "sample",
                agent_label: "backend",
                task_id: "task-real-claude",
                repository_path: &repo.to_string_lossy(),
                default_branch: "main",
                branch: "task/real-claude-test",
            })
            .unwrap();

        let prompt_path = dir.path().join("prompt.md");
        std::fs::write(&prompt_path, "Reply with exactly the word: pong\n").unwrap();
        let artifacts_dir = dir.path().join("artifacts");
        std::fs::create_dir_all(&artifacts_dir).unwrap();

        let result = ClaudeCodeRuntime::default()
            .execute(
                &ExecutionRequest {
                    project_id: "p1",
                    agent_id: "a1",
                    task_id: Some("task-real-claude"),
                    prompt_path: &prompt_path,
                    artifacts_dir: &artifacts_dir,
                    workspace: &state,
                    json_schema: None,
                },
                &|| {},
            )
            .unwrap();

        assert_eq!(result.status, ExecutionStatus::Completed);
        assert!(result.summary.to_lowercase().contains("pong"));

        mgr.destroy(&state).unwrap();
    }
}
