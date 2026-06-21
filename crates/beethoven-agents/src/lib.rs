mod model;
mod prompt;
mod registry;
mod review;
mod scheduler;

pub use model::{AgentsError, ClaudeCodeRuntime, ExecutionRequest, ExecutionResult, ExecutionStatus, ModelRuntime};
pub use prompt::{PlannerPromptContext, PromptBuilder, WorkerPromptContext};
pub use registry::{default_registry, RuntimeProfile, RuntimeRegistry};
pub use review::{ReviewOutcome, ReviewResult, ReviewService};
pub use scheduler::{FifoScheduler, QueuedTask, Scheduler, TaskQueue};

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentRole {
    Lead,
    Worker,
    Reviewer,
}

impl AgentRole {
    /// The full set of default agent templates Beethoven seeds once, globally.
    pub const ALL: [AgentRole; 3] = [AgentRole::Lead, AgentRole::Worker, AgentRole::Reviewer];

    pub fn as_str(&self) -> &'static str {
        match self {
            AgentRole::Lead => "lead",
            AgentRole::Worker => "worker",
            AgentRole::Reviewer => "reviewer",
        }
    }

    pub fn display_name(&self) -> &'static str {
        match self {
            AgentRole::Lead => "Lead",
            AgentRole::Worker => "Worker",
            AgentRole::Reviewer => "Reviewer",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskDraft {
    pub title: String,
    pub description: String,
    /// Why this task was generated -- the planner's half of Phase 2.4.5's
    /// observability requirement: every generated task must be explainable,
    /// not just creatable. Mechanical and honest for today's stub planner
    /// (it only ever extracts bullet lines or falls back to one task), not
    /// aspirational -- a smarter Phase 2.5+ planner fills in real reasoning
    /// through the exact same field.
    pub reason: String,
    /// Individual, independently-checkable outcomes (Phase 2.5B) -- a list
    /// from the start, not a blob of text, since a future review phase will
    /// want to mark each one done/not-done on its own. Empty when the
    /// planner didn't generate any (the legacy stub planner never does;
    /// the model-driven planner raises a `Decision` instead of guessing
    /// when it can't confidently determine them).
    #[serde(default)]
    pub success_criteria: Vec<String>,
    #[serde(default)]
    pub success_criteria_reason: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct DecisionDraft {
    pub question: String,
    pub context: String,
    pub reason: String,
}

#[derive(Debug, Clone, Default)]
pub struct PlanOutcome {
    pub task_drafts: Vec<TaskDraft>,
    pub decision_drafts: Vec<DecisionDraft>,
}

pub struct PlanningService;

impl PlanningService {
    /// Stub planner, used to break down any task in the tree (root objective
    /// or container) into children: turns bullet-list lines ("- " / "* "
    /// prefixed) in the description into child work -- a line containing
    /// `?` poses an open question that needs a human call rather than a work
    /// breakdown, so it becomes a `Decision` (auto-escalated to the inbox);
    /// every other line becomes a child task. Falls back to a single task
    /// named after the title when no bullet list is present.
    pub fn plan(title: &str, description: &str) -> PlanOutcome {
        let mut outcome = PlanOutcome::default();
        for item in description.lines().map(str::trim).filter_map(|line| {
            line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")).map(str::trim).filter(|t| !t.is_empty())
        }) {
            if item.contains('?') {
                outcome.decision_drafts.push(DecisionDraft {
                    question: item.to_string(),
                    context: item.to_string(),
                    reason: format!(
                        "This bullet line under \"{title}\" reads as a question (contains '?'), so it was escalated for a human decision instead of decomposed into a task."
                    ),
                });
            } else {
                outcome.task_drafts.push(TaskDraft {
                    title: item.to_string(),
                    description: String::new(),
                    reason: format!("Listed as a bullet item in \"{title}\"'s planning description."),
                    success_criteria: Vec::new(),
                    success_criteria_reason: "The legacy bullet-line planner does not generate success criteria."
                        .to_string(),
                });
            }
        }

        if outcome.task_drafts.is_empty() && outcome.decision_drafts.is_empty() {
            outcome.task_drafts.push(TaskDraft {
                title: format!("Implement: {title}"),
                description: String::new(),
                reason: format!(
                    "\"{title}\" had no bullet-list breakdown in its description, so a single fallback task was generated to represent the whole objective."
                ),
                success_criteria: Vec::new(),
                success_criteria_reason: "The legacy bullet-line planner does not generate success criteria."
                    .to_string(),
            });
        }

        outcome
    }

    /// Phase 2.5A: turns a model's response to a `PromptBuilder::planner`
    /// prompt into a `PlanOutcome` plus its overall reasoning. Prefers
    /// `structured_output` (populated when the model honors
    /// `ExecutionRequest::json_schema`); falls back to scanning
    /// `raw_response` for a top-level `{...}` block for models that ignore
    /// or only partially honor the schema. Distinct from `plan` above --
    /// that deterministic stub stays in place for tests and as the
    /// benchmark suite's fast, free baseline; this is what
    /// `Runtime::plan_task` actually calls.
    pub fn parse_model_response(
        structured: Option<&serde_json::Value>,
        raw_response: &str,
    ) -> Result<(PlanOutcome, String), AgentsError> {
        let value = match structured {
            Some(v) => v.clone(),
            None => extract_json_object(raw_response).ok_or(AgentsError::NoPlannerOutput)?,
        };
        let parsed: ModelPlanResponse = serde_json::from_value(value)?;
        if parsed.tasks.is_empty() && parsed.decisions.is_empty() {
            return Err(AgentsError::NoPlannerOutput);
        }
        Ok((
            PlanOutcome {
                task_drafts: parsed.tasks,
                decision_drafts: parsed.decisions,
            },
            parsed.reasoning,
        ))
    }
}

/// The shape `PlanningService::parse_model_response` expects, and the JSON
/// Schema (`PLANNER_RESPONSE_SCHEMA`) asked of the model via
/// `ExecutionRequest::json_schema` to produce it.
#[derive(Debug, Deserialize)]
struct ModelPlanResponse {
    #[serde(default)]
    reasoning: String,
    #[serde(default)]
    tasks: Vec<TaskDraft>,
    #[serde(default)]
    decisions: Vec<DecisionDraft>,
}

/// Passed as `ExecutionRequest::json_schema` for every planning call.
pub const PLANNER_RESPONSE_SCHEMA: &str = r#"{
  "type": "object",
  "properties": {
    "reasoning": {
      "type": "string",
      "description": "Overall reasoning behind this task breakdown."
    },
    "tasks": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "title": { "type": "string" },
          "description": { "type": "string" },
          "reason": { "type": "string", "description": "Why this specific task exists." },
          "success_criteria": {
            "type": "array",
            "items": { "type": "string" },
            "description": "Observable, verifiable, outcome-based criteria for this task -- not vague ones like 'write good code'. Leave empty (and raise a decision instead) rather than guessing if you cannot confidently determine them."
          },
          "success_criteria_reason": {
            "type": "string",
            "description": "Why these specific success criteria were chosen for this task."
          }
        },
        "required": ["title", "description", "reason", "success_criteria", "success_criteria_reason"]
      }
    },
    "decisions": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "question": { "type": "string" },
          "context": { "type": "string" },
          "reason": { "type": "string", "description": "Why this needs a human decision instead of a generated task." }
        },
        "required": ["question", "context", "reason"]
      }
    }
  },
  "required": ["reasoning", "tasks", "decisions"]
}"#;

