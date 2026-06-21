use async_trait::async_trait;
use beethoven_agents::{
    AgentsError, ExecutionRequest, ExecutionResult, ExecutionStatus, ModelRuntime, RuntimeProfile, RuntimeRegistry,
};
use beethoven_core::{
    AgentRole, AgentStatus, ArtifactKind, DecisionStatus, InboxKind, InboxStatus, PlanningSummary, ProjectStatus,
    ReviewStatus, Runtime, RunStatus, TaskStatus,
};
use beethoven_events::{Event, EventScope, EventStore, EventStoreError, EventType};
use beethoven_workspaces::{TmuxSession, WorkspaceManager, WorkspaceRequest};
use std::path::Path;
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

fn run_git(dir: &Path, args: &[&str]) {
    let status = Command::new("git").arg("-C").arg(dir).args(args).status().unwrap();
    assert!(status.success(), "git {args:?} failed");
}

/// `run_tick` now drives real `git worktree`/tmux machinery, so every
/// project's repository must be a real, committed git repo on disk.
fn init_repo(path: &Path) {
    std::fs::create_dir_all(path).unwrap();
    run_git(path, &["init", "-q", "-b", "main"]);
    run_git(path, &["config", "user.email", "test@example.com"]);
    run_git(path, &["config", "user.name", "Test"]);
    std::fs::write(path.join("README.md"), "scratch repo\n").unwrap();
    run_git(path, &["add", "-A"]);
    run_git(path, &["commit", "-q", "-m", "init"]);
}

#[derive(Clone, Default)]
struct InMemoryStore {
    events: Arc<Mutex<Vec<Event>>>,
}

#[async_trait]
impl EventStore for InMemoryStore {
    async fn append(&self, event: Event) -> Result<(), EventStoreError> {
        self.events.lock().unwrap().push(event);
        Ok(())
    }

    async fn list_all(&self) -> Result<Vec<Event>, EventStoreError> {
        Ok(self.events.lock().unwrap().clone())
    }
}

/// Every fake `ModelRuntime` below is shared by both `plan_task` and the
/// worker execution path -- there is only one registered runtime per
/// registry. A planning call is distinguished from a worker call by
/// `request.json_schema` (only `Runtime::plan_task` ever sets it); for that
/// case, every fake delegates here instead of doing its own (worker-only)
/// thing, so tests written against the legacy deterministic planner's exact
/// output (titles, fallback behavior, decisions from `?` lines) keep
/// passing unchanged: `PlanningService::plan` itself didn't go away, it
/// just moved from being called directly by `Runtime::plan_task` to being
/// the thing these test doubles use to fabricate a plausible model
/// response.
fn stub_planning_result(prompt_path: &Path) -> ExecutionResult {
    let prompt = std::fs::read_to_string(prompt_path).expect("prompt file exists");
    let title = extract_between(&prompt, "Task Title:\n", "\n\n");
    let description = extract_between(&prompt, "-----BEGIN DESCRIPTION-----\n", "\n-----END DESCRIPTION-----");
    let outcome = beethoven_agents::PlanningService::plan(title, description);
    let structured = serde_json::json!({
        "reasoning": "stub planning reasoning for tests",
        "tasks": outcome.task_drafts,
        "decisions": outcome.decision_drafts,
    });
    ExecutionResult {
        status: ExecutionStatus::Completed,
        summary: "stub planning response".to_string(),
        raw_response: structured.to_string(),
        structured_output: Some(structured),
        cost_usd: None,
    }
}

fn extract_between<'a>(text: &'a str, start: &str, end: &str) -> &'a str {
    let after_start = &text[text.find(start).expect("start marker present in prompt") + start.len()..];
    &after_start[..after_start.find(end).expect("end marker present in prompt")]
}

/// Replaces a real model call with a real tmux round trip: appends the
/// rendered prompt to NOTES.md inside the worktree, so tests still exercise
/// genuine on-disk work without invoking `claude`.
struct StubModelRuntime;

impl ModelRuntime for StubModelRuntime {
    fn provider(&self) -> &'static str {
        "stub"
    }

    fn execute(&self, request: &ExecutionRequest, on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        if request.json_schema.is_some() {
            return Ok(stub_planning_result(request.prompt_path));
        }
        let session = TmuxSession { name: request.workspace.tmux_session.clone() };
        let command = format!("cat '{}' >> NOTES.md", request.prompt_path.display());
        let output = session.run_and_wait(&command, Duration::from_secs(10), on_tick)?;
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "stub: appended the generated prompt to NOTES.md via tmux".to_string(),
            raw_response: output,
            structured_output: None,
            cost_usd: None,
        })
    }
}

/// Like `StubModelRuntime`, but records the wall-clock window of every
/// `execute()` call and sleeps inside it, so a test can assert two calls'
/// windows actually overlapped (i.e. ran concurrently, not one-after-another).
struct SlowModelRuntime {
    windows: Arc<Mutex<Vec<(Instant, Instant)>>>,
}

impl ModelRuntime for SlowModelRuntime {
    fn provider(&self) -> &'static str {
        "slow-stub"
    }

    fn execute(&self, request: &ExecutionRequest, on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        if request.json_schema.is_some() {
            return Ok(stub_planning_result(request.prompt_path));
        }
        let start = Instant::now();
        std::thread::sleep(Duration::from_millis(300));
        let session = TmuxSession { name: request.workspace.tmux_session.clone() };
        let command = format!("cat '{}' >> NOTES.md", request.prompt_path.display());
        let output = session.run_and_wait(&command, Duration::from_secs(10), on_tick)?;
        self.windows.lock().unwrap().push((start, Instant::now()));
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "slow stub: appended the generated prompt to NOTES.md via tmux".to_string(),
            raw_response: output,
            structured_output: None,
            cost_usd: None,
        })
    }
}

/// Simulates the production bug a real Claude Code run can trigger: a
/// scaffolding step inside the model's own sandboxed worktree runs `git
/// init`, replacing the `.git` link `git worktree add` wrote with a brand
/// new, disjoint repo. Writes a real file, then clobbers `.git` and commits
/// it into the bogus repo -- so without the fix, `git status`/`diff` run
/// against that bogus repo and see no changes at all, even though real work
/// was done.
struct GitClobberingModelRuntime;

impl ModelRuntime for GitClobberingModelRuntime {
    fn provider(&self) -> &'static str {
        "git-clobbering-stub"
    }

    fn execute(&self, request: &ExecutionRequest, _on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        if request.json_schema.is_some() {
            return Ok(stub_planning_result(request.prompt_path));
        }
        let worktree = Path::new(&request.workspace.git_worktree);
        std::fs::write(worktree.join("NOTES.md"), "real work the model actually did\n").unwrap();
        std::fs::remove_file(worktree.join(".git")).unwrap();
        run_git(worktree, &["init", "-q", "-b", "main"]);
        run_git(worktree, &["config", "user.email", "model@example.com"]);
        run_git(worktree, &["config", "user.name", "Model"]);
        run_git(worktree, &["add", "-A"]);
        run_git(worktree, &["commit", "-q", "-m", "bogus scaffolding init"]);
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "model run that clobbered its own .git link".to_string(),
            raw_response: "ok".to_string(),
            structured_output: None,
            cost_usd: None,
        })
    }
}

