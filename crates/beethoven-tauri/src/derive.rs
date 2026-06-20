use crate::dto::*;
use beethoven_agents::RuntimeRegistry;
use beethoven_core::{
    AgentInstance, AgentStatus, AgentTemplate, Artifact, ArtifactKind, Decision, DecisionStatus, InboxItem,
    InboxStatus, Project, RuntimeState, Task, TaskStatus,
};
use beethoven_events::{Event, EventType};
use beethoven_workspaces::{tmux_session_name, WorkspaceManager};
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
        parent_task_id: t.parent_task_id.clone(),
        title: t.title.clone(),
        description: t.description.clone(),
        status: t.status,
        assigned_agent_id: t.assigned_agent_id.clone(),
        assigned_agent_name,
        attempt: t.attempt,
        branch: t.branch.clone(),
        pr_url: t.pr_url.clone(),
        blocked_reason: t.blocked_reason.clone(),
        success_criteria: t.success_criteria.clone(),
        target_score: t.target_score,
        created_at: t.created_at,
        updated_at: t.updated_at,
    }
}

fn decision_dto(d: &Decision) -> DecisionDto {
    DecisionDto {
        id: d.id.clone(),
        project_id: d.project_id.clone(),
        task_id: d.task_id.clone(),
        question: d.question.clone(),
        context: d.context.clone(),
        answer: d.answer.clone(),
        status: d.status,
        created_at: d.created_at,
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
            TaskStatus::Cancelled => counts.cancelled += 1,
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

/// One task plus its children, recursively -- builds the unified planning
/// tree's nested shape from the flat `parent_task_id` map.
fn build_task_node(state: &RuntimeState, t: &Task) -> TaskNodeDto {
    let mut children: Vec<&Task> =
        state.tasks.values().filter(|c| c.parent_task_id.as_deref() == Some(t.id.as_str())).collect();
    children.sort_by_key(|c| c.created_at);
    TaskNodeDto {
        task: task_dto(state, t),
        children: children.into_iter().map(|c| build_task_node(state, c)).collect(),
    }
}

/// A project's root tasks (the old Initiative/Goal headers, unified), each
/// with its full descendant tree, plus the project's decisions -- or `None`
/// if the project doesn't exist.
pub fn project_detail(state: &RuntimeState, project_id: &str) -> Option<ProjectDetailDto> {
    let p = state.projects.get(project_id)?;

    let mut roots: Vec<&Task> =
        state.tasks.values().filter(|t| t.project_id == project_id && t.parent_task_id.is_none()).collect();
    roots.sort_by_key(|t| t.created_at);
    let tasks = roots.into_iter().map(|t| build_task_node(state, t)).collect();

    let mut decisions: Vec<&Decision> = state.decisions.values().filter(|d| d.project_id == project_id).collect();
    decisions.sort_by_key(|d| d.created_at);
    let decisions = decisions.into_iter().map(decision_dto).collect();

    Some(ProjectDetailDto {
        id: p.id.clone(),
        name: p.name.clone(),
        slug: p.slug.clone(),
        repository_path: p.repository_path.to_string_lossy().into_owned(),
        default_branch: p.default_branch.clone(),
        status: p.status,
        tasks,
        decisions,
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
                template_id: a.template_id.clone(),
                role: a.role,
                role_description: a.role_description.clone(),
                parent_agent_id: a.parent_agent_id.clone(),
                name: a.name.clone(),
                status: a.status,
                current_task_id: a.current_task_id.clone(),
                current_task_title,
                last_heartbeat: heartbeats.get(&a.id).copied(),
                created_at: a.created_at,
            }
        })
        .collect()
}

/// Every in-scope agent's live health across all projects (Operator Mode's
/// Agent Health dashboard, and Mission Control's CEO-facing panel, which
/// filters this down to the selected project). "In scope" means actively
/// working -- `Idle`/`Created`/`Completed`/`Paused` agents are excluded
/// entirely rather than reported `Healthy`, since they have no heartbeat to
/// judge staleness against. Health is always recomputed from `now() -
/// last_heartbeat`, never read off the `AgentMarkedSlow`/`AgentMarkedStalled`
/// audit events `Runtime::sync_agent_health_events` emits.
pub fn agent_health_for(state: &RuntimeState, events: &[Event]) -> Vec<AgentHealthDto> {
    let heartbeats = last_heartbeats(events);
    let now = Utc::now();
    let mut agents: Vec<&AgentInstance> = state
        .agent_instances
        .values()
        .filter(|a| matches!(a.status, AgentStatus::Assigned | AgentStatus::Executing))
        .collect();
    agents.sort_by_key(|a| a.name.clone());

    agents
        .into_iter()
        .filter_map(|a| {
            let project = state.projects.get(&a.project_id)?;
            let current_task_title = a.current_task_id.as_ref().and_then(|tid| state.tasks.get(tid)).map(|t| t.title.clone());
            let last_heartbeat = heartbeats.get(&a.id).copied();
            let health = match last_heartbeat {
                None => AgentHealthStatus::Stalled,
                Some(ts) => {
                    let age = (now - ts).num_seconds();
                    if age < 60 {
                        AgentHealthStatus::Healthy
                    } else if age < 180 {
                        AgentHealthStatus::Slow
                    } else {
                        AgentHealthStatus::Stalled
                    }
                }
            };
            Some(AgentHealthDto {
                agent_id: a.id.clone(),
                agent_name: a.name.clone(),
                role: a.role,
                role_description: a.role_description.clone(),
                project_id: project.id.clone(),
                project_slug: project.slug.clone(),
                current_task_id: a.current_task_id.clone(),
                current_task_title,
                last_heartbeat,
                health,
            })
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
            let decision_question = i.decision_id.as_ref().and_then(|did| state.decisions.get(did)).map(|d| d.question.clone());
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
                decision_question,
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

fn timeline_event_dto(e: &Event) -> TimelineEventDto {
    TimelineEventDto {
        id: e.id.clone(),
        project_id: e.scope.project_id().map(|s| s.to_string()),
        event_type: e.event_type.as_str().to_string(),
        actor_id: e.actor_id.clone(),
        payload: e.payload.clone(),
        timestamp: e.timestamp,
    }
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
        .map(timeline_event_dto)
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
/// latest `RunStarted` event (falling back to `TaskStarted`, then to the
/// task's own `updated_at`).
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
                e.event_type == EventType::RunStarted
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
            agent_role: agent.role,
            started_at,
        });
    }
    out.sort_by_key(|e| e.started_at);
    out
}

/// Every task in `root_id`'s subtree (including `root_id` itself).
fn subtree_ids(state: &RuntimeState, root_id: &str) -> HashSet<String> {
    let mut out = HashSet::new();
    let mut stack = vec![root_id.to_string()];
    while let Some(id) = stack.pop() {
        if out.insert(id.clone()) {
            for c in state.tasks.values().filter(|t| t.parent_task_id.as_deref() == Some(id.as_str())) {
                stack.push(c.id.clone());
            }
        }
    }
    out
}

/// The leaf tasks (no children) in `root_id`'s subtree -- the only tasks
/// with real, directly-executable work (see `Runtime::run_lead_delegation`).
/// Container tasks themselves are excluded so a long-running container
/// doesn't get counted as "ready" work forever.
fn leaf_descendants<'a>(state: &'a RuntimeState, root_id: &str) -> Vec<&'a Task> {
    let mut out = Vec::new();
    let mut stack = vec![root_id.to_string()];
    while let Some(id) = stack.pop() {
        let children: Vec<&Task> =
            state.tasks.values().filter(|t| t.parent_task_id.as_deref() == Some(id.as_str())).collect();
        if children.is_empty() {
            if let Some(t) = state.tasks.get(&id) {
                out.push(t);
            }
        } else {
            stack.extend(children.into_iter().map(|c| c.id.clone()));
        }
    }
    out
}

