use beethoven_db::SqliteEventStore;
use std::path::PathBuf;

/// Shared, read-only app state: one `SqliteEventStore` opened once at
/// startup and reused across every command. There's no `Mutex` because
/// every command is a pure read -- each re-replays a fresh `RuntimeState`
/// snapshot from `store.list_all()` rather than mutating shared state.
pub struct AppState {
    pub store: SqliteEventStore,
    pub home: PathBuf,
}
