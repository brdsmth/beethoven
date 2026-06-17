use async_trait::async_trait;
use beethoven_events::{Event, EventStore, EventStoreError, EventType};
use chrono::{DateTime, Utc};
use sqlx::sqlite::{SqliteConnectOptions, SqlitePoolOptions};
use sqlx::{Row, SqlitePool};
use std::path::{Path, PathBuf};
use std::str::FromStr;

#[derive(Debug, thiserror::Error)]
pub enum DbError {
    #[error("io error: {0}")]
    Io(#[from] std::io::Error),
    #[error("sqlite error: {0}")]
    Sqlx(#[from] sqlx::Error),
}

pub fn beethoven_home() -> PathBuf {
    let home = std::env::var("HOME").unwrap_or_else(|_| ".".to_string());
    PathBuf::from(home).join(".beethoven")
}

pub fn ensure_beethoven_home() -> Result<PathBuf, DbError> {
    let home = beethoven_home();
    for sub in ["events", "workspaces", "artifacts", "logs"] {
        std::fs::create_dir_all(home.join(sub))?;
    }
    Ok(home)
}

pub fn default_db_path() -> PathBuf {
    beethoven_home().join("beethoven.db")
}

#[derive(Clone)]
pub struct SqliteEventStore {
    pool: SqlitePool,
}

impl SqliteEventStore {
    pub async fn connect(db_path: &Path) -> Result<Self, DbError> {
        if let Some(parent) = db_path.parent() {
            std::fs::create_dir_all(parent)?;
        }

        let options = SqliteConnectOptions::new()
            .filename(db_path)
            .create_if_missing(true);
        let pool = SqlitePoolOptions::new().connect_with(options).await?;

        sqlx::query(
            "CREATE TABLE IF NOT EXISTS events (
                seq INTEGER PRIMARY KEY AUTOINCREMENT,
                id TEXT NOT NULL UNIQUE,
                event_type TEXT NOT NULL,
                actor_id TEXT NOT NULL,
                payload TEXT NOT NULL,
                timestamp TEXT NOT NULL
            )",
        )
        .execute(&pool)
        .await?;

        Ok(Self { pool })
    }
}

#[async_trait]
impl EventStore for SqliteEventStore {
    async fn append(&self, event: Event) -> Result<(), EventStoreError> {
        let payload =
            serde_json::to_string(&event.payload).map_err(|e| EventStoreError::Backend(e.to_string()))?;

        sqlx::query(
            "INSERT INTO events (id, event_type, actor_id, payload, timestamp) VALUES (?, ?, ?, ?, ?)",
        )
        .bind(&event.id)
        .bind(event.event_type.as_str())
        .bind(&event.actor_id)
        .bind(payload)
        .bind(event.timestamp.to_rfc3339())
        .execute(&self.pool)
        .await
        .map_err(|e| EventStoreError::Backend(e.to_string()))?;

        Ok(())
    }

    async fn list_all(&self) -> Result<Vec<Event>, EventStoreError> {
        let rows = sqlx::query("SELECT id, event_type, actor_id, payload, timestamp FROM events ORDER BY seq ASC")
            .fetch_all(&self.pool)
            .await
            .map_err(|e| EventStoreError::Backend(e.to_string()))?;

        let mut events = Vec::with_capacity(rows.len());
        for row in rows {
            let id: String = row.get("id");
            let event_type_raw: String = row.get("event_type");
            let actor_id: String = row.get("actor_id");
            let payload_raw: String = row.get("payload");
            let timestamp_raw: String = row.get("timestamp");

            let event_type = EventType::from_str(&event_type_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?;
            let payload = serde_json::from_str(&payload_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?;
            let timestamp = DateTime::parse_from_rfc3339(&timestamp_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?
                .with_timezone(&Utc);

            events.push(Event {
                id,
                event_type,
                actor_id,
                payload,
                timestamp,
            });
        }

        Ok(events)
    }
}
