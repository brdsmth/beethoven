use crate::error::CoreError;
use crate::models::*;
use crate::state::RuntimeState;
use beethoven_agents::{
    AgentKind, AgentsError, ExecutionOutcome, ExecutionRequest, ExecutionResult, ExecutionService, ExecutionStatus,
    InitiativePlanningService, ModelRuntime, PlanningService, PromptBuilder, RequirementInput, ReviewOutcome,
    ReviewService, RuntimeRegistry, TaskExecutionContext, WorkerPromptContext,
};
use beethoven_events::{Event, EventStore, EventType};
use beethoven_workspaces::{tmux_session_name, WorkspaceManager, WorkspaceRequest, WorkspaceState};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::Arc;
use uuid::Uuid;

/// Default policy values for `run_tick`'s single-project convenience wrapper
/// and `recover()`'s CLI default. The daemon (`beethoven start`) exposes both
/// as `--max-concurrent`/`--max-attempts` flags instead of these constants.
const DEFAULT_MAX_CONCURRENT: usize = 4;
const DEFAULT_MAX_ATTEMPTS: u32 = 3;

#[derive(Debug, Default, Clone)]
pub struct TickReport {
    pub assigned: u32,
    pub completed: u32,
    pub needs_changes: u32,
    pub escalated: u32,
    pub subtasks_spawned: u32,
}

impl std::ops::AddAssign for TickReport {
    fn add_assign(&mut self, other: Self) {
        self.assigned += other.assigned;
        self.completed += other.completed;
        self.needs_changes += other.needs_changes;
        self.escalated += other.escalated;
        self.subtasks_spawned += other.subtasks_spawned;
    }
}

/// Outcome of a crash-recovery pass (see [`Runtime::recover`]): tasks reset
/// to `Ready` for another attempt, versus tasks that had already hit
/// `max_attempts` and were escalated to the inbox instead.
#[derive(Debug, Default, Clone, PartialEq, Eq)]
pub struct RecoveryReport {
    pub recovered: Vec<String>,
    pub escalated: Vec<String>,
}

/// Everything `finish_task` needs to process one task's real model
/// execution -- entirely owned data, so it can cross a `spawn_blocking`
/// boundary (and sit in a `Vec` while other tasks' executions run
/// concurrently) without borrowing from `Runtime`.
struct PreparedTask {
    project_id: String,
    project_slug: String,
    pm_id: String,
    task_id: String,
    task: Task,
    agent_id: String,
    run_id: String,
    ws_state: WorkspaceState,
    prompt_path: PathBuf,
    artifacts_dir: PathBuf,
    model_runtime: Arc<dyn ModelRuntime + Send + Sync>,
}

/// Outcome of `start_task`: either the task is ready for a real model
/// execution, or it was already fully resolved without one (the
/// execution-gate heuristic raised a question / spawned a subtask, or
/// workspace/git/tmux/io setup itself failed and was escalated).
enum StartOutcome {
    Ready(Box<PreparedTask>),
    Resolved(TickReport),
}

/// Result of running the deterministic PM planner against an initiative:
/// the generated plan plus everything it produced (epics, their tasks, and
/// any decisions auto-escalated to the inbox).
#[derive(Debug, Clone)]
pub struct PlanOutcome {
    pub plan: Plan,
    pub epics: Vec<Epic>,
    pub tasks: Vec<Task>,
    pub decisions: Vec<Decision>,
}

fn slugify(input: &str) -> String {
    let mut slug = String::new();
    let mut last_was_dash = true;
    for ch in input.to_lowercase().chars() {
        if ch.is_ascii_alphanumeric() {
            slug.push(ch);
            last_was_dash = false;
        } else if !last_was_dash {
            slug.push('-');
            last_was_dash = true;
        }
    }
    while slug.ends_with('-') {
        slug.pop();
    }
    if slug.is_empty() {
        slug.push_str("project");
    }
    slug
}

pub struct Runtime {
    store: Box<dyn EventStore>,
    projects_root: PathBuf,
    registry: RuntimeRegistry,
    pub state: RuntimeState,
}

impl Runtime {
    pub async fn load(
        store: Box<dyn EventStore>,
        projects_root: PathBuf,
        registry: RuntimeRegistry,
    ) -> Result<Self, CoreError> {
        let events = store.list_all().await?;
        let state = RuntimeState::replay(&events);
        Ok(Self {
            store,
            projects_root,
            registry,
            state,
        })
    }

    /// Scans for tasks left mid-flight by a crash -- assigned to an agent but
    /// never reaching a terminal status, because the process died during the
    /// long synchronous window between `TaskStarted` and the next event
    /// (typically a model invocation in progress) -- and resets each one to
    /// `Ready`, freeing its agent and bumping its attempt. Call once at
    /// startup, after the initial event replay. A task already at
    /// `max_attempts` is escalated to the inbox instead of being given
    /// another automatic attempt (the same retry cap `finish_task` applies
    /// to a `NeedsChanges` review outcome).
    pub async fn recover(&mut self, max_attempts: u32) -> Result<RecoveryReport, CoreError> {
        let stuck: Vec<(String, String, String, u32)> = self
            .state
            .tasks
            .values()
            .filter(|t| matches!(t.status, TaskStatus::Assigned | TaskStatus::InProgress | TaskStatus::Review))
            .filter_map(|t| {
                t.assigned_agent_id
                    .clone()
                    .map(|agent_id| (t.id.clone(), t.project_id.clone(), agent_id, t.attempt))
            })
            .collect();

        let mut report = RecoveryReport::default();
        for (task_id, project_id, agent_id, attempt) in stuck {
            self.cleanup_orphaned_workspace(&project_id, &task_id, &agent_id);
            if attempt >= max_attempts {
                let reason = format!("gave up after crashing on attempt {attempt}");
                self.emit_project(
                    &project_id,
                    EventType::TaskBlocked,
                    "system",
                    json!({"task_id": task_id, "reason": reason}),
                )
                .await?;
                let inbox_id = Uuid::new_v4().to_string();
                self.emit_project(
                    &project_id,
                    EventType::QuestionRaised,
                    "system",
                    json!({"inbox_id": inbox_id, "task_id": task_id, "agent_id": agent_id, "prompt": reason}),
                )
                .await?;
                report.escalated.push(task_id);
            } else {
                self.emit_project(
                    &project_id,
                    EventType::TaskRecovered,
                    "system",
                    json!({"task_id": task_id, "agent_id": agent_id}),
                )
                .await?;
                report.recovered.push(task_id);
            }
        }
        Ok(report)
    }

    /// Marks the start/end of a `beethoven start` daemon session in the event
    /// log, purely for audit/timeline purposes -- no derived state tracks
    /// runtime lifecycle status yet.
    pub async fn mark_runtime_started(&mut self) -> Result<(), CoreError> {
        self.emit_global(EventType::RuntimeStarted, "system", json!({})).await
    }

    pub async fn mark_runtime_stopped(&mut self) -> Result<(), CoreError> {
        self.emit_global(EventType::RuntimeStopped, "system", json!({})).await
    }

