use crate::models::*;
use crate::payloads::*;
use beethoven_events::{Event, EventType};
use std::collections::HashMap;

#[derive(Debug, Default)]
pub struct RuntimeState {
    pub goals: HashMap<String, Goal>,
    pub tasks: HashMap<String, Task>,
    pub agents: HashMap<String, Agent>,
    pub inbox: HashMap<String, InboxItem>,
}

fn decode<T: serde::de::DeserializeOwned>(event: &Event) -> T {
    serde_json::from_value(event.payload.clone())
        .unwrap_or_else(|e| panic!("malformed payload for {:?}: {e}", event.event_type))
}

impl RuntimeState {
    pub fn replay(events: &[Event]) -> Self {
        let mut state = Self::default();
        for event in events {
            state.apply(event);
        }
        state
    }

    pub fn apply(&mut self, event: &Event) {
        let ts = event.timestamp;
        match event.event_type {
            EventType::GoalCreated => {
                let p: GoalCreatedPayload = decode(event);
                self.goals.insert(
                    p.id.clone(),
                    Goal {
                        id: p.id,
                        title: p.title,
                        description: p.description,
                        status: GoalStatus::Open,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::GoalUpdated => {
                let p: GoalUpdatedPayload = decode(event);
                if let Some(g) = self.goals.get_mut(&p.id) {
                    g.status = p.status;
                    g.updated_at = ts;
                }
            }
            EventType::AgentCreated => {
                let p: AgentCreatedPayload = decode(event);
                self.agents.insert(
                    p.id.clone(),
                    Agent {
                        id: p.id,
                        kind: p.kind,
                        status: AgentStatus::Created,
                        current_task_id: None,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::AgentStarted => {
                let p: AgentStartedPayload = decode(event);
                if let Some(a) = self.agents.get_mut(&p.id) {
                    a.status = AgentStatus::Idle;
                    a.updated_at = ts;
                }
            }
            EventType::AgentStopped => {
                let p: AgentStartedPayload = decode(event);
                if let Some(a) = self.agents.get_mut(&p.id) {
                    a.status = AgentStatus::Completed;
                    a.updated_at = ts;
                }
            }
            EventType::TaskCreated => {
                let p: TaskCreatedPayload = decode(event);
                self.tasks.insert(
                    p.id.clone(),
                    Task {
                        id: p.id,
                        goal_id: p.goal_id,
                        parent_task_id: p.parent_task_id.clone(),
                        title: p.title,
                        description: p.description,
                        status: TaskStatus::Ready,
                        assigned_agent_id: None,
                        branch: None,
                        pr_url: None,
                        attempt: 1,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
                if let Some(parent_id) = p.parent_task_id {
                    let freed_agent_id = self.tasks.get(&parent_id).and_then(|t| t.assigned_agent_id.clone());
                    if let Some(parent) = self.tasks.get_mut(&parent_id) {
                        parent.attempt += 1;
                        parent.status = TaskStatus::Ready;
                        parent.assigned_agent_id = None;
                        parent.updated_at = ts;
                    }
                    if let Some(aid) = freed_agent_id {
                        if let Some(a) = self.agents.get_mut(&aid) {
                            a.status = AgentStatus::Idle;
                            a.current_task_id = None;
                            a.updated_at = ts;
                        }
                    }
                }
            }
            EventType::TaskAssigned => {
                let p: TaskAssignedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Assigned;
                    t.assigned_agent_id = Some(p.agent_id.clone());
                    t.updated_at = ts;
                }
                if let Some(a) = self.agents.get_mut(&p.agent_id) {
                    a.status = AgentStatus::Assigned;
                    a.current_task_id = Some(p.task_id);
                    a.updated_at = ts;
                }
            }
            EventType::TaskStarted => {
                let p: TaskStartedPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::InProgress;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agents.get_mut(&aid) {
                        a.status = AgentStatus::Executing;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::TaskBlocked => {
                let p: TaskBlockedPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Blocked;
                    t.assigned_agent_id = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agents.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::TaskCompleted => {
                let p: TaskCompletedPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                let goal_id = self.tasks.get(&p.task_id).map(|t| t.goal_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Done;
                    t.assigned_agent_id = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agents.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
                if let Some(gid) = goal_id {
                    self.maybe_complete_goal(&gid, ts);
                }
            }
            EventType::QuestionRaised => {
                let p: QuestionRaisedPayload = decode(event);
                self.inbox.insert(
                    p.inbox_id.clone(),
                    InboxItem {
                        id: p.inbox_id,
                        kind: InboxKind::Question,
                        task_id: Some(p.task_id),
                        agent_id: Some(p.agent_id),
                        prompt: p.prompt,
                        status: InboxStatus::Open,
                        response: None,
                        created_at: ts,
                        resolved_at: None,
                    },
                );
            }
            EventType::QuestionAnswered => {
                let p: QuestionAnsweredPayload = decode(event);
                let task_id = self.inbox.get(&p.inbox_id).and_then(|i| i.task_id.clone());
                if let Some(item) = self.inbox.get_mut(&p.inbox_id) {
                    item.status = InboxStatus::Resolved;
                    item.response = Some(p.response);
                    item.resolved_at = Some(ts);
                }
                if let Some(tid) = task_id {
                    if let Some(t) = self.tasks.get_mut(&tid) {
                        t.status = TaskStatus::Ready;
                        t.attempt += 1;
                        t.updated_at = ts;
                    }
                }
            }
            EventType::DecisionRequested => {
                let p: DecisionRequestedPayload = decode(event);
                self.inbox.insert(
                    p.inbox_id.clone(),
                    InboxItem {
                        id: p.inbox_id,
                        kind: InboxKind::Decision,
                        task_id: p.task_id,
                        agent_id: p.agent_id,
                        prompt: p.prompt,
                        status: InboxStatus::Open,
                        response: None,
                        created_at: ts,
                        resolved_at: None,
                    },
                );
            }
            EventType::DecisionResolved => {
                let p: DecisionResolvedPayload = decode(event);
                let task_id = self.inbox.get(&p.inbox_id).and_then(|i| i.task_id.clone());
                if let Some(item) = self.inbox.get_mut(&p.inbox_id) {
                    item.status = InboxStatus::Resolved;
                    item.response = Some(p.response);
                    item.resolved_at = Some(ts);
                }
                if let Some(tid) = task_id {
                    if let Some(t) = self.tasks.get_mut(&tid) {
                        t.status = TaskStatus::Ready;
                        t.attempt += 1;
                        t.updated_at = ts;
                    }
                }
            }
            EventType::PRCreated => {
                let p: PrCreatedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.branch = Some(p.branch);
                    t.pr_url = Some(p.url);
                    t.updated_at = ts;
                }
            }
            EventType::WorkspaceCreated | EventType::WorkspaceDestroyed | EventType::PRMerged => {
                // Recorded in the event log for audit/timeline purposes only;
                // no derived-state fields track these yet.
            }
        }
    }

    fn maybe_complete_goal(&mut self, goal_id: &str, ts: chrono::DateTime<chrono::Utc>) {
        let mut has_tasks = false;
        let mut all_done = true;
        for t in self.tasks.values().filter(|t| t.goal_id == goal_id) {
            has_tasks = true;
            if t.status != TaskStatus::Done {
                all_done = false;
                break;
            }
        }
        if has_tasks && all_done {
            if let Some(g) = self.goals.get_mut(goal_id) {
                if g.status != GoalStatus::Completed {
                    g.status = GoalStatus::Completed;
                    g.updated_at = ts;
                }
            }
        }
    }
}
