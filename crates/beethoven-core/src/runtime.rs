use crate::error::CoreError;
use crate::models::*;
use crate::state::RuntimeState;
use beethoven_agents::{AgentKind, ExecutionOutcome, ExecutionService, PlanningService, TaskExecutionContext};
use beethoven_events::{Event, EventStore, EventType};
use beethoven_workspaces::WorkspaceManager;
use serde_json::json;
use std::path::PathBuf;
use uuid::Uuid;

#[derive(Debug, Default, Clone)]
pub struct TickReport {
    pub assigned: u32,
    pub completed: u32,
    pub escalated: u32,
    pub subtasks_spawned: u32,
}

pub struct Runtime {
    store: Box<dyn EventStore>,
    workspaces: WorkspaceManager,
    pub state: RuntimeState,
}

impl Runtime {
    pub async fn load(store: Box<dyn EventStore>, workspaces_dir: PathBuf) -> Result<Self, CoreError> {
        let events = store.list_all().await?;
        let state = RuntimeState::replay(&events);
        let mut runtime = Self {
            store,
            workspaces: WorkspaceManager::new(workspaces_dir),
            state,
        };
        runtime.ensure_base_agents().await?;
        Ok(runtime)
    }

    pub async fn list_events(&self) -> Result<Vec<Event>, CoreError> {
        Ok(self.store.list_all().await?)
    }

    async fn emit(
        &mut self,
        event_type: EventType,
        actor_id: &str,
        payload: serde_json::Value,
    ) -> Result<(), CoreError> {
        let event = Event::new(event_type, actor_id, payload);
        self.store.append(event.clone()).await?;
        self.state.apply(&event);
        Ok(())
    }

    async fn ensure_base_agents(&mut self) -> Result<(), CoreError> {
        if !self.state.agents.is_empty() {
            return Ok(());
        }
        self.spawn_agent(AgentKind::ProjectManager).await?;
        for kind in AgentKind::WORKERS {
            self.spawn_agent(kind).await?;
        }
        Ok(())
    }

    async fn spawn_agent(&mut self, kind: AgentKind) -> Result<String, CoreError> {
        let id = Uuid::new_v4().to_string();
        self.emit(EventType::AgentCreated, "system", json!({"id": id, "kind": kind}))
            .await?;
        self.emit(EventType::AgentStarted, "system", json!({"id": id}))
            .await?;
        Ok(id)
    }

    pub fn project_manager_id(&self) -> Option<String> {
        self.state
            .agents
            .values()
            .find(|a| a.kind == AgentKind::ProjectManager)
            .map(|a| a.id.clone())
    }

    pub async fn create_goal(&mut self, title: &str, description: &str) -> Result<Goal, CoreError> {
        let id = Uuid::new_v4().to_string();
        self.emit(
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
        let pm_id = self.project_manager_id().unwrap_or_else(|| "system".to_string());

        let drafts = PlanningService::plan(&goal.title, &goal.description);
        let mut created = Vec::with_capacity(drafts.len());
        for draft in drafts {
            let id = Uuid::new_v4().to_string();
            self.emit(
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

        self.emit(
            EventType::GoalUpdated,
            &pm_id,
            json!({"id": goal_id, "status": GoalStatus::Planned}),
        )
        .await?;

        Ok(created)
    }

    pub async fn run_tick(&mut self) -> Result<TickReport, CoreError> {
        let pm_id = self.project_manager_id().unwrap_or_else(|| "system".to_string());
        let mut report = TickReport::default();

        for _ in 0..1000 {
            let mut progressed = false;

            let ready_task_ids: Vec<String> = self
                .state
                .tasks
                .values()
                .filter(|t| t.status == TaskStatus::Ready)
                .map(|t| t.id.clone())
                .collect();

            for task_id in ready_task_ids {
                let idle_agent_id = self
                    .state
                    .agents
                    .values()
                    .find(|a| a.status == AgentStatus::Idle && a.kind != AgentKind::ProjectManager)
                    .map(|a| a.id.clone());

                if let Some(agent_id) = idle_agent_id {
                    self.emit(
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
                .filter(|t| t.status == TaskStatus::Assigned)
                .map(|t| t.id.clone())
                .collect();

            for task_id in assigned_task_ids {
                let task = self.state.tasks.get(&task_id).expect("task exists").clone();
                let agent_id = task.assigned_agent_id.clone().expect("assigned task has agent");

                self.emit(EventType::TaskStarted, &agent_id, json!({"task_id": task_id}))
                    .await?;

                let ws_state = self.workspaces.create(&task_id)?;
                self.emit(
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
                        self.emit(
                            EventType::PRCreated,
                            &agent_id,
                            json!({"task_id": task_id, "branch": branch, "url": pr.url}),
                        )
                        .await?;
                        self.emit(EventType::TaskCompleted, &agent_id, json!({"task_id": task_id}))
                            .await?;
                        self.emit(
                            EventType::PRMerged,
                            &pm_id,
                            json!({"task_id": task_id, "url": pr.url}),
                        )
                        .await?;
                        self.workspaces.destroy(&task_id)?;
                        self.emit(
                            EventType::WorkspaceDestroyed,
                            &agent_id,
                            json!({"task_id": task_id}),
                        )
                        .await?;
                        report.completed += 1;
                    }
                    ExecutionOutcome::RaisedQuestion(prompt) => {
                        self.emit(
                            EventType::TaskBlocked,
                            &agent_id,
                            json!({"task_id": task_id, "reason": prompt.clone()}),
                        )
                        .await?;
                        let inbox_id = Uuid::new_v4().to_string();
                        self.emit(
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
                        self.workspaces.destroy(&task_id)?;
                        self.emit(
                            EventType::WorkspaceDestroyed,
                            &agent_id,
                            json!({"task_id": task_id}),
                        )
                        .await?;
                        report.escalated += 1;
                    }
                    ExecutionOutcome::SpawnedSubtask(draft) => {
                        let sub_id = Uuid::new_v4().to_string();
                        self.emit(
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
                        self.workspaces.destroy(&task_id)?;
                        self.emit(
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

        let pm_id = self.project_manager_id().unwrap_or_else(|| "system".to_string());
        let event_type = match item.kind {
            InboxKind::Decision => EventType::DecisionResolved,
            InboxKind::Question | InboxKind::Approval | InboxKind::Alert => EventType::QuestionAnswered,
        };

        self.emit(
            event_type,
            &pm_id,
            json!({"inbox_id": inbox_id, "response": response}),
        )
        .await?;

        Ok(self.state.inbox.get(inbox_id).expect("just updated").clone())
    }
}