/// Completes every task without ever touching the worktree, so the captured
/// diff is always empty -- `ReviewService::review` always returns
/// `NeedsChanges` for it, deterministically driving the retry-cap path.
struct NoOpModelRuntime;

impl ModelRuntime for NoOpModelRuntime {
    fn provider(&self) -> &'static str {
        "noop-stub"
    }

    fn execute(&self, request: &ExecutionRequest, _on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        if request.json_schema.is_some() {
            return Ok(stub_planning_result(request.prompt_path));
        }
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "noop stub: made no changes".to_string(),
            raw_response: "ok".to_string(),
            structured_output: None,
            cost_usd: None,
        })
    }
}

fn registry_with(runtime: impl ModelRuntime + Send + Sync + 'static) -> RuntimeRegistry {
    let mut registry = RuntimeRegistry::new();
    registry.register_runtime("test", Arc::new(runtime));
    registry.register_profile(RuntimeProfile {
        id: "claude-default".to_string(),
        runtime_id: "test".to_string(),
        model: "test".to_string(),
    });
    registry
}

/// `create_project` now seeds only the project's Lead, so tests that need a
/// Worker to exercise (pause/resume/reassign/recover on a non-Lead agent)
/// must spawn one explicitly.
async fn spawn_worker(runtime: &mut Runtime, project_id: &str, name: &str) -> beethoven_core::AgentInstance {
    let lead_id = runtime
        .state
        .agent_instances
        .values()
        .find(|a| a.project_id == project_id && a.role == AgentRole::Lead)
        .unwrap()
        .id
        .clone();
    runtime.spawn_agent(project_id, &lead_id, AgentRole::Worker, "Backend work", name).await.unwrap()
}

#[tokio::test]
async fn task_plan_run_and_answer_flow_survives_a_simulated_restart() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");

    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root.clone(), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime
        .create_project("Sample", dir.path().join("repo"), "main")
        .await
        .unwrap();
    assert_eq!(project.slug, "sample");

    let root = runtime
        .create_task(
            &project.id,
            None,
            "Build login page",
            "- Implement the login form\n- What auth provider should we use?\n- Write the landing page copy",
            "",
            None,
            false,
        )
        .await
        .unwrap();

    let outcome = runtime.plan_task(&root.id).await.unwrap();
    assert_eq!(outcome.tasks.len(), 2);
    assert_eq!(outcome.decisions.len(), 1);

    let report = runtime.run_tick(&project.id).await.unwrap();
    // "Implement..." spawns and finishes a subtask, "Write the landing page copy"
    // completes directly; the question is a Decision escalated at plan time,
    // not an execution-time escalation, so it never touches `escalated`.
    assert_eq!(report.completed, 2);
    assert_eq!(report.escalated, 0);
    assert_eq!(report.subtasks_spawned, 1);

    // The root has all its children done, but its decision is still open, so
    // it cannot complete yet.
    assert_eq!(runtime.state.tasks[&root.id].status, TaskStatus::Ready);

    // Simulate a process restart: drop this runtime and build a fresh one from
    // the same persisted event log. State must reconstruct identically.
    drop(runtime);
    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root, registry_with(StubModelRuntime))
        .await
        .unwrap();

    let open_inbox: Vec<_> = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.status == InboxStatus::Open)
        .collect();
    assert_eq!(open_inbox.len(), 1);
    let inbox_id = open_inbox[0].id.clone();

    let answered = runtime.answer_inbox(&inbox_id, "Use OAuth via Google + GitHub").await.unwrap();
    assert_eq!(answered.status, InboxStatus::Resolved);

    // Resolving the decision immediately completes the root via the
    // parent-completion cascade -- there is nothing left for run_tick to do.
    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 0);
    assert_eq!(report.escalated, 0);
    assert_eq!(report.subtasks_spawned, 0);

    assert_eq!(runtime.state.tasks[&root.id].status, TaskStatus::Done);
    assert!(runtime.state.tasks.values().all(|t| t.status == TaskStatus::Done));

    // Recovery: the full event log replays into the exact same terminal state again.
    let events = runtime.list_events().await.unwrap();
    let replayed = beethoven_core::RuntimeState::replay(&events);
    assert_eq!(replayed.tasks[&root.id].status, TaskStatus::Done);
    assert_eq!(replayed.tasks.len(), runtime.state.tasks.len());
}

#[tokio::test]
async fn answering_an_already_resolved_inbox_item_errors() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Solo", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Goal", "- What should we do?", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let inbox_id = runtime.state.inbox.keys().next().unwrap().clone();
    runtime.answer_inbox(&inbox_id, "Answer").await.unwrap();

    let result = runtime.answer_inbox(&inbox_id, "Answer again").await;
    assert!(result.is_err());
}

#[tokio::test]
async fn two_projects_run_independently_with_no_shared_agents_or_mixed_tasks() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("sample-repo"));
    init_repo(&dir.path().join("sailboat-repo"));
    let sample = runtime
        .create_project("Sample", dir.path().join("sample-repo"), "main")
        .await
        .unwrap();
    let sailboat = runtime
        .create_project("Sailboat", dir.path().join("sailboat-repo"), "main")
        .await
        .unwrap();

    // Each project bootstraps just its own Lead agent from the shared templates.
    assert_eq!(runtime.state.agent_templates.len(), 3);
    let sample_instances: Vec<_> = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == sample.id)
        .collect();
    let sailboat_instances: Vec<_> = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == sailboat.id)
        .collect();
    assert_eq!(sample_instances.len(), 1);
    assert_eq!(sailboat_instances.len(), 1);
    assert_eq!(runtime.state.agent_instances.len(), 2);

    let sample_root =
        runtime.create_task(&sample.id, None, "Sample goal", "- Sample task", "", None, false).await.unwrap();
    let sailboat_root = runtime
        .create_task(&sailboat.id, None, "Sailboat goal", "- Sailboat task", "", None, false)
        .await
        .unwrap();

    runtime.plan_task(&sample_root.id).await.unwrap();
    runtime.plan_task(&sailboat_root.id).await.unwrap();

    // Running only Sample's tick must not touch Sailboat's task at all.
    let sample_report = runtime.run_tick(&sample.id).await.unwrap();
    assert_eq!(sample_report.completed, 1);

    let sailboat_task = runtime
        .state
        .tasks
        .values()
        .find(|t| t.project_id == sailboat.id && t.parent_task_id.is_some())
        .unwrap();
    assert_eq!(sailboat_task.status, TaskStatus::Ready);
    assert_eq!(sailboat_task.assigned_agent_id, None);

    // Running Sailboat's tick independently now completes its own task.
    let sailboat_report = runtime.run_tick(&sailboat.id).await.unwrap();
    assert_eq!(sailboat_report.completed, 1);

    let sample_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == sample.id).collect();
    let sailboat_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == sailboat.id).collect();
    assert_eq!(sample_tasks.len(), 2);
    assert_eq!(sailboat_tasks.len(), 2);
    assert!(sample_tasks.iter().all(|t| t.status == TaskStatus::Done));
    assert!(sailboat_tasks.iter().all(|t| t.status == TaskStatus::Done));
}

