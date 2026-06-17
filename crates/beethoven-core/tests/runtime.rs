use async_trait::async_trait;
use beethoven_core::{GoalStatus, InboxStatus, Runtime, TaskStatus};
use beethoven_events::{Event, EventStore, EventStoreError};
use std::sync::{Arc, Mutex};

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

    let project = runtime
        .create_project("Punch", dir.path().join("repo"), "main")
        .await
        .unwrap();
    assert_eq!(project.slug, "punch");

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

    let punch = runtime
        .create_project("Punch", dir.path().join("punch-repo"), "main")
        .await
        .unwrap();
    let sailboat = runtime
        .create_project("Sailboat", dir.path().join("sailboat-repo"), "main")
        .await
        .unwrap();

    // Each project bootstraps its own 6 agent instances from the shared templates.
    assert_eq!(runtime.state.agent_templates.len(), 6);
    let punch_instances: Vec<_> = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == punch.id)
        .collect();
    let sailboat_instances: Vec<_> = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == sailboat.id)
        .collect();
    assert_eq!(punch_instances.len(), 6);
    assert_eq!(sailboat_instances.len(), 6);
    assert_eq!(runtime.state.agent_instances.len(), 12);

    let punch_goal = runtime
        .create_goal(&punch.id, "Punch goal", "- Punch task")
        .await
        .unwrap();
    let sailboat_goal = runtime
        .create_goal(&sailboat.id, "Sailboat goal", "- Sailboat task")
        .await
        .unwrap();

    runtime.plan_goal(&punch_goal.id).await.unwrap();
    runtime.plan_goal(&sailboat_goal.id).await.unwrap();

    // Running only Punch's tick must not touch Sailboat's task at all.
    let punch_report = runtime.run_tick(&punch.id).await.unwrap();
    assert_eq!(punch_report.completed, 1);

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

    let punch_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == punch.id).collect();
    let sailboat_tasks: Vec<_> = runtime.state.tasks.values().filter(|t| t.project_id == sailboat.id).collect();
    assert_eq!(punch_tasks.len(), 1);
    assert_eq!(sailboat_tasks.len(), 1);
    assert!(punch_tasks.iter().all(|t| t.status == TaskStatus::Done));
    assert!(sailboat_tasks.iter().all(|t| t.status == TaskStatus::Done));
}