/// Best-effort fallback for a model that didn't honor `json_schema` and
/// embedded its JSON inside prose instead of returning it as
/// `structured_output` directly: grabs the first top-level `{...}` block.
fn extract_json_object(text: &str) -> Option<serde_json::Value> {
    let start = text.find('{')?;
    let end = text.rfind('}')?;
    if end < start {
        return None;
    }
    serde_json::from_str(&text[start..=end]).ok()
}

#[derive(Debug, Clone)]
pub struct TaskExecutionContext {
    pub task_id: String,
    pub title: String,
    pub attempt: u32,
}

#[derive(Debug, Clone)]
pub enum ExecutionOutcome {
    Completed,
    RaisedQuestion(String),
    SpawnedSubtask(TaskDraft),
}

pub struct ExecutionService;

impl ExecutionService {
    /// Stub executor: deterministic heuristics stand in for real agent work
    /// until a model runtime is wired up.
    pub fn execute(context: &TaskExecutionContext) -> ExecutionOutcome {
        if context.attempt == 1 && context.title.contains('?') {
            return ExecutionOutcome::RaisedQuestion(format!(
                "Need clarification on: {}",
                context.title
            ));
        }

        if context.attempt == 1 && context.title.to_lowercase().starts_with("implement") {
            return ExecutionOutcome::SpawnedSubtask(TaskDraft {
                title: format!("Write tests for {}", context.title),
                description: String::new(),
                reason: format!(
                    "\"{}\" reads as an implementation task with no test coverage requested yet, so a test-writing prerequisite was spawned before it.",
                    context.title
                ),
                success_criteria: Vec::new(),
                success_criteria_reason: "This execution-time heuristic spawns a prerequisite task, not a \
                    planning pass -- it does not generate success criteria."
                    .to_string(),
            });
        }

        ExecutionOutcome::Completed
    }
}

