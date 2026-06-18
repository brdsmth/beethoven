//! Phase 1.9a (Mission Control): a read-only Tauri command layer over the
//! same event-sourced state the CLI reads, via `beethoven_db::SqliteEventStore`
//! and `beethoven_core::RuntimeState::replay`. No `Runtime`/`ModelRuntime` is
//! constructed here since every command in this phase is a pure read.

pub mod commands;
pub mod derive;
pub mod dto;
pub mod state;
pub mod watcher;

pub use state::AppState;
