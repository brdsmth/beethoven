use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};

/// Why one generated task exists, and the quality bar applied to it.
/// `target_score`/`success_criteria` are recorded here (not just on
/// `Task`) precisely because today's planner never customizes them --
/// every generated task gets the same default, so the honest "reason" is
/// "no override was requested," not a per-task judgment call. When a
/// smarter planner (Phase 2.5+) starts customizing these, the same fields
/// carry real reasoning with no shape change.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlannedTaskExplanation {
    pub task_id: String,
    pub title: String,
    pub reason: String,
    pub target_score: f32,
    pub target_score_reason: String,
    pub success_criteria: String,
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

/// Observability-only numbers about one planning run. Per the spec:
/// these never influence runtime/scheduling behavior, only what's shown.
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PlanQualityMetrics {
    pub task_count: usize,
    pub dependency_count: usize,
    pub average_dependency_depth: f64,
    /// Decisions escalated by this run -- the planning-time equivalent of
    /// "blocked": work that needs a human call before it can proceed.
    pub blocked_task_count: usize,
    pub planning_duration_ms: i64,
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
    /// The doc's "Planning Summary": goal, generated counts, execution
    /// order, risk areas -- rendered as plain text so it displays for free
    /// anywhere an `Artifact`'s `content` is already shown as text.
    pub summary: String,
    pub generated_at: DateTime<Utc>,
}