    pub async fn mark_runtime_restarted(&mut self) -> Result<(), CoreError> {
        self.emit_global(EventType::RuntimeRestarted, "system", json!({})).await
    }

    /// Re-replays `state` from the event store. Tauri's mutating commands
    /// call this before every `Runtime` method to close the gap against any
    /// other writer to the same database (e.g. a `beethoven start` CLI daemon
    /// running concurrently) -- the same freshness guarantee the read-only
    /// commands already get by re-replaying on every call.
    pub async fn refresh(&mut self) -> Result<(), CoreError> {
        let events = self.store.list_all().await?;
        self.state = RuntimeState::replay(&events);
        Ok(())
    }

    /// Best-effort teardown of whatever workspace/tmux session the crashed
    /// attempt left behind. The workspace path, branch, and tmux session name
    /// are all deterministic functions of (project, task, agent kind), so they
    /// can be reconstructed without having persisted a `WorkspaceState`
    /// anywhere -- safe to call even if the crashed attempt never got far
    /// enough to create a workspace at all (`destroy` is then just a no-op).
    fn cleanup_orphaned_workspace(&self, project_id: &str, task_id: &str, agent_id: &str) {
        let (Some(project), Some(task)) = (self.state.projects.get(project_id), self.state.tasks.get(task_id)) else {
            return;
        };
        let agent_kind = self.state.agent_instances.get(agent_id).map(|a| a.kind).unwrap_or(AgentKind::Backend);
        let branch = task.branch.clone().unwrap_or_else(|| beethoven_git::branch_name(&task.title));
        let workspaces = self.workspace_manager_for(&project.slug);
        let git_worktree = workspaces.path_for(task_id).join("repo").to_string_lossy().into_owned();

        let _ = workspaces.destroy(&WorkspaceState {
            task_id: task_id.to_string(),
            created_at: chrono::Utc::now(),
            repository_path: project.repository_path.to_string_lossy().into_owned(),
            branch,
            tmux_session: tmux_session_name(&project.slug, agent_kind.as_str(), task_id),
            git_worktree,
            git_link: String::new(),
        });
    }

    /// Tears down a task's workspace/tmux session and records it -- the same
    /// unconditional last step every execution path (completed, needs
    /// changes, blocked, question, subtask) ends with regardless of outcome.
    async fn teardown_workspace(
        &mut self,
        project_id: &str,
        task_id: &str,
        agent_id: &str,
        workspaces: &WorkspaceManager,
        ws_state: &WorkspaceState,
    ) -> Result<(), CoreError> {
        workspaces.destroy(ws_state)?;
        self.emit_project(
            project_id,
            EventType::TmuxSessionDestroyed,
            agent_id,
            json!({"task_id": task_id, "session_name": ws_state.tmux_session}),
        )
        .await?;
        self.emit_project(project_id, EventType::WorkspaceDestroyed, agent_id, json!({"task_id": task_id}))
            .await?;
        Ok(())
    }

