use async_trait::async_trait;
use beethoven_agents::{AgentsError, ExecutionRequest, ExecutionResult, ExecutionStatus, ModelRuntime};
use beethoven_core::{
    AgentKind, AgentStatus, ArtifactKind, DecisionStatus, GoalStatus, InboxKind, InboxStatus, InitiativeStatus,
    ModelRunStatus, ProjectStatus, ReviewStatus, Runtime, TaskStatus,
};
use beethoven_events::{Event, EventStore, EventStoreError, EventType};
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

/// Replaces a real model call with a real tmux round trip: appends the
/// rendered prompt to NOTES.md inside the worktree, so tests still exercise
/// genuine on-disk work without invoking `claude`.
struct StubModelRuntime;

impl ModelRuntime for StubModelRuntime {
    fn provider(&self) -> &'static str {
        "stub"
    }

    fn execute(&self, request: &ExecutionRequest) -> Result<ExecutionResult, AgentsError> {
        let session = TmuxSession { name: request.workspace.tmux_session.clone() };
        let command = format!("cat '{}' >> NOTES.md", request.prompt_path.display());
        let output = session.run_and_wait(&command, Duration::from_secs(10))?;
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

    fn execute(&self, request: &ExecutionRequest) -> Result<ExecutionResult, AgentsError> {
        let start = Instant::now();
        std::thread::sleep(Duration::from_millis(300));
        let session = TmuxSession { name: request.workspace.tmux_session.clone() };
        let command = format!("cat '{}' >> NOTES.md", request.prompt_path.display());
        let output = session.run_and_wait(&command, Duration::from_secs(10))?;
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

    fn execute(&self, request: &ExecutionRequest) -> Result<ExecutionResult, AgentsError> {
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

    fn execute(&self, _request: &ExecutionRequest) -> Result<ExecutionResult, AgentsError> {
        Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "noop stub: made no changes".to_string(),
            raw_response: "ok".to_string(),
            structured_output: None,
            cost_usd: None,
        })
    }
}

#[tokio::test]
async fn goal_plan_run_and_answer_flow_survives_a_simulated_restart() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");

    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root.clone(), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime
        .create_project("Sample", dir.path().join("repo"), "main")
        .await
        .unwrap();
    assert_eq!(project.slug, "sample");

    let goal = runtime
        .create_goal(
            &project.id,
            "Build login page",
            "- Implement the login form\n- What auth provider should we use?\n- Write the landing page copy",
        )
        .await
        .unwrap();

    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    assert_eq!(tasks.len(), 3);

    let report = runtime.run_tick(&project.id).await.unwrap();
    // "Implement..." spawns and finishes a subtask, "Write the landing page copy"
    // completes directly, and "What auth provider..." escalates a question.
    assert_eq!(report.completed, 3);
    assert_eq!(report.escalated, 1);
    assert_eq!(report.subtasks_spawned, 1);

    // Simulate a process restart: drop this runtime and build a fresh one from
    // the same persisted event log. State must reconstruct identically.
    drop(runtime);
    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root, Box::new(StubModelRuntime))
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

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.escalated, 0);
    assert_eq!(report.subtasks_spawned, 0);

    assert_eq!(runtime.state.goals[&goal.id].status, GoalStatus::Completed);
    assert!(runtime
        .state
        .tasks
        .values()
        .all(|t| t.status == beethoven_core::TaskStatus::Done));

    // Recovery: the full event log replays into the exact same terminal state again.
    let events = runtime.list_events().await.unwrap();
    let replayed = beethoven_core::RuntimeState::replay(&events);
    assert_eq!(replayed.goals[&goal.id].status, GoalStatus::Completed);
    assert_eq!(replayed.tasks.len(), runtime.state.tasks.len());
}