#[tokio::test]
async fn a_completed_task_records_a_real_execution_run_diff_artifact_and_approved_review() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.needs_changes, 0);
    assert_eq!(report.escalated, 0);

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Done);
    assert_eq!(task.branch.as_deref(), Some("task/write-the-changelog"));
    assert!(task.pr_url.is_some());

    // The real tmux/model stub appended the rendered prompt to NOTES.md inside
    // the worktree, so the captured diff (and the run summary) reflect genuine
    // on-disk work rather than a deterministic stand-in.
    let run = runtime.state.runs.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(run.status, RunStatus::Completed);
    assert!(run.summary.as_deref().unwrap().contains("stub"));

    let diff_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == run.id && a.kind == ArtifactKind::Diff)
        .unwrap();
    assert!(diff_artifact.content.contains("Write the changelog"));

    let review = runtime.state.reviews.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(review.status, ReviewStatus::Approved);
}

#[tokio::test]
async fn a_model_run_that_reinitializes_git_inside_its_worktree_still_yields_a_real_diff() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(GitClobberingModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root =
        runtime.create_task(&project.id, None, "Docs", "- Write the landing page copy", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.needs_changes, 0);
    assert_eq!(report.escalated, 0);

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Done);

    // Without restoring the worktree's `.git` link before computing the
    // diff, this would have come back empty -- the model's own `git init`
    // replaced it with a disjoint repo that already had everything
    // committed, hiding the real change from status/diff.
    let run = runtime.state.runs.values().find(|r| r.task_id == task.id).unwrap();
    let diff_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == run.id && a.kind == ArtifactKind::Diff)
        .unwrap();
    assert!(diff_artifact.content.contains("NOTES.md"));
    assert!(diff_artifact.content.contains("real work the model actually did"));

    let review = runtime.state.reviews.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(review.status, ReviewStatus::Approved);
}

#[tokio::test]
async fn review_blocks_a_risky_task_and_escalates_through_the_existing_inbox_flow() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Ops", "- Run a risky database migration", "", None, false)
        .await
        .unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 0);
    assert_eq!(report.escalated, 1);

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Blocked);
    assert_eq!(task.assigned_agent_id, None);

    // The review path reuses the existing Worker->Review->PM->Inbox->Human
    // flow rather than inventing new escalation machinery: a Blocked review
    // outcome still surfaces as a plain Question in the inbox.
    let inbox_item = runtime
        .state
        .inbox
        .values()
        .find(|i| i.task_id.as_deref() == Some(task.id.as_str()))
        .unwrap();
    assert_eq!(inbox_item.kind, InboxKind::Question);
    assert_eq!(inbox_item.status, InboxStatus::Open);
    assert!(inbox_item.prompt.contains("risky"));

    // The review itself never reaches a terminal status -- only Approved and
    // NeedsChanges resolve a review, so a Blocked outcome leaves it Pending
    // while the human-in-the-loop inbox flow takes over.
    let review = runtime.state.reviews.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(review.status, ReviewStatus::Pending);
}

#[tokio::test]
async fn a_root_task_completes_only_once_all_its_children_are_done_and_its_decisions_are_resolved() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();

    let root = runtime
        .create_task(
            &project.id,
            None,
            "Build Gmail Agent",
            "- Support PDF attachments\n- What auth provider should we use?",
            "",
            None,
            false,
        )
        .await
        .unwrap();
    assert_eq!(root.status, TaskStatus::Ready);

    let outcome = runtime.plan_task(&root.id).await.unwrap();
    assert_eq!(outcome.tasks.len(), 1);
    assert_eq!(outcome.tasks[0].title, "Support PDF attachments");
    assert_eq!(outcome.decisions.len(), 1);
    assert_eq!(outcome.decisions[0].task_id, Some(root.id.clone()));

    let open_decision_inbox: Vec<_> = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.kind == InboxKind::Decision && i.status == InboxStatus::Open)
        .collect();
    assert_eq!(open_decision_inbox.len(), 1);
    assert_eq!(open_decision_inbox[0].decision_id, Some(outcome.decisions[0].id.clone()));

    // "Support PDF attachments" is a plain title (no "implement"/"?"), so it
    // completes directly in one tick.
    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.subtasks_spawned, 0);
    assert_eq!(report.escalated, 0);

    // The only child is done, but the decision is still open, so the root
    // cannot complete yet.
    assert_eq!(runtime.state.tasks[&root.id].status, TaskStatus::Ready);

    let decision = runtime
        .resolve_decision(&outcome.decisions[0].id, "Use OAuth via Google + GitHub")
        .await
        .unwrap();
    assert_eq!(decision.status, DecisionStatus::Resolved);

    assert_eq!(runtime.state.tasks[&root.id].status, TaskStatus::Done);
}

#[tokio::test]
async fn a_task_with_no_bullet_list_description_plans_a_single_fallback_child() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Build Gmail Agent", "", "", None, false).await.unwrap();

    let outcome = runtime.plan_task(&root.id).await.unwrap();
    assert_eq!(outcome.tasks.len(), 1);
    assert_eq!(outcome.tasks[0].title, "Implement: Build Gmail Agent");
    assert_eq!(outcome.decisions.len(), 0);
    assert_eq!(outcome.tasks[0].parent_task_id, Some(root.id.clone()));
}

