use crate::derive;
use crate::dto::*;
use crate::state::AppState;
use beethoven_core::RuntimeState;
use beethoven_events::{Event, EventStore};
use chrono::{DateTime, Utc};
use tauri::State;

async fn load_state(state: &AppState) -> Result<(RuntimeState, Vec<Event>), String> {
    let events = state.store.list_all().await.map_err(|e| e.to_string())?;
    let runtime_state = RuntimeState::replay(&events);
    Ok((runtime_state, events))
}

#[tauri::command]
pub async fn get_projects(state: State<'_, AppState>) -> Result<Vec<ProjectSummaryDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::project_summaries(&rs))
}

#[tauri::command]
pub async fn get_project(state: State<'_, AppState>, project_id: String) -> Result<ProjectDetailDto, String> {
    let (rs, _events) = load_state(&state).await?;
    derive::project_detail(&rs, &project_id).ok_or_else(|| format!("project not found: {project_id}"))
}

#[tauri::command]
pub async fn get_agents(state: State<'_, AppState>, project_id: String) -> Result<Vec<AgentDto>, String> {
    let (rs, events) = load_state(&state).await?;
    Ok(derive::agents_for(&rs, &project_id, &events))
}

#[tauri::command]
pub async fn get_tasks(state: State<'_, AppState>, project_id: String) -> Result<Vec<TaskDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::tasks_for(&rs, &project_id))
}

#[tauri::command]
pub async fn get_inbox(state: State<'_, AppState>, project_id: String) -> Result<Vec<InboxItemDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::inbox_for(&rs, &project_id))
}

#[tauri::command]
pub async fn get_timeline(
    state: State<'_, AppState>,
    project_id: Option<String>,
    agent_id: Option<String>,
    event_type: Option<String>,
    since: Option<DateTime<Utc>>,
    until: Option<DateTime<Utc>>,
) -> Result<Vec<TimelineEventDto>, String> {
    let (_rs, events) = load_state(&state).await?;
    let filters = derive::TimelineFilters {
        project_id,
        agent_id,
        event_type,
        since,
        until,
    };
    Ok(derive::timeline(&events, &filters))
}

#[tauri::command]
pub async fn runtime_status(state: State<'_, AppState>) -> Result<RuntimeStatusDto, String> {
    let (rs, events) = load_state(&state).await?;
    Ok(derive::runtime_status(&rs, &events, &state.home))
}

#[tauri::command]
pub async fn get_active_execution(state: State<'_, AppState>) -> Result<Vec<ActiveExecutionDto>, String> {
    let (rs, events) = load_state(&state).await?;
    Ok(derive::active_execution(&rs, &events))
}

#[tauri::command]
pub async fn get_goal_header(state: State<'_, AppState>, project_id: String) -> Result<GoalHeaderDto, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::goal_header(&rs, &project_id))
}

#[tauri::command]
pub async fn get_workflow_map(state: State<'_, AppState>, initiative_id: String) -> Result<WorkflowMapDto, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::workflow_map(&rs, &initiative_id))
}

#[tauri::command]
pub fn get_home_dir(state: State<'_, AppState>) -> String {
    state.home.display().to_string()
}
