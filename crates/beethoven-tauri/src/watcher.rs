use beethoven_db::SqliteEventStore;
use beethoven_events::EventStore;
use std::time::Duration;
use tauri::{AppHandle, Emitter};

/// Polls the event log every 1.5s and emits a single coarse `state-changed`
/// event whenever the event count changes -- the realtime push channel for
/// this phase, in place of a literal websocket server. The frontend listens
/// for it and invalidates its TanStack Query cache, refetching everything
/// currently mounted.
pub fn spawn_change_watcher(app: AppHandle, store: SqliteEventStore) {
    tauri::async_runtime::spawn(async move {
        let mut last_count = store.list_all().await.map(|events| events.len()).unwrap_or(0);
        loop {
            tokio::time::sleep(Duration::from_millis(1500)).await;
            match store.list_all().await {
                Ok(events) => {
                    if events.len() != last_count {
                        last_count = events.len();
                        let _ = app.emit("state-changed", ());
                    }
                }
                Err(e) => eprintln!("change watcher: failed to list events: {e}"),
            }
        }
    });
}
