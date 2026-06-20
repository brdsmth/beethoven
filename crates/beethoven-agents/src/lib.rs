mod model;
mod prompt;
mod registry;
mod review;
mod scheduler;

pub use model::{AgentsError, ClaudeCodeRuntime, ExecutionRequest, ExecutionResult, ExecutionStatus, ModelRuntime};
pub use prompt::{PromptBuilder, WorkerPromptContext};
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
}

#[derive(Debug, Clone)]
pub struct DecisionDraft {
    pub question: String,
    pub context: String,
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
                });
            } else {
                outcome.task_drafts.push(TaskDraft {
                    title: item.to_string(),
                    description: String::new(),
                });
            }
        }

        if outcome.task_drafts.is_empty() && outcome.decision_drafts.is_empty() {
            outcome.task_drafts.push(TaskDraft {
                title: format!("Implement: {title}"),
                description: String::new(),
            });
        }

        outcome
    }
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
    }

    #[test]
    fn plan_falls_back_to_a_single_task_without_bullets() {
        let outcome = PlanningService::plan("Goal Title", "just prose, no bullets");
        assert_eq!(outcome.task_drafts.len(), 1);
        assert_eq!(outcome.task_drafts[0].title, "Implement: Goal Title");
        assert!(outcome.decision_drafts.is_empty());
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
