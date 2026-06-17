pub struct PromptContext<'a> {
    pub project_name: &'a str,
    pub task_title: &'a str,
    pub task_description: &'a str,
    pub repository_path: &'a str,
    pub branch: &'a str,
}

/// Renders the structured execution request a model runtime receives.
/// Orchestration code must always go through this contract rather than
/// hand-crafting prompt text inline.
pub fn render_prompt(ctx: &PromptContext) -> String {
    let requirements: Vec<&str> = ctx
        .task_description
        .lines()
        .map(str::trim)
        .filter_map(|line| line.strip_prefix("- ").or_else(|| line.strip_prefix("* ")))
        .filter(|l| !l.is_empty())
        .collect();

    let mut out = format!(
        "Project:\n{}\n\nTask:\n{}\n\nRepository:\n{}\n\nBranch:\n{}\n",
        ctx.project_name, ctx.task_title, ctx.repository_path, ctx.branch
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn renders_project_task_repository_and_branch() {
        let prompt = render_prompt(&PromptContext {
            project_name: "Sample",
            task_title: "Implement OAuth login",
            task_description: "",
            repository_path: "/workspace/sample",
            branch: "task/oauth-login",
        });
        assert!(prompt.contains("Project:\nSample"));
        assert!(prompt.contains("Task:\nImplement OAuth login"));
        assert!(prompt.contains("Repository:\n/workspace/sample"));
        assert!(prompt.contains("Branch:\ntask/oauth-login"));
        assert!(!prompt.contains("Requirements:"));
    }

    #[test]
    fn extracts_bullet_lines_from_the_description_as_requirements() {
        let prompt = render_prompt(&PromptContext {
            project_name: "Sample",
            task_title: "Implement OAuth login",
            task_description: "- Support Google\n- Support GitHub\nsome prose",
            repository_path: "/workspace/sample",
            branch: "task/oauth-login",
        });
        assert!(prompt.contains("Requirements:\n- Support Google\n- Support GitHub\n"));
        assert!(!prompt.contains("some prose"));
    }
}
