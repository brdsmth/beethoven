use beethoven_core::Runtime;
use beethoven_db::SqliteEventStore;
use std::path::PathBuf;
use std::sync::atomic::AtomicBool;
use std::sync::Arc;
use tauri::async_runtime::JoinHandle;
use tokio::sync::Mutex;

/// A desktop-process-local daemon loop started by `runtime_start` and torn
/// down by `runtime_stop`/`runtime_restart`. Mirrors `beethoven-cli`'s
/// `run_daemon_loop` shape (recover -> mark_started -> tick on interval ->
/// mark_stopped) but lives in this process so Mission Control's Runtime
/// Controls work without a separate `beethoven start` CLI invocation.
pub struct DaemonHandle {
    pub shutdown: Arc<AtomicBool>,
    pub task: JoinHandle<()>,
}

/// Shared app state: a read-only `SqliteEventStore` (used by every read
/// command via a fresh `RuntimeState::replay` on each call, as in 1.9a) plus
/// a mutable `Runtime`, added in 1.9b so the UI can initiate real
/// orchestration actions. Every mutating command locks `runtime`, calls
/// `Runtime::refresh()` to close the gap against any other writer to the
/// same database (e.g. a concurrently running `beethoven start` CLI daemon),
/// then calls the real `Runtime` method.
pub struct AppState {
    pub store: SqliteEventStore,
    pub home: PathBuf,
    pub runtime: Mutex<Runtime>,
    pub daemon: Mutex<Option<DaemonHandle>>,
}
