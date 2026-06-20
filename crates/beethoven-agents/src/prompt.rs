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

/// The only place prompt text gets assembled. Orchestration code must always
/// go through one of these rather than hand-crafting prompt text inline.
pub struct PromptBuilder;

impl PromptBuilder {
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
}
