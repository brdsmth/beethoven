use crate::dto::*;
use beethoven_core::{
    AgentInstance, AgentStatus, Decision, DecisionStatus, Epic, Goal, GoalStatus, Initiative, InitiativeStatus,
    InboxItem, InboxStatus, Project, Requirement, RuntimeState, Task, TaskStatus,
};
use beethoven_events::{Event, EventType};
use chrono::{DateTime, Utc};
use std::collections::{HashMap, HashSet};
use std::path::Path;

fn last_heartbeats(events: &[Event]) -> HashMap<String, DateTime<Utc>> {
    let mut map: HashMap<String, DateTime<Utc>> = HashMap::new();
    for e in events {
        if e.event_type == EventType::AgentHeartbeat {
            if let Some(agent_id) = e.payload.get("agent_id").and_then(|v| v.as_str()) {
                map.entry(agent_id.to_string())
                    .and_modify(|ts| *ts = (*ts).max(e.timestamp))
                    .or_insert(e.timestamp);
            }
        }
    }
    map
}

fn task_dto(state: &RuntimeState, t: &Task) -> TaskDto {
    let assigned_agent_name = t.assigned_agent_id.as_ref().and_then(|id| state.agent_instances.get(id)).map(|a| a.name.clone());
    TaskDto {
        id: t.id.clone(),
        project_id: t.project_id.clone(),
        goal_id: t.goal_id.clone(),
        epic_id: t.epic_id.clone(),
        title: t.title.clone(),
        description: t.description.clone(),
        status: t.status,
        assigned_agent_id: t.assigned_agent_id.clone(),
        assigned_agent_name,
        attempt: t.attempt,
        branch: t.branch.clone(),
        pr_url: t.pr_url.clone(),
        created_at: t.created_at,
        updated_at: t.updated_at,
    }
}

fn decision_dto(d: &Decision) -> DecisionDto {
    DecisionDto {
        id: d.id.clone(),
        title: d.title.clone(),
        context: d.context.clone(),
        status: d.status,
    }
}

fn project_summary(state: &RuntimeState, p: &Project) -> ProjectSummaryDto {
    let tasks: Vec<&Task> = state.tasks.values().filter(|t| t.project_id == p.id).collect();
    let mut counts = TaskCountsDto::default();
    for t in &tasks {
        match t.status {
            TaskStatus::Draft => counts.draft += 1,
            TaskStatus::Ready => counts.ready += 1,
            TaskStatus::Assigned => counts.assigned += 1,
            TaskStatus::InProgress => counts.in_progress += 1,
            TaskStatus::Review => counts.review += 1,
            TaskStatus::Approved => counts.approved += 1,
            TaskStatus::Done => counts.done += 1,
            TaskStatus::Blocked => counts.blocked += 1,
        }
    }
    let open_inbox_count =
        state.inbox.values().filter(|i| i.project_id == p.id && i.status == InboxStatus::Open).count();
    ProjectSummaryDto {
        id: p.id.clone(),
        name: p.name.clone(),
        slug: p.slug.clone(),
        status: p.status,
        task_counts: counts,
        open_inbox_count,
        created_at: p.created_at,
        updated_at: p.updated_at,
    }
}

/// All projects, ordered oldest-first (mirrors `project list`'s ordering).
pub fn project_summaries(state: &RuntimeState) -> Vec<ProjectSummaryDto> {
    let mut projects: Vec<&Project> = state.projects.values().collect();
    projects.sort_by_key(|p| p.created_at);
    projects.into_iter().map(|p| project_summary(state, p)).collect()
}