    /// A workspace/git/tmux/io failure while starting a task escalates just
    /// that task (mirroring how a model failure is already handled) instead
    /// of `?`-propagating out of the whole tick and aborting every other
    /// task's progress for every other project in the same call.
    async fn escalate_start_failure(
        &mut self,
        project_id: &str,
        task_id: &str,
        agent_id: &str,
        reason: &str,
    ) -> Result<StartOutcome, CoreError> {
        self.cleanup_orphaned_workspace(project_id, task_id, agent_id);
        self.emit_project(project_id, EventType::TaskBlocked, agent_id, json!({"task_id": task_id, "reason": reason}))
            .await?;
        let inbox_id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::QuestionRaised,
            agent_id,
            json!({"inbox_id": inbox_id, "task_id": task_id, "agent_id": agent_id, "prompt": reason}),
        )
        .await?;
        Ok(StartOutcome::Resolved(TickReport {
            escalated: 1,
            ..Default::default()
        }))
    }

    pub async fn list_events(&self) -> Result<Vec<Event>, CoreError> {
        Ok(self.store.list_all().await?)
    }

    async fn emit_global(&mut self, event_type: EventType, actor_id: &str, payload: Value) -> Result<(), CoreError> {
        let event = Event::global(event_type, actor_id, payload);
        self.store.append(event.clone()).await?;
        self.state.apply(&event);
        Ok(())
    }

    async fn emit_project(
        &mut self,
        project_id: &str,
        event_type: EventType,
        actor_id: &str,
        payload: Value,
    ) -> Result<(), CoreError> {
        let event = Event::for_project(project_id, event_type, actor_id, payload);
        self.store.append(event.clone()).await?;
        self.state.apply(&event);
        Ok(())
    }

    fn workspace_manager_for(&self, slug: &str) -> WorkspaceManager {
        WorkspaceManager::new(self.projects_root.join(slug).join("workspaces"))
    }

    fn require_project(&self, project_id: &str) -> Result<Project, CoreError> {
        self.state
            .projects
            .get(project_id)
            .cloned()
            .ok_or_else(|| CoreError::ProjectNotFound(project_id.to_string()))
    }

    fn template_id_for(&self, kind: AgentKind) -> Option<String> {
        self.state
            .agent_templates
            .values()
            .find(|t| t.kind == kind)
            .map(|t| t.id.clone())
    }

    fn pm_instance_id(&self, project_id: &str) -> Option<String> {
        self.state
            .agent_instances
            .values()
            .find(|a| a.project_id == project_id && a.kind == AgentKind::ProjectManager)
            .map(|a| a.id.clone())
    }

    pub fn find_project(&self, slug_or_id: &str) -> Option<&Project> {
        self.state
            .projects
            .values()
            .find(|p| p.slug == slug_or_id || p.id == slug_or_id)
    }

    async fn ensure_templates(&mut self) -> Result<(), CoreError> {
        if !self.state.agent_templates.is_empty() {
            return Ok(());
        }
        for kind in AgentKind::ALL {
            let id = Uuid::new_v4().to_string();
            self.emit_global(
                EventType::AgentTemplateCreated,
                "system",
                json!({
                    "id": id,
                    "kind": kind,
                    "role": kind.role_label(),
                    "prompt": kind.default_prompt(),
                    "permissions": Vec::<String>::new(),
                    "tools": Vec::<String>::new(),
                    "runtime_profile_id": "claude-default",
                }),
            )
            .await?;
        }
        Ok(())
    }

    pub async fn create_project(
        &mut self,
        name: &str,
        repository_path: PathBuf,
        default_branch: &str,
    ) -> Result<Project, CoreError> {
        self.ensure_templates().await?;

        let id = Uuid::new_v4().to_string();
        let slug = slugify(name);
        self.emit_global(
            EventType::ProjectCreated,
            "human",
            json!({
                "id": id,
                "name": name,
                "slug": slug,
                "repository_path": repository_path.to_string_lossy(),
                "default_branch": default_branch,
            }),
        )
        .await?;

        for kind in AgentKind::ALL {
            let template_id = self.template_id_for(kind).expect("templates were just ensured");
            let instance_id = Uuid::new_v4().to_string();
            let instance_name = format!("{name} {}", kind.display_name());
            self.emit_project(
                &id,
                EventType::AgentCreated,
                "system",
                json!({
                    "id": instance_id,
                    "template_id": template_id,
                    "kind": kind,
                    "name": instance_name,
                }),
            )
            .await?;
            self.emit_project(&id, EventType::AgentStarted, "system", json!({"id": instance_id}))
                .await?;
        }

        Ok(self.state.projects.get(&id).expect("just inserted").clone())
    }

    pub async fn create_goal(&mut self, project_id: &str, title: &str, description: &str) -> Result<Goal, CoreError> {
        self.require_project(project_id)?;
        let id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::GoalCreated,
            "human",
            json!({"id": id, "title": title, "description": description}),
        )
        .await?;
        Ok(self.state.goals.get(&id).expect("just inserted").clone())
    }

    pub async fn plan_goal(&mut self, goal_id: &str) -> Result<Vec<Task>, CoreError> {
        let goal = self
            .state
            .goals
            .get(goal_id)
            .cloned()
            .ok_or_else(|| CoreError::GoalNotFound(goal_id.to_string()))?;
        let project_id = goal.project_id.clone();
        let pm_id = self.pm_instance_id(&project_id).unwrap_or_else(|| "system".to_string());

        let drafts = PlanningService::plan(&goal.title, &goal.description);
        let mut created = Vec::with_capacity(drafts.len());
        for draft in drafts {
            let id = Uuid::new_v4().to_string();
            self.emit_project(
                &project_id,
                EventType::TaskCreated,
                &pm_id,
                json!({
                    "id": id,
                    "goal_id": goal_id,
                    "epic_id": None::<String>,
                    "parent_task_id": None::<String>,
                    "title": draft.title,
                    "description": draft.description,
                }),
            )
            .await?;
            created.push(self.state.tasks.get(&id).expect("just inserted").clone());
        }

        self.emit_project(
            &project_id,
            EventType::GoalUpdated,
            &pm_id,
            json!({"id": goal_id, "status": GoalStatus::Planned}),
        )
        .await?;

        Ok(created)
    }

    /// Assigns every currently-`Ready` task in `project_id` to an idle
    /// non-PM agent. Returns how many were assigned.
    async fn assign_ready_tasks(&mut self, project_id: &str) -> Result<u32, CoreError> {
        let pm_id = self.pm_instance_id(project_id).unwrap_or_else(|| "system".to_string());
        let ready_task_ids: Vec<String> = self
            .state
            .tasks
            .values()
            .filter(|t| t.project_id == project_id && t.status == TaskStatus::Ready)
            .map(|t| t.id.clone())
            .collect();

        let mut assigned = 0;
        for task_id in ready_task_ids {
            let idle_agent_id = self
                .state
                .agent_instances
                .values()
                .find(|a| {
                    a.project_id == project_id && a.status == AgentStatus::Idle && a.kind != AgentKind::ProjectManager
                })
                .map(|a| a.id.clone());

            if let Some(agent_id) = idle_agent_id {
                self.emit_project(
                    project_id,
                    EventType::TaskAssigned,
                    &pm_id,
                    json!({"task_id": task_id, "agent_id": agent_id}),
                )
                .await?;
                assigned += 1;
            }
        }
        Ok(assigned)
    }

    /// Phase 1 for one `Assigned` task: `TaskStarted`, real workspace/branch/
    /// tmux setup, the execution-gate heuristic, and (if it's going to run a
    /// real model) prompt generation through `ModelRunStarted`. Never runs
    /// the model itself -- that's the only step worth running concurrently
    /// with other tasks', so it's left for the caller to batch separately.
    async fn start_task(&mut self, project_id: &str, task_id: &str) -> Result<StartOutcome, CoreError> {
        let project = self.require_project(project_id)?;
        let pm_id = self.pm_instance_id(project_id).unwrap_or_else(|| "system".to_string());
        let repository_path = project.repository_path.to_string_lossy().into_owned();
        let workspaces = self.workspace_manager_for(&project.slug);

        let task = self.state.tasks.get(task_id).expect("task exists").clone();
        let agent_id = task.assigned_agent_id.clone().expect("assigned task has agent");
        let agent_kind = self.state.agent_instances.get(&agent_id).map(|a| a.kind).unwrap_or(AgentKind::Backend);

        self.emit_project(project_id, EventType::TaskStarted, &agent_id, json!({"task_id": task_id}))
            .await?;

        let branch = beethoven_git::branch_name(&task.title);
        let ws_state = match workspaces.create(&WorkspaceRequest {
            project_slug: &project.slug,
            agent_label: agent_kind.as_str(),
            task_id,
            repository_path: &repository_path,
            default_branch: &project.default_branch,
            branch: &branch,
        }) {
            Ok(ws) => ws,
            Err(e) => return self.escalate_start_failure(project_id, task_id, &agent_id, &e.to_string()).await,
        };
        self.emit_project(
            project_id,
            EventType::WorkspaceCreated,
            &agent_id,
            json!({"task_id": task_id, "path": ws_state.git_worktree}),
        )
        .await?;
        self.emit_project(
            project_id,
            EventType::WorktreeCreated,
            &agent_id,
            json!({"task_id": task_id, "path": ws_state.git_worktree}),
        )
        .await?;
        self.emit_project(
            project_id,
            EventType::BranchCreated,
            &agent_id,
            json!({"task_id": task_id, "branch": ws_state.branch}),
        )
        .await?;
        self.emit_project(
            project_id,
            EventType::TmuxSessionCreated,
            &agent_id,
            json!({"task_id": task_id, "session_name": ws_state.tmux_session}),
        )
        .await?;

        let outcome = ExecutionService::execute(&TaskExecutionContext {
            task_id: task_id.to_string(),
            title: task.title.clone(),
            attempt: task.attempt,
        });

        match outcome {
            ExecutionOutcome::Completed => {
                let run_id = Uuid::new_v4().to_string();

                let runtime_profile_id = self
                    .state
                    .agent_instances
                    .get(&agent_id)
                    .and_then(|agent| self.state.agent_templates.get(&agent.template_id))
                    .map(|template| template.runtime_profile_id.clone())
                    .unwrap_or_else(|| "claude-default".to_string());
                let model_runtime = self
                    .registry
                    .resolve(&runtime_profile_id)
                    .map_err(|e| CoreError::InvalidState(e.to_string()))?;

                let prompt = PromptBuilder::worker(&WorkerPromptContext {
                    project_name: &project.name,
                    task_title: &task.title,
                    task_description: &task.description,
                    repository_path: &ws_state.git_worktree,
                    branch: &ws_state.branch,
                });
                let prompt_path = workspaces.path_for(task_id).join("prompts").join("prompt.md");
                if let Err(e) = std::fs::write(&prompt_path, &prompt) {
                    return self.escalate_start_failure(project_id, task_id, &agent_id, &e.to_string()).await;
                }
                self.emit_project(
                    project_id,
                    EventType::PromptGenerated,
                    &agent_id,
                    json!({"model_run_id": run_id, "task_id": task_id, "prompt": prompt}),
                )
                .await?;
                self.emit_project(
                    project_id,
                    EventType::ModelRunStarted,
                    &agent_id,
                    json!({
                        "id": run_id,
                        "agent_id": agent_id,
                        "project_id": project_id,
                        "task_id": task_id,
                        "provider": model_runtime.provider(),
                    }),
                )
                .await?;

                let artifacts_dir = workspaces.path_for(task_id).join("artifacts");
                Ok(StartOutcome::Ready(Box::new(PreparedTask {
                    project_id: project_id.to_string(),
                    project_slug: project.slug.clone(),
                    pm_id,
                    task_id: task_id.to_string(),
                    task,
                    agent_id,
                    run_id,
                    ws_state,
                    prompt_path,
                    artifacts_dir,
                    model_runtime,
                })))
            }
            ExecutionOutcome::RaisedQuestion(prompt) => {
                self.emit_project(
                    project_id,
                    EventType::TaskBlocked,
                    &agent_id,
                    json!({"task_id": task_id, "reason": prompt.clone()}),
                )
                .await?;
                let inbox_id = Uuid::new_v4().to_string();
                self.emit_project(
                    project_id,
                    EventType::QuestionRaised,
                    &agent_id,
                    json!({"inbox_id": inbox_id, "task_id": task_id, "agent_id": agent_id, "prompt": prompt}),
                )
                .await?;
                self.teardown_workspace(project_id, task_id, &agent_id, &workspaces, &ws_state).await?;
                Ok(StartOutcome::Resolved(TickReport {
                    escalated: 1,
                    ..Default::default()
                }))
            }
            ExecutionOutcome::SpawnedSubtask(draft) => {
                let sub_id = Uuid::new_v4().to_string();
                self.emit_project(
                    project_id,
                    EventType::TaskCreated,
                    &agent_id,
                    json!({
                        "id": sub_id,
                        "goal_id": task.goal_id,
                        "epic_id": task.epic_id,
                        "parent_task_id": task_id,
                        "title": draft.title,
                        "description": draft.description,
                    }),
                )
                .await?;
                self.teardown_workspace(project_id, task_id, &agent_id, &workspaces, &ws_state).await?;
                Ok(StartOutcome::Resolved(TickReport {
                    subtasks_spawned: 1,
                    ..Default::default()
                }))
            }
        }
    }

    /// Phase 3 for one task, run once its real model execution (or panic/
    /// cancellation, surfaced by the caller as an `Err`) comes back:
    /// `ModelRunCompleted`/`Failed` through review, completion/escalation,
    /// and the same unconditional workspace teardown every path ends with.
    async fn finish_task(
        &mut self,
        prepared: PreparedTask,
        execution: Result<ExecutionResult, AgentsError>,
        max_attempts: u32,
    ) -> Result<TickReport, CoreError> {
        let PreparedTask {
            project_id,
            project_slug,
            pm_id,
            task_id,
            task,
            agent_id,
            run_id,
            ws_state,
            ..
        } = prepared;
        let workspaces = self.workspace_manager_for(&project_slug);
        let mut delta = TickReport::default();

        // The task may have been cancelled while this execution was in
        // flight (started under `start_task`, running in a `spawn_blocking`
        // call elsewhere in the same `tick_projects` batch). Tear down its
        // workspace and stop here rather than resurrecting it to `Done`/
        // `Review` once the (now-irrelevant) result comes back.
        let still_cancelled =
            self.state.tasks.get(&task_id).map(|t| t.status == TaskStatus::Cancelled).unwrap_or(false);
        if still_cancelled {
            self.teardown_workspace(&project_id, &task_id, &agent_id, &workspaces, &ws_state).await?;
            return Ok(delta);
        }

        match execution {
            Ok(result) => {
                self.emit_project(
                    &project_id,
                    EventType::ModelRunCompleted,
                    &agent_id,
                    json!({
                        "id": run_id,
                        "summary": result.summary,
                        "response": result.raw_response,
                        "cost_usd": result.cost_usd,
                    }),
                )
                .await?;

                match result.status {
                    ExecutionStatus::Completed => {
                        // The model run is sandboxed but untrusted: a project
                        // scaffolding step may have run its own `git init` inside
                        // the worktree, replacing the `.git` link `git worktree add`
                        // wrote. Restore it before computing status/diff so a
                        // genuinely successful run is never misread as empty.
                        workspaces.restore_git_link(&ws_state)?;
                        let git_status = beethoven_git::status(Path::new(&ws_state.git_worktree))?;
                        self.emit_project(
                            &project_id,
                            EventType::FilesModified,
                            &agent_id,
                            json!({
                                "task_id": task_id,
                                "modified": git_status.modified,
                                "added": git_status.added,
                                "deleted": git_status.deleted,
                            }),
                        )
                        .await?;

                        let diff_text = beethoven_git::diff(Path::new(&ws_state.git_worktree))?;
                        self.emit_project(
                            &project_id,
                            EventType::DiffCaptured,
                            &agent_id,
                            json!({"task_id": task_id, "diff": diff_text}),
                        )
                        .await?;

                        let review_id = Uuid::new_v4().to_string();
                        self.emit_project(
                            &project_id,
                            EventType::ReviewStarted,
                            &pm_id,
                            json!({"review_id": review_id, "task_id": task_id, "reviewer_agent_id": pm_id}),
                        )
                        .await?;

                        match ReviewService::review(&task.title, &diff_text) {
                            ReviewOutcome::Approved => {
                                self.emit_project(
                                    &project_id,
                                    EventType::ReviewApproved,
                                    &pm_id,
                                    json!({"review_id": review_id, "task_id": task_id}),
                                )
                                .await?;

                                let pr = beethoven_git::open_pr(&task_id, &ws_state.branch, &task.title);
                                self.emit_project(
                                    &project_id,
                                    EventType::PRCreated,
                                    &agent_id,
                                    json!({"task_id": task_id, "branch": ws_state.branch, "url": pr.url}),
                                )
                                .await?;
                                self.emit_project(&project_id, EventType::TaskCompleted, &agent_id, json!({"task_id": task_id}))
                                    .await?;
                                self.emit_project(
                                    &project_id,
                                    EventType::PRMerged,
                                    &pm_id,
                                    json!({"task_id": task_id, "url": pr.url}),
                                )
                                .await?;
                                delta.completed += 1;

                                if let Some(epic_id) = task.epic_id.clone() {
                                    let initiative_id = self.state.epics.get(&epic_id).map(|e| e.initiative_id.clone());
                                    if let Some(initiative_id) = initiative_id {
                                        self.maybe_complete_initiative(&project_id, &initiative_id).await?;
                                    }
                                }
                            }
                            ReviewOutcome::NeedsChanges(reason) => {
                                if task.attempt >= max_attempts {
                                    let give_up = format!("giving up after {} attempts: {reason}", task.attempt);
                                    self.emit_project(
                                        &project_id,
                                        EventType::TaskBlocked,
                                        &agent_id,
                                        json!({"task_id": task_id, "reason": give_up}),
                                    )
                                    .await?;
                                    let inbox_id = Uuid::new_v4().to_string();
                                    self.emit_project(
                                        &project_id,
                                        EventType::QuestionRaised,
                                        &agent_id,
                                        json!({
                                            "inbox_id": inbox_id,
                                            "task_id": task_id,
                                            "agent_id": agent_id,
                                            "prompt": give_up,
                                        }),
                                    )
                                    .await?;
                                    delta.escalated += 1;
                                } else {
                                    self.emit_project(
                                        &project_id,
                                        EventType::ReviewRejected,
                                        &pm_id,
                                        json!({"review_id": review_id, "task_id": task_id, "reason": reason}),
                                    )
                                    .await?;
                                    delta.needs_changes += 1;
                                }
                            }
                            ReviewOutcome::Blocked(reason) | ReviewOutcome::RaisedQuestion(reason) => {
                                self.emit_project(
                                    &project_id,
                                    EventType::TaskBlocked,
                                    &agent_id,
                                    json!({"task_id": task_id, "reason": reason.clone()}),
                                )
                                .await?;
                                let inbox_id = Uuid::new_v4().to_string();
                                self.emit_project(
                                    &project_id,
                                    EventType::QuestionRaised,
                                    &agent_id,
                                    json!({
                                        "inbox_id": inbox_id,
                                        "task_id": task_id,
                                        "agent_id": agent_id,
                                        "prompt": reason,
                                    }),
                                )
                                .await?;
                                delta.escalated += 1;
                            }
                        }
                    }
                    ExecutionStatus::Failed => {
                        self.emit_project(
                            &project_id,
                            EventType::TaskBlocked,
                            &agent_id,
                            json!({"task_id": task_id, "reason": result.summary.clone()}),
                        )
                        .await?;
                        let inbox_id = Uuid::new_v4().to_string();
                        self.emit_project(
                            &project_id,
                            EventType::QuestionRaised,
                            &agent_id,
                            json!({
                                "inbox_id": inbox_id,
                                "task_id": task_id,
                                "agent_id": agent_id,
                                "prompt": result.summary,
                            }),
                        )
                        .await?;
                        delta.escalated += 1;
                    }
                }
            }
            Err(e) => {
                self.emit_project(
                    &project_id,
                    EventType::ModelRunFailed,
                    &agent_id,
                    json!({"id": run_id, "error": e.to_string()}),
                )
                .await?;
                self.emit_project(
                    &project_id,
                    EventType::TaskBlocked,
                    &agent_id,
                    json!({"task_id": task_id, "reason": e.to_string()}),
                )
                .await?;
                let inbox_id = Uuid::new_v4().to_string();
                self.emit_project(
                    &project_id,
                    EventType::QuestionRaised,
                    &agent_id,
                    json!({
                        "inbox_id": inbox_id,
                        "task_id": task_id,
                        "agent_id": agent_id,
                        "prompt": e.to_string(),
                    }),
                )
                .await?;
                delta.escalated += 1;
            }
        }

        self.teardown_workspace(&project_id, &task_id, &agent_id, &workspaces, &ws_state).await?;
        Ok(delta)
    }

    /// Ticks every project in `project_ids` to a fixed point: assign ready
    /// tasks, start every now-assigned task (sequential, fast), then batch
    /// every prepared task's real model execution -- across every project in
    /// the same call, not just one -- through `spawn_blocking`, bounded to
    /// `max_concurrent` in flight at once via a semaphore, before finishing
    /// each as its result comes back. This is what gives multiple agents
    /// (same or different projects) genuine wall-clock concurrency without
    /// `Runtime` ever needing shared mutable state: `&mut self` is only held
    /// during the fast assign/start/finish phases, never while a real model
    /// call is in flight.
    pub async fn tick_projects(
        &mut self,
        project_ids: &[String],
        max_concurrent: usize,
        max_attempts: u32,
    ) -> Result<HashMap<String, TickReport>, CoreError> {
        for project_id in project_ids {
            self.require_project(project_id)?;
        }

        let mut reports: HashMap<String, TickReport> =
            project_ids.iter().map(|p| (p.clone(), TickReport::default())).collect();
        let semaphore = Arc::new(tokio::sync::Semaphore::new(max_concurrent.max(1)));

        // Archived projects are removed from active execution entirely for
        // the rest of this call; Paused projects still finish in-flight work
        // below but accept no new assignments.
        let active_project_ids: Vec<String> = project_ids
            .iter()
            .filter(|p| {
                self.state.projects.get(p.as_str()).map(|p| p.status != ProjectStatus::Archived).unwrap_or(false)
            })
            .cloned()
            .collect();

        for _ in 0..1000 {
            let mut progressed = false;

            for project_id in &active_project_ids {
                let paused = self
                    .state
                    .projects
                    .get(project_id)
                    .map(|p| p.status == ProjectStatus::Paused)
                    .unwrap_or(false);
                if paused {
                    continue;
                }
                let assigned = self.assign_ready_tasks(project_id).await?;
                if assigned > 0 {
                    reports.get_mut(project_id).expect("seeded above").assigned += assigned;
                    progressed = true;
                }
            }

            let mut batch: Vec<PreparedTask> = Vec::new();
            for project_id in &active_project_ids {
                let assigned_task_ids: Vec<String> = self
                    .state
                    .tasks
                    .values()
                    .filter(|t| t.project_id == *project_id && t.status == TaskStatus::Assigned)
                    .map(|t| t.id.clone())
                    .collect();

                for task_id in assigned_task_ids {
                    match self.start_task(project_id, &task_id).await? {
                        StartOutcome::Ready(prepared) => batch.push(*prepared),
                        StartOutcome::Resolved(delta) => {
                            *reports.get_mut(project_id).expect("seeded above") += delta;
                            progressed = true;
                        }
                    }
                }
            }

            if !batch.is_empty() {
                progressed = true;
                let mut handles = Vec::with_capacity(batch.len());
                for prepared in batch {
                    let permit = Arc::clone(&semaphore).acquire_owned().await.expect("semaphore is never closed");
                    self.emit_project(
                        &prepared.project_id,
                        EventType::AgentHeartbeat,
                        &prepared.agent_id,
                        json!({"agent_id": prepared.agent_id, "project_id": prepared.project_id, "task_id": prepared.task_id}),
                    )
                    .await?;

                    let model_runtime = Arc::clone(&prepared.model_runtime);
                    let project_id = prepared.project_id.clone();
                    let agent_id = prepared.agent_id.clone();
                    let task_id = prepared.task_id.clone();
                    let prompt_path = prepared.prompt_path.clone();
                    let artifacts_dir = prepared.artifacts_dir.clone();
                    let ws_state = prepared.ws_state.clone();
                    let handle = tokio::task::spawn_blocking(move || {
                        let _permit = permit;
                        let request = ExecutionRequest {
                            project_id: &project_id,
                            agent_id: &agent_id,
                            task_id: Some(&task_id),
                            prompt_path: &prompt_path,
                            artifacts_dir: &artifacts_dir,
                            workspace: &ws_state,
                            json_schema: None,
                        };
                        model_runtime.execute(&request)
                    });
                    handles.push((prepared, handle));
                }

                for (prepared, handle) in handles {
                    let project_id = prepared.project_id.clone();
                    let execution = match handle.await {
                        Ok(result) => result,
                        Err(join_err) => {
                            Err(AgentsError::ProcessFailed(format!("execution task panicked or was cancelled: {join_err}")))
                        }
                    };
                    let delta = self.finish_task(prepared, execution, max_attempts).await?;
                    *reports.get_mut(&project_id).expect("seeded above") += delta;
                }
            }

            if !progressed {
                break;
            }
        }

        Ok(reports)
    }

    /// Convenience wrapper over [`Runtime::tick_projects`] for exactly one
    /// project (used by `beethoven run`); tasks within that project still
    /// execute concurrently, just not alongside any other project's.
    pub async fn run_tick(&mut self, project_id: &str) -> Result<TickReport, CoreError> {
        let mut reports = self
            .tick_projects(&[project_id.to_string()], DEFAULT_MAX_CONCURRENT, DEFAULT_MAX_ATTEMPTS)
            .await?;
        Ok(reports.remove(project_id).unwrap_or_default())
    }

    pub async fn answer_inbox(&mut self, inbox_id: &str, response: &str) -> Result<InboxItem, CoreError> {
        let item = self
            .state
            .inbox
            .get(inbox_id)
            .cloned()
            .ok_or_else(|| CoreError::InboxItemNotFound(inbox_id.to_string()))?;
        if item.status == InboxStatus::Resolved {
            return Err(CoreError::InboxItemAlreadyResolved(inbox_id.to_string()));
        }

        let pm_id = self.pm_instance_id(&item.project_id).unwrap_or_else(|| "system".to_string());
        let event_type = match item.kind {
            InboxKind::Decision => EventType::DecisionResolved,
            InboxKind::Question | InboxKind::Approval | InboxKind::Alert => EventType::QuestionAnswered,
        };

        self.emit_project(
            &item.project_id,
            event_type,
            &pm_id,
            json!({"inbox_id": inbox_id, "response": response}),
        )
        .await?;

        if item.kind == InboxKind::Decision {
            if let Some(decision_id) = item.decision_id.clone() {
                let initiative_id = self.state.decisions.get(&decision_id).map(|d| d.initiative_id.clone());
                if let Some(initiative_id) = initiative_id {
                    self.maybe_complete_initiative(&item.project_id, &initiative_id).await?;
                }
            }
        }

        Ok(self.state.inbox.get(inbox_id).expect("just updated").clone())
    }

    /// Prevents new task assignment for `project_id`; tasks already
    /// in-flight finish normally within the same tick that paused them.
    pub async fn pause_project(&mut self, project_id: &str) -> Result<Project, CoreError> {
        let project = self.require_project(project_id)?;
        if project.status != ProjectStatus::Active {
            return Err(CoreError::InvalidState(format!(
                "project {project_id} is not active (status: {:?})",
                project.status
            )));
        }
        self.emit_global(EventType::ProjectPaused, "human", json!({"id": project_id})).await?;
        Ok(self.state.projects.get(project_id).expect("just updated").clone())
    }

    pub async fn resume_project(&mut self, project_id: &str) -> Result<Project, CoreError> {
        let project = self.require_project(project_id)?;
        if project.status != ProjectStatus::Paused {
            return Err(CoreError::InvalidState(format!(
                "project {project_id} is not paused (status: {:?})",
                project.status
            )));
        }
        self.emit_global(EventType::ProjectResumed, "human", json!({"id": project_id})).await?;
        Ok(self.state.projects.get(project_id).expect("just updated").clone())
    }

    /// Removes `project_id` from active execution entirely -- unlike
    /// `pause_project`, no further task assignment *or* task start/finish
    /// happens for this project in `tick_projects`, even for already
    /// in-flight work.
    pub async fn archive_project(&mut self, project_id: &str) -> Result<Project, CoreError> {
        let project = self.require_project(project_id)?;
        if project.status == ProjectStatus::Archived {
            return Err(CoreError::InvalidState(format!("project {project_id} is already archived")));
        }
        self.emit_global(EventType::ProjectArchived, "human", json!({"id": project_id})).await?;
        Ok(self.state.projects.get(project_id).expect("just updated").clone())
    }

    /// Returns a `Blocked` task to `Ready` for another automatic attempt.
    pub async fn retry_task(&mut self, task_id: &str) -> Result<Task, CoreError> {
        let task = self
            .state
            .tasks
            .get(task_id)
            .cloned()
            .ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if task.status != TaskStatus::Blocked {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} is not blocked (status: {:?})",
                task.status
            )));
        }
        self.emit_project(&task.project_id, EventType::TaskRetried, "human", json!({"task_id": task_id}))
            .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Stops future execution of a task. A task already mid-execution (an
    /// `InProgress` model run already kicked off in this same `tick_projects`
    /// call) is guarded against in `finish_task`, which checks for this
    /// status before resurrecting it to `Done`.
    pub async fn cancel_task(&mut self, task_id: &str) -> Result<Task, CoreError> {
        let task = self
            .state
            .tasks
            .get(task_id)
            .cloned()
            .ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if matches!(task.status, TaskStatus::Done | TaskStatus::Cancelled) {
            return Err(CoreError::InvalidState(format!("task {task_id} is already {:?}", task.status)));
        }
        self.emit_project(&task.project_id, EventType::TaskCancelled, "human", json!({"task_id": task_id}))
            .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Moves ownership of a task to another idle agent in the same project.
    /// Restricted to `Assigned`/`Blocked` tasks -- an `InProgress` task
    /// already has a live workspace and possibly an in-flight model call that
    /// reassignment can't safely interrupt.
    pub async fn reassign_task(&mut self, task_id: &str, new_agent_id: &str) -> Result<Task, CoreError> {
        let task = self
            .state
            .tasks
            .get(task_id)
            .cloned()
            .ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if !matches!(task.status, TaskStatus::Assigned | TaskStatus::Blocked) {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} cannot be reassigned from status {:?}",
                task.status
            )));
        }
        let new_agent = self
            .state
            .agent_instances
            .get(new_agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(new_agent_id.to_string()))?;
        if new_agent.project_id != task.project_id {
            return Err(CoreError::InvalidState(format!(
                "agent {new_agent_id} is not in project {}",
                task.project_id
            )));
        }
        if new_agent.status != AgentStatus::Idle {
            return Err(CoreError::InvalidState(format!("agent {new_agent_id} is not idle")));
        }

        let old_agent_id = task.assigned_agent_id.clone();
        self.emit_project(
            &task.project_id,
            EventType::TaskReassigned,
            "human",
            json!({"task_id": task_id, "old_agent_id": old_agent_id, "new_agent_id": new_agent_id}),
        )
        .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Manual override for an escalated (`Blocked`) task: accept the work
    /// without further changes. By the time a task reaches `Blocked` its
    /// workspace has already been torn down, so this is purely a state
    /// transition, not a new PR/merge -- there is nothing left to merge.
    pub async fn approve_review(&mut self, task_id: &str) -> Result<Task, CoreError> {
        let task = self
            .state
            .tasks
            .get(task_id)
            .cloned()
            .ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if task.status != TaskStatus::Blocked {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} is not blocked (status: {:?})",
                task.status
            )));
        }
        let pm_id = self.pm_instance_id(&task.project_id).unwrap_or_else(|| "system".to_string());
        let review_id = Uuid::new_v4().to_string();
        self.emit_project(
            &task.project_id,
            EventType::ReviewApproved,
            &pm_id,
            json!({"review_id": review_id, "task_id": task_id}),
        )
        .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Manual override for an escalated (`Blocked`) task: return it to
    /// execution for another attempt, same effect as an automatic
    /// `ReviewRejected` outcome.
    pub async fn request_review_changes(&mut self, task_id: &str, reason: &str) -> Result<Task, CoreError> {
        let task = self
            .state
            .tasks
            .get(task_id)
            .cloned()
            .ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if task.status != TaskStatus::Blocked {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} is not blocked (status: {:?})",
                task.status
            )));
        }
        self.emit_project(
            &task.project_id,
            EventType::ReviewChangesRequested,
            "human",
            json!({"task_id": task_id, "reason": reason}),
        )
        .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Prevents new task assignment to an agent. Restricted to `Idle` agents
    /// so this can never silently fight `finish_task`'s "free agent to Idle"
    /// step for an agent that's still mid-task.
    pub async fn pause_agent(&mut self, agent_id: &str) -> Result<AgentInstance, CoreError> {
        let agent = self
            .state
            .agent_instances
            .get(agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(agent_id.to_string()))?;
        if agent.status != AgentStatus::Idle {
            return Err(CoreError::InvalidState(format!(
                "agent {agent_id} is not idle (status: {:?})",
                agent.status
            )));
        }
        self.emit_project(&agent.project_id, EventType::AgentPaused, "human", json!({"id": agent_id}))
            .await?;
        Ok(self.state.agent_instances.get(agent_id).expect("just updated").clone())
    }

    pub async fn resume_agent(&mut self, agent_id: &str) -> Result<AgentInstance, CoreError> {
        let agent = self
            .state
            .agent_instances
            .get(agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(agent_id.to_string()))?;
        if agent.status != AgentStatus::Paused {
            return Err(CoreError::InvalidState(format!(
                "agent {agent_id} is not paused (status: {:?})",
                agent.status
            )));
        }
        self.emit_project(&agent.project_id, EventType::AgentResumed, "human", json!({"id": agent_id}))
            .await?;
        Ok(self.state.agent_instances.get(agent_id).expect("just updated").clone())
    }

    pub async fn create_initiative(
        &mut self,
        project_id: &str,
        title: &str,
        description: &str,
    ) -> Result<Initiative, CoreError> {
        self.require_project(project_id)?;
        let id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::InitiativeCreated,
            "human",
            json!({"id": id, "title": title, "description": description}),
        )
        .await?;
        Ok(self.state.initiatives.get(&id).expect("just inserted").clone())
    }

    pub async fn create_requirement(&mut self, initiative_id: &str, description: &str) -> Result<Requirement, CoreError> {
        let initiative = self
            .state
            .initiatives
            .get(initiative_id)
            .cloned()
            .ok_or_else(|| CoreError::InitiativeNotFound(initiative_id.to_string()))?;
        let id = Uuid::new_v4().to_string();
        self.emit_project(
            &initiative.project_id,
            EventType::RequirementCreated,
            "human",
            json!({"id": id, "initiative_id": initiative_id, "description": description}),
        )
        .await?;
        Ok(self.state.requirements.get(&id).expect("just inserted").clone())
    }

    pub async fn plan_initiative(&mut self, initiative_id: &str) -> Result<PlanOutcome, CoreError> {
        let initiative = self
            .state
            .initiatives
            .get(initiative_id)
            .cloned()
            .ok_or_else(|| CoreError::InitiativeNotFound(initiative_id.to_string()))?;
        let project_id = initiative.project_id.clone();
        let pm_id = self.pm_instance_id(&project_id).unwrap_or_else(|| "system".to_string());

        self.emit_project(&project_id, EventType::PMPlanningStarted, &pm_id, json!({"initiative_id": initiative_id}))
            .await?;

        let requirement_inputs: Vec<RequirementInput> = self
            .state
            .requirements
            .values()
            .filter(|r| r.initiative_id == initiative_id)
            .map(|r| RequirementInput {
                id: r.id.clone(),
                description: r.description.clone(),
            })
            .collect();

        let plan = InitiativePlanningService::plan(&initiative.title, &requirement_inputs);

        let summary = format!(
            "{} epic(s), {} decision(s) for {}",
            plan.epics.len(),
            plan.decisions.len(),
            initiative.title
        );
        let plan_id = Uuid::new_v4().to_string();
        self.emit_project(
            &project_id,
            EventType::PlanCreated,
            &pm_id,
            json!({"id": plan_id, "initiative_id": initiative_id, "summary": summary}),
        )
        .await?;

        let mut epics = Vec::with_capacity(plan.epics.len());
        let mut tasks = Vec::with_capacity(plan.epics.len());
        for epic_draft in plan.epics {
            let epic_id = Uuid::new_v4().to_string();
            self.emit_project(
                &project_id,
                EventType::EpicCreated,
                &pm_id,
                json!({"id": epic_id, "initiative_id": initiative_id, "title": epic_draft.title}),
            )
            .await?;
            epics.push(self.state.epics.get(&epic_id).expect("just inserted").clone());

            let task_id = Uuid::new_v4().to_string();
            self.emit_project(
                &project_id,
                EventType::TaskCreated,
                &pm_id,
                json!({
                    "id": task_id,
                    "goal_id": None::<String>,
                    "epic_id": epic_id,
                    "parent_task_id": None::<String>,
                    "title": format!("Implement: {}", epic_draft.title),
                    "description": "",
                }),
            )
            .await?;
            tasks.push(self.state.tasks.get(&task_id).expect("just inserted").clone());
        }

        let mut decisions = Vec::with_capacity(plan.decisions.len());
        for decision_draft in plan.decisions {
            let decision_id = Uuid::new_v4().to_string();
            self.emit_project(
                &project_id,
                EventType::DecisionCreated,
                &pm_id,
                json!({
                    "id": decision_id,
                    "initiative_id": initiative_id,
                    "title": decision_draft.title,
                    "context": decision_draft.context,
                }),
            )
            .await?;
            decisions.push(self.state.decisions.get(&decision_id).expect("just inserted").clone());

            let inbox_id = Uuid::new_v4().to_string();
            self.emit_project(
                &project_id,
                EventType::DecisionEscalated,
                &pm_id,
                json!({"decision_id": decision_id, "inbox_id": inbox_id, "prompt": decision_draft.context}),
            )
            .await?;
        }

        self.emit_project(&project_id, EventType::InitiativePlanned, &pm_id, json!({"id": initiative_id}))
            .await?;
        self.emit_project(&project_id, EventType::PMPlanningCompleted, &pm_id, json!({"initiative_id": initiative_id}))
            .await?;

        Ok(PlanOutcome {
            plan: self.state.plans.get(&plan_id).expect("just inserted").clone(),
            epics,
            tasks,
            decisions,
        })
    }

    pub async fn resolve_decision(&mut self, decision_id: &str, response: &str) -> Result<Decision, CoreError> {
        self.state
            .decisions
            .get(decision_id)
            .ok_or_else(|| CoreError::DecisionNotFound(decision_id.to_string()))?;

        let inbox_id = self
            .state
            .inbox
            .values()
            .find(|i| i.decision_id.as_deref() == Some(decision_id) && i.status == InboxStatus::Open)
            .map(|i| i.id.clone())
            .ok_or_else(|| CoreError::DecisionNotFound(decision_id.to_string()))?;

        self.answer_inbox(&inbox_id, response).await?;

        Ok(self.state.decisions.get(decision_id).expect("just updated").clone())
    }

    async fn maybe_complete_initiative(&mut self, project_id: &str, initiative_id: &str) -> Result<(), CoreError> {
        let pm_id = self.pm_instance_id(project_id).unwrap_or_else(|| "system".to_string());

        self.emit_project(project_id, EventType::PMReviewStarted, &pm_id, json!({"initiative_id": initiative_id}))
            .await?;

        let already_completed = self
            .state
            .initiatives
            .get(initiative_id)
            .map(|i| i.status == InitiativeStatus::Completed)
            .unwrap_or(true);

        if !already_completed && self.state.initiative_is_complete(initiative_id) {
            self.emit_project(
                project_id,
                EventType::InitiativeCompleted,
                &pm_id,
                json!({"id": initiative_id}),
            )
            .await?;
        }

        self.emit_project(project_id, EventType::PMReviewCompleted, &pm_id, json!({"initiative_id": initiative_id}))
            .await?;

        Ok(())
    }
}

