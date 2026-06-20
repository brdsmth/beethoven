use crate::models::*;
use crate::payloads::*;
use beethoven_events::{Event, EventType};
use std::collections::HashMap;

#[derive(Debug, Default)]
pub struct RuntimeState {
    pub projects: HashMap<String, Project>,
    pub agent_templates: HashMap<String, AgentTemplate>,
    pub tasks: HashMap<String, Task>,
    pub agent_instances: HashMap<String, AgentInstance>,
    pub inbox: HashMap<String, InboxItem>,
    pub runs: HashMap<String, Run>,
    pub artifacts: HashMap<String, Artifact>,
    pub reviews: HashMap<String, Review>,
    pub decisions: HashMap<String, Decision>,
    pub task_dependencies: Vec<TaskDependency>,
}

fn decode<T: serde::de::DeserializeOwned>(event: &Event) -> T {
    serde_json::from_value(event.payload.clone())
        .unwrap_or_else(|e| panic!("malformed payload for {:?}: {e}", event.event_type))
}

fn project_id_of(event: &Event) -> String {
    event
        .scope
        .project_id()
        .unwrap_or_else(|| panic!("{:?} must be project-scoped", event.event_type))
        .to_string()
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
            EventType::ProjectCreated => {
                let p: ProjectCreatedPayload = decode(event);
                self.projects.insert(
                    p.id.clone(),
                    Project {
                        id: p.id,
                        name: p.name,
                        slug: p.slug,
                        repository_path: p.repository_path.into(),
                        default_branch: p.default_branch,
                        status: ProjectStatus::Active,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::AgentTemplateCreated => {
                let p: AgentTemplateCreatedPayload = decode(event);
                self.agent_templates.insert(
                    p.id.clone(),
                    AgentTemplate {
                        id: p.id,
                        role: p.role,
                        label: p.label,
                        prompt: p.prompt,
                        permissions: p.permissions,
                        tools: p.tools,
                        runtime_profile_id: p.runtime_profile_id,
                        created_at: ts,
                    },
                );
            }
            EventType::AgentSpawned => {
                let project_id = project_id_of(event);
                let p: AgentSpawnedPayload = decode(event);
                self.agent_instances.insert(
                    p.id.clone(),
                    AgentInstance {
                        id: p.id,
                        project_id,
                        template_id: p.template_id,
                        role: p.role,
                        role_description: p.role_description,
                        parent_agent_id: p.parent_agent_id,
                        name: p.name,
                        // No separate "start" event exists in this model --
                        // collapses the old AgentCreated -> AgentStarted
                        // two-step into the one event that creates the
                        // instance, so a freshly spawned agent is
                        // immediately eligible for `pause_agent`/delegation
                        // reuse exactly like the old two-event sequence left it.
                        status: AgentStatus::Idle,
                        current_task_id: None,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::AgentArchived => {
                let p: AgentArchivedPayload = decode(event);
                if let Some(a) = self.agent_instances.get_mut(&p.id) {
                    a.status = AgentStatus::Archived;
                    a.current_task_id = None;
                    a.updated_at = ts;
                }
            }
            EventType::TaskCreated => {
                let project_id = project_id_of(event);
                let p: TaskCreatedPayload = decode(event);
                self.tasks.insert(
                    p.id.clone(),
                    Task {
                        id: p.id,
                        project_id,
                        parent_task_id: p.parent_task_id.clone(),
                        title: p.title,
                        description: p.description,
                        status: p.status,
                        assigned_agent_id: None,
                        branch: None,
                        pr_url: None,
                        attempt: 1,
                        blocked_reason: None,
                        success_criteria: p.success_criteria,
                        target_score: p.target_score,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
                // A subtask spawned mid-execution (`ExecutionOutcome::SpawnedSubtask`)
                // means the parent's current attempt is abandoned in favor of
                // doing this prerequisite first -- free its agent and requeue
                // it. Gated on the parent actually being assigned, so an
                // ordinary planning-breakdown (e.g. an ex-Initiative/Epic
                // container gaining children, which is never assigned) is a
                // true no-op instead of spuriously bumping attempt once per
                // child created.
                if let Some(parent_id) = p.parent_task_id {
                    let freed_agent_id = self.tasks.get(&parent_id).and_then(|t| t.assigned_agent_id.clone());
                    if let Some(aid) = freed_agent_id {
                        if let Some(parent) = self.tasks.get_mut(&parent_id) {
                            parent.attempt += 1;
                            parent.status = TaskStatus::Ready;
                            parent.assigned_agent_id = None;
                            parent.blocked_reason = None;
                            parent.updated_at = ts;
                        }
                        if let Some(a) = self.agent_instances.get_mut(&aid) {
                            a.status = AgentStatus::Idle;
                            a.current_task_id = None;
                            a.updated_at = ts;
                        }
                    }
                }
            }
            EventType::TaskApproved => {
                let p: TaskApprovedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.updated_at = ts;
                }
            }
            EventType::TaskAssigned => {
                let p: TaskAssignedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Assigned;
                    t.assigned_agent_id = Some(p.agent_id.clone());
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(a) = self.agent_instances.get_mut(&p.agent_id) {
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
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
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
                    t.blocked_reason = Some(p.reason);
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::TaskCompleted => {
                let p: TaskCompletedPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Done;
                    t.assigned_agent_id = None;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
                        a.status = AgentStatus::Completed;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::QuestionRaised => {
                let project_id = project_id_of(event);
                let p: QuestionRaisedPayload = decode(event);
                self.inbox.insert(
                    p.inbox_id.clone(),
                    InboxItem {
                        id: p.inbox_id,
                        project_id,
                        kind: InboxKind::Question,
                        task_id: Some(p.task_id),
                        agent_id: Some(p.agent_id),
                        decision_id: None,
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
                        t.blocked_reason = None;
                        t.updated_at = ts;
                    }
                }
            }
            EventType::DecisionEscalated => {
                let project_id = project_id_of(event);
                let p: DecisionEscalatedPayload = decode(event);
                self.inbox.insert(
                    p.inbox_id.clone(),
                    InboxItem {
                        id: p.inbox_id,
                        project_id,
                        kind: InboxKind::Decision,
                        task_id: None,
                        agent_id: None,
                        decision_id: Some(p.decision_id),
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
                let decision_id = self.inbox.get(&p.inbox_id).and_then(|i| i.decision_id.clone());
                if let Some(item) = self.inbox.get_mut(&p.inbox_id) {
                    item.status = InboxStatus::Resolved;
                    item.response = Some(p.response.clone());
                    item.resolved_at = Some(ts);
                }
                if let Some(did) = decision_id {
                    if let Some(d) = self.decisions.get_mut(&did) {
                        d.status = DecisionStatus::Resolved;
                        d.answer = Some(p.response);
                    }
                }
            }
            EventType::DecisionCreated => {
                let project_id = project_id_of(event);
                let p: DecisionCreatedPayload = decode(event);
                self.decisions.insert(
                    p.id.clone(),
                    Decision {
                        id: p.id,
                        project_id,
                        task_id: p.task_id,
                        question: p.question,
                        context: p.context,
                        answer: None,
                        status: DecisionStatus::Open,
                        created_at: ts,
                    },
                );
            }
            EventType::PRCreated => {
                let p: PrCreatedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.branch = Some(p.branch);
                    t.pr_url = Some(p.url);
                    t.updated_at = ts;
                }
            }
            EventType::BranchCreated => {
                let p: BranchCreatedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.branch = Some(p.branch);
                    t.updated_at = ts;
                }
            }
            EventType::PromptGenerated => {
                let p: PromptGeneratedPayload = decode(event);
                self.artifacts.insert(
                    event.id.clone(),
                    Artifact {
                        id: event.id.clone(),
                        run_id: p.run_id,
                        kind: ArtifactKind::Prompt,
                        content: p.prompt,
                        created_at: ts,
                    },
                );
            }
            EventType::RunStarted => {
                let p: RunStartedPayload = decode(event);
                self.runs.insert(
                    p.id.clone(),
                    Run {
                        id: p.id,
                        project_id: p.project_id,
                        agent_id: p.agent_id,
                        task_id: p.task_id,
                        attempt: p.attempt,
                        provider: p.provider,
                        status: RunStatus::Running,
                        summary: None,
                        started_at: ts,
                        completed_at: None,
                    },
                );
            }
            EventType::RunCompleted => {
                let p: RunCompletedPayload = decode(event);
                if let Some(r) = self.runs.get_mut(&p.id) {
                    r.status = RunStatus::Completed;
                    r.summary = Some(p.summary);
                    r.completed_at = Some(ts);
                }
                self.artifacts.insert(
                    event.id.clone(),
                    Artifact {
                        id: event.id.clone(),
                        run_id: p.id,
                        kind: ArtifactKind::Response,
                        content: p.response,
                        created_at: ts,
                    },
                );
            }
            EventType::RunFailed => {
                let p: RunFailedPayload = decode(event);
                if let Some(r) = self.runs.get_mut(&p.id) {
                    r.status = RunStatus::Failed;
                    r.summary = Some(p.error);
                    r.completed_at = Some(ts);
                }
            }
            EventType::FilesModified => {
                let p: FilesModifiedPayload = decode(event);
                self.artifacts.insert(
                    event.id.clone(),
                    Artifact {
                        id: event.id.clone(),
                        run_id: p.run_id,
                        kind: ArtifactKind::FilesModified,
                        content: format!("modified: {:?}\nadded: {:?}\ndeleted: {:?}", p.modified, p.added, p.deleted),
                        created_at: ts,
                    },
                );
            }
            EventType::DiffCaptured => {
                let p: DiffCapturedPayload = decode(event);
                self.artifacts.insert(
                    event.id.clone(),
                    Artifact {
                        id: event.id.clone(),
                        run_id: p.run_id,
                        kind: ArtifactKind::Diff,
                        content: p.diff,
                        created_at: ts,
                    },
                );
            }
            EventType::ReviewStarted => {
                let p: ReviewStartedPayload = decode(event);
                self.reviews.insert(
                    p.review_id.clone(),
                    Review {
                        id: p.review_id,
                        task_id: p.task_id.clone(),
                        reviewer_agent_id: p.reviewer_agent_id,
                        status: ReviewStatus::Pending,
                        score: 0.0,
                        summary: String::new(),
                        recommendations: Vec::new(),
                        created_at: ts,
                    },
                );
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Review;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
            }
            EventType::ReviewApproved => {
                let p: ReviewApprovedPayload = decode(event);
                if let Some(r) = self.reviews.get_mut(&p.review_id) {
                    r.status = ReviewStatus::Approved;
                    r.score = p.score;
                    r.summary = p.summary;
                    r.recommendations = p.recommendations;
                }
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Approved;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
            }
            EventType::ReviewRejected => {
                let p: ReviewRejectedPayload = decode(event);
                if let Some(r) = self.reviews.get_mut(&p.review_id) {
                    r.status = ReviewStatus::NeedsChanges;
                    r.score = p.score;
                    r.summary = p.summary;
                    r.recommendations = p.recommendations;
                }
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.assigned_agent_id = None;
                    t.attempt += 1;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::TaskRecovered => {
                let p: TaskRecoveredPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.assigned_agent_id = None;
                    t.attempt += 1;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(a) = self.agent_instances.get_mut(&p.agent_id) {
                    a.status = AgentStatus::Idle;
                    a.current_task_id = None;
                    a.updated_at = ts;
                }
            }
            EventType::ProjectPaused => {
                let p: ProjectStatusChangedPayload = decode(event);
                if let Some(proj) = self.projects.get_mut(&p.id) {
                    proj.status = ProjectStatus::Paused;
                    proj.updated_at = ts;
                }
            }
            EventType::ProjectResumed => {
                let p: ProjectStatusChangedPayload = decode(event);
                if let Some(proj) = self.projects.get_mut(&p.id) {
                    proj.status = ProjectStatus::Active;
                    proj.updated_at = ts;
                }
            }
            EventType::ProjectArchived => {
                let p: ProjectStatusChangedPayload = decode(event);
                if let Some(proj) = self.projects.get_mut(&p.id) {
                    proj.status = ProjectStatus::Archived;
                    proj.updated_at = ts;
                }
            }
            EventType::TaskRetried => {
                let p: TaskRetriedPayload = decode(event);
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.attempt += 1;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
            }
            EventType::TaskCancelled => {
                let p: TaskCancelledPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Cancelled;
                    t.assigned_agent_id = None;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::TaskReassigned => {
                let p: TaskReassignedPayload = decode(event);
                if let Some(old_id) = &p.old_agent_id {
                    if let Some(a) = self.agent_instances.get_mut(old_id) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
                if let Some(a) = self.agent_instances.get_mut(&p.new_agent_id) {
                    a.status = AgentStatus::Assigned;
                    a.current_task_id = Some(p.task_id.clone());
                    a.updated_at = ts;
                }
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Assigned;
                    t.assigned_agent_id = Some(p.new_agent_id.clone());
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
            }
            EventType::ReviewChangesRequested => {
                let p: ReviewChangesRequestedPayload = decode(event);
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.assigned_agent_id = None;
                    t.attempt += 1;
                    t.blocked_reason = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
                        a.status = AgentStatus::Idle;
                        a.current_task_id = None;
                        a.updated_at = ts;
                    }
                }
            }
            EventType::AgentPaused => {
                let p: AgentStatusChangedPayload = decode(event);
                if let Some(a) = self.agent_instances.get_mut(&p.id) {
                    a.status = AgentStatus::Paused;
                    a.updated_at = ts;
                }
            }
            EventType::AgentResumed => {
                let p: AgentStatusChangedPayload = decode(event);
                if let Some(a) = self.agent_instances.get_mut(&p.id) {
                    a.status = AgentStatus::Idle;
                    a.updated_at = ts;
                }
            }
            EventType::RepositoryPathChanged => {
                let p: RepositoryPathChangedPayload = decode(event);
                if let Some(proj) = self.projects.get_mut(&p.id) {
                    proj.repository_path = p.repository_path.into();
                    proj.updated_at = ts;
                }
            }
            EventType::DefaultBranchChanged => {
                let p: DefaultBranchChangedPayload = decode(event);
                if let Some(proj) = self.projects.get_mut(&p.id) {
                    proj.default_branch = p.default_branch;
                    proj.updated_at = ts;
                }
            }
            EventType::TaskDependencyCreated => {
                let p: TaskDependencyPayload = decode(event);
                let dep = TaskDependency { task_id: p.task_id, depends_on_task_id: p.depends_on_task_id };
                if !self.task_dependencies.contains(&dep) {
                    self.task_dependencies.push(dep);
                }
            }
            EventType::TaskDependencyRemoved => {
                let p: TaskDependencyPayload = decode(event);
                self.task_dependencies
                    .retain(|d| !(d.task_id == p.task_id && d.depends_on_task_id == p.depends_on_task_id));
            }
            EventType::WorkspaceCreated
            | EventType::WorkspaceDestroyed
            | EventType::PRMerged
            | EventType::WorktreeCreated
            | EventType::TmuxSessionCreated
            | EventType::TmuxSessionDestroyed
            | EventType::TaskPlanningStarted
            | EventType::TaskPlanningCompleted
            | EventType::RuntimeStarted
            | EventType::RuntimeStopped
            | EventType::RuntimeRestarted
            | EventType::AgentHeartbeat
            | EventType::AgentMarkedSlow
            | EventType::AgentMarkedStalled
            | EventType::AgentDelegatedTask => {
                // Recorded in the event log for audit/timeline purposes only;
                // no derived-state fields track these yet.
            }
        }
    }

    /// Whether `task_id` is a container (has children) ready to be marked
    /// `Done`: every child is `Done` and every `Decision` raised against it
    /// is `Resolved`. `Runtime` calls this (never the reducer itself) and,
    /// if true, emits `TaskCompleted` for it -- so completion cascading up a
    /// multi-level tree stays an explicit event per level, not a silent
    /// derived-state side effect. False for tasks already `Done` or with no
    /// children at all (a childless task completes only via its own
    /// execution/review cycle, never this path).
    pub fn parent_task_is_complete(&self, task_id: &str) -> bool {
        let Some(task) = self.tasks.get(task_id) else { return false };
        if task.status == TaskStatus::Done {
            return false;
        }

        let mut has_children = false;
        for t in self.tasks.values().filter(|t| t.parent_task_id.as_deref() == Some(task_id)) {
            has_children = true;
            if t.status != TaskStatus::Done {
                return false;
            }
        }
        if !has_children {
            return false;
        }

        if self
            .decisions
            .values()
            .any(|d| d.task_id.as_deref() == Some(task_id) && d.status != DecisionStatus::Resolved)
        {
            return false;
        }

        true
    }

    /// Every task `task_id` directly depends on.
    pub fn dependencies(&self, task_id: &str) -> Vec<&TaskDependency> {
        self.task_dependencies.iter().filter(|d| d.task_id == task_id).collect()
    }

    /// Every task that directly depends on `task_id`.
    pub fn dependents(&self, task_id: &str) -> Vec<&TaskDependency> {
        self.task_dependencies.iter().filter(|d| d.depends_on_task_id == task_id).collect()
    }

    /// True if recording `task_id` depends_on `depends_on_task_id` would
    /// close a cycle -- i.e. `depends_on_task_id` already (transitively)
    /// depends on `task_id`. Checked by `Runtime::add_task_dependency`
    /// before the edge is ever persisted.
    pub fn creates_cycle(&self, task_id: &str, depends_on_task_id: &str) -> bool {
        let mut stack = vec![depends_on_task_id.to_string()];
        let mut seen = std::collections::HashSet::new();
        while let Some(current) = stack.pop() {
            if current == task_id {
                return true;
            }
            if !seen.insert(current.clone()) {
                continue;
            }
            for dep in self.dependencies(&current) {
                stack.push(dep.depends_on_task_id.clone());
            }
        }
        false
    }

    /// Phase 2.4's central predicate: a leaf task is executable exactly when
    /// `Ready`, every task it depends on is `Done`, its project is `Active`,
    /// and no unresolved `Decision` is raised against it directly.
    /// Deliberately never persisted as its own status -- recomputed from
    /// current state every time a queue is built (`Runtime::ready_task_queue`),
    /// so a task automatically becomes executable the instant its last
    /// blocker clears, with no "unblock" event required and no way for a
    /// cached blocked flag to drift from the tasks/decisions/project status
    /// it's derived from.
    pub fn is_executable(&self, task_id: &str) -> bool {
        let Some(task) = self.tasks.get(task_id) else { return false };
        if task.status != TaskStatus::Ready {
            return false;
        }
        if self.tasks.values().any(|c| c.parent_task_id.as_deref() == Some(task_id)) {
            return false; // a container, never directly executable
        }
        let project_active =
            self.projects.get(&task.project_id).map(|p| p.status == ProjectStatus::Active).unwrap_or(false);
        if !project_active {
            return false;
        }
        let dependencies_satisfied = self
            .dependencies(task_id)
            .iter()
            .all(|d| self.tasks.get(&d.depends_on_task_id).map(|t| t.status == TaskStatus::Done).unwrap_or(false));
        if !dependencies_satisfied {
            return false;
        }
        if self.decisions.values().any(|d| d.task_id.as_deref() == Some(task_id) && d.status != DecisionStatus::Resolved)
        {
            return false;
        }

        true
    }
}
