use crate::error::CoreError;
use crate::models::*;
use crate::planning_summary::{
    PlanQualityMetrics, PlannedDecisionExplanation, PlannedTaskExplanation, PlanningSummary, ScoreBandCount,
};
use crate::state::RuntimeState;
use beethoven_agents::{
    AgentRole, AgentsError, DelegationAgent, DelegationAgentStatus, DelegationDecision, DelegationService,
    DelegationTask, ExecutionOutcome, ExecutionRequest, ExecutionResult, ExecutionService, ExecutionStatus,
    FifoScheduler, ModelRuntime, PlanOutcome, PlannerPromptContext, PlanningService, PromptBuilder, QueuedTask,
    ReviewOutcome, ReviewService, RuntimeRegistry, Scheduler, TaskExecutionContext, TaskQueue, WorkerPromptContext,
    PLANNER_RESPONSE_SCHEMA,
};
use beethoven_events::{Event, EventStore, EventType};
use beethoven_workspaces::{tmux_session_name, WorkspaceManager, WorkspaceRequest, WorkspaceState};
use serde_json::{json, Value};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};
use uuid::Uuid;

/// Default policy values for `run_tick`'s single-project convenience wrapper
/// and `recover()`'s CLI default. The daemon (`beethoven start`) exposes both
/// as `--max-concurrent`/`--max-attempts` flags instead of these constants.
const DEFAULT_MAX_CONCURRENT: usize = 4;
const DEFAULT_MAX_ATTEMPTS: u32 = 3;

/// Default quality bar for a task created without an explicit one.
const DEFAULT_TARGET_SCORE: f32 = 70.0;

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
    lead_id: String,
    task_id: String,
    task: Task,
    agent_id: String,
    run_id: String,
    ws_state: WorkspaceState,
    prompt_path: PathBuf,
    artifacts_dir: PathBuf,
    model_runtime: Arc<dyn ModelRuntime + Send + Sync>,
    store: Arc<dyn EventStore>,
}

/// Outcome of `start_task`: either the task is ready for a real model
/// execution, or it was already fully resolved without one (the
/// execution-gate heuristic raised a question / spawned a subtask, or
/// workspace/git/tmux/io setup itself failed and was escalated).
enum StartOutcome {
    Ready(Box<PreparedTask>),
    Resolved(TickReport),
}

/// Result of planning a task: the children it produced (real work to do),
/// any decisions auto-escalated to the inbox, and the id of the planning
/// `Run` whose `PlanningSummary` artifact explains why each of them exists.
/// `Serialize` so the Tauri `plan_task` command can hand it straight to the
/// frontend.
#[derive(Debug, Clone, serde::Serialize)]
pub struct TaskPlanOutcome {
    pub tasks: Vec<Task>,
    pub decisions: Vec<Decision>,
    pub planning_run_id: String,
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

/// Deterministic keyword scan over generated titles/questions -- an
/// observability annotation for the Planning Summary's "Risk Areas," not a
/// planning-quality judgment: it only labels what was already generated,
/// never changes it.
const RISK_KEYWORDS: [&str; 6] = ["security", "auth", "payment", "migration", "production", "delete"];

/// Plain-text rendering of one planning run -- goal, generated counts, a
/// sequential execution order (creation order, since the planner never
/// generates dependencies to reorder it by), flagged risk areas, and (for a
/// model-driven run) the model's own stated reasoning. Lands in the
/// planning Run's `response` field, so it displays for free through the
/// existing Task Detail "Response" view.
fn render_planning_summary(
    task: &Task,
    tasks: &[Task],
    decisions: &[Decision],
    metrics: &PlanQualityMetrics,
    reasoning: &str,
) -> String {
    let mut out = String::new();
    out.push_str("Planning Summary\n\n");
    out.push_str(&format!("Goal:\n{}\n\n", task.title));
    out.push_str(&format!(
        "Generated:\n- {} Task(s)\n- {} Dependency(ies)\n- {} Decision(s)\n\n",
        tasks.len(),
        metrics.dependency_count,
        decisions.len()
    ));

    if tasks.is_empty() {
        out.push_str("Execution Order:\nNone -- no tasks were generated.\n\n");
    } else {
        let order: Vec<&str> = tasks.iter().map(|t| t.title.as_str()).collect();
        out.push_str(&format!(
            "Execution Order (creation order -- no dependencies generated yet):\n{}\n\n",
            order.join("\n→ ")
        ));
    }

    if !tasks.is_empty() {
        out.push_str("Success Criteria (count per task):\n");
        for t in tasks {
            out.push_str(&format!("- {}: {} criteria\n", t.title, t.success_criteria.len()));
        }
        out.push_str(&format!(
            "Coverage: {}/{} tasks ({:.0}%), {:.1} criteria/task average\n\n",
            metrics.tasks_with_criteria,
            tasks.len(),
            metrics.criteria_coverage_percent,
            metrics.average_criteria_per_task
        ));

        out.push_str("Target Scores (quality bar per task):\n");
        for t in tasks {
            out.push_str(&format!("- {}: {:.0}\n", t.title, t.target_score));
        }
        out.push_str(&format!(
            "Average: {:.1}, Highest: {:.0}, Lowest: {:.0}\n",
            metrics.average_target_score, metrics.highest_target_score, metrics.lowest_target_score
        ));
        out.push_str("Score Distribution:\n");
        for band in &metrics.score_distribution {
            out.push_str(&format!("- {}: {}\n", band.band, band.count));
        }
        out.push('\n');
    }

    let mut risk_areas: Vec<&str> = RISK_KEYWORDS
        .iter()
        .filter(|kw| {
            tasks.iter().any(|t| t.title.to_lowercase().contains(*kw))
                || decisions.iter().any(|d| d.question.to_lowercase().contains(*kw))
        })
        .copied()
        .collect();
    risk_areas.sort_unstable();
    out.push_str("Risk Areas:\n");
    if risk_areas.is_empty() {
        out.push_str("None flagged.\n");
    } else {
        for area in risk_areas {
            out.push_str(&format!("- {area}\n"));
        }
    }

    if !reasoning.is_empty() {
        out.push_str(&format!("\nReasoning:\n{reasoning}\n"));
    }

    out.push_str(&format!("\nPlanning Duration: {}ms\n", metrics.planning_duration_ms));
    out
}

/// The planner's "Existing Task Tree" input: every task already in the
/// project, indented by depth under its parent, oldest-first -- so the
/// model can see what's already been planned and avoid regenerating it.
fn render_task_tree(state: &RuntimeState, project_id: &str) -> String {
    let mut by_parent: HashMap<Option<String>, Vec<&Task>> = HashMap::new();
    for t in state.tasks.values().filter(|t| t.project_id == project_id) {
        by_parent.entry(t.parent_task_id.clone()).or_default().push(t);
    }
    if by_parent.is_empty() {
        return "(no tasks exist yet in this project)".to_string();
    }
    for siblings in by_parent.values_mut() {
        siblings.sort_by_key(|t| t.created_at);
    }

    fn walk(by_parent: &HashMap<Option<String>, Vec<&Task>>, parent: Option<&str>, depth: usize, out: &mut String) {
        let Some(children) = by_parent.get(&parent.map(str::to_string)) else {
            return;
        };
        for child in children {
            out.push_str(&"  ".repeat(depth));
            out.push_str(&format!("- {} [{:?}]\n", child.title, child.status));
            walk(by_parent, Some(child.id.as_str()), depth + 1, out);
        }
    }

    let mut out = String::new();
    walk(&by_parent, None, 0, &mut out);
    out
}

/// The planner's "Current Project Facts" input: a short, factual snapshot
/// of the project it's working within.
fn render_project_facts(state: &RuntimeState, project: &Project) -> String {
    let task_count = state.tasks.values().filter(|t| t.project_id == project.id).count();
    let agent_count = state.agent_instances.values().filter(|a| a.project_id == project.id).count();
    format!(
        "Repository: {}\nDefault branch: {}\nExisting tasks in project: {}\nActive agents in project: {}",
        project.repository_path.display(),
        project.default_branch,
        task_count,
        agent_count,
    )
}

/// Which Phase 2.5C quality band (see `PromptBuilder::planner`'s guide) a
/// generated score falls into -- a post-hoc label for display, never a
/// classification the planner itself computes from.
fn score_band(score: f32) -> &'static str {
    if score >= 95.0 {
        "95-100"
    } else if score >= 90.0 {
        "90-95"
    } else if score >= 80.0 {
        "80-90"
    } else if score >= 70.0 {
        "70-80"
    } else {
        "below-70"
    }
}

