use beethoven_agents::AgentRole;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use std::path::PathBuf;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ProjectStatus {
    Active,
    Paused,
    Archived,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Project {
    pub id: String,
    pub name: String,
    pub slug: String,
    pub repository_path: PathBuf,
    pub default_branch: String,
    pub status: ProjectStatus,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Global, metadata-only definition of an agent role. Never executes
/// directly -- projects instantiate `AgentInstance`s from these.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentTemplate {
    pub id: String,
    pub role: AgentRole,
    pub label: String,
    pub prompt: String,
    pub permissions: Vec<String>,
    pub tools: Vec<String>,
    pub runtime_profile_id: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum TaskStatus {
    Draft,
    Ready,
    Assigned,
    InProgress,
    Review,
    Approved,
    Done,
    Blocked,
    Cancelled,
}

/// The single planning primitive: every objective, work-stream breakdown,
/// and unit of execution is a `Task`, related only via `parent_task_id`. A
/// task with children is a container (never delegated/executed directly,
/// auto-completes once every child is `Done` and every `Decision` raised
/// against it is `Resolved` -- see `Runtime::maybe_complete_parent_task`); a
/// task with none is a leaf, the only kind delegation ever assigns to an
/// agent. `Draft` is for tasks proposed outside the normal planning flow
/// (the old manual "change request") pending human approval into `Ready`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Task {
    pub id: String,
    pub project_id: String,
    pub parent_task_id: Option<String>,
    pub title: String,
    pub description: String,
    pub status: TaskStatus,
    pub assigned_agent_id: Option<String>,
    pub branch: Option<String>,
    pub pr_url: Option<String>,
    pub attempt: u32,
    pub blocked_reason: Option<String>,
    /// Individual, independently-checkable outcomes (Phase 2.5B), not a
    /// single blob of text -- so a future review phase can mark each one
    /// done/not-done on its own rather than needing a second migration.
    pub success_criteria: Vec<String>,
    pub target_score: f32,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

/// Splits raw human-entered text (the CLI/Tauri `create_task` surface,
/// which still takes a single string for ergonomics) into individual
/// criteria: one per non-empty line, with an optional leading `- `/`* `
/// bullet marker stripped. `""` yields an empty list, not `[""]`.
pub fn parse_success_criteria_text(text: &str) -> Vec<String> {
    text.lines()
        .map(str::trim)
        .map(|line| line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")).unwrap_or(line))
        .map(str::trim)
        .filter(|line| !line.is_empty())
        .map(str::to_string)
        .collect()
}

/// Accepts either a JSON array of strings (the current shape) or a single
/// JSON string (every `success_criteria` ever persisted before Phase
/// 2.5B introduced the list) so a pre-existing `TaskCreated`/
/// `PlanningSummaryGenerated` event still replays instead of failing to
/// deserialize. A legacy string is split the same way fresh human input is.
pub fn deserialize_success_criteria<'de, D>(deserializer: D) -> Result<Vec<String>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum Repr {
        List(Vec<String>),
        Legacy(String),
    }
    Ok(match Repr::deserialize(deserializer)? {
        Repr::List(items) => items,
        Repr::Legacy(text) => parse_success_criteria_text(&text),
    })
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentStatus {
    Created,
    Idle,
    Assigned,
    Executing,
    Completed,
    Paused,
    Archived,
}

/// A project-scoped, running agent. Owns memory/task-queue/workspace
/// isolation. The Lead is created directly when its project is created;
/// Worker/Reviewer agents are spawned dynamically by the Lead (or manually),
/// with `parent_agent_id` set to the Lead's id (`None` only for the Lead
/// itself).
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct AgentInstance {
    pub id: String,
    pub project_id: String,
    pub template_id: String,
    pub role: AgentRole,
    pub role_description: String,
    pub parent_agent_id: Option<String>,
    pub name: String,
    pub status: AgentStatus,
    pub current_task_id: Option<String>,
    pub created_at: DateTime<Utc>,
    pub updated_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InboxKind {
    Decision,
    Approval,
    Question,
    Alert,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum InboxStatus {
    Open,
    Resolved,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct InboxItem {
    pub id: String,
    pub project_id: String,
    pub kind: InboxKind,
    pub task_id: Option<String>,
    pub agent_id: Option<String>,
    pub decision_id: Option<String>,
    pub prompt: String,
    pub status: InboxStatus,
    pub response: Option<String>,
    pub created_at: DateTime<Utc>,
    pub resolved_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum RunStatus {
    Running,
    Completed,
    Failed,
}

/// One execution attempt of a `Task` -- a single invocation of a
/// `ModelRuntime`. `attempt` mirrors `Task.attempt` at the moment the run
/// started, so historical runs stay attributable to a specific iteration
/// even as the task keeps retrying.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Run {
    pub id: String,
    pub project_id: String,
    pub agent_id: String,
    pub task_id: String,
    pub attempt: u32,
    pub provider: String,
    pub status: RunStatus,
    pub summary: Option<String>,
    pub started_at: DateTime<Utc>,
    pub completed_at: Option<DateTime<Utc>>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ArtifactKind {
    Prompt,
    Response,
    Diff,
    FilesModified,
    /// JSON-encoded `PlanningSummary` -- the one artifact kind whose
    /// `content` isn't plain text, since it serves double duty as both a
    /// human-readable summary and the machine-traceable record of why
    /// every task/decision a planning run produced was produced.
    PlanningSummary,
}

/// Everything a `Run` produces -- the prompt sent, the raw response
/// received, the git diff/files-changed output captured once execution
/// finishes (the workspace itself is destroyed at the end of every
/// attempt, so this is the only durable record of what happened), or the
/// `PlanningSummary` a planning run (see `crate::planning_summary`) produced.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Artifact {
    pub id: String,
    pub run_id: String,
    pub kind: ArtifactKind,
    pub content: String,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReviewStatus {
    Pending,
    Approved,
    NeedsChanges,
}

/// The evaluation of one `Run`'s output against its task's
/// `success_criteria`/`target_score`. `score >= target_score` is what
/// drives the retry-vs-complete decision (see `Runtime::finish_task`) --
/// this *is* Beethoven's loop, with no separate Loop entity required.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Review {
    pub id: String,
    pub task_id: String,
    pub reviewer_agent_id: String,
    pub status: ReviewStatus,
    pub score: f32,
    pub summary: String,
    pub recommendations: Vec<String>,
    pub created_at: DateTime<Utc>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DecisionStatus {
    Open,
    Resolved,
}

/// Tracks an architectural or product decision raised during planning.
/// Humans remain the bottleneck: every open decision is mirrored as an
/// `InboxItem` so a human resolves it, per the Worker -> Lead -> Decision ->
/// Inbox -> Human flow. `task_id` is the task the decision blocks (a
/// container task with an unresolved decision against it cannot
/// auto-complete); `None` for decisions not tied to a specific task.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Decision {
    pub id: String,
    pub project_id: String,
    pub task_id: Option<String>,
    pub question: String,
    pub context: String,
    pub answer: Option<String>,
    pub status: DecisionStatus,
    pub created_at: DateTime<Utc>,
}

/// One edge of the task dependency graph: `task_id` may become eligible for
/// execution only once `depends_on_task_id` is `Done` (see
/// `RuntimeState::is_executable`). Deliberately carries no status of its
/// own -- whether it's currently "satisfied" is never stored, only
/// recomputed from the two tasks it names, so it can never drift out of
/// sync with them.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct TaskDependency {
    pub task_id: String,
    pub depends_on_task_id: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parse_success_criteria_text_splits_lines_and_strips_bullet_markers() {
        let parsed = parse_success_criteria_text("- Google OAuth works\n* GitHub OAuth works\nPlain line\n\n");
        assert_eq!(parsed, vec!["Google OAuth works", "GitHub OAuth works", "Plain line"]);
    }

    #[test]
    fn parse_success_criteria_text_returns_empty_for_blank_input() {
        assert!(parse_success_criteria_text("").is_empty());
        assert!(parse_success_criteria_text("   \n  \n").is_empty());
    }

    #[derive(Debug, Deserialize)]
    struct Wrapper {
        #[serde(deserialize_with = "deserialize_success_criteria")]
        success_criteria: Vec<String>,
    }

    #[test]
    fn deserialize_success_criteria_accepts_a_real_list() {
        let w: Wrapper = serde_json::from_str(r#"{"success_criteria": ["a", "b"]}"#).unwrap();
        assert_eq!(w.success_criteria, vec!["a", "b"]);
    }

    #[test]
    fn deserialize_success_criteria_splits_a_legacy_string_for_backward_compatible_replay() {
        let w: Wrapper = serde_json::from_str(r#"{"success_criteria": "- a\n- b"}"#).unwrap();
        assert_eq!(w.success_criteria, vec!["a", "b"]);
    }

    #[test]
    fn deserialize_success_criteria_treats_a_legacy_empty_string_as_an_empty_list() {
        let w: Wrapper = serde_json::from_str(r#"{"success_criteria": ""}"#).unwrap();
        assert!(w.success_criteria.is_empty());
    }
}