/// Simulates a crash in the exact window `run_tick` leaves open: `TaskStarted`
/// (and the real workspace/branch/tmux session it sets up) was recorded, but
/// the process died before the next event (normally `PromptGenerated` /
/// `ModelRunStarted`) ever got appended. A fresh `Runtime::load` (replaying
/// the same log, as any restart would) must come up with the task stuck in
/// `InProgress` and its agent stuck in `Executing` -- `recover()` is what
/// resets that to a workable state and tears down the orphaned workspace.
#[tokio::test]
async fn recover_resets_a_task_crashed_mid_execution_and_cleans_up_its_workspace() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");
    let repo = dir.path().join("repo");
    init_repo(&repo);

    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root.clone(), registry_with(StubModelRuntime))
        .await
        .unwrap();
    let project = runtime.create_project("Sample", repo.clone(), "main").await.unwrap();
    // A plain title (no "implement"/"?") so re-running it post-recovery
    // completes directly instead of spawning a subtask or raising a question.
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    let outcome = runtime.plan_task(&root.id).await.unwrap();
    let task = outcome.tasks[0].clone();

    let agent = spawn_worker(&mut runtime, &project.id, "Worker").await;

    // Hand-roll exactly the event sequence `run_tick` would have produced up
    // to (and including) standing up a real workspace, then stop -- as if the
    // process died right before generating the prompt / invoking the model.
    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskAssigned,
            &agent.id,
            serde_json::json!({"task_id": task.id, "agent_id": agent.id}),
        ))
        .await
        .unwrap();
    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskStarted,
            &agent.id,
            serde_json::json!({"task_id": task.id}),
        ))
        .await
        .unwrap();

    let branch = beethoven_git::branch_name(&task.title);
    let workspaces = WorkspaceManager::new(projects_root.join(&project.slug).join("workspaces"));
    let ws_state = workspaces
        .create(&WorkspaceRequest {
            project_slug: &project.slug,
            agent_label: agent.role.as_str(),
            task_id: &task.id,
            repository_path: &repo.to_string_lossy(),
            default_branch: "main",
            branch: &branch,
        })
        .unwrap();
    store
        .append(Event::for_project(
            &project.id,
            EventType::BranchCreated,
            &agent.id,
            serde_json::json!({"task_id": task.id, "branch": ws_state.branch}),
        ))
        .await
        .unwrap();
    assert!(TmuxSession::list_active().unwrap().contains(&ws_state.tmux_session));

    // Simulate a process restart: a fresh `Runtime::load` replays the log
    // above into a task stuck `InProgress` with its agent stuck `Executing`.
    drop(runtime);
    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root, registry_with(StubModelRuntime))
        .await
        .unwrap();
    assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::InProgress);
    assert_eq!(runtime.state.agent_instances[&agent.id].status, AgentStatus::Executing);

    let recovery = runtime.recover(3).await.unwrap();
    assert_eq!(recovery.recovered, vec![task.id.clone()]);
    assert!(recovery.escalated.is_empty());

    let recovered_task = &runtime.state.tasks[&task.id];
    assert_eq!(recovered_task.status, TaskStatus::Ready);
    assert_eq!(recovered_task.assigned_agent_id, None);
    assert_eq!(recovered_task.attempt, 2);
    let recovered_agent = &runtime.state.agent_instances[&agent.id];
    assert_eq!(recovered_agent.status, AgentStatus::Idle);
    assert_eq!(recovered_agent.current_task_id, None);

    // The orphaned worktree/tmux session from the crashed attempt are torn down.
    assert!(!Path::new(&ws_state.git_worktree).exists());
    assert!(!TmuxSession::list_active().unwrap().contains(&ws_state.tmux_session));

    let events = runtime.list_events().await.unwrap();
    assert!(events.iter().any(|e| e.event_type == EventType::TaskRecovered));

    // No task loss: the recovered task is picked back up and completes normally.
    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::Done);
}

#[tokio::test]
async fn two_tasks_in_the_same_tick_execute_concurrently_when_max_concurrent_allows_it() {
    let windows = Arc::new(Mutex::new(Vec::new()));
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(
        Arc::new(store),
        dir.path().join("projects"),
        registry_with(SlowModelRuntime { windows: windows.clone() }),
    )
    .await
    .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Docs", "- Write the changelog\n- Write the readme", "", None, false)
        .await
        .unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let mut reports = runtime.tick_projects(std::slice::from_ref(&project.id), 2, 3).await.unwrap();
    let report = reports.remove(&project.id).unwrap();
    assert_eq!(report.completed, 2);

    let recorded = windows.lock().unwrap().clone();
    assert_eq!(recorded.len(), 2);
    let (start_a, end_a) = recorded[0];
    let (start_b, end_b) = recorded[1];
    assert!(start_a < end_b && start_b < end_a, "expected the two executions' windows to overlap: {recorded:?}");
}

#[tokio::test]
async fn a_workspace_creation_failure_escalates_just_that_project_not_the_whole_tick() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    // Planning now also needs a real workspace (Phase 2.5A calls a real
    // model), so the repo must be valid through planning and only break
    // afterwards -- otherwise planning itself would be the first thing to
    // fail, never reaching `tick_projects` at all. Repointed to a path that
    // was never `git init`-ed right before the tick: `git worktree add`
    // will fail for the child task's execution specifically.
    init_repo(&dir.path().join("initially-fine"));
    let broken = runtime
        .create_project("Broken", dir.path().join("initially-fine"), "main")
        .await
        .unwrap();
    let broken_root =
        runtime.create_task(&broken.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&broken_root.id).await.unwrap();
    runtime.pause_project(&broken.id).await.unwrap();
    runtime.update_repository_path(&broken.id, dir.path().join("does-not-exist").to_string_lossy().into_owned()).await.unwrap();
    runtime.resume_project(&broken.id).await.unwrap();

    init_repo(&dir.path().join("good-repo"));
    let healthy = runtime.create_project("Healthy", dir.path().join("good-repo"), "main").await.unwrap();
    let healthy_root =
        runtime.create_task(&healthy.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&healthy_root.id).await.unwrap();

    let mut reports = runtime.tick_projects(&[broken.id.clone(), healthy.id.clone()], 4, 3).await.unwrap();

    let broken_report = reports.remove(&broken.id).unwrap();
    assert_eq!(broken_report.escalated, 1);
    assert_eq!(broken_report.completed, 0);
    let broken_task =
        runtime.state.tasks.values().find(|t| t.project_id == broken.id && t.parent_task_id.is_some()).unwrap();
    assert_eq!(broken_task.status, TaskStatus::Blocked);
    let inbox_item = runtime.state.inbox.values().find(|i| i.project_id == broken.id).unwrap();
    assert_eq!(inbox_item.kind, InboxKind::Question);

    // The sibling project's task completes normally in the same tick.
    let healthy_report = reports.remove(&healthy.id).unwrap();
    assert_eq!(healthy_report.completed, 1);
    assert_eq!(healthy_report.escalated, 0);
}

#[tokio::test]
async fn a_task_stuck_needing_changes_escalates_instead_of_retrying_forever() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(NoOpModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    // NoOpModelRuntime never touches the worktree, so the diff is always
    // empty and ReviewService::review always returns NeedsChanges -- this
    // would retry forever within tick_projects' own drain loop without the
    // attempt cap.
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let mut reports = runtime.tick_projects(std::slice::from_ref(&project.id), 4, 2).await.unwrap();
    let report = reports.remove(&project.id).unwrap();

    // One ordinary rejection (attempt 1 -> 2), then the next rejection at
    // attempt 2 hits the cap and escalates instead of looping forever.
    assert_eq!(report.needs_changes, 1);
    assert_eq!(report.escalated, 1);
    assert_eq!(report.completed, 0);

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap();
    assert_eq!(task.status, TaskStatus::Blocked);
    assert_eq!(task.attempt, 2);
    let inbox_item = runtime.state.inbox.values().find(|i| i.task_id.as_deref() == Some(task.id.as_str())).unwrap();
    assert!(inbox_item.prompt.contains("giving up after"));
}