/// A ready task as seen by `DelegationService` -- deliberately narrower than
/// `beethoven_core::Task` so this crate never has to depend back on
/// `beethoven-core`.
#[derive(Debug, Clone)]
pub struct DelegationTask {
    pub id: String,
    pub title: String,
}

/// Whether a non-Lead agent can be handed a new task (`Idle`/`Completed`) or
/// has no remaining work to be reused for (`Completed`, when left over after
/// every ready task has been delegated). Agents that are `Busy` (already
/// `Assigned`/`Executing`) or `Created`/`Paused` are simply not considered.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum DelegationAgentStatus {
    Idle,
    Completed,
    Busy,
}

#[derive(Debug, Clone)]
pub struct DelegationAgent {
    pub id: String,
    pub status: DelegationAgentStatus,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum DelegationDecision {
    DelegateToExisting { agent_id: String, task_id: String },
    SpawnWorker { task_id: String, role_description: String, name: String },
    ArchiveAgent { agent_id: String },
}

pub struct DelegationService;

impl DelegationService {
    /// Stub delegator: reuse an idle or previously-completed agent if one is
    /// available for a ready task, otherwise spawn a new Worker
    /// named/described from the task itself; archive any `Completed` agent
    /// left over with no ready task to reuse it for. Deterministic until a
    /// model runtime decides this instead.
    pub fn delegate(ready_tasks: &[DelegationTask], agents: &[DelegationAgent]) -> Vec<DelegationDecision> {
        let mut available: Vec<&DelegationAgent> = agents
            .iter()
            .filter(|a| matches!(a.status, DelegationAgentStatus::Idle | DelegationAgentStatus::Completed))
            .collect();

        let mut decisions = Vec::new();
        for task in ready_tasks {
            if let Some(agent) = available.pop() {
                decisions.push(DelegationDecision::DelegateToExisting {
                    agent_id: agent.id.clone(),
                    task_id: task.id.clone(),
                });
            } else {
                decisions.push(DelegationDecision::SpawnWorker {
                    task_id: task.id.clone(),
                    role_description: format!("Worker agent for: {}", task.title),
                    name: format!("{} Worker", task.title),
                });
            }
        }

        for agent in available {
            if agent.status == DelegationAgentStatus::Completed {
                decisions.push(DelegationDecision::ArchiveAgent { agent_id: agent.id.clone() });
            }
        }

        decisions
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn ctx(title: &str, attempt: u32) -> TaskExecutionContext {
        TaskExecutionContext {
            task_id: "t1".to_string(),
            title: title.to_string(),
            attempt,
        }
    }

    #[test]
    fn plan_extracts_bullet_list_lines_and_ignores_prose() {
        let outcome = PlanningService::plan("Goal", "- one\n* two\nnot a bullet\n- three");
        let titles: Vec<&str> = outcome.task_drafts.iter().map(|d| d.title.as_str()).collect();
        assert_eq!(titles, vec!["one", "two", "three"]);
        assert!(outcome.decision_drafts.is_empty());
        assert!(outcome.task_drafts.iter().all(|d| d.reason.contains("Goal")));
    }

    #[test]
    fn plan_falls_back_to_a_single_task_without_bullets() {
        let outcome = PlanningService::plan("Goal Title", "just prose, no bullets");
        assert_eq!(outcome.task_drafts.len(), 1);
        assert_eq!(outcome.task_drafts[0].title, "Implement: Goal Title");
        assert!(outcome.decision_drafts.is_empty());
        assert!(outcome.task_drafts[0].reason.contains("no bullet-list breakdown"));
    }

    #[test]
    fn plan_turns_a_question_line_into_a_decision_not_a_task() {
        let outcome = PlanningService::plan(
            "Build Gmail Agent",
            "- Support PDF attachments\n- What auth provider should we use?",
        );
        assert_eq!(outcome.task_drafts.len(), 1);
        assert_eq!(outcome.task_drafts[0].title, "Support PDF attachments");
        assert_eq!(outcome.decision_drafts.len(), 1);
        assert_eq!(outcome.decision_drafts[0].question, "What auth provider should we use?");
        assert!(!outcome.task_drafts[0].reason.is_empty());
        assert!(outcome.decision_drafts[0].reason.contains("question"));
    }

    #[test]
    fn parse_model_response_prefers_structured_output_over_raw_text() {
        let structured = serde_json::json!({
            "reasoning": "Split backend, frontend, and tests apart for parallel work.",
            "tasks": [
                {
                    "title": "Implement OAuth Backend",
                    "description": "",
                    "reason": "Backend work is independent of UI.",
                    "success_criteria": ["Google OAuth login succeeds", "GitHub OAuth login succeeds"],
                    "success_criteria_reason": "These are the minimum outcomes for a working OAuth backend.",
                },
                {"title": "Implement OAuth Frontend", "description": "", "reason": "UI work is independent of backend."},
            ],
            "decisions": [],
        });
        let (outcome, reasoning) =
            PlanningService::parse_model_response(Some(&structured), "ignored raw text").unwrap();
        assert_eq!(outcome.task_drafts.len(), 2);
        assert_eq!(outcome.task_drafts[0].title, "Implement OAuth Backend");
        assert_eq!(
            outcome.task_drafts[0].success_criteria,
            vec!["Google OAuth login succeeds", "GitHub OAuth login succeeds"]
        );
        assert!(outcome.task_drafts[0].success_criteria_reason.contains("minimum outcomes"));
        // A task draft that omits success_criteria entirely (a model only
        // partially honoring the schema) defaults to empty rather than
        // failing to parse the whole response.
        assert!(outcome.task_drafts[1].success_criteria.is_empty());
        assert!(reasoning.contains("parallel work"));
    }

    #[test]
    fn parse_model_response_falls_back_to_extracting_json_from_raw_text() {
        let raw = "Sure, here is the plan:\n{\"reasoning\": \"r\", \"tasks\": [{\"title\": \"t\", \"description\": \"\", \"reason\": \"because\"}], \"decisions\": []}\nLet me know if you need changes.";
        let (outcome, reasoning) = PlanningService::parse_model_response(None, raw).unwrap();
        assert_eq!(outcome.task_drafts.len(), 1);
        assert_eq!(outcome.task_drafts[0].title, "t");
        assert_eq!(reasoning, "r");
    }

    #[test]
    fn parse_model_response_errors_on_empty_tasks_and_decisions() {
        let structured = serde_json::json!({"reasoning": "nothing to do", "tasks": [], "decisions": []});
        let err = PlanningService::parse_model_response(Some(&structured), "").unwrap_err();
        assert!(matches!(err, AgentsError::NoPlannerOutput));
    }

    #[test]
    fn parse_model_response_errors_when_no_json_is_present_anywhere() {
        let err = PlanningService::parse_model_response(None, "no json here at all").unwrap_err();
        assert!(matches!(err, AgentsError::NoPlannerOutput));
    }

    #[test]
    fn plan_falls_back_to_a_single_task_without_bullets_even_with_a_question_in_prose() {
        let outcome = PlanningService::plan("Build Gmail Agent", "what should this even do?");
        assert_eq!(outcome.task_drafts.len(), 1);
        assert_eq!(outcome.task_drafts[0].title, "Implement: Build Gmail Agent");
        assert!(outcome.decision_drafts.is_empty());
    }

    #[test]
    fn execute_raises_a_question_only_on_the_first_attempt() {
        assert!(matches!(
            ExecutionService::execute(&ctx("What now?", 1)),
            ExecutionOutcome::RaisedQuestion(_)
        ));
        // Regression: re-attempting an already-answered question must not re-escalate forever.
        assert!(matches!(
            ExecutionService::execute(&ctx("What now?", 2)),
            ExecutionOutcome::Completed
        ));
    }

    #[test]
    fn execute_spawns_a_subtask_only_for_titles_starting_with_implement_on_first_attempt() {
        assert!(matches!(
            ExecutionService::execute(&ctx("Implement login", 1)),
            ExecutionOutcome::SpawnedSubtask(_)
        ));
        // Retrying the same task after the subtask completes must finish, not spawn again.
        assert!(matches!(
            ExecutionService::execute(&ctx("Implement login", 2)),
            ExecutionOutcome::Completed
        ));
    }

    #[test]
    fn generated_subtask_titles_do_not_recursively_spawn_more_subtasks() {
        // Regression: subtask titles ("Write tests for Implement ...") used to still
        // *contain* "implement", which kept triggering further subtask spawning forever.
        assert!(matches!(
            ExecutionService::execute(&ctx("Write tests for Implement login", 1)),
            ExecutionOutcome::Completed
        ));
    }

    #[test]
    fn execute_completes_plain_titles_immediately() {
        assert!(matches!(
            ExecutionService::execute(&ctx("Write docs", 1)),
            ExecutionOutcome::Completed
        ));
    }

    #[test]
    fn agent_role_as_str_and_display_name_round_trip_for_all_variants() {
        for role in AgentRole::ALL {
            assert!(!role.as_str().is_empty());
            assert!(!role.display_name().is_empty());
        }
        assert_eq!(AgentRole::Lead.as_str(), "lead");
        assert_eq!(AgentRole::Worker.as_str(), "worker");
        assert_eq!(AgentRole::Reviewer.as_str(), "reviewer");
    }

    fn task(id: &str, title: &str) -> DelegationTask {
        DelegationTask { id: id.to_string(), title: title.to_string() }
    }

    fn agent(id: &str, status: DelegationAgentStatus) -> DelegationAgent {
        DelegationAgent { id: id.to_string(), status }
    }

    #[test]
    fn delegate_reuses_an_idle_agent_for_a_ready_task() {
        let tasks = vec![task("t1", "Write docs")];
        let agents = vec![agent("a1", DelegationAgentStatus::Idle), agent("a2", DelegationAgentStatus::Busy)];
        let decisions = DelegationService::delegate(&tasks, &agents);
        assert_eq!(decisions, vec![DelegationDecision::DelegateToExisting { agent_id: "a1".to_string(), task_id: "t1".to_string() }]);
    }

    #[test]
    fn delegate_spawns_a_new_worker_when_no_agent_is_available() {
        let tasks = vec![task("t1", "Write docs")];
        let agents = vec![agent("a1", DelegationAgentStatus::Busy)];
        let decisions = DelegationService::delegate(&tasks, &agents);
        assert_eq!(
            decisions,
            vec![DelegationDecision::SpawnWorker {
                task_id: "t1".to_string(),
                role_description: "Worker agent for: Write docs".to_string(),
                name: "Write docs Worker".to_string(),
            }]
        );
    }

    #[test]
    fn delegate_archives_a_completed_agent_left_with_no_ready_task_to_reuse_it_for() {
        let agents = vec![agent("a1", DelegationAgentStatus::Completed)];
        let decisions = DelegationService::delegate(&[], &agents);
        assert_eq!(decisions, vec![DelegationDecision::ArchiveAgent { agent_id: "a1".to_string() }]);
    }

    #[test]
    fn delegate_does_not_archive_an_idle_agent_left_with_no_ready_task() {
        let agents = vec![agent("a1", DelegationAgentStatus::Idle)];
        let decisions = DelegationService::delegate(&[], &agents);
        assert!(decisions.is_empty());
    }
}