/// A single project's initiatives (with requirements/epics/decisions) and
/// goals (with tasks), or `None` if the project doesn't exist.
pub fn project_detail(state: &RuntimeState, project_id: &str) -> Option<ProjectDetailDto> {
    let p = state.projects.get(project_id)?;

    let mut initiatives: Vec<&Initiative> = state.initiatives.values().filter(|i| i.project_id == project_id).collect();
    initiatives.sort_by_key(|i| i.created_at);
    let initiatives = initiatives
        .into_iter()
        .map(|i| {
            let mut requirements: Vec<&Requirement> =
                state.requirements.values().filter(|r| r.initiative_id == i.id).collect();
            requirements.sort_by_key(|r| r.created_at);
            let requirements = requirements
                .into_iter()
                .map(|r| RequirementDto { id: r.id.clone(), description: r.description.clone() })
                .collect();

            let mut epics: Vec<&Epic> = state.epics.values().filter(|e| e.initiative_id == i.id).collect();
            epics.sort_by_key(|e| e.created_at);
            let epics = epics
                .into_iter()
                .map(|e| {
                    let mut tasks: Vec<&Task> =
                        state.tasks.values().filter(|t| t.epic_id.as_deref() == Some(e.id.as_str())).collect();
                    tasks.sort_by_key(|t| t.created_at);
                    EpicDto {
                        id: e.id.clone(),
                        title: e.title.clone(),
                        tasks: tasks.into_iter().map(|t| task_dto(state, t)).collect(),
                    }
                })
                .collect();

            let mut decisions: Vec<&Decision> = state.decisions.values().filter(|d| d.initiative_id == i.id).collect();
            decisions.sort_by_key(|d| d.created_at);
            let decisions = decisions.into_iter().map(decision_dto).collect();

            InitiativeDto {
                id: i.id.clone(),
                title: i.title.clone(),
                description: i.description.clone(),
                status: i.status,
                requirements,
                epics,
                decisions,
                created_at: i.created_at,
                updated_at: i.updated_at,
            }
        })
        .collect();

    let mut goals: Vec<&Goal> = state.goals.values().filter(|g| g.project_id == project_id).collect();
    goals.sort_by_key(|g| g.created_at);
    let goals = goals
        .into_iter()
        .map(|g| {
            let mut tasks: Vec<&Task> =
                state.tasks.values().filter(|t| t.goal_id.as_deref() == Some(g.id.as_str())).collect();
            tasks.sort_by_key(|t| t.created_at);
            GoalDto {
                id: g.id.clone(),
                title: g.title.clone(),
                description: g.description.clone(),
                status: g.status,
                tasks: tasks.into_iter().map(|t| task_dto(state, t)).collect(),
                created_at: g.created_at,
                updated_at: g.updated_at,
            }
        })
        .collect();

    Some(ProjectDetailDto {
        id: p.id.clone(),
        name: p.name.clone(),
        slug: p.slug.clone(),
        status: p.status,
        initiatives,
        goals,
        created_at: p.created_at,
        updated_at: p.updated_at,
    })
}

/// A project's tasks, oldest-first, joined with their assigned agent's name.
pub fn tasks_for(state: &RuntimeState, project_id: &str) -> Vec<TaskDto> {
    let mut tasks: Vec<&Task> = state.tasks.values().filter(|t| t.project_id == project_id).collect();
    tasks.sort_by_key(|t| t.created_at);
    tasks.into_iter().map(|t| task_dto(state, t)).collect()
}

/// A project's agent instances, joined with their current task's title and
/// last `AgentHeartbeat` timestamp.
pub fn agents_for(state: &RuntimeState, project_id: &str, events: &[Event]) -> Vec<AgentDto> {
    let heartbeats = last_heartbeats(events);
    let mut agents: Vec<&AgentInstance> = state.agent_instances.values().filter(|a| a.project_id == project_id).collect();
    agents.sort_by_key(|a| a.name.clone());
    agents
        .into_iter()
        .map(|a| {
            let current_task_title = a.current_task_id.as_ref().and_then(|tid| state.tasks.get(tid)).map(|t| t.title.clone());
            AgentDto {
                id: a.id.clone(),
                project_id: a.project_id.clone(),
                kind: a.kind,
                name: a.name.clone(),
                status: a.status,
                current_task_id: a.current_task_id.clone(),
                current_task_title,
                last_heartbeat: heartbeats.get(&a.id).copied(),
            }
        })
        .collect()
}

