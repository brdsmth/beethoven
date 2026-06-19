mod model;
mod prompt;
mod registry;
mod review;
mod schema;

pub use model::{AgentsError, ClaudeCodeRuntime, ExecutionRequest, ExecutionResult, ExecutionStatus, ModelRuntime};
pub use prompt::{PmPlanningPromptContext, PromptBuilder, ReviewPromptContext, WorkerPromptContext};
pub use registry::{default_registry, RuntimeProfile, RuntimeRegistry};
pub use review::{ReviewOutcome, ReviewService};
pub use schema::{
    parse_structured_output, PmPlanResponse, ReviewDecision, ReviewResponse, PM_PLANNING_JSON_SCHEMA,
    REVIEW_JSON_SCHEMA,
};

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum AgentKind {
    ProjectManager,
    Backend,
    Frontend,
    Qa,
    Research,
    DevOps,
}

impl AgentKind {
    pub const WORKERS: [AgentKind; 5] = [
        AgentKind::Backend,
        AgentKind::Frontend,
        AgentKind::Qa,
        AgentKind::Research,
        AgentKind::DevOps,
    ];

    /// The full set of default agent templates Beethoven seeds once, globally.
    pub const ALL: [AgentKind; 6] = [
        AgentKind::ProjectManager,
        AgentKind::Backend,
        AgentKind::Frontend,
        AgentKind::Qa,
        AgentKind::Research,
        AgentKind::DevOps,
    ];

    pub fn as_str(&self) -> &'static str {
        match self {
            AgentKind::ProjectManager => "project_manager",
            AgentKind::Backend => "backend",
            AgentKind::Frontend => "frontend",
            AgentKind::Qa => "qa",
            AgentKind::Research => "research",
            AgentKind::DevOps => "devops",
        }
    }

    pub fn role_label(&self) -> &'static str {
        match self {
            AgentKind::ProjectManager => "Project Manager",
            AgentKind::Backend => "Backend Engineer",
            AgentKind::Frontend => "Frontend Engineer",
            AgentKind::Qa => "QA Engineer",
            AgentKind::Research => "Research Agent",
            AgentKind::DevOps => "DevOps Agent",
        }
    }

    pub fn default_prompt(&self) -> String {
        format!("You are the {} for this project.", self.role_label())
    }

    pub fn display_name(&self) -> &'static str {
        match self {
            AgentKind::ProjectManager => "PM",
            AgentKind::Backend => "Backend",
            AgentKind::Frontend => "Frontend",
            AgentKind::Qa => "QA",
            AgentKind::Research => "Research",
            AgentKind::DevOps => "DevOps",
        }
    }
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TaskDraft {
    pub title: String,
    pub description: String,
}

pub struct PlanningService;

impl PlanningService {
    /// Stub planner: turns bullet-list lines ("- " / "* " prefixed) in the goal
    /// description into discrete tasks. Falls back to a single task named after
    /// the goal when no bullet list is present.
    pub fn plan(goal_title: &str, goal_description: &str) -> Vec<TaskDraft> {
        let bullets: Vec<TaskDraft> = goal_description
            .lines()
            .map(str::trim)
            .filter_map(|line| {
                line.strip_prefix("- ")
                    .or_else(|| line.strip_prefix("* "))
                    .map(str::trim)
                    .filter(|t| !t.is_empty())
                    .map(|title| TaskDraft {
                        title: title.to_string(),
                        description: String::new(),
                    })
            })
            .collect();

        if bullets.is_empty() {
            vec![TaskDraft {
                title: format!("Implement: {goal_title}"),
                description: String::new(),
            }]
        } else {
            bullets
        }
    }
}

#[derive(Debug, Clone)]
pub struct RequirementInput {
    pub id: String,
    pub description: String,
}

#[derive(Debug, Clone)]
pub struct EpicDraft {
    pub title: String,
}

#[derive(Debug, Clone)]
pub struct DecisionDraft {
    pub title: String,
    pub context: String,
}

#[derive(Debug, Clone, Default)]
pub struct InitiativePlan {
    pub epics: Vec<EpicDraft>,
    pub decisions: Vec<DecisionDraft>,
}

pub struct InitiativePlanningService;

impl InitiativePlanningService {
    /// Stub PM planner: a requirement that poses an open question ("?") needs
    /// a human decision rather than a work breakdown, so it becomes a
    /// `Decision` (auto-escalated to the inbox); every other requirement
    /// becomes one epic. Falls back to a single epic named after the
    /// initiative when there are no requirements yet.
    pub fn plan(initiative_title: &str, requirements: &[RequirementInput]) -> InitiativePlan {
        let mut plan = InitiativePlan::default();
        for requirement in requirements {
            if requirement.description.contains('?') {
                plan.decisions.push(DecisionDraft {
                    title: requirement.description.clone(),
                    context: requirement.description.clone(),
                });
            } else {
                plan.epics.push(EpicDraft {
                    title: requirement.description.clone(),
                });
            }
        }

        if plan.epics.is_empty() && plan.decisions.is_empty() {
            plan.epics.push(EpicDraft {
                title: initiative_title.to_string(),
            });
        }

        plan
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
        let drafts = PlanningService::plan("Goal", "- one\n* two\nnot a bullet\n- three");
        let titles: Vec<&str> = drafts.iter().map(|d| d.title.as_str()).collect();
        assert_eq!(titles, vec!["one", "two", "three"]);
    }

    #[test]
    fn plan_falls_back_to_a_single_task_without_bullets() {
        let drafts = PlanningService::plan("Goal Title", "just prose, no bullets");
        assert_eq!(drafts.len(), 1);
        assert_eq!(drafts[0].title, "Implement: Goal Title");
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

    fn requirement(description: &str) -> RequirementInput {
        RequirementInput {
            id: "r1".to_string(),
            description: description.to_string(),
        }
    }

    #[test]
    fn initiative_plan_turns_each_plain_requirement_into_one_epic() {
        let reqs = vec![requirement("Support PDF attachments"), requirement("Route emails by company")];
        let plan = InitiativePlanningService::plan("Build Gmail Agent", &reqs);
        let titles: Vec<&str> = plan.epics.iter().map(|e| e.title.as_str()).collect();
        assert_eq!(titles, vec!["Support PDF attachments", "Route emails by company"]);
        assert!(plan.decisions.is_empty());
    }

    #[test]
    fn initiative_plan_turns_a_question_requirement_into_a_decision_not_an_epic() {
        let reqs = vec![requirement("Support PDF attachments"), requirement("What auth provider should we use?")];
        let plan = InitiativePlanningService::plan("Build Gmail Agent", &reqs);
        assert_eq!(plan.epics.len(), 1);
        assert_eq!(plan.epics[0].title, "Support PDF attachments");
        assert_eq!(plan.decisions.len(), 1);
        assert_eq!(plan.decisions[0].title, "What auth provider should we use?");
    }

    #[test]
    fn initiative_plan_falls_back_to_a_single_epic_without_requirements() {
        let plan = InitiativePlanningService::plan("Build Gmail Agent", &[]);
        assert_eq!(plan.epics.len(), 1);
        assert_eq!(plan.epics[0].title, "Build Gmail Agent");
        assert!(plan.decisions.is_empty());
    }
}
