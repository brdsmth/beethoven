use async_trait::async_trait;
use beethoven_core::{
    ArtifactKind, ExecutionRunStatus, GoalStatus, InboxKind, InboxStatus, ReviewStatus, Runtime, TaskStatus,
};
use beethoven_events::{Event, EventStore, EventStoreError};
use std::path::Path;
use std::process::Command;
use std::sync::{Arc, Mutex};

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

#[tokio::test]
async fn goal_plan_run_and_answer_flow_survives_a_simulated_restart() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();
    let projects_root = dir.path().join("projects");

    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root.clone())
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
    let mut runtime = Runtime::load(Box::new(store.clone()), projects_root)
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"))
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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects")).await.unwrap();

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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects")).await.unwrap();

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
    let run = runtime.state.execution_runs.values().find(|r| r.task_id == task.id).unwrap();
    assert_eq!(run.status, ExecutionRunStatus::Completed);
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
async fn review_blocks_a_risky_task_and_escalates_through_the_existing_inbox_flow() {
    let store = InMemoryStore::default();
    let dir = tempfile::tempdir().unwrap();

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects")).await.unwrap();

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