/// A project's inbox items, oldest-first, joined with the title/name of
/// whatever they reference (task, agent, decision).
pub fn inbox_for(state: &RuntimeState, project_id: &str) -> Vec<InboxItemDto> {
    let mut items: Vec<&InboxItem> = state.inbox.values().filter(|i| i.project_id == project_id).collect();
    items.sort_by_key(|i| i.created_at);
    items
        .into_iter()
        .map(|i| {
            let task_title = i.task_id.as_ref().and_then(|tid| state.tasks.get(tid)).map(|t| t.title.clone());
            let agent_name = i.agent_id.as_ref().and_then(|aid| state.agent_instances.get(aid)).map(|a| a.name.clone());
            let decision_title = i.decision_id.as_ref().and_then(|did| state.decisions.get(did)).map(|d| d.title.clone());
            InboxItemDto {
                id: i.id.clone(),
                project_id: i.project_id.clone(),
                kind: i.kind,
                status: i.status,
                prompt: i.prompt.clone(),
                task_id: i.task_id.clone(),
                task_title,
                agent_id: i.agent_id.clone(),
                agent_name,
                decision_id: i.decision_id.clone(),
                decision_title,
                response: i.response.clone(),
                created_at: i.created_at,
                resolved_at: i.resolved_at,
            }
        })
        .collect()
}

#[derive(Debug, Clone, Default)]
pub struct TimelineFilters {
    pub project_id: Option<String>,
    pub agent_id: Option<String>,
    pub event_type: Option<String>,
    pub since: Option<DateTime<Utc>>,
    pub until: Option<DateTime<Utc>>,
}

/// The event log, filtered and returned newest-first.
pub fn timeline(events: &[Event], filters: &TimelineFilters) -> Vec<TimelineEventDto> {
    let mut out: Vec<TimelineEventDto> = events
        .iter()
        .filter(|e| {
            if let Some(pid) = &filters.project_id {
                if e.scope.project_id() != Some(pid.as_str()) {
                    return false;
                }
            }
            if let Some(aid) = &filters.agent_id {
                if e.actor_id.as_deref() != Some(aid.as_str()) {
                    return false;
                }
            }
            if let Some(et) = &filters.event_type {
                if e.event_type.as_str() != et.as_str() {
                    return false;
                }
            }
            if let Some(since) = filters.since {
                if e.timestamp < since {
                    return false;
                }
            }
            if let Some(until) = filters.until {
                if e.timestamp > until {
                    return false;
                }
            }
            true
        })
        .map(|e| TimelineEventDto {
            id: e.id.clone(),
            project_id: e.scope.project_id().map(|s| s.to_string()),
            event_type: e.event_type.as_str().to_string(),
            actor_id: e.actor_id.clone(),
            payload: e.payload.clone(),
            timestamp: e.timestamp,
        })
        .collect();
    out.reverse();
    out
}

/// Mirrors the CLI's `print_runtime_status`: pidfile liveness, last
/// start/stop, and a snapshot of org-wide activity.
pub fn runtime_status(state: &RuntimeState, events: &[Event], home: &Path) -> RuntimeStatusDto {
    let pid_file = beethoven_db::pid_path(home);
    let pid = beethoven_db::read_pid(&pid_file);
    let daemon_running = pid.map(beethoven_db::is_pid_alive).unwrap_or(false);
    let stale_pidfile = pid.is_some() && !daemon_running;

    let last_started = events.iter().rev().find(|e| e.event_type == EventType::RuntimeStarted).map(|e| e.timestamp);
    let last_stopped = events.iter().rev().find(|e| e.event_type == EventType::RuntimeStopped).map(|e| e.timestamp);

    let active_agent_count = state
        .agent_instances
        .values()
        .filter(|a| !matches!(a.status, AgentStatus::Idle | AgentStatus::Created | AgentStatus::Completed))
        .count();
    let active_task_count = state.tasks.values().filter(|t| t.status == TaskStatus::InProgress).count();
    let open_inbox_count = state.inbox.values().filter(|i| i.status == InboxStatus::Open).count();

    RuntimeStatusDto {
        daemon_running,
        pid,
        stale_pidfile,
        last_started,
        last_stopped,
        active_agent_count,
        active_task_count,
        open_inbox_count,
    }
}

