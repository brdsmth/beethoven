use crate::error::CoreError;
use crate::models::*;
use crate::state::RuntimeState;
use beethoven_agents::{
    AgentKind, ExecutionOutcome, ExecutionRequest, ExecutionService, ExecutionStatus, InitiativePlanningService,
    ModelRuntime, PlanningService, PromptBuilder, RequirementInput, ReviewOutcome, ReviewService,
    TaskExecutionContext, WorkerPromptContext,
};
use beethoven_events::{Event, EventStore, EventType};
use beethoven_workspaces::{WorkspaceManager, WorkspaceRequest};
use serde_json::{json, Value};
use std::path::{Path, PathBuf};
use uuid::Uuid;

#[derive(Debug, Default, Clone)]
pub struct TickReport {
    pub assigned: u32,
    pub completed: u32,
    pub needs_changes: u32,
    pub escalated: u32,
    pub subtasks_spawned: u32,
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
    model_runtime: Box<dyn ModelRuntime + Send + Sync>,
    pub state: RuntimeState,
}

impl Runtime {
    pub async fn load(
        store: Box<dyn EventStore>,
        projects_root: PathBuf,
        model_runtime: Box<dyn ModelRuntime + Send + Sync>,
    ) -> Result<Self, CoreError> {
        let events = store.list_all().await?;
        let state = RuntimeState::replay(&events);
        Ok(Self {
            store,
            projects_root,
            model_runtime,
            state,
        })
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

    pub async fn run_tick(&mut self, project_id: &str) -> Result<TickReport, CoreError> {
        let project = self.require_project(project_id)?;
        let pm_id = self.pm_instance_id(project_id).unwrap_or_else(|| "system".to_string());
        let repository_path = project.repository_path.to_string_lossy().into_owned();
        let workspaces = self.workspace_manager_for(&project.slug);
        let mut report = TickReport::default();

        for _ in 0..1000 {
            let mut progressed = false;

            let ready_task_ids: Vec<String> = self
                .state
                .tasks
                .values()
                .filter(|t| t.project_id == project_id && t.status == TaskStatus::Ready)
                .map(|t| t.id.clone())
                .collect();

            for task_id in ready_task_ids {
                let idle_agent_id = self
                    .state
                    .agent_instances
                    .values()
                    .find(|a| {
                        a.project_id == project_id
                            && a.status == AgentStatus::Idle
                            && a.kind != AgentKind::ProjectManager
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
                    report.assigned += 1;
                    progressed = true;
                }
            }

            let assigned_task_ids: Vec<String> = self
                .state
                .tasks
                .values()
                .filter(|t| t.project_id == project_id && t.status == TaskStatus::Assigned)
                .map(|t| t.id.clone())
                .collect();

            for task_id in assigned_task_ids {
                let task = self.state.tasks.get(&task_id).expect("task exists").clone();
                let agent_id = task.assigned_agent_id.clone().expect("assigned task has agent");
                let agent_kind = self
                    .state
                    .agent_instances
                    .get(&agent_id)
                    .map(|a| a.kind)
                    .unwrap_or(AgentKind::Backend);

                self.emit_project(project_id, EventType::TaskStarted, &agent_id, json!({"task_id": task_id}))
                    .await?;

                let branch = beethoven_git::branch_name(&task.title);
                let ws_state = workspaces.create(&WorkspaceRequest {
                    project_slug: &project.slug,
                    agent_label: agent_kind.as_str(),
                    task_id: &task_id,
                    repository_path: &repository_path,
                    default_branch: &project.default_branch,
                    branch: &branch,
                })?;
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
                    task_id: task_id.clone(),
                    title: task.title.clone(),
                    attempt: task.attempt,
                });

                match outcome {
                    ExecutionOutcome::Completed => {
                        let run_id = Uuid::new_v4().to_string();

                        let prompt = PromptBuilder::worker(&WorkerPromptContext {
                            project_name: &project.name,
                            task_title: &task.title,
                            task_description: &task.description,
                            repository_path: &ws_state.git_worktree,
                            branch: &ws_state.branch,
                        });
                        let prompt_path = workspaces.path_for(&task_id).join("prompts").join("prompt.md");
                        std::fs::write(&prompt_path, &prompt)?;
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
                                "provider": self.model_runtime.provider(),
                            }),
                        )
                        .await?;

                        let artifacts_dir = workspaces.path_for(&task_id).join("artifacts");
                        let execution = self.model_runtime.execute(&ExecutionRequest {
                            project_id,
                            agent_id: &agent_id,
                            task_id: Some(&task_id),
                            prompt_path: &prompt_path,
                            artifacts_dir: &artifacts_dir,
                            workspace: &ws_state,
                            json_schema: None,
                        });

                        match execution {
                            Ok(result) => {
                                self.emit_project(
                                    project_id,
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
                                        let git_status = beethoven_git::status(Path::new(&ws_state.git_worktree))?;
                                        self.emit_project(
                                            project_id,
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
                                            project_id,
                                            EventType::DiffCaptured,
                                            &agent_id,
                                            json!({"task_id": task_id, "diff": diff_text}),
                                        )
                                        .await?;

                                        let review_id = Uuid::new_v4().to_string();
                                        self.emit_project(
                                            project_id,
                                            EventType::ReviewStarted,
                                            &pm_id,
                                            json!({"review_id": review_id, "task_id": task_id, "reviewer_agent_id": pm_id}),
                                        )
                                        .await?;

                                        match ReviewService::review(&task.title, &diff_text) {
                                            ReviewOutcome::Approved => {
                                                self.emit_project(
                                                    project_id,
                                                    EventType::ReviewApproved,
                                                    &pm_id,
                                                    json!({"review_id": review_id, "task_id": task_id}),
                                                )
                                                .await?;

                                                let pr = beethoven_git::open_pr(&task_id, &ws_state.branch, &task.title);
                                                self.emit_project(
                                                    project_id,
                                                    EventType::PRCreated,
                                                    &agent_id,
                                                    json!({"task_id": task_id, "branch": ws_state.branch, "url": pr.url}),
                                                )
                                                .await?;
                                                self.emit_project(
                                                    project_id,
                                                    EventType::TaskCompleted,
                                                    &agent_id,
                                                    json!({"task_id": task_id}),
                                                )
                                                .await?;
                                                self.emit_project(
                                                    project_id,
                                                    EventType::PRMerged,
                                                    &pm_id,
                                                    json!({"task_id": task_id, "url": pr.url}),
                                                )
                                                .await?;
                                                report.completed += 1;

                                                if let Some(epic_id) = task.epic_id.clone() {
                                                    let initiative_id =
                                                        self.state.epics.get(&epic_id).map(|e| e.initiative_id.clone());
                                                    if let Some(initiative_id) = initiative_id {
                                                        self.maybe_complete_initiative(project_id, &initiative_id).await?;
                                                    }
                                                }
                                            }
                                            ReviewOutcome::NeedsChanges(reason) => {
                                                self.emit_project(
                                                    project_id,
                                                    EventType::ReviewRejected,
                                                    &pm_id,
                                                    json!({"review_id": review_id, "task_id": task_id, "reason": reason}),
                                                )
                                                .await?;
                                                report.needs_changes += 1;
                                            }
                                            ReviewOutcome::Blocked(reason) | ReviewOutcome::RaisedQuestion(reason) => {
                                                self.emit_project(
                                                    project_id,
                                                    EventType::TaskBlocked,
                                                    &agent_id,
                                                    json!({"task_id": task_id, "reason": reason.clone()}),
                                                )
                                                .await?;
                                                let inbox_id = Uuid::new_v4().to_string();
                                                self.emit_project(
                                                    project_id,
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
                                                report.escalated += 1;
                                            }
                                        }
                                    }
                                    ExecutionStatus::Failed => {
                                        self.emit_project(
                                            project_id,
                                            EventType::TaskBlocked,
                                            &agent_id,
                                            json!({"task_id": task_id, "reason": result.summary.clone()}),
                                        )
                                        .await?;
                                        let inbox_id = Uuid::new_v4().to_string();
                                        self.emit_project(
                                            project_id,
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
                                        report.escalated += 1;
                                    }
                                }
                            }
                            Err(e) => {
                                self.emit_project(
                                    project_id,
                                    EventType::ModelRunFailed,
                                    &agent_id,
                                    json!({"id": run_id, "error": e.to_string()}),
                                )
                                .await?;
                                self.emit_project(
                                    project_id,
                                    EventType::TaskBlocked,
                                    &agent_id,
                                    json!({"task_id": task_id, "reason": e.to_string()}),
                                )
                                .await?;
                                let inbox_id = Uuid::new_v4().to_string();
                                self.emit_project(
                                    project_id,
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
                                report.escalated += 1;
                            }
                        }
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
                            json!({
                                "inbox_id": inbox_id,
                                "task_id": task_id,
                                "agent_id": agent_id,
                                "prompt": prompt,
                            }),
                        )
                        .await?;
                        report.escalated += 1;
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
                        report.subtasks_spawned += 1;
                    }
                }

                workspaces.destroy(&ws_state)?;
                self.emit_project(
                    project_id,
                    EventType::TmuxSessionDestroyed,
                    &agent_id,
                    json!({"task_id": task_id, "session_name": ws_state.tmux_session}),
                )
                .await?;
                self.emit_project(
                    project_id,
                    EventType::WorkspaceDestroyed,
                    &agent_id,
                    json!({"task_id": task_id}),
                )
                .await?;

                progressed = true;
            }

            if !progressed {
                break;
            }
        }

        Ok(report)
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
