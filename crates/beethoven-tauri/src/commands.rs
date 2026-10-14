use crate::derive;
use crate::dto::*;
use crate::state::{AppState, DaemonHandle};
use beethoven_agents::{parse_chat_response, PROJECT_CHAT_RESPONSE_SCHEMA};
use beethoven_core::{
    validate_suggested_actions, AgentInstance, AgentRole, Decision, InboxItem, Project, ProjectChatTurn, Runtime,
    RuntimeState, SuggestedAction, Task, TaskPlanOutcome,
};
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
pub async fn get_inbox(state: State<'_, AppState>, project_id: Option<String>) -> Result<Vec<InboxItemDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::inbox_for(&rs, project_id.as_deref()))
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

/// Kills every real tmux/workspace session that might be running -- both
/// every task tracked as `Assigned|InProgress|Review` and any stray
/// `beethoven_*` session not tied to a tracked task at all (a crash orphan)
/// -- and resets each affected task to `Ready`. Deliberately does NOT use
/// `with_runtime` for the kill step: `tick_projects` holds the runtime mutex
/// across every in-flight model call in a batch, so going through
/// `with_runtime` here would just queue this command behind whatever batch
/// happens to be running instead of stopping anything *now*. Instead this
/// snapshots state lock-free, kills sessions lock-free, then takes the lock
/// only briefly afterward to record the bookkeeping events.
#[tauri::command]
pub async fn stop_all_agents(state: State<'_, AppState>) -> Result<StopAllReportDto, String> {
    let (rs, _events) = load_state(&state).await?;
    let projects_root = beethoven_db::projects_root(&state.home);
    let targets = rs.plan_stop_all();
    let outcome = beethoven_core::stop_all_sessions(&targets, &projects_root);

    let mut runtime = with_runtime(&state).await?;
    runtime.record_stop_all(&outcome).await.map_err(|e| e.to_string())?;
    Ok(outcome.into())
}

// --- Project Chat ---------------------------------------------------------

/// The project's Lead agent's `runtime_profile_id`, lock-free against a
/// `RuntimeState` snapshot -- same fallback chain `plan_task` uses (no Lead
/// yet -> "claude-default") so a project with no Lead never hard-errors.
fn lead_runtime_profile_for(rs: &RuntimeState, project_id: &str) -> String {
    rs.agent_instances
        .values()
        .find(|a| a.project_id == project_id && a.role == AgentRole::Lead)
        .and_then(|lead| rs.agent_templates.get(&lead.template_id))
        .map(|template| template.runtime_profile_id.clone())
        .unwrap_or_else(|| "claude-default".to_string())
}

const MAX_PRIOR_CHAT_TURNS: usize = 10;

/// Assembles the full prompt: current state (authoritative) + recent prior
/// turns (continuity only -- may describe state that's since changed) +
/// the new question. Keeping these visually separate matters: an old
/// answer can describe a task as blocked when the state section above now
/// shows it retried and done, and the model needs to know which to trust.
fn build_chat_prompt(context: &str, prior_turns: &[ProjectChatTurnDto], question: &str) -> String {
    let mut prompt = String::new();
    prompt.push_str(
        "You are the Lead agent for this Beethoven project, answering a human operator's question in \
         Project Chat. You are advisory only: you may propose suggested_actions, but you never execute \
         anything yourself -- the human always clicks a button to confirm. Ground your answer only in \
         the state given below; say you don't know rather than guessing.\n\n",
    );
    prompt.push_str("# Current project state (authoritative, as of now)\n\n");
    prompt.push_str(context);

    if !prior_turns.is_empty() {
        prompt.push_str(
            "\n# Prior conversation (for continuity only -- may reference task/decision states that \
             have since changed; the section above is always the current truth if they conflict)\n\n",
        );
        let start = prior_turns.len().saturating_sub(MAX_PRIOR_CHAT_TURNS);
        for turn in &prior_turns[start..] {
            prompt.push_str(&format!("Q: {}\nA: {}\n\n", turn.question, turn.answer));
        }
    }

    prompt.push_str(&format!("\n# New question\n\n{question}\n\n"));
    prompt.push_str(
        "Respond with the required JSON shape: `answer` (plain prose) and `suggested_actions` (zero or \
         more, each referencing a real id seen above). Note `inbox_id` (for answer_inbox) and \
         `decision_id` (for resolve_decision) are different id spaces on related-but-different rows -- \
         don't conflate them.\n",
    );
    prompt
}

/// Asks the project's Lead a question, advisory only. Deliberately mirrors
/// `stop_all_agents`'s shape rather than going through `with_runtime` for
/// the whole call: a chat answer can take up to the runtime's chat timeout
/// (tens of seconds) on its own, and `tick_projects` holds the runtime
/// mutex across every in-flight model call in a batch -- routing this
/// through `with_runtime` end-to-end would either queue behind a running
/// tick or block every other mutating command for the whole call. Instead:
/// snapshot state and build the prompt lock-free, resolve+clone the model
/// runtime under a brief lock, run the actual call lock-free, then take the
/// lock again only to record the bookkeeping event (re-validating
/// suggested actions against fresh state, since the pre-call snapshot can
/// be stale by the time a slow call finishes).
#[tauri::command]
pub async fn ask_project_chat(
    state: State<'_, AppState>,
    project_id: String,
    question: String,
    focus_task_id: Option<String>,
) -> Result<ProjectChatTurn, String> {
    let (rs, events) = load_state(&state).await?;
    let context = derive::project_chat_context(&rs, &events, &project_id, focus_task_id.as_deref())?;
    let prior_turns = derive::project_chat_history(&rs, &project_id);
    let prompt = build_chat_prompt(&context, &prior_turns, &question);
    let runtime_profile_id = lead_runtime_profile_for(&rs, &project_id);

    let guard = with_runtime(&state).await?;
    let model_runtime = guard.registry().resolve(&runtime_profile_id).map_err(|e| e.to_string())?;
    drop(guard);

    let scratch_dir = beethoven_db::chat_scratch_root(&state.home).join(uuid::Uuid::new_v4().to_string());
    std::fs::create_dir_all(&scratch_dir).map_err(|e| e.to_string())?;
    let scratch_dir_for_cleanup = scratch_dir.clone();
    let call_result = tokio::task::spawn_blocking(move || {
        model_runtime.ask(&prompt, &scratch_dir, Some(PROJECT_CHAT_RESPONSE_SCHEMA))
    })
    .await
    .map_err(|e| format!("chat call panicked or was cancelled: {e}"));
    let _ = std::fs::remove_dir_all(&scratch_dir_for_cleanup);
    let result = call_result?.map_err(|e| e.to_string())?;

    let parsed = parse_chat_response(result.structured_output.as_ref(), &result.raw_response).map_err(|e| e.to_string())?;
    let suggested_actions: Vec<SuggestedAction> = parsed.suggested_actions.into_iter().map(Into::into).collect();
    let suggested_actions = validate_suggested_actions(&rs, suggested_actions);

    let mut runtime = with_runtime(&state).await?;
    runtime
        .record_project_chat_turn(&project_id, &question, &parsed.answer, focus_task_id.as_deref(), suggested_actions)
        .await
        .map_err(|e| e.to_string())
}

#[tauri::command]
pub async fn get_project_chat_history(
    state: State<'_, AppState>,
    project_id: String,
) -> Result<Vec<ProjectChatTurnDto>, String> {
    let (rs, _events) = load_state(&state).await?;
    Ok(derive::project_chat_history(&rs, &project_id))
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