#[tokio::test]
async fn answering_an_already_resolved_inbox_item_errors() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Solo", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Goal", "- What should we do?")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
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

    // Each project bootstraps its own 6 agent instances from the shared templates.
    assert_eq!(runtime.state.agent_templates.len(), 6);
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
    assert_eq!(sample_instances.len(), 6);
    assert_eq!(sailboat_instances.len(), 6);
    assert_eq!(runtime.state.agent_instances.len(), 12);

    let sample_goal = runtime
        .create_goal(&sample.id, "Sample goal", "- Sample task")
        .await
        .unwrap();
    let sailboat_goal = runtime
        .create_goal(&sailboat.id, "Sailboat goal", "- Sailboat task")
        .await
        .unwrap();

    runtime.plan_goal(&sample_goal.id).await.unwrap();
    runtime.plan_goal(&sailboat_goal.id).await.unwrap();

    // Running only Sample's tick must not touch Sailboat's task at all.
    let sample_report = runtime.run_tick(&sample.id).await.unwrap();
    assert_eq!(sample_report.completed, 1);

    let sailboat_task = runtime
        .state
        .tasks
        .values()
        .find(|t| t.project_id == sailboat.id)
        .unwrap();
    assert_eq!(sailboat_task.status, TaskStatus::Ready);
    assert_eq!(sailboat_task.assigned_agent_id, None);

    // Running Sailboat's tick independently now completes its own task.
    let sailboat_report = runtime.run_tick(&sailboat.id).await.unwrap();
    assert_eq!(sailboat_report.completed, 1);

    let sample_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == sample.id).collect();
    let sailboat_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == sailboat.id).collect();
    assert_eq!(sample_tasks.len(), 1);
    assert_eq!(sailboat_tasks.len(), 1);
    assert!(sample_tasks.iter().all(|t| t.status == TaskStatus::Done));
    assert!(sailboat_tasks.iter().all(|t| t.status == TaskStatus::Done));
}

#[tokio::test]
async fn a_completed_task_records_a_real_execution_run_diff_artifact_and_approved_review() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Docs", "- Write the changelog")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.needs_changes, 0);
    assert_eq!(report.escalated, 0);

    let task = runtime.state.tasks.values().next().unwrap().clone();
    assert_eq!(task.status, TaskStatus::Done);
    assert_eq!(task.branch.as_deref(), Some("task/write-the-changelog"));
    assert!(task.pr_url.is_some());

    // The real tmux/model stub appended the rendered prompt to NOTES.md inside
    // the worktree, so the captured diff (and the run summary) reflect genuine
    // on-disk work rather than a deterministic stand-in.
    let run = runtime.state.model_runs.values().find(|r| r.task_id.as_deref() == Some(task.id.as_str())).unwrap();
    assert_eq!(run.status, ModelRunStatus::Completed);
    assert!(run.summary.as_deref().unwrap().contains("stub"));

    let diff_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.task_id == task.id && a.kind == ArtifactKind::Diff)
        .unwrap();
    assert!(diff_artifact.content.contains("Write the changelog"));

    let review = runtime.state.reviews.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(review.status, ReviewStatus::Approved);
}