#[cfg(test)]
mod tests {
    //! White-box tests reaching into private internals (`start_task`,
    //! `finish_task`, `PreparedTask`) to reproduce the exact race
    //! `finish_task`'s cancellation guard defends against -- a task cancelled
    //! after its model run was kicked off but before that run's result comes
    //! back. The public `tick_projects`/`run_tick` API runs `start_task` and
    //! `finish_task` back-to-back within one call with no seam for a test to
    //! inject a cancellation in between, so this can only be exercised here.
    use super::*;
    use async_trait::async_trait;
    use beethoven_agents::RuntimeProfile;
    use std::process::Command;

    fn run_git(dir: &Path, args: &[&str]) {
        let status = Command::new("git").arg("-C").arg(dir).args(args).status().unwrap();
        assert!(status.success(), "git {args:?} failed");
    }

    fn init_repo(path: &Path) {
        std::fs::create_dir_all(path).unwrap();
        run_git(path, &["init", "-q", "-b", "main"]);
        run_git(path, &["config", "user.email", "test@example.com"]);
        run_git(path, &["config", "user.name", "Test"]);
        std::fs::write(path.join("README.md"), "scratch repo\n").unwrap();
        run_git(path, &["add", "-A"]);
        run_git(path, &["commit", "-q", "-m", "init"]);
    }

