//! Mission Control's Tauri command layer over the same event-sourced state
//! the CLI reads. Read commands replay a fresh `RuntimeState` snapshot from
//! `beethoven_db::SqliteEventStore::list_all()` on every call (1.9a). Mutating
//! commands (1.9b) lock a real `beethoven_core::Runtime` held in `AppState`,
//! refresh it against the store, and call its methods directly -- the Rust
//! runtime remains the sole owner of orchestration; no command in this crate
//! performs orchestration logic itself.

pub mod commands;
pub mod derive;
pub mod dto;
pub mod state;
pub mod watcher;

pub use state::AppState;