#[tokio::test]
async fn recover_escalates_instead_of_recovering_a_task_that_already_hit_max_attempts() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");
    let repo = dir.path().join("repo");
    init_repo(&repo);

    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root.clone(), registry_with(StubModelRuntime))
        .await
        .unwrap();
    let project = runtime.create_project("Sample", repo.clone(), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    let outcome = runtime.plan_task(&root.id).await.unwrap();
    let task = outcome.tasks[0].clone();

    let agent = spawn_worker(&mut runtime, &project.id, "Worker").await;

    // Simulate a task already on its second attempt (e.g. one prior crash
    // already recovered it once) that crashes again mid-execution.
    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskRecovered,
            "system",
            serde_json::json!({"task_id": task.id, "agent_id": agent.id}),
        ))
        .await
        .unwrap();
    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskAssigned,
            &agent.id,
            serde_json::json!({"task_id": task.id, "agent_id": agent.id}),
        ))
        .await
        .unwrap();
    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskStarted,
            &agent.id,
            serde_json::json!({"task_id": task.id}),
        ))
        .await
        .unwrap();

    drop(runtime);
    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root, registry_with(StubModelRuntime))
        .await
        .unwrap();
    assert_eq!(runtime.state.tasks[&task.id].attempt, 2);

    let recovery = runtime.recover(2).await.unwrap();
    assert!(recovery.recovered.is_empty());
    assert_eq!(recovery.escalated, vec![task.id.clone()]);

    let escalated_task = &runtime.state.tasks[&task.id];
    assert_eq!(escalated_task.status, TaskStatus::Blocked);
    let inbox_item = runtime.state.inbox.values().find(|i| i.task_id.as_deref() == Some(task.id.as_str())).unwrap();
    assert!(inbox_item.prompt.contains("gave up after crashing"));
}

#[tokio::test]
async fn pause_project_prevents_new_assignment_until_resumed() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let paused = runtime.pause_project(&project.id).await.unwrap();
    assert_eq!(paused.status, ProjectStatus::Paused);

    // Pausing an already-paused project is invalid.
    assert!(runtime.pause_project(&project.id).await.is_err());

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.assigned, 0);
    assert_eq!(report.completed, 0);
    assert!(runtime.state.tasks.values().all(|t| t.status == TaskStatus::Ready));

    let resumed = runtime.resume_project(&project.id).await.unwrap();
    assert_eq!(resumed.status, ProjectStatus::Active);

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
}

#[tokio::test]
async fn archive_project_removes_it_from_active_execution_entirely() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root.id).await.unwrap();

    let archived = runtime.archive_project(&project.id).await.unwrap();
    assert_eq!(archived.status, ProjectStatus::Archived);

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.assigned, 0);
    assert_eq!(report.completed, 0);
    assert!(runtime.state.tasks.values().all(|t| t.status == TaskStatus::Ready));

    assert!(runtime.archive_project(&project.id).await.is_err());
}

#[tokio::test]
async fn retry_task_returns_a_blocked_task_to_ready_for_another_attempt() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Ops", "- Run a risky database migration", "", None, false)
        .await
        .unwrap();
    runtime.plan_task(&root.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Blocked);

    let retried = runtime.retry_task(&task.id).await.unwrap();
    assert_eq!(retried.status, TaskStatus::Ready);
    assert_eq!(retried.attempt, task.attempt + 1);

    // No longer blocked, so retrying again is invalid.
    assert!(runtime.retry_task(&task.id).await.is_err());
}

#[tokio::test]
async fn cancel_task_stops_it_from_future_execution() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime.create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    let outcome = runtime.plan_task(&root.id).await.unwrap();
    let task = outcome.tasks[0].clone();

    let cancelled = runtime.cancel_task(&task.id).await.unwrap();
    assert_eq!(cancelled.status, TaskStatus::Cancelled);

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.assigned, 0);
    assert_eq!(report.completed, 0);
    assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::Cancelled);

    // A cancelled task is terminal; cancelling it again is invalid.
    assert!(runtime.cancel_task(&task.id).await.is_err());
}

#[tokio::test]
async fn reassign_task_moves_ownership_to_a_new_idle_agent_in_the_same_project() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");

    let mut runtime = Runtime::load(Arc::new(store.clone()), projects_root.clone(), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Ops", "- Run a risky database migration", "", None, false)
        .await
        .unwrap();
    let outcome = runtime.plan_task(&root.id).await.unwrap();
    let task = outcome.tasks[0].clone();

    let old_agent = spawn_worker(&mut runtime, &project.id, "Worker A").await;
    let new_agent = spawn_worker(&mut runtime, &project.id, "Worker B").await;

    store
        .append(Event::for_project(
            &project.id,
            EventType::TaskAssigned,
            &old_agent.id,
            serde_json::json!({"task_id": task.id, "agent_id": old_agent.id}),
        ))
        .await
        .unwrap();

    // Reload so the hand-appended assignment above is reflected in state,
    // the same way `recover_resets_a_task_crashed_mid_execution...` does.
    drop(runtime);
    let mut runtime = Runtime::load(Arc::new(store), projects_root, registry_with(StubModelRuntime)).await.unwrap();
    assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::Assigned);

    let reassigned = runtime.reassign_task(&task.id, &new_agent.id).await.unwrap();
    assert_eq!(reassigned.status, TaskStatus::Assigned);
    assert_eq!(reassigned.assigned_agent_id, Some(new_agent.id.clone()));
    assert_eq!(runtime.state.agent_instances[&old_agent.id].status, AgentStatus::Idle);
    assert_eq!(runtime.state.agent_instances[&old_agent.id].current_task_id, None);
    assert_eq!(runtime.state.agent_instances[&new_agent.id].status, AgentStatus::Assigned);
    assert_eq!(runtime.state.agent_instances[&new_agent.id].current_task_id, Some(task.id.clone()));

    // The new agent is no longer idle, so reassigning to it again is invalid.
    assert!(runtime.reassign_task(&task.id, &new_agent.id).await.is_err());
}

#[tokio::test]
async fn approve_review_accepts_a_blocked_task_as_a_manual_override() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Ops", "- Run a risky database migration", "", None, false)
        .await
        .unwrap();
    runtime.plan_task(&root.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Blocked);

    let approved = runtime.approve_review(&task.id).await.unwrap();
    assert_eq!(approved.status, TaskStatus::Approved);

    // No longer blocked, so approving again is invalid.
    assert!(runtime.approve_review(&task.id).await.is_err());
}