/// Every `InProgress` task, joined with its agent and the timestamp of its
/// latest `ModelRunStarted` event (falling back to `TaskStarted`, then to
/// the task's own `updated_at`).
pub fn active_execution(state: &RuntimeState, events: &[Event]) -> Vec<ActiveExecutionDto> {
    let mut out = Vec::new();
    for t in state.tasks.values().filter(|t| t.status == TaskStatus::InProgress) {
        let Some(agent_id) = &t.assigned_agent_id else { continue };
        let Some(agent) = state.agent_instances.get(agent_id) else { continue };
        let Some(project) = state.projects.get(&t.project_id) else { continue };

        let started_at = events
            .iter()
            .rev()
            .find(|e| {
                e.event_type == EventType::ModelRunStarted
                    && e.payload.get("task_id").and_then(|v| v.as_str()) == Some(t.id.as_str())
            })
            .or_else(|| {
                events.iter().rev().find(|e| {
                    e.event_type == EventType::TaskStarted
                        && e.payload.get("task_id").and_then(|v| v.as_str()) == Some(t.id.as_str())
                })
            })
            .map(|e| e.timestamp)
            .unwrap_or(t.updated_at);

        out.push(ActiveExecutionDto {
            task_id: t.id.clone(),
            task_title: t.title.clone(),
            project_id: project.id.clone(),
            project_slug: project.slug.clone(),
            agent_id: agent.id.clone(),
            agent_name: agent.name.clone(),
            agent_kind: agent.kind,
            started_at,
        });
    }
    out.sort_by_key(|e| e.started_at);
    out
}

/// The active project's most-recently-updated non-`Completed` initiative
/// (progress = done epic-tasks + resolved decisions, over the total of
/// both), falling back to its most-recently-updated non-`Completed` goal
/// (progress = done tasks / total tasks) if it has no initiatives at all.
pub fn goal_header(state: &RuntimeState, project_id: &str) -> GoalHeaderDto {
    let project_name = state.projects.get(project_id).map(|p| p.name.clone()).unwrap_or_default();

    let mut initiatives: Vec<&Initiative> = state
        .initiatives
        .values()
        .filter(|i| i.project_id == project_id && i.status != InitiativeStatus::Completed)
        .collect();
    initiatives.sort_by_key(|i| i.updated_at);

    if let Some(initiative) = initiatives.last() {
        let epic_ids: HashSet<&str> =
            state.epics.values().filter(|e| e.initiative_id == initiative.id).map(|e| e.id.as_str()).collect();
        let tasks: Vec<&Task> =
            state.tasks.values().filter(|t| t.epic_id.as_deref().is_some_and(|eid| epic_ids.contains(eid))).collect();
        let decisions: Vec<&Decision> = state.decisions.values().filter(|d| d.initiative_id == initiative.id).collect();

        let total = tasks.len() + decisions.len();
        let done = tasks.iter().filter(|t| t.status == TaskStatus::Done).count()
            + decisions.iter().filter(|d| d.status == DecisionStatus::Resolved).count();
        let percent_complete = if total == 0 { 0.0 } else { (done as f64 / total as f64) * 100.0 };

        return GoalHeaderDto {
            project_id: project_id.to_string(),
            project_name,
            source: GoalHeaderSource::Initiative,
            active_label: Some(initiative.title.clone()),
            active_initiative_id: Some(initiative.id.clone()),
            percent_complete,
        };
    }

    let mut goals: Vec<&Goal> =
        state.goals.values().filter(|g| g.project_id == project_id && g.status != GoalStatus::Completed).collect();
    goals.sort_by_key(|g| g.updated_at);

    if let Some(goal) = goals.last() {
        let tasks: Vec<&Task> = state.tasks.values().filter(|t| t.goal_id.as_deref() == Some(goal.id.as_str())).collect();
        let total = tasks.len();
        let done = tasks.iter().filter(|t| t.status == TaskStatus::Done).count();
        let percent_complete = if total == 0 { 0.0 } else { (done as f64 / total as f64) * 100.0 };

        return GoalHeaderDto {
            project_id: project_id.to_string(),
            project_name,
            source: GoalHeaderSource::Goal,
            active_label: Some(goal.title.clone()),
            active_initiative_id: None,
            percent_complete,
        };
    }

    GoalHeaderDto {
        project_id: project_id.to_string(),
        project_name,
        source: GoalHeaderSource::None,
        active_label: None,
        active_initiative_id: None,
        percent_complete: 0.0,
    }
}

