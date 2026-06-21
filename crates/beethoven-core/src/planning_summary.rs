use crate::models::deserialize_success_criteria;
use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// Why one generated task exists, and the quality bar applied to it.
/// `target_score`/`success_criteria` are recorded here (not just on
/// `Task`) so tracing either back to "why" is always "find the planning
/// artifact that mentions this task," never a column on `Task` itself.
/// Both are genuinely model-generated as of Phase 2.5B (success criteria)
/// and 2.5C (target score), so their reasons are real, per-task reasoning
/// rather than a constant.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannedTaskExplanation {
    pub task_id: String,
    pub title: String,
    pub reason: String,
    pub target_score: f32,
    pub target_score_reason: String,
    /// `#[serde(deserialize_with = ...)]`: a pre-existing artifact recorded
    /// this as a single (always-empty) string before Phase 2.5B turned it
    /// into a real list -- accept either shape so it still replays.
    #[serde(default, deserialize_with = "deserialize_success_criteria")]
    pub success_criteria: Vec<String>,
    #[serde(default)]
    pub success_criteria_reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannedDecisionExplanation {
    pub decision_id: String,
    pub question: String,
    pub reason: String,
}

/// Why one dependency edge was generated. Always empty today -- the
/// current planner never infers dependencies between the tasks it
/// generates (that's Phase 2.5's job) -- but the shape exists now so
/// nothing changes when it does.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannedDependencyExplanation {
    pub task_id: String,
    pub depends_on_task_id: String,
    pub reason: String,
}

/// One quality band's count, for the Phase 2.5C "Score Distribution"
/// metric -- a post-hoc bucketing of scores the planner already generated,
/// purely for display (it never feeds back into generation). Always all
/// five bands, even ones with a zero count, so a histogram has consistent
/// bars across planning runs.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ScoreBandCount {
    pub band: String,
    pub count: usize,
}

/// Observability-only numbers about one planning run. Per the spec:
/// these never influence runtime/scheduling behavior, only what's shown.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlanQualityMetrics {
    pub task_count: usize,
    /// Average combined title+description length (characters) across this
    /// pass's generated tasks -- a crude granularity signal: tasks with an
    /// empty description (the legacy stub planner's output, and a model
    /// response that under-specified) pull this down. `#[serde(default)]`:
    /// added in Phase 2.5A, after `PlanningSummaryGenerated` events already
    /// existed in the event log -- a pre-existing artifact must still
    /// replay, so an old payload without this field defaults rather than
    /// failing to parse.
    #[serde(default)]
    pub average_task_length: f64,
    /// Of the tasks this pass generated, how many have no children of
    /// their own. Phase 2.5A only ever generates one level of decomposition
    /// (no recursive breakdown), so this always equals `task_count` today --
    /// computed honestly rather than hardcoded so it stays correct the
    /// moment a future phase changes that. `#[serde(default)]`: see
    /// `average_task_length` above.
    #[serde(default)]
    pub leaf_task_count: usize,
    pub dependency_count: usize,
    pub average_dependency_depth: f64,
    /// Decisions escalated by this run -- the planning-time equivalent of
    /// "blocked": work that needs a human call before it can proceed.
    pub blocked_task_count: usize,
    pub planning_duration_ms: i64,
    /// Phase 2.5B coverage metrics, observability-only (never gate runtime
    /// behavior): how many of this pass's tasks got at least one criterion,
    /// how many criteria per task on average, and coverage as a percentage.
    /// `#[serde(default)]`: see `average_task_length` above.
    #[serde(default)]
    pub tasks_with_criteria: usize,
    #[serde(default)]
    pub average_criteria_per_task: f64,
    #[serde(default)]
    pub criteria_coverage_percent: f64,
    /// Phase 2.5C quality metrics, observability-only. Zero/empty when no
    /// tasks were generated this pass (the failure-escalation path).
    /// `#[serde(default)]`: see `average_task_length` above.
    #[serde(default)]
    pub average_target_score: f32,
    #[serde(default)]
    pub highest_target_score: f32,
    #[serde(default)]
    pub lowest_target_score: f32,
    #[serde(default)]
    pub score_distribution: Vec<ScoreBandCount>,
}

/// The structured content of a `PlanningSummary` artifact (`ArtifactKind::
/// PlanningSummary`) -- the complete audit trail for one planning run.
/// `Task`/`Decision`/`TaskDependency` never carry a "why" field of their
/// own: tracing one back to its reason is always "find the planning
/// artifact that mentions this id" (see `Runtime::plan_task`), not a
/// column on the thing itself. JSON-encoded into `Artifact.content`.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlanningSummary {
    pub planned_task_id: String,
    pub planned_task_title: String,
    pub tasks: Vec<PlannedTaskExplanation>,
    pub decisions: Vec<PlannedDecisionExplanation>,
    pub dependencies: Vec<PlannedDependencyExplanation>,
    pub metrics: PlanQualityMetrics,
    /// The model's own stated reasoning for this breakdown as a whole,
    /// distinct from each task's individual `reason` above -- Phase 2.5A's
    /// "Task Generation Reasoning," a debugging tool for judging *why* a
    /// shape was chosen, not just what it produced. Empty for the legacy
    /// stub planner (nothing to report) or a planning run that failed
    /// before a model response was parsed.
    ///
    /// The planner's prompt and raw response are deliberately *not*
    /// duplicated here: emitting real `PromptGenerated`/`RunCompleted`
    /// events for the planning Run already records them as ordinary
    /// `ArtifactKind::Prompt`/`Response` artifacts, visible through the
    /// same Task Detail views a worker Run's prompt/response already use.
    /// `#[serde(default)]`: see `PlanQualityMetrics::average_task_length`.
    #[serde(default)]
    pub task_generation_reasoning: String,
    /// The doc's "Planning Summary": goal, generated counts, execution
    /// order, risk areas -- rendered as plain text so it displays for free
    /// anywhere an `Artifact`'s `content` is already shown as text.
    pub summary: String,
    pub generated_at: DateTime<Utc>,
}