#[tokio::test]
async fn request_review_changes_returns_a_blocked_task_to_ready() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Ops", "- Run a risky database migration", "", None, false)
        .await
        .unwrap();
    runtime.plan_task(&root.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().find(|t| t.parent_task_id.is_some()).unwrap().clone();
    assert_eq!(task.status, TaskStatus::Blocked);

    let updated = runtime.request_review_changes(&task.id, "needs more tests").await.unwrap();
    assert_eq!(updated.status, TaskStatus::Ready);
    assert_eq!(updated.attempt, task.attempt + 1);
    assert_eq!(updated.assigned_agent_id, None);

    // No longer blocked, so requesting changes again is invalid.
    assert!(runtime.request_review_changes(&task.id, "again").await.is_err());
}

#[tokio::test]
async fn pause_agent_and_resume_agent_require_idle_and_paused_respectively() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let agent = spawn_worker(&mut runtime, &project.id, "Worker").await;
    assert_eq!(agent.status, AgentStatus::Idle);

    let paused = runtime.pause_agent(&agent.id).await.unwrap();
    assert_eq!(paused.status, AgentStatus::Paused);

    // Already paused (not idle), so pausing again is invalid.
    assert!(runtime.pause_agent(&agent.id).await.is_err());

    let resumed = runtime.resume_agent(&agent.id).await.unwrap();
    assert_eq!(resumed.status, AgentStatus::Idle);

    // Already idle (not paused), so resuming again is invalid.
    assert!(runtime.resume_agent(&agent.id).await.is_err());
}

#[tokio::test]
async fn sync_agent_health_events_marks_slow_and_stalled_and_suppresses_after_a_fresh_heartbeat() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store.clone()), dir.path().join("projects"), registry_with(NoOpModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();

    spawn_worker(&mut runtime, &project.id, "Worker A").await;
    spawn_worker(&mut runtime, &project.id, "Worker B").await;
    spawn_worker(&mut runtime, &project.id, "Worker C").await;

    let mut agent_ids: Vec<String> = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == project.id && a.role != AgentRole::Lead)
        .map(|a| a.id.clone())
        .collect();
    agent_ids.sort();
    assert_eq!(agent_ids.len(), 3, "spawned exactly 3 workers to exercise 3 independent scenarios");
    let slow_agent = agent_ids[0].clone();
    let escalating_agent = agent_ids[1].clone();
    let recovered_agent = agent_ids[2].clone();

    let now = chrono::Utc::now();
    for (i, agent_id) in [&slow_agent, &escalating_agent, &recovered_agent].into_iter().enumerate() {
        store
            .append(Event {
                id: format!("assign-{i}"),
                scope: EventScope::Project(project.id.clone()),
                event_type: EventType::TaskAssigned,
                actor_id: Some("test".to_string()),
                payload: serde_json::json!({"task_id": format!("fake-task-{i}"), "agent_id": agent_id}),
                timestamp: now,
            })
            .await
            .unwrap();
    }

    // Scenario 1: no marker yet, heartbeat is 90s stale -> emits AgentMarkedSlow.
    store
        .append(Event {
            id: "hb-slow".to_string(),
            scope: EventScope::Project(project.id.clone()),
            event_type: EventType::AgentHeartbeat,
            actor_id: Some(slow_agent.clone()),
            payload: serde_json::json!({"agent_id": slow_agent}),
            timestamp: now - chrono::Duration::seconds(90),
        })
        .await
        .unwrap();

    // Scenario 2: already marked Slow a while back; the heartbeat hasn't
    // moved since and is now 200s stale -> escalates to AgentMarkedStalled,
    // not a duplicate Slow.
    store
        .append(Event {
            id: "hb-escalating".to_string(),
            scope: EventScope::Project(project.id.clone()),
            event_type: EventType::AgentHeartbeat,
            actor_id: Some(escalating_agent.clone()),
            payload: serde_json::json!({"agent_id": escalating_agent}),
            timestamp: now - chrono::Duration::seconds(200),
        })
        .await
        .unwrap();
    store
        .append(Event {
            id: "marker-escalating-slow".to_string(),
            scope: EventScope::Project(project.id.clone()),
            event_type: EventType::AgentMarkedSlow,
            actor_id: Some(escalating_agent.clone()),
            payload: serde_json::json!({"agent_id": escalating_agent, "task_id": null}),
            timestamp: now - chrono::Duration::seconds(150),
        })
        .await
        .unwrap();

    // Scenario 3: was marked Stalled, but a heartbeat 10s ago -- fresher than
    // the marker -- is implicit recovery evidence, so no new marker fires.
    store
        .append(Event {
            id: "marker-recovered-stalled".to_string(),
            scope: EventScope::Project(project.id.clone()),
            event_type: EventType::AgentMarkedStalled,
            actor_id: Some(recovered_agent.clone()),
            payload: serde_json::json!({"agent_id": recovered_agent, "task_id": null}),
            timestamp: now - chrono::Duration::seconds(200),
        })
        .await
        .unwrap();
    store
        .append(Event {
            id: "hb-recovered".to_string(),
            scope: EventScope::Project(project.id.clone()),
            event_type: EventType::AgentHeartbeat,
            actor_id: Some(recovered_agent.clone()),
            payload: serde_json::json!({"agent_id": recovered_agent}),
            timestamp: now - chrono::Duration::seconds(10),
        })
        .await
        .unwrap();

    runtime.refresh().await.unwrap();
    runtime.sync_agent_health_events().await.unwrap();

    let events = runtime.list_events().await.unwrap();
    let markers_for = |agent_id: &str| -> Vec<EventType> {
        events
            .iter()
            .filter(|e| e.payload.get("agent_id").and_then(|v| v.as_str()) == Some(agent_id))
            .filter(|e| matches!(e.event_type, EventType::AgentMarkedSlow | EventType::AgentMarkedStalled))
            .map(|e| e.event_type)
            .collect()
    };

    assert_eq!(markers_for(&slow_agent), vec![EventType::AgentMarkedSlow]);
    assert_eq!(markers_for(&escalating_agent), vec![EventType::AgentMarkedSlow, EventType::AgentMarkedStalled]);
    assert_eq!(markers_for(&recovered_agent), vec![EventType::AgentMarkedStalled]);

    // Idempotent: an unchanged level (e.g. the slow agent is still just as
    // stale) must not re-emit on a second call.
    runtime.sync_agent_health_events().await.unwrap();
    let events_after_second_call = runtime.list_events().await.unwrap();
    assert_eq!(events_after_second_call.len(), events.len());
}