    #[derive(Clone, Default)]
    struct InMemoryStore {
        events: Arc<std::sync::Mutex<Vec<Event>>>,
    }

    #[async_trait]
    impl EventStore for InMemoryStore {
        async fn append(&self, event: Event) -> Result<(), beethoven_events::EventStoreError> {
            self.events.lock().unwrap().push(event);
            Ok(())
        }

        async fn list_all(&self) -> Result<Vec<Event>, beethoven_events::EventStoreError> {
            Ok(self.events.lock().unwrap().clone())
        }
    }

    /// Never actually invoked in this test -- the model "execution" is faked
    /// directly via a hand-built `ExecutionResult` passed to `finish_task`.
    struct UnusedModelRuntime;

    impl ModelRuntime for UnusedModelRuntime {
        fn provider(&self) -> &'static str {
            "unused-stub"
        }

        fn execute(&self, _request: &ExecutionRequest) -> Result<ExecutionResult, AgentsError> {
            panic!("this test drives finish_task directly and should never invoke the model runtime");
        }
    }

    fn registry_with(runtime: impl ModelRuntime + Send + Sync + 'static) -> RuntimeRegistry {
        let mut registry = RuntimeRegistry::new();
        registry.register_runtime("test", Arc::new(runtime));
        registry.register_profile(RuntimeProfile {
            id: "claude-default".to_string(),
            runtime_id: "test".to_string(),
            model: "test".to_string(),
        });
        registry
    }

    #[tokio::test]
    async fn cancelling_a_task_mid_execution_stops_finish_task_from_resurrecting_it() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();

        let mut runtime = Runtime::load(Box::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        let goal = runtime.create_goal(&project.id, "Docs", "- Write the changelog").await.unwrap();
        runtime.plan_goal(&goal.id).await.unwrap();

        let assigned = runtime.assign_ready_tasks(&project.id).await.unwrap();
        assert_eq!(assigned, 1);

        let task_id = runtime.state.tasks.values().next().unwrap().id.clone();
        let prepared = match runtime.start_task(&project.id, &task_id).await.unwrap() {
            StartOutcome::Ready(prepared) => *prepared,
            StartOutcome::Resolved(_) => panic!("expected a plain completion path, not an early resolution"),
        };
        assert_eq!(runtime.state.tasks[&task_id].status, TaskStatus::InProgress);

        // The cancellation a human issues through the UI while the model run
        // above is still (hypothetically) in flight.
        let cancelled = runtime.cancel_task(&task_id).await.unwrap();
        assert_eq!(cancelled.status, TaskStatus::Cancelled);

        let ws_path = Path::new(&prepared.ws_state.git_worktree).to_path_buf();
        assert!(ws_path.exists());

        let execution = Ok(ExecutionResult {
            status: ExecutionStatus::Completed,
            summary: "late result that must be ignored".to_string(),
            raw_response: "ok".to_string(),
            structured_output: None,
            cost_usd: None,
        });
        let delta = runtime.finish_task(prepared, execution, DEFAULT_MAX_ATTEMPTS).await.unwrap();

        assert_eq!(delta.completed, 0);
        assert_eq!(runtime.state.tasks[&task_id].status, TaskStatus::Cancelled);
        // `start_task` already recorded the run as `Running` (via
        // `ModelRunStarted`) before the cancellation happened; the guard's
        // job is just to never let it reach `Completed`.
        assert!(runtime
            .state
            .model_runs
            .values()
            .filter(|r| r.task_id.as_deref() == Some(task_id.as_str()))
            .all(|r| r.status == ModelRunStatus::Running));
        assert!(!ws_path.exists(), "the orphaned workspace from the cancelled run must still be torn down");

        let events = runtime.list_events().await.unwrap();
        assert!(!events.iter().any(|e| e.event_type == EventType::TaskCompleted));
        assert!(!events.iter().any(|e| e.event_type == EventType::ModelRunCompleted));
    }
}