/// Buckets this pass's generated target scores into all five quality
/// bands, in band order, even ones with a zero count -- so a histogram has
/// consistent bars across planning runs.
fn score_distribution(scores: &[f32]) -> Vec<ScoreBandCount> {
    ["95-100", "90-95", "80-90", "70-80", "below-70"]
        .into_iter()
        .map(|band| ScoreBandCount {
            band: band.to_string(),
            count: scores.iter().filter(|&&s| score_band(s) == band).count(),
        })
        .collect()
}

pub struct Runtime {
    store: Arc<dyn EventStore>,
    projects_root: PathBuf,
    registry: RuntimeRegistry,
    scheduler: Box<dyn Scheduler + Send + Sync>,
    pub state: RuntimeState,
}

impl Runtime {
    pub async fn load(
        store: Arc<dyn EventStore>,
        projects_root: PathBuf,
        registry: RuntimeRegistry,
    ) -> Result<Self, CoreError> {
        let events = store.list_all().await?;
        let state = RuntimeState::replay(&events);
        Ok(Self {
            store,
            projects_root,
            registry,
            scheduler: Box::new(FifoScheduler),
            state,
        })
    }

    /// Read-only access to the registered runtime profiles, for display
    /// purposes (e.g. read-only Runtime Settings/Agent Configuration views).
    pub fn registry(&self) -> &RuntimeRegistry {
        &self.registry
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
    /// are all deterministic functions of (project, task, agent role), so they
    /// can be reconstructed without having persisted a `WorkspaceState`
    /// anywhere -- safe to call even if the crashed attempt never got far
    /// enough to create a workspace at all (`destroy` is then just a no-op).
    fn cleanup_orphaned_workspace(&self, project_id: &str, task_id: &str, agent_id: &str) {
        let (Some(project), Some(task)) = (self.state.projects.get(project_id), self.state.tasks.get(task_id)) else {
            return;
        };
        let agent_role = self.state.agent_instances.get(agent_id).map(|a| a.role).unwrap_or(AgentRole::Worker);
        let branch = task.branch.clone().unwrap_or_else(|| beethoven_git::branch_name(&task.title));
        let workspaces = self.workspace_manager_for(&project.slug);
        let git_worktree = workspaces.path_for(task_id).join("repo").to_string_lossy().into_owned();

        let _ = workspaces.destroy(&WorkspaceState {
            task_id: task_id.to_string(),
            created_at: chrono::Utc::now(),
            repository_path: project.repository_path.to_string_lossy().into_owned(),
            branch,
            tmux_session: tmux_session_name(&project.slug, agent_role.as_str(), task_id),
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

    fn template_id_for(&self, role: AgentRole) -> Option<String> {
        self.state
            .agent_templates
            .values()
            .find(|t| t.role == role)
            .map(|t| t.id.clone())
    }

    fn lead_instance_id(&self, project_id: &str) -> Option<String> {
        self.state
            .agent_instances
            .values()
            .find(|a| a.project_id == project_id && a.role == AgentRole::Lead)
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
        for role in AgentRole::ALL {
            let id = Uuid::new_v4().to_string();
            let prompt = match role {
                AgentRole::Lead => "You are the lead agent for this project.",
                AgentRole::Worker => "You are a worker agent.",
                AgentRole::Reviewer => "You are a reviewer agent.",
            };
            self.emit_global(
                EventType::AgentTemplateCreated,
                "system",
                json!({
                    "id": id,
                    "role": role,
                    "label": role.display_name(),
                    "prompt": prompt,
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

        let lead_template_id = self.template_id_for(AgentRole::Lead).expect("templates were just ensured");
        let lead_id = Uuid::new_v4().to_string();
        self.emit_project(
            &id,
            EventType::AgentSpawned,
            "system",
            json!({
                "id": lead_id,
                "template_id": lead_template_id,
                "role": AgentRole::Lead,
                "role_description": format!("Lead agent for {name}"),
                "parent_agent_id": None::<String>,
                "name": format!("{name} Lead"),
            }),
        )
        .await?;

        Ok(self.state.projects.get(&id).expect("just inserted").clone())
    }

    /// Spawns a Worker/Reviewer under `parent_agent_id`, which must be the
    /// project's Lead -- workers/reviewers never spawn their own children in
    /// this model.
    pub async fn spawn_agent(
        &mut self,
        project_id: &str,
        parent_agent_id: &str,
        role: AgentRole,
        role_description: &str,
        name: &str,
    ) -> Result<AgentInstance, CoreError> {
        if role == AgentRole::Lead {
            return Err(CoreError::InvalidState("cannot spawn a second Lead agent".to_string()));
        }
        let parent = self
            .state
            .agent_instances
            .get(parent_agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(parent_agent_id.to_string()))?;
        if parent.role != AgentRole::Lead {
            return Err(CoreError::InvalidState(format!(
                "agent {parent_agent_id} is not the Lead (role: {:?})",
                parent.role
            )));
        }

        let template_id = self.template_id_for(role).expect("templates were just ensured");
        let id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::AgentSpawned,
            parent_agent_id,
            json!({
                "id": id,
                "template_id": template_id,
                "role": role,
                "role_description": role_description,
                "parent_agent_id": parent_agent_id,
                "name": name,
            }),
        )
        .await?;
        Ok(self.state.agent_instances.get(&id).expect("just inserted").clone())
    }

    /// Archives a Worker/Reviewer from `Created`/`Idle`/`Completed` only --
    /// never mid-task, never the Lead.
    pub async fn archive_agent(&mut self, agent_id: &str) -> Result<AgentInstance, CoreError> {
        let agent = self
            .state
            .agent_instances
            .get(agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(agent_id.to_string()))?;
        if agent.role == AgentRole::Lead {
            return Err(CoreError::InvalidState("the Lead agent cannot be archived".to_string()));
        }
        if !matches!(agent.status, AgentStatus::Created | AgentStatus::Idle | AgentStatus::Completed) {
            return Err(CoreError::InvalidState(format!(
                "agent {agent_id} cannot be archived from status {:?}",
                agent.status
            )));
        }
        self.emit_project(&agent.project_id, EventType::AgentArchived, "system", json!({"id": agent_id}))
            .await?;
        Ok(self.state.agent_instances.get(agent_id).expect("just updated").clone())
    }

    /// Creates a task anywhere in the tree -- `parent_task_id: None` for a
    /// root objective (the old Goal/Initiative), `Some(id)` for a child
    /// (the old Epic/subtask). `draft: true` creates it in `TaskStatus::Draft`
    /// instead of `Ready` -- proposed work pending human approval (the old
    /// ChangeRequest), invisible to delegation until `approve_draft_task`.
    /// `success_criteria` stays a single string at this human-facing surface
    /// for CLI/Tauri ergonomics -- one criterion per non-empty line, an
    /// optional leading `- `/`* ` stripped -- and is split into the real
    /// `Vec<String>` storage shape via `parse_success_criteria_text` before
    /// it's stored.
    pub async fn create_task(
        &mut self,
        project_id: &str,
        parent_task_id: Option<&str>,
        title: &str,
        description: &str,
        success_criteria: &str,
        target_score: Option<f32>,
        draft: bool,
    ) -> Result<Task, CoreError> {
        self.require_project(project_id)?;
        if let Some(parent_id) = parent_task_id {
            self.state.tasks.get(parent_id).ok_or_else(|| CoreError::TaskNotFound(parent_id.to_string()))?;
        }
        let id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::TaskCreated,
            "human",
            json!({
                "id": id,
                "parent_task_id": parent_task_id,
                "title": title,
                "description": description,
                "status": if draft { TaskStatus::Draft } else { TaskStatus::Ready },
                "success_criteria": parse_success_criteria_text(success_criteria),
                "target_score": target_score.unwrap_or(DEFAULT_TARGET_SCORE),
            }),
        )
        .await?;
        Ok(self.state.tasks.get(&id).expect("just inserted").clone())
    }

    /// Breaks a task down into children via a real model call (Phase 2.5A):
    /// stands up a real workspace exactly like a worker task would (the
    /// only path this codebase has for invoking a model), prompts it with
    /// the goal plus the project's existing task tree/facts, and turns its
    /// structured response into child `Task`s and any `Decision`s it
    /// raised. Works at any level of the tree -- a freshly-created root
    /// task and a task several levels deep are planned identically.
    /// Dependencies, success criteria, and target scores are untouched --
    /// Phase 2.5A's planner only ever produces `{title, description,
    /// reason}` per task, same as the legacy stub planner did.
    pub async fn plan_task(&mut self, task_id: &str) -> Result<TaskPlanOutcome, CoreError> {
        let task = self.state.tasks.get(task_id).cloned().ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        let project_id = task.project_id.clone();
        let project = self.require_project(&project_id)?;
        let lead_id = self.lead_instance_id(&project_id).unwrap_or_else(|| "system".to_string());
        let started_at = Instant::now();

        self.emit_project(&project_id, EventType::TaskPlanningStarted, &lead_id, json!({"task_id": task_id}))
            .await?;

        let runtime_profile_id = self
            .state
            .agent_instances
            .get(&lead_id)
            .and_then(|agent| self.state.agent_templates.get(&agent.template_id))
            .map(|template| template.runtime_profile_id.clone())
            .unwrap_or_else(|| "claude-default".to_string());
        let model_runtime = self
            .registry
            .resolve(&runtime_profile_id)
            .map_err(|e| CoreError::InvalidState(e.to_string()))?;

        // Standing up a real git worktree/branch/tmux session purely to run
        // a planning prompt is the same cost every worker task already
        // pays for every attempt -- there is no second, lighter-weight way
        // to invoke a model in this codebase, and reusing this machinery
        // (rather than adding one) keeps planning Runs and worker Runs
        // identical in shape.
        let workspaces = self.workspace_manager_for(&project.slug);
        let repository_path = project.repository_path.to_string_lossy().into_owned();
        let branch = beethoven_git::branch_name(&format!("plan-{}", task.title));
        let ws_state = match workspaces.create(&WorkspaceRequest {
            project_slug: &project.slug,
            agent_label: "planner",
            task_id,
            repository_path: &repository_path,
            default_branch: &project.default_branch,
            branch: &branch,
        }) {
            Ok(ws) => ws,
            Err(e) => {
                // Unlike a worker task's workspace failure, no run has been
                // minted yet -- this is an infra failure (git/tmux/io), not
                // a planning judgment one, so it escalates the same way
                // `escalate_start_failure` does rather than via a Decision.
                let _ = std::fs::remove_dir_all(workspaces.path_for(task_id));
                return self.escalate_planning_infra_failure(&project_id, task_id, &lead_id, &e.to_string()).await;
            }
        };
        self.emit_project(
            &project_id,
            EventType::WorkspaceCreated,
            &lead_id,
            json!({"task_id": task_id, "path": ws_state.git_worktree}),
        )
        .await?;
        self.emit_project(
            &project_id,
            EventType::WorktreeCreated,
            &lead_id,
            json!({"task_id": task_id, "path": ws_state.git_worktree}),
        )
        .await?;
        self.emit_project(
            &project_id,
            EventType::BranchCreated,
            &lead_id,
            json!({"task_id": task_id, "branch": ws_state.branch}),
        )
        .await?;
        self.emit_project(
            &project_id,
            EventType::TmuxSessionCreated,
            &lead_id,
            json!({"task_id": task_id, "session_name": ws_state.tmux_session}),
        )
        .await?;

        let existing_task_tree = render_task_tree(&self.state, &project_id);
        let project_facts = render_project_facts(&self.state, &project);
        let prompt = PromptBuilder::planner(&PlannerPromptContext {
            project_name: &project.name,
            task_title: &task.title,
            task_description: &task.description,
            existing_task_tree: &existing_task_tree,
            project_facts: &project_facts,
        });
        let prompt_path = workspaces.path_for(task_id).join("prompts").join("plan_prompt.md");
        if let Err(e) = std::fs::write(&prompt_path, &prompt) {
            self.teardown_workspace(&project_id, task_id, &lead_id, &workspaces, &ws_state).await?;
            return self.escalate_planning_infra_failure(&project_id, task_id, &lead_id, &e.to_string()).await;
        }

        let run_id = Uuid::new_v4().to_string();
        self.emit_project(
            &project_id,
            EventType::PromptGenerated,
            &lead_id,
            json!({"run_id": run_id, "task_id": task_id, "prompt": prompt}),
        )
        .await?;
        self.emit_project(
            &project_id,
            EventType::RunStarted,
            &lead_id,
            json!({
                "id": run_id,
                "agent_id": lead_id,
                "project_id": project_id,
                "task_id": task_id,
                "attempt": task.attempt,
                "provider": model_runtime.provider(),
            }),
        )
        .await?;

        // The actual model call is the one part of planning worth running
        // off the async runtime's worker thread -- same reasoning as the
        // worker execution path's `spawn_blocking`, just for a single call
        // instead of a batch (planning isn't part of the concurrent
        // multi-task tick). Everything the blocking closure touches is
        // cloned into owned locals first since none of it can borrow from
        // `self` or a stack frame across the `'static` boundary.
        let artifacts_dir = workspaces.path_for(task_id).join("artifacts");
        let blocking_runtime = Arc::clone(&model_runtime);
        let blocking_project_id = project_id.clone();
        let blocking_agent_id = lead_id.clone();
        let blocking_task_id = task_id.to_string();
        let blocking_prompt_path = prompt_path.clone();
        let blocking_artifacts_dir = artifacts_dir.clone();
        let blocking_ws_state = ws_state.clone();
        let execution = match tokio::task::spawn_blocking(move || {
            let request = ExecutionRequest {
                project_id: &blocking_project_id,
                agent_id: &blocking_agent_id,
                task_id: Some(&blocking_task_id),
                prompt_path: &blocking_prompt_path,
                artifacts_dir: &blocking_artifacts_dir,
                workspace: &blocking_ws_state,
                json_schema: Some(PLANNER_RESPONSE_SCHEMA),
            };
            blocking_runtime.execute(&request, &|| {})
        })
        .await
        {
            Ok(result) => result,
            Err(join_err) => {
                Err(AgentsError::ProcessFailed(format!("planning execution panicked or was cancelled: {join_err}")))
            }
        };

        self.teardown_workspace(&project_id, task_id, &lead_id, &workspaces, &ws_state).await?;

        match execution {
            Err(e) => {
                self.emit_project(&project_id, EventType::RunFailed, &lead_id, json!({"id": run_id, "error": e.to_string()}))
                    .await?;
                self.escalate_planning_model_failure(
                    &project_id,
                    task_id,
                    &task.title,
                    &lead_id,
                    &run_id,
                    started_at,
                    &e.to_string(),
                )
                .await
            }
            Ok(result) => {
                self.emit_project(
                    &project_id,
                    EventType::RunCompleted,
                    &lead_id,
                    json!({
                        "id": run_id,
                        "summary": result.summary,
                        "response": result.raw_response,
                        "cost_usd": result.cost_usd,
                    }),
                )
                .await?;

                match PlanningService::parse_model_response(result.structured_output.as_ref(), &result.raw_response) {
                    Err(e) => {
                        self.escalate_planning_model_failure(
                            &project_id,
                            task_id,
                            &task.title,
                            &lead_id,
                            &run_id,
                            started_at,
                            &format!("model response could not be parsed into task drafts: {e}"),
                        )
                        .await
                    }
                    Ok((plan, reasoning)) => {
                        self.apply_plan(&project_id, task_id, &task, &lead_id, &run_id, started_at, plan, reasoning).await
                    }
                }
            }
        }
    }

    /// The success path of `plan_task`: turns a parsed `PlanOutcome` into
    /// real child `Task`s/`Decision`s, computes this pass's quality
    /// metrics, and records the `PlanningSummary` artifact that traces
    /// every one of them back to the model's stated reasoning.
    #[allow(clippy::too_many_arguments)]
    async fn apply_plan(
        &mut self,
        project_id: &str,
        task_id: &str,
        task: &Task,
        lead_id: &str,
        run_id: &str,
        started_at: Instant,
        plan: PlanOutcome,
        reasoning: String,
    ) -> Result<TaskPlanOutcome, CoreError> {
        let mut tasks = Vec::with_capacity(plan.task_drafts.len());
        let mut task_explanations = Vec::with_capacity(plan.task_drafts.len());
        for draft in plan.task_drafts {
            let id = Uuid::new_v4().to_string();
            self.emit_project(
                project_id,
                EventType::TaskCreated,
                lead_id,
                json!({
                    "id": id,
                    "parent_task_id": task_id,
                    "title": draft.title,
                    "description": draft.description,
                    "status": TaskStatus::Ready,
                    "success_criteria": draft.success_criteria.clone(),
                    "target_score": draft.target_score,
                }),
            )
            .await?;
            let created = self.state.tasks.get(&id).expect("just inserted").clone();
            task_explanations.push(PlannedTaskExplanation {
                task_id: created.id.clone(),
                title: created.title.clone(),
                reason: draft.reason,
                target_score: draft.target_score,
                target_score_reason: draft.target_score_reason,
                success_criteria: draft.success_criteria,
                success_criteria_reason: draft.success_criteria_reason,
            });
            tasks.push(created);
        }

        let mut decisions = Vec::with_capacity(plan.decision_drafts.len());
        let mut decision_explanations = Vec::with_capacity(plan.decision_drafts.len());
        for decision_draft in plan.decision_drafts {
            let decision_id = Uuid::new_v4().to_string();
            self.emit_project(
                project_id,
                EventType::DecisionCreated,
                lead_id,
                json!({
                    "id": decision_id,
                    "task_id": task_id,
                    "question": decision_draft.question,
                    "context": decision_draft.context,
                }),
            )
            .await?;
            let created = self.state.decisions.get(&decision_id).expect("just inserted").clone();
            decision_explanations.push(PlannedDecisionExplanation {
                decision_id: created.id.clone(),
                question: created.question.clone(),
                reason: decision_draft.reason,
            });
            decisions.push(created);

            let inbox_id = Uuid::new_v4().to_string();
            self.emit_project(
                project_id,
                EventType::DecisionEscalated,
                lead_id,
                json!({"decision_id": decision_id, "inbox_id": inbox_id, "prompt": decision_draft.context}),
            )
            .await?;
        }

        let average_task_length = if tasks.is_empty() {
            0.0
        } else {
            tasks.iter().map(|t| (t.title.len() + t.description.len()) as f64).sum::<f64>() / tasks.len() as f64
        };
        let tasks_with_criteria = task_explanations.iter().filter(|t| !t.success_criteria.is_empty()).count();
        let total_criteria: usize = task_explanations.iter().map(|t| t.success_criteria.len()).sum();
        let scores: Vec<f32> = tasks.iter().map(|t| t.target_score).collect();
        let average_target_score =
            if scores.is_empty() { 0.0 } else { scores.iter().sum::<f32>() / scores.len() as f32 };
        // `reduce` (not `fold`) since there's no seed value that's
        // guaranteed to lose to every real score on both ends -- an empty
        // iterator falls back to 0.0 instead of a fold seed leaking through.
        let highest_target_score = scores.iter().copied().reduce(f32::max).unwrap_or(0.0);
        let lowest_target_score = scores.iter().copied().reduce(f32::min).unwrap_or(0.0);
        let score_distribution = score_distribution(&scores);
        let metrics = PlanQualityMetrics {
            task_count: tasks.len(),
            average_task_length,
            // Phase 2.5A only generates one level of decomposition, so
            // every task this pass produced is, by construction, a leaf.
            leaf_task_count: tasks.len(),
            // Phase 2.5A's planner never infers dependencies between the
            // tasks it generates -- that's a later phase's job. Kept at
            // zero, honestly, rather than fabricated.
            dependency_count: 0,
            average_dependency_depth: 0.0,
            blocked_task_count: decisions.len(),
            planning_duration_ms: started_at.elapsed().as_millis() as i64,
            tasks_with_criteria,
            average_criteria_per_task: if tasks.is_empty() { 0.0 } else { total_criteria as f64 / tasks.len() as f64 },
            criteria_coverage_percent: if tasks.is_empty() {
                0.0
            } else {
                tasks_with_criteria as f64 / tasks.len() as f64 * 100.0
            },
            average_target_score,
            highest_target_score,
            lowest_target_score,
            score_distribution,
        };
        let summary_text = render_planning_summary(task, &tasks, &decisions, &metrics, &reasoning);
        let planning_summary = PlanningSummary {
            planned_task_id: task_id.to_string(),
            planned_task_title: task.title.clone(),
            tasks: task_explanations,
            decisions: decision_explanations,
            dependencies: Vec::new(),
            metrics,
            task_generation_reasoning: reasoning,
            summary: summary_text,
            generated_at: chrono::Utc::now(),
        };
        self.emit_project(
            project_id,
            EventType::PlanningSummaryGenerated,
            lead_id,
            json!({
                "run_id": run_id,
                "content": serde_json::to_string(&planning_summary).expect("PlanningSummary always serializes"),
            }),
        )
        .await?;

        self.emit_project(project_id, EventType::TaskPlanningCompleted, lead_id, json!({"task_id": task_id}))
            .await?;

        Ok(TaskPlanOutcome { tasks, decisions, planning_run_id: run_id.to_string() })
    }

    /// A workspace/git/tmux/io failure while setting up a planning run --
    /// mirrors `escalate_start_failure`'s worker-task convention exactly,
    /// since this is the same class of infra failure, not a planning
    /// judgment call. No `Run` is minted (nothing ever got far enough to
    /// invoke a model), so the outcome is empty rather than carrying a
    /// failure `Decision`.
    async fn escalate_planning_infra_failure(
        &mut self,
        project_id: &str,
        task_id: &str,
        lead_id: &str,
        reason: &str,
    ) -> Result<TaskPlanOutcome, CoreError> {
        self.emit_project(project_id, EventType::TaskBlocked, lead_id, json!({"task_id": task_id, "reason": reason}))
            .await?;
        let inbox_id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::QuestionRaised,
            lead_id,
            json!({"inbox_id": inbox_id, "task_id": task_id, "agent_id": lead_id, "prompt": reason}),
        )
        .await?;
        Ok(TaskPlanOutcome { tasks: Vec::new(), decisions: Vec::new(), planning_run_id: String::new() })
    }

    /// The planner's own "Failure Cases" path: the model call itself failed,
    /// or it returned output that couldn't be parsed into task/decision
    /// drafts. Per the spec, this routes to a `Decision` in the inbox --
    /// the same mechanism an ambiguous/contradictory *goal* already uses
    /// (the model raises that as a `decisions` entry in its own response,
    /// handled by `apply_plan` like any other), just escalated by Beethoven
    /// itself instead of the model when there's no usable response to read
    /// decisions out of at all.
    #[allow(clippy::too_many_arguments)]
    async fn escalate_planning_model_failure(
        &mut self,
        project_id: &str,
        task_id: &str,
        task_title: &str,
        lead_id: &str,
        run_id: &str,
        started_at: Instant,
        reason: &str,
    ) -> Result<TaskPlanOutcome, CoreError> {
        let decision_id = Uuid::new_v4().to_string();
        let question = format!("Planning failed for \"{task_title}\" -- please review and plan it manually.");
        self.emit_project(
            project_id,
            EventType::DecisionCreated,
            lead_id,
            json!({"id": decision_id, "task_id": task_id, "question": question, "context": reason}),
        )
        .await?;
        let decision = self.state.decisions.get(&decision_id).expect("just inserted").clone();
        let inbox_id = Uuid::new_v4().to_string();
        self.emit_project(
            project_id,
            EventType::DecisionEscalated,
            lead_id,
            json!({"decision_id": decision_id, "inbox_id": inbox_id, "prompt": reason}),
        )
        .await?;

        let metrics = PlanQualityMetrics {
            task_count: 0,
            average_task_length: 0.0,
            leaf_task_count: 0,
            dependency_count: 0,
            average_dependency_depth: 0.0,
            blocked_task_count: 1,
            planning_duration_ms: started_at.elapsed().as_millis() as i64,
            tasks_with_criteria: 0,
            average_criteria_per_task: 0.0,
            criteria_coverage_percent: 0.0,
            average_target_score: 0.0,
            highest_target_score: 0.0,
            lowest_target_score: 0.0,
            score_distribution: score_distribution(&[]),
        };
        let planning_summary = PlanningSummary {
            planned_task_id: task_id.to_string(),
            planned_task_title: task_title.to_string(),
            tasks: Vec::new(),
            decisions: vec![PlannedDecisionExplanation {
                decision_id: decision.id.clone(),
                question: decision.question.clone(),
                reason: reason.to_string(),
            }],
            dependencies: Vec::new(),
            metrics,
            task_generation_reasoning: String::new(),
            summary: format!("Planning Summary\n\nGoal:\n{task_title}\n\nPlanning failed:\n{reason}\n"),
            generated_at: chrono::Utc::now(),
        };
        self.emit_project(
            project_id,
            EventType::PlanningSummaryGenerated,
            lead_id,
            json!({
                "run_id": run_id,
                "content": serde_json::to_string(&planning_summary).expect("PlanningSummary always serializes"),
            }),
        )
        .await?;
        self.emit_project(project_id, EventType::TaskPlanningCompleted, lead_id, json!({"task_id": task_id}))
            .await?;

        Ok(TaskPlanOutcome { tasks: Vec::new(), decisions: vec![decision], planning_run_id: run_id.to_string() })
    }

    /// Approves a `Draft` task (the old ChangeRequest approval) into `Ready`,
    /// making it eligible for delegation.
    pub async fn approve_draft_task(&mut self, task_id: &str) -> Result<Task, CoreError> {
        let task = self.state.tasks.get(task_id).cloned().ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if task.status != TaskStatus::Draft {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} is not a draft (status: {:?})",
                task.status
            )));
        }
        self.emit_project(&task.project_id, EventType::TaskApproved, "human", json!({"task_id": task_id}))
            .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Rejects a `Draft` task (the old ChangeRequest rejection) into
    /// `Cancelled`.
    pub async fn reject_draft_task(&mut self, task_id: &str) -> Result<Task, CoreError> {
        let task = self.state.tasks.get(task_id).cloned().ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        if task.status != TaskStatus::Draft {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} is not a draft (status: {:?})",
                task.status
            )));
        }
        self.emit_project(&task.project_id, EventType::TaskCancelled, "human", json!({"task_id": task_id}))
            .await?;
        Ok(self.state.tasks.get(task_id).expect("just updated").clone())
    }

    /// Walks up the tree from `task_id`, emitting `TaskCompleted` for each
    /// ancestor whose children are now all `Done` and whose own `Decision`s
    /// are all `Resolved` -- stopping at the first ancestor not yet ready.
    /// This is the entire completion mechanism for container tasks (former
    /// Initiative/Epic/Goal): they're never executed or reviewed directly,
    /// only ever completed by their children finishing.
    async fn complete_parent_chain(&mut self, project_id: &str, task_id: &str) -> Result<(), CoreError> {
        let mut current = task_id.to_string();
        while self.state.parent_task_is_complete(&current) {
            self.emit_project(project_id, EventType::TaskCompleted, "system", json!({"task_id": current})).await?;
            match self.state.tasks.get(&current).and_then(|t| t.parent_task_id.clone()) {
                Some(parent_id) => current = parent_id,
                None => break,
            }
        }
        Ok(())
    }

    /// Eligible work for one project, oldest-first: every task
    /// `RuntimeState::is_executable` -- Ready, a leaf, its project Active,
    /// every dependency `Done`, and no unresolved `Decision` against it
    /// directly. Shared by every path that can put a task in an agent's
    /// hands (`run_lead_delegation`'s automatic batch pass and
    /// `claim_next_task`'s explicit pull) so there is exactly one
    /// definition of "ready to release," and dependency/decision/pause
    /// gating is evaluated fresh on every call rather than cached anywhere.
    fn ready_task_queue(&self, project_id: &str) -> TaskQueue {
        TaskQueue::new(
            self.state
                .tasks
                .values()
                .filter(|t| t.project_id == project_id && self.state.is_executable(&t.id))
                .map(|t| QueuedTask { task_id: t.id.clone(), priority: 0, queued_at: t.created_at })
                .collect(),
        )
    }

    /// Hands `task_id` to `agent_id` -- the single place a task moves out of
    /// the scheduler's queue into an agent's hands, whether the Lead matched
    /// it on the agent's behalf (`run_lead_delegation`) or the agent pulled
    /// it itself (`claim_next_task`). `delegated_by` records which: the
    /// Lead's id for the former, `agent_id` itself for a self-pull.
    async fn claim_task_for_agent(
        &mut self,
        project_id: &str,
        delegated_by: &str,
        agent_id: &str,
        task_id: &str,
    ) -> Result<(), CoreError> {
        let objective = self.state.tasks.get(task_id).map(|t| t.title.clone()).unwrap_or_default();
        self.emit_project(
            project_id,
            EventType::AgentDelegatedTask,
            delegated_by,
            json!({"agent_id": agent_id, "task_id": task_id, "delegated_by": delegated_by, "objective": objective}),
        )
        .await?;
        self.emit_project(
            project_id,
            EventType::TaskAssigned,
            delegated_by,
            json!({"task_id": task_id, "agent_id": agent_id}),
        )
        .await?;
        Ok(())
    }

    /// A `Worker`/`Reviewer` agent requesting its next unit of work, pulled
    /// from the scheduler's queue rather than pushed by the Lead -- the
    /// "Worker -> Request Task -> Scheduler -> Receive Task" flow, made a
    /// real callable primitive rather than only ever simulated inside the
    /// automatic tick loop. Returns `None` (not an error) if `agent_id` isn't
    /// currently available to pull -- the Lead itself, already busy/paused/
    /// archived, or the project has no released work right now -- since
    /// "nothing to claim yet" is routine for a worker polling, not a failure.
    pub async fn claim_next_task(&mut self, agent_id: &str) -> Result<Option<Task>, CoreError> {
        let agent = self
            .state
            .agent_instances
            .get(agent_id)
            .cloned()
            .ok_or_else(|| CoreError::AgentNotFound(agent_id.to_string()))?;
        if agent.role == AgentRole::Lead || !matches!(agent.status, AgentStatus::Idle | AgentStatus::Completed) {
            return Ok(None);
        }

        // Project-active, dependency, and decision gating all live in
        // `is_executable` (via `ready_task_queue`) -- nothing to duplicate here.
        let queue = self.ready_task_queue(&agent.project_id);
        let Some(task_id) = self.scheduler.next_tasks(&queue, 1).into_iter().next() else {
            return Ok(None);
        };

        self.claim_task_for_agent(&agent.project_id, agent_id, agent_id, &task_id).await?;
        Ok(self.state.tasks.get(&task_id).cloned())
    }

    /// Has the project's Lead delegate up to `max_tasks` currently-`Ready`
    /// tasks: reuse an available Worker/Reviewer if one exists, otherwise
    /// spawn a new Worker; archive any leftover agent the delegation decides
    /// has no further work. Returns how many tasks were assigned.
    ///
    /// Task *selection* (which Ready tasks, in what order, how many at once)
    /// is the scheduler's job, not this method's: it asks `self.scheduler`
    /// which of `ready_task_queue`'s entries to release. `DelegationService`
    /// then only ever decides *who* (which agent) a released task goes to,
    /// never *whether*.
    async fn run_lead_delegation(&mut self, project_id: &str, max_tasks: usize) -> Result<u32, CoreError> {
        let lead_id = self.lead_instance_id(project_id).unwrap_or_else(|| "system".to_string());

        let queue = self.ready_task_queue(project_id);
        let released_task_ids = self.scheduler.next_tasks(&queue, max_tasks);

        // Preserve the scheduler's release order rather than re-filtering
        // `self.state.tasks` (a `HashMap`, so iteration order is arbitrary).
        let ready_tasks: Vec<DelegationTask> = released_task_ids
            .iter()
            .filter_map(|id| self.state.tasks.get(id))
            .map(|t| DelegationTask { id: t.id.clone(), title: t.title.clone() })
            .collect();

        let agents: Vec<DelegationAgent> = self
            .state
            .agent_instances
            .values()
            .filter(|a| a.project_id == project_id && a.role != AgentRole::Lead)
            .map(|a| DelegationAgent {
                id: a.id.clone(),
                status: match a.status {
                    AgentStatus::Created | AgentStatus::Idle => DelegationAgentStatus::Idle,
                    AgentStatus::Completed => DelegationAgentStatus::Completed,
                    _ => DelegationAgentStatus::Busy,
                },
            })
            .collect();

        let decisions = DelegationService::delegate(&ready_tasks, &agents);

        let mut assigned = 0;
        for decision in decisions {
            match decision {
                DelegationDecision::DelegateToExisting { agent_id, task_id } => {
                    self.claim_task_for_agent(project_id, &lead_id, &agent_id, &task_id).await?;
                    assigned += 1;
                }
                DelegationDecision::SpawnWorker { task_id, role_description, name } => {
                    let agent =
                        self.spawn_agent(project_id, &lead_id, AgentRole::Worker, &role_description, &name).await?;
                    self.claim_task_for_agent(project_id, &lead_id, &agent.id, &task_id).await?;
                    assigned += 1;
                }
                DelegationDecision::ArchiveAgent { agent_id } => {
                    self.archive_agent(&agent_id).await?;
                }
            }
        }
        Ok(assigned)
    }

    /// Phase 1 for one `Assigned` task: `TaskStarted`, real workspace/branch/
    /// tmux setup, the execution-gate heuristic, and (if it's going to run a
    /// real model) prompt generation through `RunStarted`. Never runs
    /// the model itself -- that's the only step worth running concurrently
    /// with other tasks', so it's left for the caller to batch separately.
    async fn start_task(&mut self, project_id: &str, task_id: &str) -> Result<StartOutcome, CoreError> {
        let project = self.require_project(project_id)?;
        let lead_id = self.lead_instance_id(project_id).unwrap_or_else(|| "system".to_string());
        let repository_path = project.repository_path.to_string_lossy().into_owned();
        let workspaces = self.workspace_manager_for(&project.slug);

        let task = self.state.tasks.get(task_id).expect("task exists").clone();
        let agent_id = task.assigned_agent_id.clone().expect("assigned task has agent");
        let agent = self.state.agent_instances.get(&agent_id).cloned();
        let agent_role = agent.as_ref().map(|a| a.role).unwrap_or(AgentRole::Worker);
        let agent_role_description = agent.as_ref().map(|a| a.role_description.clone()).unwrap_or_default();

        self.emit_project(project_id, EventType::TaskStarted, &agent_id, json!({"task_id": task_id}))
            .await?;

        let branch = beethoven_git::branch_name(&task.title);
        let ws_state = match workspaces.create(&WorkspaceRequest {
            project_slug: &project.slug,
            agent_label: agent_role.as_str(),
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
                    role: agent_role,
                    role_description: &agent_role_description,
                });
                let prompt_path = workspaces.path_for(task_id).join("prompts").join("prompt.md");
                if let Err(e) = std::fs::write(&prompt_path, &prompt) {
                    return self.escalate_start_failure(project_id, task_id, &agent_id, &e.to_string()).await;
                }
                self.emit_project(
                    project_id,
                    EventType::PromptGenerated,
                    &agent_id,
                    json!({"run_id": run_id, "task_id": task_id, "prompt": prompt}),
                )
                .await?;
                self.emit_project(
                    project_id,
                    EventType::RunStarted,
                    &agent_id,
                    json!({
                        "id": run_id,
                        "agent_id": agent_id,
                        "project_id": project_id,
                        "task_id": task_id,
                        "attempt": task.attempt,
                        "provider": model_runtime.provider(),
                    }),
                )
                .await?;

                let artifacts_dir = workspaces.path_for(task_id).join("artifacts");
                Ok(StartOutcome::Ready(Box::new(PreparedTask {
                    project_id: project_id.to_string(),
                    project_slug: project.slug.clone(),
                    lead_id,
                    task_id: task_id.to_string(),
                    task,
                    agent_id,
                    run_id,
                    ws_state,
                    prompt_path,
                    artifacts_dir,
                    model_runtime,
                    store: Arc::clone(&self.store),
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
                        "parent_task_id": task_id,
                        "title": draft.title,
                        "description": draft.description,
                        "status": TaskStatus::Ready,
                        "success_criteria": draft.success_criteria,
                        "target_score": draft.target_score,
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
    /// `RunCompleted`/`Failed` through review, completion/escalation,
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
            lead_id,
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
                    EventType::RunCompleted,
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
                                "run_id": run_id,
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
                            json!({"run_id": run_id, "diff": diff_text}),
                        )
                        .await?;

                        let review_id = Uuid::new_v4().to_string();
                        self.emit_project(
                            &project_id,
                            EventType::ReviewStarted,
                            &lead_id,
                            json!({"review_id": review_id, "task_id": task_id, "reviewer_agent_id": lead_id}),
                        )
                        .await?;

                        match ReviewService::review(&task.title, &diff_text) {
                            ReviewOutcome::Scored(scored) if scored.score >= task.target_score => {
                                self.emit_project(
                                    &project_id,
                                    EventType::ReviewApproved,
                                    &lead_id,
                                    json!({
                                        "review_id": review_id,
                                        "task_id": task_id,
                                        "score": scored.score,
                                        "summary": scored.summary,
                                        "recommendations": scored.recommendations,
                                    }),
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
                                    &lead_id,
                                    json!({"task_id": task_id, "url": pr.url}),
                                )
                                .await?;
                                delta.completed += 1;

                                if let Some(parent_id) = task.parent_task_id.clone() {
                                    self.complete_parent_chain(&project_id, &parent_id).await?;
                                }
                            }
                            ReviewOutcome::Scored(scored) => {
                                if task.attempt >= max_attempts {
                                    let give_up = format!(
                                        "giving up after {} attempts: score {} below target {}: {}",
                                        task.attempt, scored.score, task.target_score, scored.summary
                                    );
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
                                        &lead_id,
                                        json!({
                                            "review_id": review_id,
                                            "task_id": task_id,
                                            "score": scored.score,
                                            "summary": scored.summary,
                                            "recommendations": scored.recommendations,
                                        }),
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
                    EventType::RunFailed,
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
                let assigned = self.run_lead_delegation(project_id, max_concurrent).await?;
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
                    let heartbeat_store = Arc::clone(&prepared.store);
                    let handle = tokio::task::spawn_blocking(move || {
                        let _permit = permit;
                        let last_heartbeat = Mutex::new(Instant::now());
                        let on_tick = || {
                            let mut last = last_heartbeat.lock().expect("heartbeat mutex is never poisoned");
                            if last.elapsed() < Duration::from_secs(30) {
                                return;
                            }
                            *last = Instant::now();
                            let event = Event::for_project(
                                project_id.as_str(),
                                EventType::AgentHeartbeat,
                                agent_id.as_str(),
                                json!({"agent_id": agent_id, "project_id": project_id, "task_id": task_id}),
                            );
                            let _ = tokio::runtime::Handle::current().block_on(heartbeat_store.append(event));
                        };
                        let request = ExecutionRequest {
                            project_id: &project_id,
                            agent_id: &agent_id,
                            task_id: Some(&task_id),
                            prompt_path: &prompt_path,
                            artifacts_dir: &artifacts_dir,
                            workspace: &ws_state,
                            json_schema: None,
                        };
                        model_runtime.execute(&request, &on_tick)
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

        let lead_id = self.lead_instance_id(&item.project_id).unwrap_or_else(|| "system".to_string());
        let event_type = match item.kind {
            InboxKind::Decision => EventType::DecisionResolved,
            InboxKind::Question | InboxKind::Approval | InboxKind::Alert => EventType::QuestionAnswered,
        };

        self.emit_project(
            &item.project_id,
            event_type,
            &lead_id,
            json!({"inbox_id": inbox_id, "response": response}),
        )
        .await?;

        if item.kind == InboxKind::Decision {
            if let Some(decision_id) = item.decision_id.clone() {
                let task_id = self.state.decisions.get(&decision_id).and_then(|d| d.task_id.clone());
                if let Some(task_id) = task_id {
                    self.complete_parent_chain(&item.project_id, &task_id).await?;
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

    /// Repointing a project's repository is only safe while it's `Paused`
    /// -- an `Active` project may have in-flight worktrees/tmux sessions
    /// checked out against the old path.
    pub async fn update_repository_path(&mut self, project_id: &str, new_path: String) -> Result<Project, CoreError> {
        let project = self.require_project(project_id)?;
        if project.status != ProjectStatus::Paused {
            return Err(CoreError::InvalidState(format!(
                "project {project_id} must be paused to change its repository path (status: {:?})",
                project.status
            )));
        }
        self.emit_global(
            EventType::RepositoryPathChanged,
            "human",
            json!({"id": project_id, "repository_path": new_path}),
        )
        .await?;
        Ok(self.state.projects.get(project_id).expect("just updated").clone())
    }

    pub async fn update_default_branch(&mut self, project_id: &str, new_branch: String) -> Result<Project, CoreError> {
        let project = self.require_project(project_id)?;
        if project.status != ProjectStatus::Paused {
            return Err(CoreError::InvalidState(format!(
                "project {project_id} must be paused to change its default branch (status: {:?})",
                project.status
            )));
        }
        self.emit_global(
            EventType::DefaultBranchChanged,
            "human",
            json!({"id": project_id, "default_branch": new_branch}),
        )
        .await?;
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

    /// Records that `task_id` may only become executable
    /// (`RuntimeState::is_executable`) once `depends_on_task_id` is `Done`.
    /// Rejects a self-dependency, a cross-project edge, a dependency that
    /// would close a cycle, or either task not existing -- all checked
    /// *before* anything is emitted, so an invalid edge is never written to
    /// the event log in the first place rather than needing to be detected
    /// and repaired afterward.
    pub async fn add_task_dependency(&mut self, task_id: &str, depends_on_task_id: &str) -> Result<(), CoreError> {
        if task_id == depends_on_task_id {
            return Err(CoreError::InvalidState(format!("task {task_id} cannot depend on itself")));
        }
        let task = self.state.tasks.get(task_id).ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        let dependency = self
            .state
            .tasks
            .get(depends_on_task_id)
            .ok_or_else(|| CoreError::TaskNotFound(depends_on_task_id.to_string()))?;
        if task.project_id != dependency.project_id {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} and {depends_on_task_id} are not in the same project"
            )));
        }
        if self.state.creates_cycle(task_id, depends_on_task_id) {
            return Err(CoreError::InvalidState(format!(
                "task {task_id} cannot depend on {depends_on_task_id}: it would create a cycle"
            )));
        }

        let project_id = task.project_id.clone();
        self.emit_project(
            &project_id,
            EventType::TaskDependencyCreated,
            "human",
            json!({"task_id": task_id, "depends_on_task_id": depends_on_task_id}),
        )
        .await?;
        Ok(())
    }

    /// Removes a previously-recorded dependency. A no-op (not an error) if
    /// the edge doesn't exist -- removing something already absent reaches
    /// the same end state either way.
    pub async fn remove_task_dependency(&mut self, task_id: &str, depends_on_task_id: &str) -> Result<(), CoreError> {
        let task = self.state.tasks.get(task_id).ok_or_else(|| CoreError::TaskNotFound(task_id.to_string()))?;
        let project_id = task.project_id.clone();
        self.emit_project(
            &project_id,
            EventType::TaskDependencyRemoved,
            "human",
            json!({"task_id": task_id, "depends_on_task_id": depends_on_task_id}),
        )
        .await?;
        Ok(())
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
        let lead_id = self.lead_instance_id(&task.project_id).unwrap_or_else(|| "system".to_string());
        let review_id = Uuid::new_v4().to_string();
        self.emit_project(
            &task.project_id,
            EventType::ReviewApproved,
            &lead_id,
            json!({
                "review_id": review_id,
                "task_id": task_id,
                "score": task.target_score,
                "summary": "manually approved",
                "recommendations": Vec::<String>::new(),
            }),
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

    /// Reconciles the `AgentMarkedSlow`/`AgentMarkedStalled` audit trail with
    /// every in-scope agent's live health (same thresholds as
    /// `derive::agent_health_for`: Healthy < 60s, Slow 60-180s, Stalled
    /// >= 180s since the last heartbeat). A fresh heartbeat received after
    /// the most recent marker is treated as implicit recovery evidence --
    /// there is no `AgentMarkedHealthy` event in the spec -- so a marker is
    /// only emitted when the live level is *worse* than whichever of
    /// {last marker, an implicit Healthy from a fresher heartbeat} is more
    /// recent. Idempotent: an unchanged level never re-emits. Call once per
    /// daemon tick, alongside `tick_projects`.
    pub async fn sync_agent_health_events(&mut self) -> Result<(), CoreError> {
        #[derive(Clone, Copy, PartialEq, Eq, PartialOrd, Ord)]
        enum Level {
            Healthy,
            Slow,
            Stalled,
        }

        fn level_for_age(age_seconds: i64) -> Level {
            if age_seconds < 60 {
                Level::Healthy
            } else if age_seconds < 180 {
                Level::Slow
            } else {
                Level::Stalled
            }
        }

        let events = self.store.list_all().await?;
        let now = chrono::Utc::now();

        let in_scope: Vec<(String, String)> = self
            .state
            .agent_instances
            .values()
            .filter(|a| {
                matches!(
                    a.status,
                    AgentStatus::Assigned | AgentStatus::Executing
                )
            })
            .map(|a| (a.id.clone(), a.project_id.clone()))
            .collect();

        for (agent_id, project_id) in in_scope {
            let mut last_heartbeat: Option<chrono::DateTime<chrono::Utc>> = None;
            let mut last_marker: Option<(chrono::DateTime<chrono::Utc>, Level)> = None;
            let mut current_task_id: Option<String> = None;

            for e in &events {
                if e.payload.get("agent_id").and_then(|v| v.as_str()) != Some(agent_id.as_str()) {
                    continue;
                }
                match e.event_type {
                    EventType::AgentHeartbeat => {
                        if last_heartbeat.is_none_or(|ts| e.timestamp > ts) {
                            last_heartbeat = Some(e.timestamp);
                            current_task_id =
                                e.payload.get("task_id").and_then(|v| v.as_str()).map(|s| s.to_string());
                        }
                    }
                    EventType::AgentMarkedSlow if last_marker.as_ref().is_none_or(|(ts, _)| e.timestamp > *ts) => {
                        last_marker = Some((e.timestamp, Level::Slow));
                    }
                    EventType::AgentMarkedStalled if last_marker.as_ref().is_none_or(|(ts, _)| e.timestamp > *ts) => {
                        last_marker = Some((e.timestamp, Level::Stalled));
                    }
                    _ => {}
                }
            }

            let effective_last_level = match (last_marker, last_heartbeat) {
                (Some((marker_ts, _)), Some(hb_ts)) if hb_ts > marker_ts => Level::Healthy,
                (Some((_, level)), _) => level,
                (None, _) => Level::Healthy,
            };

            let current_level = match last_heartbeat {
                None => Level::Stalled,
                Some(ts) => level_for_age((now - ts).num_seconds()),
            };

            if current_level != effective_last_level && current_level != Level::Healthy {
                let event_type = match current_level {
                    Level::Slow => EventType::AgentMarkedSlow,
                    Level::Stalled => EventType::AgentMarkedStalled,
                    Level::Healthy => unreachable!("filtered out above"),
                };
                self.emit_project(
                    &project_id,
                    event_type,
                    &agent_id,
                    json!({"agent_id": agent_id, "task_id": current_task_id}),
                )
                .await?;
            }
        }

        Ok(())
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

    /// The worker execution this test drives directly via a hand-built
    /// `ExecutionResult` passed to `finish_task` -- so `execute` must never
    /// be invoked for that. `plan_task` (Phase 2.5A) does call a real model
    /// now, though, so the one planning call this test's setup makes is
    /// handled the same way `tests/runtime.rs`'s fakes handle it: extract
    /// the original title/description back out of the rendered prompt and
    /// run them through the legacy deterministic planner to fabricate a
    /// plausible response.
    struct UnusedModelRuntime;

    impl ModelRuntime for UnusedModelRuntime {
        fn provider(&self) -> &'static str {
            "unused-stub"
        }

        fn execute(&self, request: &ExecutionRequest, _on_tick: &dyn Fn()) -> Result<ExecutionResult, AgentsError> {
            if request.json_schema.is_some() {
                let prompt = std::fs::read_to_string(request.prompt_path).expect("prompt file exists");
                let title = extract_between(&prompt, "Task Title:\n", "\n\n");
                let description =
                    extract_between(&prompt, "-----BEGIN DESCRIPTION-----\n", "\n-----END DESCRIPTION-----");
                let outcome = PlanningService::plan(title, description);
                let structured = serde_json::json!({
                    "reasoning": "unused-stub planning reasoning for tests",
                    "tasks": outcome.task_drafts,
                    "decisions": outcome.decision_drafts,
                });
                return Ok(ExecutionResult {
                    status: ExecutionStatus::Completed,
                    summary: "stub planning response".to_string(),
                    raw_response: structured.to_string(),
                    structured_output: Some(structured),
                    cost_usd: None,
                });
            }
            panic!("this test drives finish_task directly and should never invoke the model runtime for execution");
        }
    }

    fn extract_between<'a>(text: &'a str, start: &str, end: &str) -> &'a str {
        let after_start = &text[text.find(start).expect("start marker present in prompt") + start.len()..];
        &after_start[..after_start.find(end).expect("end marker present in prompt")]
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

        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        let root = runtime
            .create_task(&project.id, None, "Docs", "- Write the changelog", "", None, false)
            .await
            .unwrap();
        runtime.plan_task(&root.id).await.unwrap();

        let assigned = runtime.run_lead_delegation(&project.id, DEFAULT_MAX_CONCURRENT).await.unwrap();
        assert_eq!(assigned, 1);

        let task_id = runtime.state.tasks.values().find(|t| t.id != root.id).unwrap().id.clone();
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
        let execution_run_id = prepared.run_id.clone();

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
        // `RunStarted`) before the cancellation happened; the guard's
        // job is just to never let it reach `Completed`.
        assert!(runtime
            .state
            .runs
            .values()
            .filter(|r| r.task_id == task_id)
            .all(|r| r.status == RunStatus::Running));
        assert!(!ws_path.exists(), "the orphaned workspace from the cancelled run must still be torn down");

        let events = runtime.list_events().await.unwrap();
        assert!(!events.iter().any(|e| e.event_type == EventType::TaskCompleted));
        // The planning run from `plan_task` above legitimately completes --
        // only the cancelled task's own execution run must never reach
        // `RunCompleted`.
        assert!(!events.iter().any(|e| {
            e.event_type == EventType::RunCompleted
                && e.payload.get("id").and_then(|v| v.as_str()) == Some(execution_run_id.as_str())
        }));
    }

    /// Phase 2.1's acceptance criteria in one test: the scheduler (not
    /// `DelegationService`) owns *how many* `Ready` tasks get released per
    /// pass, and never releases more than asked -- the rest stay `Ready`
    /// rather than being silently dropped or assigned anyway.
    #[tokio::test]
    async fn run_lead_delegation_releases_at_most_max_tasks_leaving_the_rest_ready() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();

        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();

        // Three independent root tasks -- each is a leaf (no children yet),
        // so all three are immediately `Ready` and queue-eligible.
        for title in ["Task A", "Task B", "Task C"] {
            runtime.create_task(&project.id, None, title, "", "", None, false).await.unwrap();
        }

        let assigned = runtime.run_lead_delegation(&project.id, 2).await.unwrap();
        assert_eq!(assigned, 2, "the scheduler must cap release to max_tasks");

        let statuses: Vec<TaskStatus> = runtime.state.tasks.values().map(|t| t.status).collect();
        assert_eq!(statuses.iter().filter(|s| **s == TaskStatus::Assigned).count(), 2);
        assert_eq!(
            statuses.iter().filter(|s| **s == TaskStatus::Ready).count(),
            1,
            "the task left out of this release must remain Ready, not lost"
        );
    }

    /// The Lead coordinates delegation; it never executes a task itself, so
    /// it can never be on the other end of a pull.
    #[tokio::test]
    async fn claim_next_task_returns_none_for_the_lead_agent() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();
        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        let task = runtime.create_task(&project.id, None, "Docs", "", "", None, false).await.unwrap();
        let lead_id = runtime.lead_instance_id(&project.id).unwrap();

        let claimed = runtime.claim_next_task(&lead_id).await.unwrap();
        assert!(claimed.is_none());
        assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::Ready);
    }

    /// The literal "Worker -> Request Task -> Scheduler -> Receive Task"
    /// flow: an idle worker pulls its own task rather than waiting for the
    /// Lead to push one. `delegated_by` on the resulting event is the
    /// worker's own id, not the Lead's -- nobody else made this decision.
    #[tokio::test]
    async fn claim_next_task_lets_an_idle_worker_pull_its_own_task() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();
        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        let task = runtime.create_task(&project.id, None, "Docs", "", "", None, false).await.unwrap();
        let lead_id = runtime.lead_instance_id(&project.id).unwrap();
        let worker = runtime.spawn_agent(&project.id, &lead_id, AgentRole::Worker, "generalist", "Worker 1").await.unwrap();

        let claimed = runtime.claim_next_task(&worker.id).await.unwrap().expect("a ready task was available");
        assert_eq!(claimed.id, task.id);
        assert_eq!(runtime.state.tasks[&task.id].status, TaskStatus::Assigned);
        assert_eq!(runtime.state.tasks[&task.id].assigned_agent_id, Some(worker.id.clone()));
        assert_eq!(runtime.state.agent_instances[&worker.id].status, AgentStatus::Assigned);

        let events = runtime.list_events().await.unwrap();
        let delegated = events
            .iter()
            .find(|e| e.event_type == EventType::AgentDelegatedTask)
            .expect("a delegation event was emitted");
        assert_eq!(delegated.payload.get("delegated_by").and_then(|v| v.as_str()), Some(worker.id.as_str()));
    }

    /// A worker that's already mid-task (or paused) has nothing to offer a
    /// second pull -- it must not double-claim, even though another task is
    /// sitting `Ready` in the same project's queue.
    #[tokio::test]
    async fn claim_next_task_returns_none_for_an_agent_that_is_not_idle() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();
        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        runtime.create_task(&project.id, None, "Task A", "", "", None, false).await.unwrap();
        runtime.create_task(&project.id, None, "Task B", "", "", None, false).await.unwrap();
        let lead_id = runtime.lead_instance_id(&project.id).unwrap();
        let worker = runtime.spawn_agent(&project.id, &lead_id, AgentRole::Worker, "generalist", "Worker 1").await.unwrap();

        runtime.claim_next_task(&worker.id).await.unwrap().expect("claims Task A");
        let second_claim = runtime.claim_next_task(&worker.id).await.unwrap();
        assert!(second_claim.is_none(), "a busy worker must not be able to claim a second task");
    }

    /// `pause_project` is documented as preventing new assignment entirely;
    /// an explicit pull must respect that exactly like the automatic
    /// Lead-driven path already does.
    #[tokio::test]
    async fn claim_next_task_returns_none_for_a_paused_project() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();
        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        runtime.create_task(&project.id, None, "Docs", "", "", None, false).await.unwrap();
        let lead_id = runtime.lead_instance_id(&project.id).unwrap();
        let worker = runtime.spawn_agent(&project.id, &lead_id, AgentRole::Worker, "generalist", "Worker 1").await.unwrap();

        runtime.pause_project(&project.id).await.unwrap();
        let claimed = runtime.claim_next_task(&worker.id).await.unwrap();
        assert!(claimed.is_none());
    }

    /// Phase 2.3's scaling goal in one test: nothing in delegation hardcodes
    /// a ceiling on how many workers can exist or how many tasks can be
    /// released in one pass -- a dozen ready tasks spawn a dozen workers.
    #[tokio::test]
    async fn run_lead_delegation_scales_to_a_dozen_ready_tasks_with_no_hardcoded_worker_cap() {
        let store = InMemoryStore::default();
        let dir = tempfile::tempdir().unwrap();
        let mut runtime = Runtime::load(Arc::new(store), dir.path().join("projects"), registry_with(UnusedModelRuntime))
            .await
            .unwrap();

        init_repo(&dir.path().join("repo"));
        let project = runtime.create_project("Sample", dir.path().join("repo"), "main").await.unwrap();
        const N: usize = 12;
        for i in 0..N {
            runtime.create_task(&project.id, None, &format!("Task {i}"), "", "", None, false).await.unwrap();
        }

        let assigned = runtime.run_lead_delegation(&project.id, N).await.unwrap();
        assert_eq!(assigned, N as u32);
        let workers = runtime.state.agent_instances.values().filter(|a| a.role == AgentRole::Worker).count();
        assert_eq!(workers, N, "one fresh worker per ready task, with no spawn ceiling");
    }
}