/// Buckets an initiative's epic-derived tasks into the spec's 6 workflow
/// stages. Blocked tasks are deliberately excluded -- they're surfaced via
/// Project Health, not the linear Initiative->Epic->Task->Execution->Review->Complete map.
pub fn workflow_map(state: &RuntimeState, initiative_id: &str) -> WorkflowMapDto {
    let initiative_count = usize::from(state.initiatives.contains_key(initiative_id));
    let epics: Vec<&Epic> = state.epics.values().filter(|e| e.initiative_id == initiative_id).collect();
    let epic_count = epics.len();
    let epic_ids: HashSet<&str> = epics.iter().map(|e| e.id.as_str()).collect();

    let mut map = WorkflowMapDto {
        initiative_count,
        epic_count,
        ..Default::default()
    };
    for t in state.tasks.values().filter(|t| t.epic_id.as_deref().is_some_and(|eid| epic_ids.contains(eid))) {
        match t.status {
            TaskStatus::Draft | TaskStatus::Ready | TaskStatus::Assigned => map.task_count += 1,
            TaskStatus::InProgress => map.execution_count += 1,
            TaskStatus::Review | TaskStatus::Approved => map.review_count += 1,
            TaskStatus::Done => map.complete_count += 1,
            TaskStatus::Blocked => {}
        }
    }
    map
}

#[cfg(test)]
mod tests {
    use super::*;
    use beethoven_events::EventScope;
    use serde_json::json;

    fn ev(seq: i64, scope: EventScope, event_type: EventType, actor: Option<&str>, payload: serde_json::Value) -> Event {
        Event {
            id: format!("evt-{seq}"),
            scope,
            event_type,
            actor_id: actor.map(|s| s.to_string()),
            payload,
            timestamp: Utc::now() + chrono::Duration::milliseconds(seq),
        }
    }

