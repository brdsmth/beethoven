use serde::{Deserialize, Serialize};

pub fn branch_name(task_title: &str) -> String {
    let mut slug = String::new();
    let mut last_was_dash = true; // suppress leading dashes
    for ch in task_title.to_lowercase().chars() {
        if ch.is_ascii_alphanumeric() {
            slug.push(ch);
            last_was_dash = false;
        } else if !last_was_dash {
            slug.push('-');
            last_was_dash = true;
        }
    }
    while slug.ends_with('-') {
        slug.pop();
    }
    if slug.is_empty() {
        slug.push_str("task");
    }
    format!("task/{slug}")
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PullRequest {
    pub task_id: String,
    pub branch: String,
    pub title: String,
    pub url: String,
}

/// Stand-in for a real git/GitHub integration: records intent without
/// touching a working tree or remote.
pub fn create_branch(task_id: &str, task_title: &str) -> String {
    let _ = task_id;
    branch_name(task_title)
}

pub fn open_pr(task_id: &str, branch: &str, title: &str) -> PullRequest {
    PullRequest {
        task_id: task_id.to_string(),
        branch: branch.to_string(),
        title: title.to_string(),
        url: format!("stub://pr/{task_id}"),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn slugifies_a_basic_title() {
        assert_eq!(branch_name("Implement Auth"), "task/implement-auth");
    }

    #[test]
    fn collapses_punctuation_and_repeated_separators() {
        assert_eq!(branch_name("Fix   tests!! (urgent)"), "task/fix-tests-urgent");
    }

    #[test]
    fn falls_back_to_task_for_empty_or_symbol_only_titles() {
        assert_eq!(branch_name(""), "task/task");
        assert_eq!(branch_name("???"), "task/task");
    }

    #[test]
    fn open_pr_records_task_branch_and_title() {
        let pr = open_pr("abc123", "task/foo", "Foo");
        assert_eq!(pr.task_id, "abc123");
        assert_eq!(pr.branch, "task/foo");
        assert_eq!(pr.title, "Foo");
        assert_eq!(pr.url, "stub://pr/abc123");
    }
}
