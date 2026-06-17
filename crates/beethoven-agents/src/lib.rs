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
