use crate::state::RuntimeState;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// One specific, concrete action a Project Chat answer proposed, already
/// validated against real state (see `validate_suggested_actions`) -- one
/// variant per existing single-entity mutating command relevant to
/// "unblocking work". Advisory only: nothing here is ever executed by
/// Beethoven itself, it's only ever rendered as a one-click button the
/// human still has to confirm through the normal action flow.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum SuggestedAction {
    RetryTask { task_id: String },
    CancelTask { task_id: String },
    ReassignTask { task_id: String, new_agent_id: String },
    AnswerInbox { inbox_id: String, response: String },
    ResolveDecision { decision_id: String, response: String },
    ApproveReview { task_id: String },
    RequestReviewChanges { task_id: String, reason: String },
}

impl From<beethoven_agents::SuggestedActionDraft> for SuggestedAction {
    fn from(draft: beethoven_agents::SuggestedActionDraft) -> Self {
        use beethoven_agents::SuggestedActionDraft as Draft;
        match draft {
            Draft::RetryTask { task_id } => SuggestedAction::RetryTask { task_id },
            Draft::CancelTask { task_id } => SuggestedAction::CancelTask { task_id },
            Draft::ReassignTask { task_id, new_agent_id } => SuggestedAction::ReassignTask { task_id, new_agent_id },
            Draft::AnswerInbox { inbox_id, response } => SuggestedAction::AnswerInbox { inbox_id, response },
            Draft::ResolveDecision { decision_id, response } => {
                SuggestedAction::ResolveDecision { decision_id, response }
            }
            Draft::ApproveReview { task_id } => SuggestedAction::ApproveReview { task_id },
            Draft::RequestReviewChanges { task_id, reason } => SuggestedAction::RequestReviewChanges { task_id, reason },
        }
    }
}

/// Never trust a model-produced id blindly: drops any action whose
/// referenced task/agent/inbox-item/decision no longer exists in `state`.
/// Called twice by `ask_project_chat` -- once against the pre-call
/// snapshot right after parsing, and again against fresh state inside
/// `Runtime::record_project_chat_turn`, since a chat call can take long
/// enough for the snapshot to go stale.
pub fn validate_suggested_actions(state: &RuntimeState, actions: Vec<SuggestedAction>) -> Vec<SuggestedAction> {
    actions
        .into_iter()
        .filter(|action| match action {
            SuggestedAction::RetryTask { task_id }
            | SuggestedAction::CancelTask { task_id }
            | SuggestedAction::ApproveReview { task_id }
            | SuggestedAction::RequestReviewChanges { task_id, .. } => state.tasks.contains_key(task_id),
            SuggestedAction::ReassignTask { task_id, new_agent_id } => {
                state.tasks.contains_key(task_id) && state.agent_instances.contains_key(new_agent_id)
            }
            SuggestedAction::AnswerInbox { inbox_id, .. } => state.inbox.contains_key(inbox_id),
            SuggestedAction::ResolveDecision { decision_id, .. } => state.decisions.contains_key(decision_id),
        })
        .collect()
}