/// The project's most-recently-updated open root task (the old
/// Initiative/Goal header, unified) and how far its subtree has progressed
/// -- leaf tasks done, plus any `Decision`s raised anywhere in the subtree
/// resolved, over the total of both.
pub fn goal_header(state: &RuntimeState, project_id: &str) -> GoalHeaderDto {
    let project_name = state.projects.get(project_id).map(|p| p.name.clone()).unwrap_or_default();

    let mut roots: Vec<&Task> = state
        .tasks
        .values()
        .filter(|t| {
            t.project_id == project_id && t.parent_task_id.is_none() && !matches!(t.status, TaskStatus::Done | TaskStatus::Cancelled)
        })
        .collect();
    roots.sort_by_key(|t| t.updated_at);

    let Some(root) = roots.last() else {
        return GoalHeaderDto {
            project_id: project_id.to_string(),
            project_name,
            active_task_id: None,
            active_label: None,
            percent_complete: 0.0,
        };
    };

    let leaves = leaf_descendants(state, &root.id);
    let subtree = subtree_ids(state, &root.id);
    let decisions: Vec<&Decision> =
        state.decisions.values().filter(|d| d.task_id.as_deref().is_some_and(|tid| subtree.contains(tid))).collect();

    let total = leaves.len() + decisions.len();
    let done = leaves.iter().filter(|t| t.status == TaskStatus::Done).count()
        + decisions.iter().filter(|d| d.status == DecisionStatus::Resolved).count();
    let percent_complete = if total == 0 { 0.0 } else { (done as f64 / total as f64) * 100.0 };

    GoalHeaderDto {
        project_id: project_id.to_string(),
        project_name,
        active_task_id: Some(root.id.clone()),
        active_label: Some(root.title.clone()),
        percent_complete,
    }
}

