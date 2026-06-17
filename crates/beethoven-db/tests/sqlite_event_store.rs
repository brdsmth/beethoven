use beethoven_db::SqliteEventStore;
use beethoven_events::{Event, EventStore, EventType};
use serde_json::json;

#[tokio::test]
async fn append_and_list_all_round_trips_in_insertion_order() {
    let dir = tempfile::tempdir().unwrap();
    let store = SqliteEventStore::connect(&dir.path().join("test.db")).await.unwrap();

    store
        .append(Event::new(EventType::GoalCreated, "human", json!({"id": "g1"})))
        .await
        .unwrap();
    store
        .append(Event::new(EventType::TaskCreated, "pm", json!({"id": "t1"})))
        .await
        .unwrap();

    let events = store.list_all().await.unwrap();

    assert_eq!(events.len(), 2);
    assert_eq!(events[0].event_type, EventType::GoalCreated);
    assert_eq!(events[0].actor_id, "human");
    assert_eq!(events[1].event_type, EventType::TaskCreated);
    assert_eq!(events[1].payload, json!({"id": "t1"}));
}

#[tokio::test]
async fn events_persist_across_a_reconnect() {
    let dir = tempfile::tempdir().unwrap();
    let db_path = dir.path().join("test.db");

    {
        let store = SqliteEventStore::connect(&db_path).await.unwrap();
        store
            .append(Event::new(EventType::GoalCreated, "human", json!({"id": "g1"})))
            .await
            .unwrap();
    }

    let store = SqliteEventStore::connect(&db_path).await.unwrap();
    let events = store.list_all().await.unwrap();

    assert_eq!(events.len(), 1);
    assert_eq!(events[0].event_type, EventType::GoalCreated);
}

#[tokio::test]
async fn connect_creates_missing_parent_directories() {
    let dir = tempfile::tempdir().unwrap();
    let nested_path = dir.path().join("nested/dirs/test.db");

    let store = SqliteEventStore::connect(&nested_path).await.unwrap();
    store
        .append(Event::new(EventType::GoalCreated, "human", json!({})))
        .await
        .unwrap();

    assert!(nested_path.exists());
}