#[tokio::test]
async fn a_model_run_that_reinitializes_git_inside_its_worktree_still_yields_a_real_diff() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(GitClobberingModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Docs", "- Write the landing page copy")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.needs_changes, 0);
    assert_eq!(report.escalated, 0);

    let task = runtime.state.tasks.values().next().unwrap().clone();
    assert_eq!(task.status, TaskStatus::Done);

    // Without restoring the worktree's `.git` link before computing the
    // diff, this would have come back empty -- the model's own `git init`
    // replaced it with a disjoint repo that already had everything
    // committed, hiding the real change from status/diff.
    let diff_artifact = runtime
        .state
        .artifacts
        .values()
        .find(|a| a.task_id == task.id && a.kind == ArtifactKind::Diff)
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Ops", "- Run a risky database migration")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 0);
    assert_eq!(report.escalated, 1);

    let task = runtime.state.tasks.values().next().unwrap().clone();
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
async fn initiative_completes_only_once_its_epic_task_is_done_and_its_decision_is_resolved() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();

    let initiative = runtime
        .create_initiative(&project.id, "Build Gmail Agent", "Routes inbound email to the right team")
        .await
        .unwrap();
    assert_eq!(initiative.status, InitiativeStatus::Discovery);

    runtime.create_requirement(&initiative.id, "Support PDF attachments").await.unwrap();
    runtime
        .create_requirement(&initiative.id, "What auth provider should we use?")
        .await
        .unwrap();

    let outcome = runtime.plan_initiative(&initiative.id).await.unwrap();
    assert_eq!(outcome.epics.len(), 1);
    assert_eq!(outcome.tasks.len(), 1);
    assert_eq!(outcome.decisions.len(), 1);
    assert_eq!(runtime.state.initiatives[&initiative.id].status, InitiativeStatus::Planned);

    let open_decision_inbox: Vec<_> = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.kind == InboxKind::Decision && i.status == InboxStatus::Open)
        .collect();
    assert_eq!(open_decision_inbox.len(), 1);
    assert_eq!(open_decision_inbox[0].decision_id, Some(outcome.decisions[0].id.clone()));

    // "Implement: ..." spawns and finishes a subtask before the epic's task itself completes.
    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 2);
    assert_eq!(report.subtasks_spawned, 1);
    assert_eq!(report.escalated, 0);

    // The epic's task is done, but the decision is still open, so the initiative cannot complete yet.
    assert_eq!(runtime.state.initiatives[&initiative.id].status, InitiativeStatus::Planned);

    let decision = runtime
        .resolve_decision(&outcome.decisions[0].id, "Use OAuth via Google + GitHub")
        .await
        .unwrap();
    assert_eq!(decision.status, DecisionStatus::Resolved);

    assert_eq!(runtime.state.initiatives[&initiative.id].status, InitiativeStatus::Completed);
}

#[tokio::test]
async fn an_initiative_with_no_requirements_plans_a_single_fallback_epic() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let initiative = runtime
        .create_initiative(&project.id, "Build Gmail Agent", "")
        .await
        .unwrap();

    let outcome = runtime.plan_initiative(&initiative.id).await.unwrap();
    assert_eq!(outcome.epics.len(), 1);
    assert_eq!(outcome.epics[0].title, "Build Gmail Agent");
    assert_eq!(outcome.decisions.len(), 0);
    assert_eq!(outcome.tasks.len(), 1);
    assert_eq!(outcome.tasks[0].epic_id, Some(outcome.epics[0].id.clone()));
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

    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root.clone(), Box::new(StubModelRuntime))
        .await
        .unwrap();
    let project = runtime.create_project("Sample", repo.clone(), "main").await.unwrap();
    // A plain title (no "implement"/"?") so re-running it post-recovery
    // completes directly instead of spawning a subtask or raising a question.
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    let task = tasks[0].clone();

    let agent = runtime
        .state
        .agent_instances
        .values()
        .find(|a| a.project_id == project.id && a.kind != AgentKind::ProjectManager)
        .unwrap()
        .clone();

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
            agent_label: agent.kind.as_str(),
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
    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root, Box::new(StubModelRuntime))
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
        Box::new(store),
        dir.path().join("projects"),
        Box::new(SlowModelRuntime { windows: windows.clone() }),
    )
    .await
    .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Docs", "- Write the changelog\n- Write the readme")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    // A repository path that was never `git init`-ed: `git worktree add`
    // will fail for every task in this project.
    let broken = runtime
        .create_project("Broken", dir.path().join("does-not-exist"), "main")
        .await
        .unwrap();
    let broken_goal = runtime.create_goal(&broken.id, "Docs", "- Write the changelog").await.unwrap();
    runtime.plan_goal(&broken_goal.id).await.unwrap();

    init_repo(&dir.path().join("good-repo"));
    let healthy = runtime.create_project("Healthy", dir.path().join("good-repo"), "main").await.unwrap();
    let healthy_goal = runtime.create_goal(&healthy.id, "Docs", "- Write the changelog").await.unwrap();
    runtime.plan_goal(&healthy_goal.id).await.unwrap();

    let mut reports = runtime.tick_projects(&[broken.id.clone(), healthy.id.clone()], 4, 3).await.unwrap();

    let broken_report = reports.remove(&broken.id).unwrap();
    assert_eq!(broken_report.escalated, 1);
    assert_eq!(broken_report.completed, 0);
    let broken_task = runtime.state.tasks.values().find(|t| t.project_id == broken.id).unwrap();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(NoOpModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    // NoOpModelRuntime never touches the worktree, so the diff is always
    // empty and ReviewService::review always returns NeedsChanges -- this
    // would retry forever within tick_projects' own drain loop without the
    // attempt cap.
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let mut reports = runtime.tick_projects(std::slice::from_ref(&project.id), 4, 2).await.unwrap();
    let report = reports.remove(&project.id).unwrap();

    // One ordinary rejection (attempt 1 -> 2), then the next rejection at
    // attempt 2 hits the cap and escalates instead of looping forever.
    assert_eq!(report.needs_changes, 1);
    assert_eq!(report.escalated, 1);
    assert_eq!(report.completed, 0);

    let task = runtime.state.tasks.values().next().unwrap();
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

    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root.clone(), Box::new(StubModelRuntime))
        .await
        .unwrap();
    let project = runtime.create_project("Sample", repo.clone(), "main").await.unwrap();
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    let task = tasks[0].clone();

    let agent = runtime
        .state
        .agent_instances
        .values()
        .find(|a| a.project_id == project.id && a.kind != AgentKind::ProjectManager)
        .unwrap()
        .clone();

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
    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root, Box::new(StubModelRuntime))
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let paused = runtime.pause_project(&project.id).await.unwrap();
    assert_eq!(paused.status, ProjectStatus::Paused);

    // Pausing an already-paused project is invalid.
    assert!(runtime.pause_project(&project.id).await.is_err());

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.assigned, 0);
    assert_eq!(report.completed, 0);
    assert_eq!(runtime.state.tasks.values().next().unwrap().status, TaskStatus::Ready);

    let resumed = runtime.resume_project(&project.id).await.unwrap();
    assert_eq!(resumed.status, ProjectStatus::Active);

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.completed, 1);
}

