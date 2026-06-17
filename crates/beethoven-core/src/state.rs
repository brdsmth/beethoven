use crate::models::*;
use crate::payloads::*;
use beethoven_events::{Event, EventType};
use std::collections::HashMap;

#[derive(Debug, Default)]
pub struct RuntimeState {
    pub projects: HashMap<String, Project>,
    pub agent_templates: HashMap<String, AgentTemplate>,
    pub goals: HashMap<String, Goal>,
    pub tasks: HashMap<String, Task>,
    pub agent_instances: HashMap<String, AgentInstance>,
    pub inbox: HashMap<String, InboxItem>,
    pub model_runs: HashMap<String, ModelRun>,
    pub model_artifacts: HashMap<String, ModelArtifact>,
    pub artifacts: HashMap<String, Artifact>,
    pub reviews: HashMap<String, Review>,
    pub initiatives: HashMap<String, Initiative>,
    pub requirements: HashMap<String, Requirement>,
    pub plans: HashMap<String, Plan>,
    pub epics: HashMap<String, Epic>,
    pub decisions: HashMap<String, Decision>,
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
                        kind: p.kind,
                        role: p.role,
                        prompt: p.prompt,
                        permissions: p.permissions,
                        tools: p.tools,
                        created_at: ts,
                    },
                );
            }
            EventType::GoalCreated => {
                let project_id = project_id_of(event);
                let p: GoalCreatedPayload = decode(event);
                self.goals.insert(
                    p.id.clone(),
                    Goal {
                        id: p.id,
                        project_id,
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
                let project_id = project_id_of(event);
                let p: AgentCreatedPayload = decode(event);
                self.agent_instances.insert(
                    p.id.clone(),
                    AgentInstance {
                        id: p.id,
                        project_id,
                        template_id: p.template_id,
                        kind: p.kind,
                        name: p.name,
                        status: AgentStatus::Created,
                        current_task_id: None,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::AgentStarted => {
                let p: AgentStartedPayload = decode(event);
                if let Some(a) = self.agent_instances.get_mut(&p.id) {
                    a.status = AgentStatus::Idle;
                    a.updated_at = ts;
                }
            }
            EventType::AgentStopped => {
                let p: AgentStartedPayload = decode(event);
                if let Some(a) = self.agent_instances.get_mut(&p.id) {
                    a.status = AgentStatus::Completed;
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
                        goal_id: p.goal_id,
                        epic_id: p.epic_id,
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
                        if let Some(a) = self.agent_instances.get_mut(&aid) {
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
                let goal_id = self.tasks.get(&p.task_id).and_then(|t| t.goal_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Done;
                    t.assigned_agent_id = None;
                    t.updated_at = ts;
                }
                if let Some(aid) = agent_id {
                    if let Some(a) = self.agent_instances.get_mut(&aid) {
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
                        t.updated_at = ts;
                    }
                }
            }
            EventType::DecisionRequested => {
                let project_id = project_id_of(event);
                let p: DecisionRequestedPayload = decode(event);
                self.inbox.insert(
                    p.inbox_id.clone(),
                    InboxItem {
                        id: p.inbox_id,
                        project_id,
                        kind: InboxKind::Decision,
                        task_id: p.task_id,
                        agent_id: p.agent_id,
                        decision_id: None,
                        prompt: p.prompt,
                        status: InboxStatus::Open,
                        response: None,
                        created_at: ts,
                        resolved_at: None,
                    },
                );
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
                let task_id = self.inbox.get(&p.inbox_id).and_then(|i| i.task_id.clone());
                let decision_id = self.inbox.get(&p.inbox_id).and_then(|i| i.decision_id.clone());
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
                if let Some(did) = decision_id {
                    if let Some(d) = self.decisions.get_mut(&did) {
                        d.status = DecisionStatus::Resolved;
                    }
                }
            }
            EventType::InitiativeCreated => {
                let project_id = project_id_of(event);
                let p: InitiativeCreatedPayload = decode(event);
                self.initiatives.insert(
                    p.id.clone(),
                    Initiative {
                        id: p.id,
                        project_id,
                        title: p.title,
                        description: p.description,
                        status: InitiativeStatus::Discovery,
                        created_at: ts,
                        updated_at: ts,
                    },
                );
            }
            EventType::InitiativePlanned => {
                let p: InitiativePlannedPayload = decode(event);
                if let Some(i) = self.initiatives.get_mut(&p.id) {
                    i.status = InitiativeStatus::Planned;
                    i.updated_at = ts;
                }
            }
            EventType::InitiativeCompleted => {
                let p: InitiativeCompletedPayload = decode(event);
                if let Some(i) = self.initiatives.get_mut(&p.id) {
                    i.status = InitiativeStatus::Completed;
                    i.updated_at = ts;
                }
            }
            EventType::RequirementCreated => {
                let project_id = project_id_of(event);
                let p: RequirementCreatedPayload = decode(event);
                self.requirements.insert(
                    p.id.clone(),
                    Requirement {
                        id: p.id,
                        project_id,
                        initiative_id: p.initiative_id,
                        description: p.description,
                        created_at: ts,
                    },
                );
            }
            EventType::PlanCreated => {
                let project_id = project_id_of(event);
                let p: PlanCreatedPayload = decode(event);
                self.plans.insert(
                    p.id.clone(),
                    Plan {
                        id: p.id,
                        project_id,
                        initiative_id: p.initiative_id,
                        summary: p.summary,
                        created_at: ts,
                    },
                );
            }
            EventType::EpicCreated => {
                let project_id = project_id_of(event);
                let p: EpicCreatedPayload = decode(event);
                self.epics.insert(
                    p.id.clone(),
                    Epic {
                        id: p.id,
                        project_id,
                        initiative_id: p.initiative_id,
                        title: p.title,
                        created_at: ts,
                    },
                );
            }
            EventType::DecisionCreated => {
                let project_id = project_id_of(event);
                let p: DecisionCreatedPayload = decode(event);
                self.decisions.insert(
                    p.id.clone(),
                    Decision {
                        id: p.id,
                        project_id,
                        initiative_id: p.initiative_id,
                        title: p.title,
                        context: p.context,
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
                self.model_artifacts.insert(
                    event.id.clone(),
                    ModelArtifact {
                        id: event.id.clone(),
                        model_run_id: p.model_run_id,
                        kind: ModelArtifactKind::Prompt,
                        content: p.prompt,
                        created_at: ts,
                    },
                );
            }
            EventType::ModelRunStarted => {
                let p: ModelRunStartedPayload = decode(event);
                self.model_runs.insert(
                    p.id.clone(),
                    ModelRun {
                        id: p.id,
                        project_id: p.project_id,
                        agent_id: p.agent_id,
                        task_id: p.task_id,
                        provider: p.provider,
                        status: ModelRunStatus::Running,
                        summary: None,
                        started_at: ts,
                        completed_at: None,
                    },
                );
            }
            EventType::ModelRunCompleted => {
                let p: ModelRunCompletedPayload = decode(event);
                if let Some(r) = self.model_runs.get_mut(&p.id) {
                    r.status = ModelRunStatus::Completed;
                    r.summary = Some(p.summary);
                    r.completed_at = Some(ts);
                }
                self.model_artifacts.insert(
                    event.id.clone(),
                    ModelArtifact {
                        id: event.id.clone(),
                        model_run_id: p.id,
                        kind: ModelArtifactKind::Response,
                        content: p.response,
                        created_at: ts,
                    },
                );
            }
            EventType::ModelRunFailed => {
                let p: ModelRunFailedPayload = decode(event);
                if let Some(r) = self.model_runs.get_mut(&p.id) {
                    r.status = ModelRunStatus::Failed;
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
                        task_id: p.task_id,
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
                        task_id: p.task_id,
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
                        notes: String::new(),
                        created_at: ts,
                    },
                );
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Review;
                    t.updated_at = ts;
                }
            }
            EventType::ReviewApproved => {
                let p: ReviewApprovedPayload = decode(event);
                if let Some(r) = self.reviews.get_mut(&p.review_id) {
                    r.status = ReviewStatus::Approved;
                }
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Approved;
                    t.updated_at = ts;
                }
            }
            EventType::ReviewRejected => {
                let p: ReviewRejectedPayload = decode(event);
                if let Some(r) = self.reviews.get_mut(&p.review_id) {
                    r.status = ReviewStatus::NeedsChanges;
                    r.notes = p.reason;
                }
                let agent_id = self.tasks.get(&p.task_id).and_then(|t| t.assigned_agent_id.clone());
                if let Some(t) = self.tasks.get_mut(&p.task_id) {
                    t.status = TaskStatus::Ready;
                    t.assigned_agent_id = None;
                    t.attempt += 1;
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
                    t.updated_at = ts;
                }
                if let Some(a) = self.agent_instances.get_mut(&p.agent_id) {
                    a.status = AgentStatus::Idle;
                    a.current_task_id = None;
                    a.updated_at = ts;
                }
            }
            EventType::WorkspaceCreated
            | EventType::WorkspaceDestroyed
            | EventType::PRMerged
            | EventType::WorktreeCreated
            | EventType::TmuxSessionCreated
            | EventType::TmuxSessionDestroyed
            | EventType::RequirementUpdated
            | EventType::PMPlanningStarted
            | EventType::PMPlanningCompleted
            | EventType::PMReviewStarted
            | EventType::PMReviewCompleted
            | EventType::RuntimeStarted
            | EventType::RuntimeStopped
            | EventType::AgentHeartbeat => {
                // Recorded in the event log for audit/timeline purposes only;
                // no derived-state fields track these yet.
            }
        }
    }

    fn maybe_complete_goal(&mut self, goal_id: &str, ts: chrono::DateTime<chrono::Utc>) {
        let mut has_tasks = false;
        let mut all_done = true;
        for t in self.tasks.values().filter(|t| t.goal_id.as_deref() == Some(goal_id)) {
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

    /// Whether every epic-derived task and every decision under an initiative
    /// has reached a terminal state. `Runtime` calls this (never the reducer
    /// itself) so `InitiativeCompleted` stays an explicitly emitted event
    /// rather than a silent derived-state side effect.
    pub fn initiative_is_complete(&self, initiative_id: &str) -> bool {
        let epic_ids: std::collections::HashSet<&str> = self
            .epics
            .values()
            .filter(|e| e.initiative_id == initiative_id)
            .map(|e| e.id.as_str())
            .collect();

        let mut has_work = false;
        for t in self
            .tasks
            .values()
            .filter(|t| t.epic_id.as_deref().is_some_and(|eid| epic_ids.contains(eid)))
        {
            has_work = true;
            if t.status != TaskStatus::Done {
                return false;
            }
        }

        let decisions: Vec<&Decision> = self.decisions.values().filter(|d| d.initiative_id == initiative_id).collect();
        if !decisions.is_empty() {
            has_work = true;
            if decisions.iter().any(|d| d.status != DecisionStatus::Resolved) {
                return false;
            }
        }

        has_work
    }
}
