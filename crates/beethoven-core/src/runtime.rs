use crate::error::CoreError;
use crate::models::*;
use crate::state::RuntimeState;
use beethoven_agents::{AgentKind, ExecutionOutcome, ExecutionService, PlanningService, TaskExecutionContext};
use beethoven_events::{Event, EventStore, EventType};
use beethoven_workspaces::WorkspaceManager;
use serde_json::{json, Value};
use std::path::PathBuf;
use uuid::Uuid;

#[derive(Debug, Default, Clone)]
pub struct TickReport {
    pub assigned: u32,
    pub completed: u32,
    pub escalated: u32,
    pub subtasks_spawned: u32,
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
    pub state: RuntimeState,
}

impl Runtime {
    pub async fn load(store: Box<dyn EventStore>, projects_root: PathBuf) -> Result<Self, CoreError> {
        let events = store.list_all().await?;
        let state = RuntimeState::replay(&events);
        Ok(Self {
            store,
            projects_root,
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

                self.emit_project(project_id, EventType::TaskStarted, &agent_id, json!({"task_id": task_id}))
                    .await?;

                let ws_state = workspaces.create(&task_id, &repository_path)?;
                self.emit_project(
                    project_id,
                    EventType::WorkspaceCreated,
                    &agent_id,
                    json!({"task_id": task_id, "path": ws_state.git_worktree}),
                )
                .await?;

                let outcome = ExecutionService::execute(&TaskExecutionContext {
                    task_id: task_id.clone(),
                    title: task.title.clone(),
                    attempt: task.attempt,
                });

                match outcome {
                    ExecutionOutcome::Completed => {
                        let branch = beethoven_git::create_branch(&task_id, &task.title);
                        let pr = beethoven_git::open_pr(&task_id, &branch, &task.title);
                        self.emit_project(
                            project_id,
                            EventType::PRCreated,
                            &agent_id,
                            json!({"task_id": task_id, "branch": branch, "url": pr.url}),
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
                        workspaces.destroy(&task_id)?;
                        self.emit_project(
                            project_id,
                            EventType::WorkspaceDestroyed,
                            &agent_id,
                            json!({"task_id": task_id}),
                        )
                        .await?;
                        report.completed += 1;
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
                        workspaces.destroy(&task_id)?;
                        self.emit_project(
                            project_id,
                            EventType::WorkspaceDestroyed,
                            &agent_id,
                            json!({"task_id": task_id}),
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
                                "parent_task_id": task_id,
                                "title": draft.title,
                                "description": draft.description,
                            }),
                        )
                        .await?;
                        workspaces.destroy(&task_id)?;
                        self.emit_project(
                            project_id,
                            EventType::WorkspaceDestroyed,
                            &agent_id,
                            json!({"task_id": task_id}),
                        )
                        .await?;
                        report.subtasks_spawned += 1;
                    }
                }

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

        Ok(self.state.inbox.get(inbox_id).expect("just updated").clone())
    }
}
