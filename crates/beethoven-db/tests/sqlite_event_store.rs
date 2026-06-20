use beethoven_db::SqliteEventStore;
use beethoven_events::{Event, EventScope, EventStore, EventType};
use serde_json::json;

#[tokio::test]
async fn append_and_list_all_round_trips_in_insertion_order() {
    let dir = tempfile::tempdir().unwrap();
    let store = SqliteEventStore::connect(&dir.path().join("test.db")).await.unwrap();

    store
        .append(Event::for_project("p1", EventType::DecisionCreated, "human", json!({"id": "d1"})))
        .await
        .unwrap();
    store
        .append(Event::for_project("p1", EventType::TaskCreated, "pm", json!({"id": "t1"})))
        .await
        .unwrap();

    let events = store.list_all().await.unwrap();

    assert_eq!(events.len(), 2);
    assert_eq!(events[0].event_type, EventType::DecisionCreated);
    assert_eq!(events[0].actor_id, Some("human".to_string()));
    assert_eq!(events[0].scope, EventScope::Project("p1".to_string()));
    assert_eq!(events[1].event_type, EventType::TaskCreated);
    assert_eq!(events[1].payload, json!({"id": "t1"}));
}

#[tokio::test]
async fn global_scope_round_trips_with_no_project_id() {
    let dir = tempfile::tempdir().unwrap();
    let store = SqliteEventStore::connect(&dir.path().join("test.db")).await.unwrap();

    store
        .append(Event::global(EventType::ProjectCreated, "human", json!({"id": "p1"})))
        .await
        .unwrap();

    let events = store.list_all().await.unwrap();

    assert_eq!(events.len(), 1);
    assert_eq!(events[0].scope, EventScope::Global);
}

#[tokio::test]
async fn events_persist_across_a_reconnect() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("test.db");

    {
        let store = SqliteEventStore::connect(&db_path).await.unwrap();
        store
            .append(Event::for_project("p1", EventType::DecisionCreated, "human", json!({"id": "d1"})))
            .await
            .unwrap();
    }

    let store = SqliteEventStore::connect(&db_path).await.unwrap();
    let events = store.list_all().await.unwrap();

    assert_eq!(events.len(), 1);
    assert_eq!(events[0].event_type, EventType::DecisionCreated);
}

#[tokio::test]
async fn connect_creates_missing_parent_directories() {
    let dir = tempfile::tempdir().unwrap();
    let nested_path = dir.path().join("nested/dirs/test.db");

    let store = SqliteEventStore::connect(&nested_path).await.unwrap();
    store
        .append(Event::global(EventType::ProjectCreated, "human", json!({})))
        .await
        .unwrap();

    assert!(nested_path.exists());
}