/// Buckets a root task's leaf descendants into the spec's 6 workflow
/// stages -- the old Initiative/Epic-specific stage map, generalized to any
/// task subtree. `Cancelled` leaves are deliberately excluded -- they're
/// surfaced via Project Health, not this linear progress map.
pub fn workflow_map(state: &RuntimeState, task_id: &str) -> WorkflowMapDto {
    let mut map = WorkflowMapDto::default();
    for t in leaf_descendants(state, task_id) {
        match t.status {
            TaskStatus::Draft => map.draft_count += 1,
            TaskStatus::Ready | TaskStatus::Assigned => map.ready_count += 1,
            TaskStatus::InProgress => map.executing_count += 1,
            TaskStatus::Review | TaskStatus::Approved => map.review_count += 1,
            TaskStatus::Done => map.done_count += 1,
            TaskStatus::Blocked => map.blocked_count += 1,
            TaskStatus::Cancelled => {}
        }
    }
    map
}

/// Every decision across every project, oldest-first, joined to its
/// project/task for display and action context.
pub fn decisions_for(state: &RuntimeState) -> Vec<DecisionSummaryDto> {
    let mut decisions: Vec<&Decision> = state.decisions.values().collect();
    decisions.sort_by_key(|d| d.created_at);
    decisions
        .into_iter()
        .filter_map(|d| {
            let project = state.projects.get(&d.project_id)?;
            let task_title = d.task_id.as_ref().and_then(|tid| state.tasks.get(tid)).map(|t| t.title.clone());
            Some(DecisionSummaryDto {
                id: d.id.clone(),
                project_id: project.id.clone(),
                project_slug: project.slug.clone(),
                task_id: d.task_id.clone(),
                task_title,
                question: d.question.clone(),
                context: d.context.clone(),
                status: d.status,
                created_at: d.created_at,
            })
        })
        .collect()
}

