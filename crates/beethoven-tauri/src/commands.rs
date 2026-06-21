use crate::derive;
use crate::dto::*;
use crate::state::{AppState, DaemonHandle};
use beethoven_core::{AgentInstance, AgentRole, Decision, InboxItem, Project, Runtime, RuntimeState, Task, TaskPlanOutcome};
use beethoven_events::{Event, EventStore};
use chrono::{DateTime, Utc};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;
use tauri::{AppHandle, Manager, State};
use tokio::sync::MutexGuard;

/// The same tick policy defaults `beethoven-cli`'s `beethoven start` uses
/// (see `DEFAULT_MAX_CONCURRENT`/`DEFAULT_MAX_ATTEMPTS` in
/// `beethoven-core::runtime`, and `Start`'s `clap` defaults in
/// `beethoven-cli`) -- the desktop daemon loop isn't configurable from the
/// UI in this phase, so it just matches the CLI's own defaults.
const DAEMON_TICK_INTERVAL_SECS: u64 = 5;
const DAEMON_MAX_CONCURRENT: usize = 4;
const DAEMON_MAX_ATTEMPTS: u32 = 3;

async fn load_state(state: &AppState) -> Result<(RuntimeState, Vec<Event>), String> {
    let events = state.store.list_all().await.map_err(|e| e.to_string())?;
    let runtime_state = RuntimeState::replay(&events);
    Ok((runtime_state, events))
}

/// Locks `AppState::runtime` and refreshes it from the event store before
/// handing back the guard, closing the gap against any other writer to the
/// same database (e.g. a `beethoven start` CLI daemon running concurrently)
/// -- the same freshness guarantee `load_state` already gives every read
/// command by re-replaying on every call.
async fn with_runtime(state: &AppState) -> Result<MutexGuard<'_, Runtime>, String> {
    let mut guard = state.runtime.lock().await;
    guard.refresh().await.map_err(|e| e.to_string())?;
    Ok(guard)
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
pub async fn get_agent_health(state: State<'_, AppState>) -> Result<Vec<AgentHealthDto>, String> {
    let (rs, events) = load_state(&state).await?;
    Ok(derive::agent_health_for(&rs, &events))
}

#[tauri::command]
pub async fn get_tasks(state: State<'_, AppState>, project_id: String) -> Result<Vec<TaskDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::tasks_for(&rs, &project_id))
}

#[tauri::command]
pub async fn create_project(
    state: State<'_, AppState>,
    name: String,
    repository_path: String,
    default_branch: String,
) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime
        .create_project(&name, std::path::PathBuf::from(repository_path), &default_branch)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn create_task(
    state: State<'_, AppState>,
    project_id: String,
    parent_task_id: Option<String>,
    title: String,
    description: String,
    success_criteria: String,
    target_score: Option<f32>,
    draft: bool,
) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime
        .create_task(&project_id, parent_task_id.as_deref(), &title, &description, &success_criteria, target_score, draft)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn plan_task(state: State<'_, AppState>, task_id: String) -> Result<TaskPlanOutcome, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.plan_task(&task_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn approve_draft_task(state: State<'_, AppState>, task_id: String) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.approve_draft_task(&task_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn reject_draft_task(state: State<'_, AppState>, task_id: String) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.reject_draft_task(&task_id).await.map_err(|e| e.to_string())
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
pub async fn get_workflow_map(state: State<'_, AppState>, task_id: String) -> Result<WorkflowMapDto, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::workflow_map(&rs, &task_id))
}

#[tauri::command]
pub fn get_home_dir(state: State<'_, AppState>) -> String {
    state.home.display().to_string()
}

#[tauri::command]
pub async fn get_decisions(state: State<'_, AppState>) -> Result<Vec<DecisionSummaryDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::decisions_for(&rs))
}

#[tauri::command]
pub async fn get_planning_history(
    state: State<'_, AppState>,
    project_id: String,
) -> Result<Vec<PlanningRunDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::planning_runs_for_project(&rs, &project_id))
}

#[tauri::command]
pub async fn get_workspaces(state: State<'_, AppState>) -> Result<Vec<WorkspaceDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::workspaces_for(&rs, &beethoven_db::projects_root(&state.home)))
}

#[tauri::command]
pub async fn get_recent_diffs(state: State<'_, AppState>, limit: Option<usize>) -> Result<Vec<DiffSummaryDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::recent_diffs(&rs, limit.unwrap_or(50)))
}

#[tauri::command]
pub async fn get_recent_prompts(state: State<'_, AppState>, limit: Option<usize>) -> Result<Vec<PromptSummaryDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::recent_prompts(&rs, limit.unwrap_or(50)))
}

