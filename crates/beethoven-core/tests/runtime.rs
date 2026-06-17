use async_trait::async_trait;
use beethoven_core::{GoalStatus, InboxStatus, Runtime};
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
    let workspaces_dir = dir.path().join("workspaces");

    let mut runtime = Runtime::load(Box::new(store.clone()), workspaces_dir.clone())
        .await
        .unwrap();

    let goal = runtime
        .create_goal(
            "Build login page",
            "- Implement the login form\n- What auth provider should we use?\n- Write the landing page copy",
        )
        .await
        .unwrap();

    let tasks = runtime.plan_goal(&goal.id).await.unwrap();
    assert_eq!(tasks.len(), 3);

    let report = runtime.run_tick().await.unwrap();
    // "Implement..." spawns and finishes a subtask, "Write the landing page copy"
    // completes directly, and "What auth provider..." escalates a question.
    assert_eq!(report.completed, 3);
    assert_eq!(report.escalated, 1);
    assert_eq!(report.subtasks_spawned, 1);

    // Simulate a process restart: drop this runtime and build a fresh one from
    // the same persisted event log. State must reconstruct identically.
    drop(runtime);
    let mut runtime = Runtime::load(Box::new(store.clone()), workspaces_dir)
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

    let report = runtime.run_tick().await.unwrap();
    assert_eq!(report.completed, 1);
    assert_eq!(report.escalated, 0);
    assert_eq!(report.subtasks_spawned, 0);

    assert_eq!(runtime.state.goals[&goal.id].status, GoalStatus::Completed);
    assert!(runtime.state.tasks.values().all(|t| t.status == beethoven_core::TaskStatus::Done));

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

    let mut runtime = Runtime::load(Box::new(store), dir.path().join("workspaces"))
        .await
        .unwrap();

    let goal = runtime
        .create_goal("Goal", "- What should we do?")
        .await
        .unwrap();
    runtime.plan_goal(&goal.id).await.unwrap();
    runtime.run_tick().await.unwrap();

    let inbox_id = runtime.state.inbox.keys().next().unwrap().clone();
    runtime.answer_inbox(&inbox_id, "Answer").await.unwrap();

    let result = runtime.answer_inbox(&inbox_id, "Answer again").await;
    assert!(result.is_err());
}