#[tokio::test]
async fn archive_project_removes_it_from_active_execution_entirely() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();

    let archived = runtime.archive_project(&project.id).await.unwrap();
    assert_eq!(archived.status, ProjectStatus::Archived);

    let report = runtime.run_tick(&project.id).await.unwrap();
    assert_eq!(report.assigned, 0);
    assert_eq!(report.completed, 0);
    assert_eq!(runtime.state.tasks.values().next().unwrap().status, TaskStatus::Ready);

    assert!(runtime.archive_project(&project.id).await.is_err());
}

#[tokio::test]
async fn retry_task_returns_a_blocked_task_to_ready_for_another_attempt() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Ops", "- Run a risky database migration")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().next().unwrap().clone();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    let task = tasks[0].clone();

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

    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root.clone(), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Ops", "- Run a risky database migration")
        .await
        .unwrap();
    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    let task = tasks[0].clone();

    let mut candidates = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == project.id && a.kind != AgentKind::ProjectManager)
        .cloned();
    let old_agent = candidates.next().unwrap();
    let new_agent = candidates.next().unwrap();

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
    let mut runtime = Runtime::load(Box::new(store), projects_root, Box::new(StubModelRuntime)).await.unwrap();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Ops", "- Run a risky database migration")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().next().unwrap().clone();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let goal = runtime
        .create_goal(&project.id, "Ops", "- Run a risky database migration")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();
    runtime.run_tick(&project.id).await.unwrap();

    let task = runtime.state.tasks.values().next().unwrap().clone();
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), Box::new(StubModelRuntime))
        .await
        .unwrap();

    init_repo(&dir.path().join("repo"));
    let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
    let agent = runtime
        .state
        .agent_instances
        .values()
        .find(|a| a.project_id == project.id && a.kind != AgentKind::ProjectManager)
        .unwrap()
        .clone();
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