#[tokio::test]
async fn task_blocked_sets_blocked_reason_and_clearing_it_via_retry_or_question_answer_resets_it() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    // Two projects, each planned against a real repo (planning now also
    // needs a real workspace) and then repointed to a repo that was never
    // `git init`-ed -- `git worktree add` fails immediately for the
    // already-planned child task's execution, blocking it on the first tick.
    init_repo(&dir.path().join("repo-a"));
    let project_a = runtime.create_project("Retry", dir.path().join("repo-a"), "main").await.unwrap();
    let root_a =
        runtime.create_task(&project_a.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root_a.id).await.unwrap();
    runtime.pause_project(&project_a.id).await.unwrap();
    runtime
        .update_repository_path(&project_a.id, dir.path().join("missing-repo-a").to_string_lossy().into_owned())
        .await
        .unwrap();
    runtime.resume_project(&project_a.id).await.unwrap();

    init_repo(&dir.path().join("repo-b"));
    let project_b = runtime.create_project("Answer", dir.path().join("repo-b"), "main").await.unwrap();
    let root_b =
        runtime.create_task(&project_b.id, None, "Docs", "- Write the changelog", "", None, false).await.unwrap();
    runtime.plan_task(&root_b.id).await.unwrap();
    runtime.pause_project(&project_b.id).await.unwrap();
    runtime
        .update_repository_path(&project_b.id, dir.path().join("missing-repo-b").to_string_lossy().into_owned())
        .await
        .unwrap();
    runtime.resume_project(&project_b.id).await.unwrap();

    runtime.tick_projects(&[project_a.id.clone(), project_b.id.clone()], 4, 3).await.unwrap();

    let task_a = runtime
        .state
        .tasks
        .values()
        .find(|t| t.project_id == project_a.id && t.parent_task_id.is_some())
        .unwrap()
        .clone();
    assert_eq!(task_a.status, TaskStatus::Blocked);
    assert!(task_a.blocked_reason.is_some());

    let task_b = runtime
        .state
        .tasks
        .values()
        .find(|t| t.project_id == project_b.id && t.parent_task_id.is_some())
        .unwrap()
        .clone();
    assert_eq!(task_b.status, TaskStatus::Blocked);
    assert!(task_b.blocked_reason.is_some());

    // Clearing path 1: a human retries a blocked task directly.
    runtime.retry_task(&task_a.id).await.unwrap();
    let retried = runtime.state.tasks.get(&task_a.id).unwrap();
    assert_eq!(retried.status, TaskStatus::Ready);
    assert_eq!(retried.blocked_reason, None);

    // Clearing path 2: a human answers the inbox question the escalation
    // raised, which both resolves the inbox item and resets the task.
    let inbox_item =
        runtime.state.inbox.values().find(|i| i.task_id.as_deref() == Some(task_b.id.as_str())).unwrap().clone();
    runtime.answer_inbox(&inbox_item.id, "use this path instead").await.unwrap();
    let answered = runtime.state.tasks.get(&task_b.id).unwrap();
    assert_eq!(answered.status, TaskStatus::Ready);
    assert_eq!(answered.blocked_reason, None);
}

#[tokio::test]
async fn add_task_dependency_rejects_self_dependency_and_missing_tasks() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let a = runtime.create_task(&project.id, None, "A", "", "", None, false).await.unwrap();

    assert!(runtime.add_task_dependency(&a.id, &a.id).await.is_err(), "a task cannot depend on itself");
    assert!(runtime.add_task_dependency(&a.id, "does-not-exist").await.is_err());
    assert!(runtime.add_task_dependency("does-not-exist", &a.id).await.is_err());
}

#[tokio::test]
async fn add_task_dependency_rejects_a_dependency_that_would_close_a_cycle() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let a = runtime.create_task(&project.id, None, "A", "", "", None, false).await.unwrap();
    let b = runtime.create_task(&project.id, None, "B", "", "", None, false).await.unwrap();
    let c = runtime.create_task(&project.id, None, "C", "", "", None, false).await.unwrap();

    runtime.add_task_dependency(&a.id, &b.id).await.unwrap(); // A depends on B
    runtime.add_task_dependency(&b.id, &c.id).await.unwrap(); // B depends on C

    let result = runtime.add_task_dependency(&c.id, &a.id).await; // would close A -> B -> C -> A
    assert!(result.is_err(), "C depending on A would close a cycle through the existing A -> B -> C chain");
}

#[tokio::test]
async fn add_task_dependency_rejects_a_cross_project_edge() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo-a"));
    init_repo(&dir.path().join("repo-b"));
    let project_a = runtime.create_project("A", dir.path().join("repo-a"), "main").await.unwrap();
    let project_b = runtime.create_project("B", dir.path().join("repo-b"), "main").await.unwrap();
    let task_a = runtime.create_task(&project_a.id, None, "Task A", "", "", None, false).await.unwrap();
    let task_b = runtime.create_task(&project_b.id, None, "Task B", "", "", None, false).await.unwrap();

    assert!(runtime.add_task_dependency(&task_a.id, &task_b.id).await.is_err());
}

/// The doc's own example, end to end: `Tests depends_on Backend` means
/// Tests must never be assigned before Backend completes, and once Backend
/// completes Tests is released with no further human action -- both within
/// the same `run_tick` call, since `tick_projects`'s inner loop keeps
/// re-evaluating the queue until nothing more can progress.
#[tokio::test]
async fn a_dependent_task_is_never_assigned_before_its_dependency_completes_and_then_unblocks_automatically() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let backend = runtime.create_task(&project.id, None, "Backend", "", "", None, false).await.unwrap();
    let tests = runtime.create_task(&project.id, None, "Tests", "", "", None, false).await.unwrap();
    runtime.add_task_dependency(&tests.id, &backend.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 2, "Backend completes, which automatically unblocks and completes Tests too");
    assert_eq!(runtime.state.tasks[&backend.id].status, TaskStatus::Done);
    assert_eq!(runtime.state.tasks[&tests.id].status, TaskStatus::Done);

    fn task_id_of(e: &Event) -> Option<&str> {
        e.payload.get("task_id").and_then(|v| v.as_str())
    }

    let events = runtime.list_events().await.unwrap();
    let backend_completed_at = events
        .iter()
        .position(|e| e.event_type == EventType::TaskCompleted && task_id_of(e) == Some(backend.id.as_str()))
        .expect("backend completed");
    let tests_assigned_at = events
        .iter()
        .position(|e| e.event_type == EventType::TaskAssigned && task_id_of(e) == Some(tests.id.as_str()))
        .expect("tests was assigned");
    assert!(backend_completed_at < tests_assigned_at, "Tests must never be assigned before Backend completes");
}