#[tauri::command]
pub async fn get_agent_templates(state: State<'_, AppState>) -> Result<Vec<AgentTemplateDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    let runtime = state.runtime.lock().await;
    Ok(derive::agent_templates_for(&rs, runtime.registry()))
}

#[tauri::command]
pub async fn get_task_detail(state: State<'_, AppState>, task_id: String) -> Result<TaskDetailDto, String> {
    let (rs, events) = load_state(&state).await?;
    derive::task_detail(&rs, &events, &task_id).ok_or_else(|| format!("task not found: {task_id}"))
}

// --- Inbox actions ---------------------------------------------------

#[tauri::command]
pub async fn answer_inbox_item(
    state: State<'_, AppState>,
    inbox_id: String,
    response: String,
) -> Result<InboxItem, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.answer_inbox(&inbox_id, &response).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn resolve_decision(
    state: State<'_, AppState>,
    decision_id: String,
    response: String,
) -> Result<Decision, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.resolve_decision(&decision_id, &response).await.map_err(|e| e.to_string())
}

// --- Project actions ---------------------------------------------------

#[tauri::command]
pub async fn pause_project(state: State<'_, AppState>, project_id: String) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.pause_project(&project_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn resume_project(state: State<'_, AppState>, project_id: String) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.resume_project(&project_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn archive_project(state: State<'_, AppState>, project_id: String) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.archive_project(&project_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn update_project_repository_path(
    state: State<'_, AppState>,
    project_id: String,
    repository_path: String,
) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.update_repository_path(&project_id, repository_path).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn update_project_default_branch(
    state: State<'_, AppState>,
    project_id: String,
    default_branch: String,
) -> Result<Project, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.update_default_branch(&project_id, default_branch).await.map_err(|e| e.to_string())
}

// --- Task actions ---------------------------------------------------

#[tauri::command]
pub async fn retry_task(state: State<'_, AppState>, task_id: String) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.retry_task(&task_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn cancel_task(state: State<'_, AppState>, task_id: String) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.cancel_task(&task_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn reassign_task(
    state: State<'_, AppState>,
    task_id: String,
    new_agent_id: String,
) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.reassign_task(&task_id, &new_agent_id).await.map_err(|e| e.to_string())
}

// --- Review actions ---------------------------------------------------

#[tauri::command]
pub async fn approve_review(state: State<'_, AppState>, task_id: String) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.approve_review(&task_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn request_review_changes(
    state: State<'_, AppState>,
    task_id: String,
    reason: String,
) -> Result<Task, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.request_review_changes(&task_id, &reason).await.map_err(|e| e.to_string())
}

// --- Agent actions ---------------------------------------------------

#[tauri::command]
pub async fn pause_agent(state: State<'_, AppState>, agent_id: String) -> Result<AgentInstance, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.pause_agent(&agent_id).await.map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn resume_agent(state: State<'_, AppState>, agent_id: String) -> Result<AgentInstance, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.resume_agent(&agent_id).await.map_err(|e| e.to_string())
}

fn lead_id_for(runtime: &Runtime, project_id: &str) -> Result<String, String> {
    runtime
        .state
        .agent_instances
        .values()
        .find(|a| a.project_id == project_id && a.role == AgentRole::Lead)
        .map(|a| a.id.clone())
        .ok_or_else(|| format!("no Lead agent found for project {project_id}"))
}