/// Every `Assigned`/`InProgress` task's on-disk workspace -- path, branch,
/// tmux session -- reconstructed the same deterministic way
/// `Runtime::cleanup_orphaned_workspace` does, with no new persisted state.
pub fn workspaces_for(state: &RuntimeState, projects_root: &Path) -> Vec<WorkspaceDto> {
    let mut tasks: Vec<&Task> =
        state.tasks.values().filter(|t| matches!(t.status, TaskStatus::Assigned | TaskStatus::InProgress)).collect();
    tasks.sort_by_key(|t| t.created_at);

    tasks
        .into_iter()
        .filter_map(|t| {
            let agent_id = t.assigned_agent_id.as_ref()?;
            let agent = state.agent_instances.get(agent_id)?;
            let project = state.projects.get(&t.project_id)?;

            let branch = t.branch.clone().unwrap_or_else(|| beethoven_git::branch_name(&t.title));
            let workspaces = WorkspaceManager::new(projects_root.join(&project.slug).join("workspaces"));
            let git_worktree = workspaces.path_for(&t.id).join("repo").to_string_lossy().into_owned();
            let tmux_session = tmux_session_name(&project.slug, agent.role.as_str(), &t.id);

            Some(WorkspaceDto {
                task_id: t.id.clone(),
                task_title: t.title.clone(),
                project_id: project.id.clone(),
                project_slug: project.slug.clone(),
                agent_id: agent.id.clone(),
                agent_name: agent.name.clone(),
                branch,
                git_worktree,
                tmux_session,
            })
        })
        .collect()
}

/// The `limit` most recently captured diff artifacts across every project,
/// newest-first, joined to their run's task/project.
pub fn recent_diffs(state: &RuntimeState, limit: usize) -> Vec<DiffSummaryDto> {
    let mut diffs: Vec<&Artifact> = state.artifacts.values().filter(|a| a.kind == ArtifactKind::Diff).collect();
    diffs.sort_by_key(|a| a.created_at);
    diffs.reverse();
    diffs.truncate(limit);

    diffs
        .into_iter()
        .filter_map(|a| {
            let run = state.runs.get(&a.run_id)?;
            let task = state.tasks.get(&run.task_id)?;
            let project = state.projects.get(&task.project_id)?;
            Some(DiffSummaryDto {
                id: a.id.clone(),
                task_id: task.id.clone(),
                task_title: task.title.clone(),
                project_id: project.id.clone(),
                project_slug: project.slug.clone(),
                content: a.content.clone(),
                created_at: a.created_at,
            })
        })
        .collect()
}

/// Every `AgentTemplate`, joined to the `RuntimeProfile` its
/// `runtime_profile_id` resolves to. Read-only: Phase 1.9c does not add any
/// new editing/persistence for runtime profiles.
pub fn agent_templates_for(state: &RuntimeState, registry: &RuntimeRegistry) -> Vec<AgentTemplateDto> {
    let mut templates: Vec<&AgentTemplate> = state.agent_templates.values().collect();
    templates.sort_by_key(|t| t.created_at);
    templates
        .into_iter()
        .map(|t| {
            let runtime_profile = registry.profile(&t.runtime_profile_id).map(|p| RuntimeProfileDto {
                id: p.id.clone(),
                runtime_id: p.runtime_id.clone(),
                model: p.model.clone(),
            });
            AgentTemplateDto {
                id: t.id.clone(),
                role: t.role,
                label: t.label.clone(),
                prompt: t.prompt.clone(),
                permissions: t.permissions.clone(),
                tools: t.tools.clone(),
                runtime_profile,
                created_at: t.created_at,
            }
        })
        .collect()
}

