use async_trait::async_trait;
use beethoven_events::{Event, EventScope, EventStore, EventStoreError, EventType};
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
    for sub in ["events", "projects", "artifacts", "logs"] {
        std::fs::create_dir_all(home.join(sub))?;
    }
    Ok(home)
}

pub fn default_db_path() -> PathBuf {
    beethoven_home().join("beethoven.db")
}

pub fn projects_root() -> PathBuf {
    beethoven_home().join("projects")
}

fn current_project_path() -> PathBuf {
    beethoven_home().join("current-project")
}

pub fn read_current_project() -> Option<String> {
    let contents = std::fs::read_to_string(current_project_path()).ok()?;
    let trimmed = contents.trim();
    if trimmed.is_empty() {
        None
    } else {
        Some(trimmed.to_string())
    }
}

pub fn write_current_project(project_id: &str) -> Result<(), DbError> {
    std::fs::write(current_project_path(), project_id)?;
    Ok(())
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
                scope_kind TEXT NOT NULL,
                scope_project_id TEXT,
                event_type TEXT NOT NULL,
                actor_id TEXT,
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
        let (scope_kind, scope_project_id): (&str, Option<String>) = match &event.scope {
            EventScope::Global => ("global", None),
            EventScope::Project(id) => ("project", Some(id.clone())),
        };

        sqlx::query(
            "INSERT INTO events (id, scope_kind, scope_project_id, event_type, actor_id, payload, timestamp)
             VALUES (?, ?, ?, ?, ?, ?, ?)",
        )
        .bind(&event.id)
        .bind(scope_kind)
        .bind(scope_project_id)
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
        let rows = sqlx::query(
            "SELECT id, scope_kind, scope_project_id, event_type, actor_id, payload, timestamp
             FROM events ORDER BY seq ASC",
        )
        .fetch_all(&self.pool)
        .await
        .map_err(|e| EventStoreError::Backend(e.to_string()))?;

        let mut events = Vec::with_capacity(rows.len());
        for row in rows {
            let id: String = row.get("id");
            let scope_kind: String = row.get("scope_kind");
            let scope_project_id: Option<String> = row.get("scope_project_id");
            let event_type_raw: String = row.get("event_type");
            let actor_id: Option<String> = row.get("actor_id");
            let payload_raw: String = row.get("payload");
            let timestamp_raw: String = row.get("timestamp");

            let scope = match scope_kind.as_str() {
                "global" => EventScope::Global,
                "project" => EventScope::Project(scope_project_id.ok_or_else(|| {
                    EventStoreError::Backend("project-scoped event missing scope_project_id".to_string())
                })?),
                other => return Err(EventStoreError::Backend(format!("unknown scope_kind: {other}"))),
            };
            let event_type = EventType::from_str(&event_type_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?;
            let payload = serde_json::from_str(&payload_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?;
            let timestamp = DateTime::parse_from_rfc3339(&timestamp_raw)
                .map_err(|e| EventStoreError::Backend(e.to_string()))?
                .with_timezone(&Utc);

            events.push(Event {
                id,
                scope,
                event_type,
                actor_id,
                payload,
                timestamp,
            });
        }

        Ok(events)
    }
}