/// Phase 2.4.5's central acceptance criteria: every task and decision
/// `plan_task` generates is traceable to a `PlanningSummary` artifact via a
/// planning Run, and that artifact carries a non-empty reason for each one
/// -- not just creatable, but explainable.
#[tokio::test]
async fn plan_task_produces_a_traceable_planning_run_and_summary_artifact() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(
            &project.id,
            None,
            "Build OAuth",
            "- Backend\n- Frontend\n- What auth provider should we use?",
            "",
            None,
            false,
        )
        .await
        .unwrap();

    let outcome = runtime.plan_task(&root.id).await.unwrap();
    assert_eq!(outcome.tasks.len(), 2);
    assert_eq!(outcome.decisions.len(), 1);

    // The planning Run exists, completed, with the real model runtime's
    // provider name (Phase 2.5A: planning is a real model call now, so its
    // Run's provider reflects whichever runtime actually ran it -- same
    // convention a worker Run already uses -- rather than a hardcoded
    // "planner" label. Nothing identifies "is this a planning run" by
    // provider string; that's always done via the sibling PlanningSummary
    // artifact below.
    let run = runtime.state.runs.get(&outcome.planning_run_id).expect("planning run recorded");
    assert_eq!(run.provider, "stub");
    assert_eq!(run.status, RunStatus::Completed);
    assert_eq!(run.task_id, root.id);

    // Its PlanningSummary artifact is the traceable audit trail.
    let artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == outcome.planning_run_id && a.kind == ArtifactKind::PlanningSummary)
        .expect("planning summary artifact recorded");
    let summary: PlanningSummary = serde_json::from_str(&artifact.content).expect("valid PlanningSummary JSON");

    assert_eq!(summary.planned_task_id, root.id);
    assert_eq!(summary.tasks.len(), 2);
    assert!(summary.tasks.iter().all(|t| !t.reason.is_empty()), "every generated task has a reason");
    assert!(
        summary.tasks.iter().all(|t| !t.target_score_reason.is_empty() && !t.success_criteria_reason.is_empty()),
        "every generated task's score and success criteria are explained, even when defaulted"
    );
    assert_eq!(summary.decisions.len(), 1);
    assert!(!summary.decisions[0].reason.is_empty(), "the escalated decision has a reason");
    assert!(summary.dependencies.is_empty(), "the stub planner generates no dependencies yet");
    assert_eq!(summary.metrics.task_count, 2);
    assert_eq!(summary.metrics.blocked_task_count, 1);

    // The model's raw response (Phase 2.5A's "Planner Response" debugging
    // tool) is visible "for free" as the planning run's response artifact,
    // through the existing Task Detail view -- distinct from
    // `summary.summary` above, which is the rendered human-readable text a
    // person actually reads.
    let response_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == outcome.planning_run_id && a.kind == ArtifactKind::Response)
        .expect("response artifact recorded");
    let raw: serde_json::Value = serde_json::from_str(&response_artifact.content).expect("raw model response is JSON");
    assert!(raw.get("tasks").is_some(), "the raw planner response, not the rendered summary, is recorded");
    assert_ne!(response_artifact.content, summary.summary);

    // The model's overall reasoning is also captured, distinct from each
    // task's individual reason, and folded into the rendered summary too.
    assert_eq!(summary.task_generation_reasoning, "stub planning reasoning for tests");
    assert!(summary.summary.contains("stub planning reasoning for tests"));

    // The planner's prompt is likewise visible "for free" as a Prompt
    // artifact on the same Run.
    let prompt_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == outcome.planning_run_id && a.kind == ArtifactKind::Prompt)
        .expect("prompt artifact recorded");
    assert!(prompt_artifact.content.contains("Build OAuth"));
}

/// A model that always returns explicit, non-empty success criteria for
/// the one task it generates -- standing in for a real Phase 2.5B model
/// response, since the legacy stub planner `stub_planning_result` falls
/// back to never generates any.
struct CriteriaModelRuntime;

impl ModelRuntime for CriteriaModelRuntime {
    fn provider(&self) -> &'static str {
        "criteria-stub"
    }

    fn execute(&self, request: &ExecutionRequest, _on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
        assert!(request.json_schema.is_some(), "this fake is only used for planning calls");
        let structured = serde_json::json!({
            "reasoning": "One backend task covers the whole OAuth objective.",
            "tasks": [{
                "title": "Implement OAuth Backend",
                "description": "",
                "reason": "Backend work for OAuth login.",
                "success_criteria": [
                    "Google OAuth login succeeds",
                    "GitHub OAuth login succeeds",
                    "Session creation works",
                ],
                "success_criteria_reason": "These are the minimum outcomes for a complete OAuth backend.",
                "target_score": 95.0,
                "target_score_reason": "Authentication systems require elevated quality and security standards.",
            }],
            "decisions": [],
        });
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "criteria stub planning response".to_string(),
            raw_response: structured.to_string(),
            structured_output: Some(structured),
            cost_usd: None,
        })
    }
}

/// Phase 2.5B's central acceptance criteria: the planner generates success
/// criteria as a real list (not a blob of text), every criterion's
/// presence is explained, and the criteria reach `Task.success_criteria`
/// itself -- not just the planning artifact.
#[tokio::test]
async fn plan_task_stores_model_generated_success_criteria_as_a_list_and_traces_them() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(CriteriaModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let root = runtime
        .create_task(&project.id, None, "Build OAuth", "Add OAuth login.", "", None, false)
        .await
        .unwrap();

    let outcome = runtime.plan_task(&root.id).await.unwrap();
    assert_eq!(outcome.tasks.len(), 1);
    let generated = &outcome.tasks[0];

    // Stored as a real list on the task itself, not a single string.
    assert_eq!(
        generated.success_criteria,
        vec!["Google OAuth login succeeds", "GitHub OAuth login succeeds", "Session creation works"]
    );

    let artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.run_id == outcome.planning_run_id && a.kind == ArtifactKind::PlanningSummary)
        .expect("planning summary artifact recorded");
    let summary: PlanningSummary = serde_json::from_str(&artifact.content).expect("valid PlanningSummary JSON");

    let explanation = summary.tasks.iter().find(|t| t.task_id == generated.id).expect("task explained");
    assert_eq!(explanation.success_criteria, generated.success_criteria);
    assert!(explanation.success_criteria_reason.contains("minimum outcomes"));

    // Coverage metrics reflect the one fully-covered task.
    assert_eq!(summary.metrics.tasks_with_criteria, 1);
    assert_eq!(summary.metrics.average_criteria_per_task, 3.0);
    assert_eq!(summary.metrics.criteria_coverage_percent, 100.0);
    assert!(summary.summary.contains("3 criteria"));

    // Phase 2.5C: the model-chosen target score is stored on the task
    // itself (not the hardcoded 70.0 default), explained, and reflected in
    // this pass's score metrics.
    assert_eq!(generated.target_score, 95.0);
    assert_eq!(explanation.target_score, 95.0);
    assert!(explanation.target_score_reason.contains("elevated quality"));
    assert_eq!(summary.metrics.average_target_score, 95.0);
    assert_eq!(summary.metrics.highest_target_score, 95.0);
    assert_eq!(summary.metrics.lowest_target_score, 95.0);
    let band_95_100 = summary.metrics.score_distribution.iter().find(|b| b.band == "95-100").unwrap();
    assert_eq!(band_95_100.count, 1);
    assert!(summary.summary.contains("Score Distribution"));
}