/// The most recent `Artifact{kind: Prompt}`s across all projects, joined to
/// their run/task/project (Operator Mode's Prompts page).
pub fn recent_prompts(state: &RuntimeState, limit: usize) -> Vec<PromptSummaryDto> {
    let mut prompts: Vec<&Artifact> = state.artifacts.values().filter(|a| a.kind == ArtifactKind::Prompt).collect();
    prompts.sort_by_key(|a| a.created_at);
    prompts.reverse();
    prompts.truncate(limit);

    prompts
        .into_iter()
        .filter_map(|a| {
            let run = state.runs.get(&a.run_id)?;
            let task = state.tasks.get(&run.task_id)?;
            let project = state.projects.get(&task.project_id)?;
            Some(PromptSummaryDto {
                id: a.id.clone(),
                run_id: a.run_id.clone(),
                task_id: task.id.clone(),
                task_title: task.title.clone(),
                project_id: project.id.clone(),
                project_slug: project.slug.clone(),
                content: a.content.clone(),
                created_at: a.created_at,
            })
        })
        .collect()
}

/// A task's full execution history: itself, its runs (with prompt/response
/// denormalized on), its reviews, its captured artifacts, and the timeline
/// slice that mentions its `task_id` directly or one of its runs' `run_id`
/// -- backs the shared Task Detail panel's "View Diff"/"View Logs" actions.
pub fn task_detail(state: &RuntimeState, events: &[Event], task_id: &str) -> Option<TaskDetailDto> {
    let t = state.tasks.get(task_id)?;
    let task = task_dto(state, t);

    let mut runs: Vec<&beethoven_core::Run> = state.runs.values().filter(|r| r.task_id == task_id).collect();
    runs.sort_by_key(|r| r.started_at);
    let run_ids: HashSet<&str> = runs.iter().map(|r| r.id.as_str()).collect();
    let runs = runs
        .into_iter()
        .map(|r| {
            let mut prompt = None;
            let mut response = None;
            for artifact in state.artifacts.values().filter(|a| a.run_id == r.id) {
                match artifact.kind {
                    ArtifactKind::Prompt => prompt = Some(artifact.content.clone()),
                    ArtifactKind::Response => response = Some(artifact.content.clone()),
                    ArtifactKind::Diff | ArtifactKind::FilesModified => {}
                }
            }
            RunDto {
                id: r.id.clone(),
                agent_id: r.agent_id.clone(),
                attempt: r.attempt,
                provider: r.provider.clone(),
                status: r.status,
                summary: r.summary.clone(),
                prompt,
                response,
                started_at: r.started_at,
                completed_at: r.completed_at,
            }
        })
        .collect();

    let mut reviews: Vec<&beethoven_core::Review> = state.reviews.values().filter(|r| r.task_id == task_id).collect();
    reviews.sort_by_key(|r| r.created_at);
    let reviews = reviews
        .into_iter()
        .map(|r| ReviewDto {
            id: r.id.clone(),
            status: r.status,
            score: r.score,
            summary: r.summary.clone(),
            recommendations: r.recommendations.clone(),
            created_at: r.created_at,
        })
        .collect();

    let mut artifacts: Vec<&Artifact> = state.artifacts.values().filter(|a| run_ids.contains(a.run_id.as_str())).collect();
    artifacts.sort_by_key(|a| a.created_at);
    let artifacts = artifacts
        .into_iter()
        .map(|a| ArtifactDto {
            id: a.id.clone(),
            kind: a.kind,
            content: a.content.clone(),
            created_at: a.created_at,
        })
        .collect();

    let mut timeline: Vec<TimelineEventDto> = events
        .iter()
        .filter(|e| {
            let matches_task = e.payload.get("task_id").and_then(|v| v.as_str()) == Some(task_id);
            let matches_run =
                e.payload.get("run_id").and_then(|v| v.as_str()).is_some_and(|rid| run_ids.contains(rid));
            matches_task || matches_run
        })
        .map(timeline_event_dto)
        .collect();
    timeline.sort_by_key(|e| e.timestamp);
    timeline.reverse();

    Some(TaskDetailDto {
        task,
        runs,
        reviews,
        artifacts,
        timeline,
    })
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

    /// One project ("p1"/"punch") with: a root task ("g1", the old Goal)
    /// with one leaf child ("t1"), assigned to agent "a1" and in progress;
    /// a second root task ("i1", the old Initiative) with a mid-level
    /// container child ("e1", the old Epic) whose own leaf child ("t2") is
    /// done (assigned to a separate agent "a3" so it doesn't clear "a1"'s
    /// current task), and a decision ("d1") raised against "i1" that's
    /// still open; an idle Lead agent "a2"; an open inbox question for t1
    /// and an open inbox decision for d1; a heartbeat for "a1"; and a
    /// RuntimeStarted event.
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
                EventType::AgentSpawned,
                Some("system"),
                json!({"id": "a2", "template_id": "tmpl-lead", "role": "lead", "role_description": "Lead agent for Punch", "parent_agent_id": null, "name": "PM-1"}),
            ),
            ev(
                2,
                p.clone(),
                EventType::AgentSpawned,
                Some("a2"),
                json!({"id": "a1", "template_id": "tmpl-worker", "role": "worker", "role_description": "Backend work", "parent_agent_id": "a2", "name": "Backend-1"}),
            ),
            ev(
                3,
                p.clone(),
                EventType::AgentSpawned,
                Some("a2"),
                json!({"id": "a3", "template_id": "tmpl-worker", "role": "worker", "role_description": "Backend work", "parent_agent_id": "a2", "name": "Backend-2"}),
            ),
            ev(
                7,
                p.clone(),
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "g1", "parent_task_id": null, "title": "Build login page", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(
                8,
                p.clone(),
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "t1", "parent_task_id": "g1", "title": "Implement login form", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(9, p.clone(), EventType::TaskAssigned, Some("a2"), json!({"task_id": "t1", "agent_id": "a1"})),
            ev(10, p.clone(), EventType::TaskStarted, Some("a1"), json!({"task_id": "t1"})),
            ev(
                11,
                p.clone(),
                EventType::RunStarted,
                Some("a1"),
                json!({"id": "mr1", "agent_id": "a1", "project_id": "p1", "task_id": "t1", "attempt": 1, "provider": "claude"}),
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
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "i1", "parent_task_id": null, "title": "Build Gmail Agent", "description": "Routes inbound email", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(
                14,
                p.clone(),
                EventType::TaskCreated,
                Some("a2"),
                json!({"id": "e1", "parent_task_id": "i1", "title": "Implement OAuth", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(
                15,
                p.clone(),
                EventType::TaskCreated,
                Some("a2"),
                json!({"id": "t2", "parent_task_id": "e1", "title": "Implement: Implement OAuth", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(16, p.clone(), EventType::TaskAssigned, Some("a2"), json!({"task_id": "t2", "agent_id": "a3"})),
            ev(17, p.clone(), EventType::TaskStarted, Some("a3"), json!({"task_id": "t2"})),
            ev(18, p.clone(), EventType::TaskCompleted, Some("a3"), json!({"task_id": "t2"})),
            ev(
                19,
                p.clone(),
                EventType::DecisionCreated,
                Some("a2"),
                json!({"id": "d1", "task_id": "i1", "question": "Pick an email provider", "context": "Gmail vs IMAP"}),
            ),
            ev(
                20,
                p.clone(),
                EventType::DecisionEscalated,
                Some("a2"),
                json!({"decision_id": "d1", "inbox_id": "inbox2", "prompt": "Pick an email provider"}),
            ),
            ev(21, p.clone(), EventType::AgentHeartbeat, Some("a1"), json!({"agent_id": "a1"})),
            ev(22, EventScope::Global, EventType::RuntimeStarted, Some("system"), json!({})),
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
        assert_eq!(punch.task_counts.ready, 3); // g1, i1, e1
        assert_eq!(punch.open_inbox_count, 2); // inbox1 + inbox2
    }

    #[test]
    fn project_detail_nests_the_full_task_tree_and_lists_decisions() {
        let (state, _events) = fixture();
        let detail = project_detail(&state, "p1").expect("project exists");

        assert_eq!(detail.tasks.len(), 2); // g1, i1 -- both roots
        let g1 = detail.tasks.iter().find(|n| n.task.id == "g1").unwrap();
        assert_eq!(g1.children.len(), 1);
        assert_eq!(g1.children[0].task.id, "t1");
        assert!(g1.children[0].children.is_empty());

        let i1 = detail.tasks.iter().find(|n| n.task.id == "i1").unwrap();
        assert_eq!(i1.children.len(), 1);
        let e1 = &i1.children[0];
        assert_eq!(e1.task.id, "e1");
        assert_eq!(e1.children.len(), 1);
        assert_eq!(e1.children[0].task.id, "t2");

        assert_eq!(detail.decisions.len(), 1);
        assert_eq!(detail.decisions[0].task_id, Some("i1".to_string()));
        assert_eq!(detail.decisions[0].status, DecisionStatus::Open);

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
    fn agent_health_for_excludes_idle_agents_and_reports_a_fresh_heartbeat_as_healthy() {
        let (state, events) = fixture();
        let health = agent_health_for(&state, &events);

        // a1 is Executing (in scope); a2 is an idle Lead and a3 went back to
        // Idle once its task completed -- both excluded entirely, not
        // reported Healthy.
        assert_eq!(health.len(), 1);
        let a1 = &health[0];
        assert_eq!(a1.agent_id, "a1");
        assert_eq!(a1.project_slug, "punch");
        assert_eq!(a1.current_task_id, Some("t1".to_string()));
        assert!(a1.last_heartbeat.is_some());
        assert_eq!(a1.health, AgentHealthStatus::Healthy);
    }

    #[test]
    fn inbox_for_resolves_task_and_decision_titles() {
        let (state, _events) = fixture();
        let items = inbox_for(&state, "p1");
        assert_eq!(items.len(), 2);
        let question = items.iter().find(|i| i.id == "inbox1").unwrap();
        assert_eq!(question.task_title, Some("Implement login form".to_string()));
        let decision_item = items.iter().find(|i| i.id == "inbox2").unwrap();
        assert_eq!(decision_item.decision_question, Some("Pick an email provider".to_string()));
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
    fn active_execution_uses_run_started_timestamp() {
        let (state, events) = fixture();
        let active = active_execution(&state, &events);
        assert_eq!(active.len(), 1);
        assert_eq!(active[0].task_id, "t1");
        assert_eq!(active[0].agent_name, "Backend-1");
        let run_started = events.iter().find(|e| e.event_type == EventType::RunStarted).unwrap().timestamp;
        assert_eq!(active[0].started_at, run_started);
    }

    #[test]
    fn goal_header_picks_the_most_recently_updated_open_root_and_computes_percent_complete() {
        let (state, _events) = fixture();
        let header = goal_header(&state, "p1");
        // i1 was created after g1 and neither has been touched since, so i1
        // is "most recently updated".
        assert_eq!(header.active_task_id, Some("i1".to_string()));
        assert_eq!(header.active_label, Some("Build Gmail Agent".to_string()));
        // 1 done leaf (t2) + 0 resolved decisions, out of 1 leaf + 1 decision = 50%.
        assert_eq!(header.percent_complete, 50.0);
    }

    #[test]
    fn goal_header_falls_back_to_zero_percent_with_no_decisions() {
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
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "g1", "parent_task_id": null, "title": "Build login page", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
            ev(
                2,
                EventScope::Project("p1".to_string()),
                EventType::TaskCreated,
                Some("human"),
                json!({"id": "t1", "parent_task_id": "g1", "title": "Implement login form", "description": "...", "status": "ready", "success_criteria": "", "target_score": 70.0}),
            ),
        ];
        let state = RuntimeState::replay(&events);
        let header = goal_header(&state, "p1");
        assert_eq!(header.active_task_id, Some("g1".to_string()));
        assert_eq!(header.active_label, Some("Build login page".to_string()));
        assert_eq!(header.percent_complete, 0.0);
    }

    #[test]
    fn workflow_map_buckets_leaf_descendants_by_status() {
        let (state, _events) = fixture();
        let map = workflow_map(&state, "i1");
        assert_eq!(map.done_count, 1); // t2 is Done
        assert_eq!(map.ready_count, 0);
        assert_eq!(map.executing_count, 0);
    }

    #[test]
    fn decisions_for_joins_project_and_task_context() {
        let (state, _events) = fixture();
        let decisions = decisions_for(&state);
        assert_eq!(decisions.len(), 1);
        let d1 = &decisions[0];
        assert_eq!(d1.project_slug, "punch");
        assert_eq!(d1.task_title, Some("Build Gmail Agent".to_string()));
        assert_eq!(d1.status, DecisionStatus::Open);
    }

    #[test]
    fn workspaces_for_lists_only_assigned_and_in_progress_tasks_with_deterministic_paths() {
        let (state, _events) = fixture();
        let root = std::env::temp_dir().join("beethoven-tauri-test-workspaces-root");
        let workspaces = workspaces_for(&state, &root);
        // t1 is InProgress; t2 is Done and excluded.
        assert_eq!(workspaces.len(), 1);
        let w = &workspaces[0];
        assert_eq!(w.task_id, "t1");
        assert_eq!(w.project_slug, "punch");
        assert_eq!(w.agent_name, "Backend-1");
        assert_eq!(w.branch, beethoven_git::branch_name("Implement login form"));
        let expected_manager = WorkspaceManager::new(root.join("punch").join("workspaces"));
        assert_eq!(w.git_worktree, expected_manager.path_for("t1").join("repo").to_string_lossy().into_owned());
        assert_eq!(w.tmux_session, tmux_session_name("punch", "worker", "t1"));
    }

    #[test]
    fn recent_diffs_joins_run_to_task_and_project_and_respects_the_limit() {
        let (_state, mut events) = fixture();
        events.push(ev(
            23,
            EventScope::Project("p1".to_string()),
            EventType::DiffCaptured,
            Some("a1"),
            json!({"run_id": "mr1", "diff": "diff --git a/foo b/foo\n+added line"}),
        ));
        let state = RuntimeState::replay(&events);

        let diffs = recent_diffs(&state, 10);
        assert_eq!(diffs.len(), 1);
        assert_eq!(diffs[0].task_id, "t1");
        assert_eq!(diffs[0].project_slug, "punch");

        assert_eq!(recent_diffs(&state, 0).len(), 0);
    }

    #[test]
    fn task_detail_collects_runs_artifacts_and_a_task_scoped_timeline() {
        let (_state, mut events) = fixture();
        events.push(ev(
            23,
            EventScope::Project("p1".to_string()),
            EventType::DiffCaptured,
            Some("a1"),
            json!({"run_id": "mr1", "diff": "diff --git a/foo b/foo\n+added line"}),
        ));
        let state = RuntimeState::replay(&events);

        let detail = task_detail(&state, &events, "t1").expect("task exists");
        assert_eq!(detail.task.id, "t1");
        assert_eq!(detail.runs.len(), 1);
        assert_eq!(detail.runs[0].id, "mr1");
        assert_eq!(detail.artifacts.len(), 1);
        assert_eq!(detail.artifacts[0].kind, ArtifactKind::Diff);
        // TaskAssigned, TaskStarted, RunStarted, QuestionRaised, DiffCaptured
        // (the last one joined via run_id, not task_id).
        assert_eq!(detail.timeline.len(), 5);
        assert_eq!(detail.timeline[0].event_type, "DiffCaptured"); // newest first

        assert!(task_detail(&state, &events, "missing").is_none());
    }
}