    /// One project ("p1"/"punch") with: a goal ("g1") and its task ("t1"),
    /// assigned to agent "a1" and in progress; an initiative ("i1") with a
    /// requirement, an epic ("e1") whose task ("t2") is done (assigned to a
    /// separate agent "a3" so it doesn't clear "a1"'s current task), and a
    /// decision ("d1") that's still open; an idle PM agent "a2"; an open
    /// inbox question for t1 and an open inbox decision for d1; a heartbeat
    /// for "a1"; and a RuntimeStarted event.
    fn fixture() -> (RuntimeState, Vec<Event>) {
        let p = EventScope::Project("p1".to_string());
        let events = vec![
            ev(
                0,
                EventScope::Global,
                EventType::ProjectCreated,
                Some("human"),
                json!({"id": "p1", "name": "Punch", "slug": "punch", "repository_path": "/repo", "default_branch": "main"}),
            ),
            ev(
                1,
                p.clone(),
                EventType::AgentCreated,
                Some("human"),
                json!({"id": "a1", "template_id": "tmpl-be", "kind": "backend", "name": "Backend-1"}),
            ),
            ev(
                2,
                p.clone(),
                EventType::AgentCreated,
                Some("human"),
                json!({"id": "a2", "template_id": "tmpl-pm", "kind": "project_manager", "name": "PM-1"}),
            ),
            ev(
                3,
                p.clone(),
                EventType::AgentCreated,
                Some("human"),
                json!({"id": "a3", "template_id": "tmpl-be", "kind": "backend", "name": "Backend-2"}),
            ),
            ev(4, p.clone(), EventType::AgentStarted, Some("human"), json!({"id": "a1"})),
            ev(5, p.clone(), EventType::AgentStarted, Some("human"), json!({"id": "a2"})),
            ev(6, p.clone(), EventType::AgentStarted, Some("human"), json!({"id": "a3"})),
            ev(
                7,
                p.clone(),
                EventType::GoalCreated,
                Some("human"),
                json!({"id": "g1", "title": "Build login page", "description": "..."}),
            ),
            ev(
                8,
                p.clone(),
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "t1", "goal_id": "g1", "epic_id": null, "parent_task_id": null, "title": "Implement login form", "description": "..."}),
            ),
            ev(9, p.clone(), EventType::TaskAssigned, Some("a2"), json!({"task_id": "t1", "agent_id": "a1"})),
            ev(10, p.clone(), EventType::TaskStarted, Some("a1"), json!({"task_id": "t1"})),
            ev(
                11,
                p.clone(),
                EventType::ModelRunStarted,
                Some("a1"),
                json!({"id": "mr1", "agent_id": "a1", "project_id": "p1", "task_id": "t1", "provider": "claude"}),
            ),
            ev(
                12,
                p.clone(),
                EventType::QuestionRaised,
                Some("a1"),
                json!({"inbox_id": "inbox1", "task_id": "t1", "agent_id": "a1", "prompt": "Which auth provider?"}),
            ),
            ev(
                13,
                p.clone(),
                EventType::InitiativeCreated,
                Some("human"),
                json!({"id": "i1", "title": "Build Gmail Agent", "description": "Routes inbound email"}),
            ),
            ev(
                14,
                p.clone(),
                EventType::RequirementCreated,
                Some("human"),
                json!({"id": "r1", "initiative_id": "i1", "description": "Support PDF attachments"}),
            ),
            ev(
                15,
                p.clone(),
                EventType::EpicCreated,
                Some("a2"),
                json!({"id": "e1", "initiative_id": "i1", "title": "Implement OAuth"}),
            ),
            ev(
                16,
                p.clone(),
                EventType::TaskCreated,
                Some("a2"),
                json!({"id": "t2", "goal_id": null, "epic_id": "e1", "parent_task_id": null, "title": "Implement: Implement OAuth", "description": "..."}),
            ),
            ev(17, p.clone(), EventType::TaskAssigned, Some("a2"), json!({"task_id": "t2", "agent_id": "a3"})),
            ev(18, p.clone(), EventType::TaskStarted, Some("a3"), json!({"task_id": "t2"})),
            ev(19, p.clone(), EventType::TaskCompleted, Some("a3"), json!({"task_id": "t2"})),
            ev(
                20,
                p.clone(),
                EventType::DecisionCreated,
                Some("a2"),
                json!({"id": "d1", "initiative_id": "i1", "title": "Pick an email provider", "context": "Gmail vs IMAP"}),
            ),
            ev(
                21,
                p.clone(),
                EventType::DecisionEscalated,
                Some("a2"),
                json!({"decision_id": "d1", "inbox_id": "inbox2", "prompt": "Pick an email provider"}),
            ),
            ev(22, p.clone(), EventType::AgentHeartbeat, Some("a1"), json!({"agent_id": "a1"})),
            ev(23, EventScope::Global, EventType::RuntimeStarted, Some("system"), json!({})),
        ];
        let state = RuntimeState::replay(&events);
        (state, events)
    }

    #[test]
    fn project_summaries_counts_tasks_and_open_inbox_per_project() {
        let (state, _events) = fixture();
        let summaries = project_summaries(&state);
        assert_eq!(summaries.len(), 1);
        let punch = &summaries[0];
        assert_eq!(punch.slug, "punch");
        assert_eq!(punch.task_counts.in_progress, 1); // t1
        assert_eq!(punch.task_counts.done, 1); // t2
        assert_eq!(punch.open_inbox_count, 2); // inbox1 + inbox2
    }

    #[test]
    fn project_detail_nests_initiatives_and_goals_with_their_children() {
        let (state, _events) = fixture();
        let detail = project_detail(&state, "p1").expect("project exists");
        assert_eq!(detail.goals.len(), 1);
        assert_eq!(detail.goals[0].tasks.len(), 1);
        assert_eq!(detail.goals[0].tasks[0].id, "t1");

        assert_eq!(detail.initiatives.len(), 1);
        let initiative = &detail.initiatives[0];
        assert_eq!(initiative.requirements.len(), 1);
        assert_eq!(initiative.epics.len(), 1);
        assert_eq!(initiative.epics[0].tasks[0].id, "t2");
        assert_eq!(initiative.decisions.len(), 1);
        assert_eq!(initiative.decisions[0].status, DecisionStatus::Open);

        assert!(project_detail(&state, "missing").is_none());
    }

    #[test]
    fn tasks_for_includes_assigned_agent_name() {
        let (state, _events) = fixture();
        let tasks = tasks_for(&state, "p1");
        let t1 = tasks.iter().find(|t| t.id == "t1").unwrap();
        assert_eq!(t1.assigned_agent_name, Some("Backend-1".to_string()));
    }

    #[test]
    fn agents_for_joins_current_task_title_and_last_heartbeat() {
        let (state, events) = fixture();
        let agents = agents_for(&state, "p1", &events);
        let a1 = agents.iter().find(|a| a.id == "a1").unwrap();
        assert_eq!(a1.current_task_title, Some("Implement login form".to_string()));
        assert!(a1.last_heartbeat.is_some());

        let a2 = agents.iter().find(|a| a.id == "a2").unwrap();
        assert_eq!(a2.current_task_id, None);
        assert_eq!(a2.last_heartbeat, None);
    }

    #[test]
    fn inbox_for_resolves_task_and_decision_titles() {
        let (state, _events) = fixture();
        let items = inbox_for(&state, "p1");
        assert_eq!(items.len(), 2);
        let question = items.iter().find(|i| i.id == "inbox1").unwrap();
        assert_eq!(question.task_title, Some("Implement login form".to_string()));
        let decision_item = items.iter().find(|i| i.id == "inbox2").unwrap();
        assert_eq!(decision_item.decision_title, Some("Pick an email provider".to_string()));
    }

    #[test]
    fn timeline_filters_by_event_type_and_returns_newest_first() {
        let (_state, events) = fixture();
        let all = timeline(&events, &TimelineFilters::default());
        assert_eq!(all.len(), events.len());
        assert_eq!(all[0].event_type, "RuntimeStarted"); // last emitted, first returned

        let filtered = timeline(
            &events,
            &TimelineFilters {
                event_type: Some("TaskCompleted".to_string()),
                ..Default::default()
            },
        );
        assert_eq!(filtered.len(), 1);
        assert_eq!(filtered[0].payload.get("task_id").and_then(|v| v.as_str()), Some("t2"));
    }

    #[test]
    fn runtime_status_reports_no_daemon_and_runtime_started_timestamp() {
        let (state, events) = fixture();
        let home = std::env::temp_dir().join("beethoven-tauri-test-nonexistent-home");
        let status = runtime_status(&state, &events, &home);
        assert!(!status.daemon_running);
        assert_eq!(status.pid, None);
        assert!(status.last_started.is_some());
        assert_eq!(status.last_stopped, None);
        assert_eq!(status.active_task_count, 1); // t1 is in progress
        assert_eq!(status.open_inbox_count, 2);
    }

    #[test]
    fn active_execution_uses_model_run_started_timestamp() {
        let (state, events) = fixture();
        let active = active_execution(&state, &events);
        assert_eq!(active.len(), 1);
        assert_eq!(active[0].task_id, "t1");
        assert_eq!(active[0].agent_name, "Backend-1");
        let model_run_started = events.iter().find(|e| e.event_type == EventType::ModelRunStarted).unwrap().timestamp;
        assert_eq!(active[0].started_at, model_run_started);
    }

    #[test]
    fn goal_header_prefers_initiative_over_goal_and_computes_percent_complete() {
        let (state, _events) = fixture();
        let header = goal_header(&state, "p1");
        assert_eq!(header.source, GoalHeaderSource::Initiative);
        assert_eq!(header.active_label, Some("Build Gmail Agent".to_string()));
        assert_eq!(header.active_initiative_id, Some("i1".to_string()));
        // 1 done task (t2) + 0 resolved decisions, out of 1 task + 1 decision = 50%.
        assert_eq!(header.percent_complete, 50.0);
    }

    #[test]
    fn goal_header_falls_back_to_goal_when_no_initiatives_exist() {
        let events = vec![
            ev(
                0,
                EventScope::Global,
                EventType::ProjectCreated,
                Some("human"),
                json!({"id": "p1", "name": "Punch", "slug": "punch", "repository_path": "/repo", "default_branch": "main"}),
            ),
            ev(
                1,
                EventScope::Project("p1".to_string()),
                EventType::GoalCreated,
                Some("human"),
                json!({"id": "g1", "title": "Build login page", "description": "..."}),
            ),
            ev(
                2,
                EventScope::Project("p1".to_string()),
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "t1", "goal_id": "g1", "epic_id": null, "parent_task_id": null, "title": "Implement login form", "description": "..."}),
            ),
        ];
        let state = RuntimeState::replay(&events);
        let header = goal_header(&state, "p1");
        assert_eq!(header.source, GoalHeaderSource::Goal);
        assert_eq!(header.active_label, Some("Build login page".to_string()));
        assert_eq!(header.percent_complete, 0.0);
    }

    #[test]
    fn workflow_map_buckets_epic_tasks_by_stage() {
        let (state, _events) = fixture();
        let map = workflow_map(&state, "i1");
        assert_eq!(map.initiative_count, 1);
        assert_eq!(map.epic_count, 1);
        assert_eq!(map.complete_count, 1); // t2 is Done
        assert_eq!(map.execution_count, 0);
        assert_eq!(map.task_count, 0);
    }
}