/// One question-and-answer turn of Project Chat -- advisory, project-
/// scoped, with no persistent model session behind it: each turn is its
/// own fresh one-shot `ModelRuntime::ask` call, grounded in a freshly
/// assembled context bundle plus the prior turns below for continuity.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ProjectChatTurn {
    pub id: String,
    pub project_id: String,
    pub question: String,
    pub answer: String,
    /// The task this turn was scoped to, if any -- narrows the context
    /// bundle to that task and its siblings (the direct fix for "three
    /// Claude sessions in one graph that don't know about each other").
    /// `None` means the question was asked about the whole project.
    pub focus_task_id: Option<String>,
    pub suggested_actions: Vec<SuggestedAction>,
    pub created_at: DateTime<Utc>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::models::{AgentInstance, AgentStatus, Decision, DecisionStatus, InboxItem, InboxKind, InboxStatus, Task, TaskStatus};
    use beethoven_agents::AgentRole;

    fn state_with_one_of_each() -> RuntimeState {
        let mut state = RuntimeState::default();
        let now = Utc::now();
        state.tasks.insert(
            "t1".to_string(),
            Task {
                id: "t1".to_string(),
                project_id: "p1".to_string(),
                parent_task_id: None,
                title: "Task".to_string(),
                description: String::new(),
                status: TaskStatus::Blocked,
                assigned_agent_id: None,
                branch: None,
                pr_url: None,
                attempt: 1,
                blocked_reason: None,
                success_criteria: Vec::new(),
                target_score: 70.0,
                created_at: now,
                updated_at: now,
            },
        );
        state.agent_instances.insert(
            "a1".to_string(),
            AgentInstance {
                id: "a1".to_string(),
                project_id: "p1".to_string(),
                template_id: "tmpl1".to_string(),
                role: AgentRole::Worker,
                role_description: String::new(),
                parent_agent_id: None,
                name: "Worker".to_string(),
                status: AgentStatus::Idle,
                current_task_id: None,
                created_at: now,
                updated_at: now,
            },
        );
        state.inbox.insert(
            "i1".to_string(),
            InboxItem {
                id: "i1".to_string(),
                project_id: "p1".to_string(),
                kind: InboxKind::Question,
                task_id: None,
                agent_id: None,
                decision_id: None,
                prompt: String::new(),
                status: InboxStatus::Open,
                response: None,
                created_at: now,
                resolved_at: None,
            },
        );
        state.decisions.insert(
            "d1".to_string(),
            Decision {
                id: "d1".to_string(),
                project_id: "p1".to_string(),
                task_id: None,
                question: String::new(),
                context: String::new(),
                answer: None,
                status: DecisionStatus::Open,
                created_at: now,
            },
        );
        state
    }

    #[test]
    fn validate_suggested_actions_keeps_actions_referencing_real_ids() {
        let state = state_with_one_of_each();
        let actions = vec![
            SuggestedAction::RetryTask { task_id: "t1".to_string() },
            SuggestedAction::CancelTask { task_id: "t1".to_string() },
            SuggestedAction::ReassignTask { task_id: "t1".to_string(), new_agent_id: "a1".to_string() },
            SuggestedAction::AnswerInbox { inbox_id: "i1".to_string(), response: "ok".to_string() },
            SuggestedAction::ResolveDecision { decision_id: "d1".to_string(), response: "ok".to_string() },
            SuggestedAction::ApproveReview { task_id: "t1".to_string() },
            SuggestedAction::RequestReviewChanges { task_id: "t1".to_string(), reason: "why".to_string() },
        ];
        let kept = validate_suggested_actions(&state, actions.clone());
        assert_eq!(kept.len(), actions.len(), "every action references a real id and must survive validation");
    }

    #[test]
    fn validate_suggested_actions_drops_actions_referencing_an_id_that_does_not_exist() {
        let state = state_with_one_of_each();
        let actions = vec![
            SuggestedAction::RetryTask { task_id: "ghost".to_string() },
            SuggestedAction::ReassignTask { task_id: "t1".to_string(), new_agent_id: "ghost".to_string() },
            SuggestedAction::AnswerInbox { inbox_id: "ghost".to_string(), response: "ok".to_string() },
            SuggestedAction::ResolveDecision { decision_id: "ghost".to_string(), response: "ok".to_string() },
        ];
        let kept = validate_suggested_actions(&state, actions);
        assert!(kept.is_empty(), "never trust a model-produced id blindly");
    }

    #[test]
    fn validate_suggested_actions_is_a_pure_per_item_filter_not_all_or_nothing() {
        let state = state_with_one_of_each();
        let actions = vec![
            SuggestedAction::RetryTask { task_id: "t1".to_string() },
            SuggestedAction::RetryTask { task_id: "ghost".to_string() },
        ];
        let kept = validate_suggested_actions(&state, actions);
        assert_eq!(kept.len(), 1);
        assert!(matches!(&kept[0], SuggestedAction::RetryTask { task_id } if task_id == "t1"));
    }
}