#[tauri::command]
pub async fn spawn_worker_agent(
    state: State<'_, AppState>,
    project_id: String,
    role_description: String,
    name: String,
) -> Result<AgentInstance, String> {
    let mut runtime = with_runtime(&state).await?;
    let lead_id = lead_id_for(&runtime, &project_id)?;
    runtime
        .spawn_agent(&project_id, &lead_id, AgentRole::Worker, &role_description, &name)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn spawn_reviewer_agent(
    state: State<'_, AppState>,
    project_id: String,
    role_description: String,
    name: String,
) -> Result<AgentInstance, String> {
    let mut runtime = with_runtime(&state).await?;
    let lead_id = lead_id_for(&runtime, &project_id)?;
    runtime
        .spawn_agent(&project_id, &lead_id, AgentRole::Reviewer, &role_description, &name)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn archive_agent(state: State<'_, AppState>, agent_id: String) -> Result<AgentInstance, String> {
    let mut runtime = with_runtime(&state).await?;
    runtime.archive_agent(&agent_id).await.map_err(|e| e.to_string())
}

// --- Runtime controls ---------------------------------------------------
//
// The desktop process's own in-process daemon loop, gated by the same
// pidfile `beethoven-cli`'s `beethoven start`/`beethoven stop` use, so the
// CLI daemon and this loop can never tick the same project concurrently.
// Mirrors `run_daemon_loop` in `beethoven-cli/src/main.rs`: recover -> mark
// started -> tick on an interval -> mark stopped.

async fn run_desktop_daemon_loop(app: AppHandle, shutdown: Arc<AtomicBool>) {
    let state = app.state::<AppState>();

    {
        let mut runtime = state.runtime.lock().await;
        if let Err(e) = runtime.refresh().await {
            eprintln!("desktop runtime loop: refresh failed: {e}");
        }
        if let Err(e) = runtime.recover(DAEMON_MAX_ATTEMPTS).await {
            eprintln!("desktop runtime loop: recovery failed: {e}");
        }
        if let Err(e) = runtime.mark_runtime_started().await {
            eprintln!("desktop runtime loop: mark_runtime_started failed: {e}");
        }
    }

    while !shutdown.load(Ordering::SeqCst) {
        {
            let mut runtime = state.runtime.lock().await;
            match runtime.refresh().await {
                Ok(()) => {
                    let project_ids: Vec<String> = runtime.state.projects.keys().cloned().collect();
                    if !project_ids.is_empty() {
                        if let Err(e) =
                            runtime.tick_projects(&project_ids, DAEMON_MAX_CONCURRENT, DAEMON_MAX_ATTEMPTS).await
                        {
                            eprintln!("desktop runtime loop: tick failed: {e}");
                        }
                    }
                }
                Err(e) => eprintln!("desktop runtime loop: refresh failed: {e}"),
            }
            if let Err(e) = runtime.sync_agent_health_events().await {
                eprintln!("desktop runtime loop: agent health sync failed: {e}");
            }
        }

        for _ in 0..DAEMON_TICK_INTERVAL_SECS {
            if shutdown.load(Ordering::SeqCst) {
                break;
            }
            tokio::time::sleep(Duration::from_secs(1)).await;
        }
    }

    let mut runtime = state.runtime.lock().await;
    if let Err(e) = runtime.mark_runtime_stopped().await {
        eprintln!("desktop runtime loop: mark_runtime_stopped failed: {e}");
    }
}

#[tauri::command]
pub async fn runtime_start(app: AppHandle, state: State<'_, AppState>) -> Result<(), String> {
    let pid_file = beethoven_db::pid_path(&state.home);
    if let Some(existing_pid) = beethoven_db::read_pid(&pid_file) {
        if beethoven_db::is_pid_alive(existing_pid) {
            return Err(format!("a daemon is already running (pid {existing_pid}); stop it first"));
        }
    }

    let mut daemon_guard = state.daemon.lock().await;
    if daemon_guard.is_some() {
        return Err("the runtime is already running".to_string());
    }

    std::fs::write(&pid_file, std::process::id().to_string()).map_err(|e| e.to_string())?;

    let shutdown = Arc::new(AtomicBool::new(false));
    let task_shutdown = shutdown.clone();
    let task = tauri::async_runtime::spawn(run_desktop_daemon_loop(app, task_shutdown));
    *daemon_guard = Some(DaemonHandle { shutdown, task });
    Ok(())
}

#[tauri::command]
pub async fn runtime_stop(state: State<'_, AppState>) -> Result<(), String> {
    let pid_file = beethoven_db::pid_path(&state.home);
    let Some(pid) = beethoven_db::read_pid(&pid_file) else {
        return Err("no daemon is running".to_string());
    };
    if !beethoven_db::is_pid_alive(pid) {
        let _ = std::fs::remove_file(&pid_file);
        return Err(format!("no daemon is running (removed stale pidfile for pid {pid})"));
    }

    if pid == std::process::id() {
        let mut daemon_guard = state.daemon.lock().await;
        let Some(handle) = daemon_guard.take() else {
            return Err("the runtime is not running in this process".to_string());
        };
        handle.shutdown.store(true, Ordering::SeqCst);
        let _ = tokio::time::timeout(Duration::from_secs(10), handle.task).await;
        let _ = std::fs::remove_file(&pid_file);
        Ok(())
    } else {
        std::process::Command::new("kill")
            .arg("-TERM")
            .arg(pid.to_string())
            .status()
            .map_err(|e| e.to_string())?;
        for _ in 0..50 {
            if !beethoven_db::is_pid_alive(pid) {
                return Ok(());
            }
            tokio::time::sleep(Duration::from_millis(200)).await;
        }
        Err(format!("daemon (pid {pid}) did not exit within 10s of being sent SIGTERM"))
    }
}

#[tauri::command]
pub async fn runtime_restart(app: AppHandle, state: State<'_, AppState>) -> Result<(), String> {
    runtime_stop(state.clone()).await?;
    runtime_start(app, state.clone()).await?;
    let mut runtime = with_runtime(&state).await?;
    runtime.mark_runtime_restarted().await.map_err(|e| e.to_string())
}
