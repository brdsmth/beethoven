use crate::schema::{PM_PLANNING_JSON_SCHEMA, REVIEW_JSON_SCHEMA};

pub struct WorkerPromptContext<'a> {
    pub project_name: &'a str,
    pub task_title: &'a str,
    pub task_description: &'a str,
    pub repository_path: &'a str,
    pub branch: &'a str,
}

pub struct PmPlanningPromptContext<'a> {
    pub project_name: &'a str,
    pub initiative_title: &'a str,
    pub initiative_description: &'a str,
    pub requirements: &'a [String],
    pub repository_path: &'a str,
}

pub struct ReviewPromptContext<'a> {
    pub project_name: &'a str,
    pub task_title: &'a str,
    pub files_changed: &'a [String],
    pub diff: &'a str,
    pub execution_summary: &'a str,
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

    pub fn pm_planning(ctx: &PmPlanningPromptContext) -> String {
        let mut out = format!(
            "Project:\n{}\n\nInitiative:\n{}\n\nDescription:\n{}\n\nRepository:\n{}\n",
            ctx.project_name, ctx.initiative_title, ctx.initiative_description, ctx.repository_path
        );

        if !ctx.requirements.is_empty() {
            out.push_str("\nRequirements:\n");
            for r in ctx.requirements {
                out.push_str("- ");
                out.push_str(r);
                out.push('\n');
            }
        }

        out.push_str(&format!(
            "\nRespond with structured JSON only, matching this schema:\n{PM_PLANNING_JSON_SCHEMA}\n"
        ));
        out
    }

    pub fn review(ctx: &ReviewPromptContext) -> String {
        let mut out = format!(
            "Project:\n{}\n\nTask:\n{}\n\nExecution summary:\n{}\n\nFiles changed:\n{}\n\nDiff:\n{}\n",
            ctx.project_name,
            ctx.task_title,
            ctx.execution_summary,
            ctx.files_changed.join("\n"),
            ctx.diff
        );

        out.push_str(&format!(
            "\nRespond with structured JSON only, matching this schema:\n{REVIEW_JSON_SCHEMA}\n"
        ));
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
        });
        assert!(prompt.contains("Project:\nSample"));
        assert!(prompt.contains("Task:\nImplement OAuth login"));
        assert!(prompt.contains("Repository:\n/workspace/sample"));
        assert!(prompt.contains("Branch:\ntask/oauth-login"));
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
        });
        assert!(prompt.contains("Requirements:\n- Support Google\n- Support GitHub\n"));
        assert!(!prompt.contains("some prose"));
    }

    #[test]
    fn pm_planning_includes_requirements_and_the_json_schema() {
        let requirements = vec!["Support PDF attachments".to_string()];
        let prompt = PromptBuilder::pm_planning(&PmPlanningPromptContext {
            project_name: "Sample",
            initiative_title: "Build Gmail Agent",
            initiative_description: "Routes inbound email",
            requirements: &requirements,
            repository_path: "/workspace/sample",
        });
        assert!(prompt.contains("Initiative:\nBuild Gmail Agent"));
        assert!(prompt.contains("- Support PDF attachments"));
        assert!(prompt.contains("\"requirements\""));
    }

    #[test]
    fn review_includes_the_diff_and_the_json_schema() {
        let files = vec!["src/lib.rs".to_string()];
        let prompt = PromptBuilder::review(&ReviewPromptContext {
            project_name: "Sample",
            task_title: "Implement OAuth login",
            files_changed: &files,
            diff: "+ fn login() {}",
            execution_summary: "Added the login function",
        });
        assert!(prompt.contains("Diff:\n+ fn login() {}"));
        assert!(prompt.contains("src/lib.rs"));
        assert!(prompt.contains("\"decision\""));
    }
}
