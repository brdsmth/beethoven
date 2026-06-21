use crate::AgentRole;

pub struct WorkerPromptContext<'a> {
    pub project_name: &'a str,
    pub task_title: &'a str,
    pub task_description: &'a str,
    pub repository_path: &'a str,
    pub branch: &'a str,
    pub role: AgentRole,
    pub role_description: &'a str,
}

/// Everything `PlanningService` needs the model to ground its breakdown in:
/// the goal itself, what's already been planned in this project (so it
/// doesn't duplicate work), and a few facts about the project. The
/// `-----BEGIN/END DESCRIPTION-----` delimiters around `task_description`
/// are load-bearing, not decoration -- a real description can itself
/// contain blank lines or `- `-prefixed text, so an unambiguous, exact-text
/// boundary is required for anything (tests, a future re-parser) that needs
/// to recover the original description from the rendered prompt.
pub struct PlannerPromptContext<'a> {
    pub project_name: &'a str,
    pub task_title: &'a str,
    pub task_description: &'a str,
    pub existing_task_tree: &'a str,
    pub project_facts: &'a str,
}

/// The only place prompt text gets assembled. Orchestration code must always
/// go through one of these rather than hand-crafting prompt text inline.
pub struct PromptBuilder;

impl PromptBuilder {
    /// Phase 2.5A: model-driven task decomposition. Optimizes for
    /// completeness, correct granularity, and parallelizability; instructs
    /// the model to raise a `Decision` instead of guessing when the goal is
    /// ambiguous, underspecified, or self-contradictory. The expected
    /// response shape is also enforced via `ExecutionRequest::json_schema`
    /// (see `PLANNER_RESPONSE_SCHEMA`) -- this text restates it for models
    /// that ignore or partially honor the schema.
    pub fn planner(ctx: &PlannerPromptContext) -> String {
        format!(
            "You are Beethoven's planning agent. Break the task below down into a set of \
             well-scoped child tasks that worker agents could pick up independently.\n\n\
             Project:\n{project_name}\n\n\
             Task Title:\n{task_title}\n\n\
             Task Description:\n-----BEGIN DESCRIPTION-----\n{task_description}\n-----END DESCRIPTION-----\n\n\
             Existing Task Tree:\n{existing_task_tree}\n\n\
             Current Project Facts:\n{project_facts}\n\n\
             Optimize the breakdown for:\n\
             - Completeness -- the child tasks should fully cover the goal\n\
             - Correct granularity -- not too large, not too small\n\
             - Parallelizability -- independent tasks workers could pick up at once\n\
             - Software engineering best practices\n\n\
             Avoid:\n\
             - Extremely large tasks (break them down further)\n\
             - Extremely small tasks (merge them into something substantive)\n\
             - Duplicate tasks (check the existing task tree above first)\n\n\
             If the goal is ambiguous, missing requirements, or internally contradictory, do not \
             guess: raise it as a decision (a clear question) instead of silently deciding for the \
             user.\n\n\
             Respond with a JSON object with exactly three fields: \"reasoning\" (a short \
             explanation of your overall breakdown approach), \"tasks\" (an array of objects with \
             \"title\", \"description\", and \"reason\" -- one per child task, with \"reason\" \
             explaining why that specific task exists), and \"decisions\" (an array of objects with \
             \"question\", \"context\", and \"reason\" for anything ambiguous or contradictory that \
             needs a human call -- empty if there is nothing to escalate).",
            project_name = ctx.project_name,
            task_title = ctx.task_title,
            task_description = ctx.task_description,
            existing_task_tree = ctx.existing_task_tree,
            project_facts = ctx.project_facts,
        )
    }

    pub fn worker(ctx: &WorkerPromptContext) -> String {
        let requirements: Vec<&str> = ctx
            .task_description
            .lines()
            .map(str::trim)
            .filter_map(|line| line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")))
            .filter(|l| !l.is_empty())
            .collect();

        let role_line = match ctx.role {
            AgentRole::Lead => format!("You are the lead agent for this project. Role: {}\n\n", ctx.role_description),
            AgentRole::Worker => format!("You are a worker agent. Role: {}\n\n", ctx.role_description),
            AgentRole::Reviewer => format!("You are a reviewer agent. Role: {}\n\n", ctx.role_description),
        };

        let mut out = format!(
            "{}Project:\n{}\n\nTask:\n{}\n\nRepository:\n{}\n\nBranch:\n{}\n",
            role_line, ctx.project_name, ctx.task_title, ctx.repository_path, ctx.branch
        );

        if !requirements.is_empty() {
            out.push_str("\nRequirements:\n");
            for r in requirements {
                out.push_str("- ");
                out.push_str(r);
                out.push('\n');
            }
        }

        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn worker_renders_project_task_repository_and_branch() {
        let prompt = PromptBuilder::worker(&WorkerPromptContext {
            project_name: "Sample",
            task_title: "Implement OAuth login",
            task_description: "",
            repository_path: "/workspace/sample",
            branch: "task/oauth-login",
            role: AgentRole::Worker,
            role_description: "OAuth Research Agent",
        });
        assert!(prompt.contains("Project:\nSample"));
        assert!(prompt.contains("Task:\nImplement OAuth login"));
        assert!(prompt.contains("Repository:\n/workspace/sample"));
        assert!(prompt.contains("Branch:\ntask/oauth-login"));
        assert!(prompt.contains("You are a worker agent. Role: OAuth Research Agent"));
        assert!(!prompt.contains("Requirements:"));
    }

    #[test]
    fn worker_extracts_bullet_lines_from_the_description_as_requirements() {
        let prompt = PromptBuilder::worker(&WorkerPromptContext {
            project_name: "Sample",
            task_title: "Implement OAuth login",
            task_description: "- Support Google\n- Support GitHub\nsome prose",
            repository_path: "/workspace/sample",
            branch: "task/oauth-login",
            role: AgentRole::Worker,
            role_description: "OAuth Research Agent",
        });
        assert!(prompt.contains("Requirements:\n- Support Google\n- Support GitHub\n"));
        assert!(!prompt.contains("some prose"));
    }

    #[test]
    fn planner_renders_all_context_sections_and_preserves_the_description_verbatim() {
        let description = "- Support Google\n\n- Support GitHub (open question: which scopes?)";
        let prompt = PromptBuilder::planner(&PlannerPromptContext {
            project_name: "Sample",
            task_title: "Build OAuth Authentication",
            task_description: description,
            existing_task_tree: "(no tasks exist yet in this project)",
            project_facts: "Repository: /workspace/sample\nDefault branch: main",
        });
        assert!(prompt.contains("Task Title:\nBuild OAuth Authentication"));
        assert!(prompt.contains(&format!(
            "-----BEGIN DESCRIPTION-----\n{description}\n-----END DESCRIPTION-----"
        )));
        assert!(prompt.contains("Existing Task Tree:\n(no tasks exist yet in this project)"));
        assert!(prompt.contains("Current Project Facts:\nRepository: /workspace/sample"));
        assert!(prompt.contains("\"reasoning\""));
        assert!(prompt.contains("raise it as a decision"));
    }
}
