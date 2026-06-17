use serde::{Deserialize, Serialize};
use std::path::Path;
use std::process::Command;

#[derive(Debug, thiserror::Error)]
pub enum GitError {
    #[error("io error running git: {0}")]
    Io(#[from] std::io::Error),
    #[error("git {args} failed: {stderr}")]
    CommandFailed { args: String, stderr: String },
}

fn run_git(repo_dir: &Path, args: &[&str]) -> Result<String, GitError> {
    let output = Command::new("git").arg("-C").arg(repo_dir).args(args).output()?;
    if !output.status.success() {
        return Err(GitError::CommandFailed {
            args: args.join(" "),
            stderr: String::from_utf8_lossy(&output.stderr).trim().to_string(),
        });
    }
    Ok(String::from_utf8_lossy(&output.stdout).into_owned())
}

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

/// Real `git worktree add`: creates `worktree_path` checked out on a fresh
/// `branch` cut from `base_branch`. `repo_path` is the project's main
/// repository clone, never mutated directly.
pub fn create_worktree(repo_path: &Path, worktree_path: &Path, branch: &str, base_branch: &str) -> Result<(), GitError> {
    run_git(
        repo_path,
        &["worktree", "add", "-b", branch, &worktree_path.to_string_lossy(), base_branch],
    )?;
    Ok(())
}

/// Force-removes the worktree and deletes its branch so a retried task can
/// recreate the same branch name cleanly on its next attempt.
pub fn remove_worktree(repo_path: &Path, worktree_path: &Path, branch: &str) -> Result<(), GitError> {
    let _ = run_git(repo_path, &["worktree", "remove", "--force", &worktree_path.to_string_lossy()]);
    let _ = run_git(repo_path, &["branch", "-D", branch]);
    Ok(())
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct GitStatus {
    pub modified: Vec<String>,
    pub added: Vec<String>,
    pub deleted: Vec<String>,
}

impl GitStatus {
    pub fn is_clean(&self) -> bool {
        self.modified.is_empty() && self.added.is_empty() && self.deleted.is_empty()
    }
}

/// Real `git status --porcelain` against a worktree, classified into
/// modified/added/deleted file lists.
pub fn status(worktree_path: &Path) -> Result<GitStatus, GitError> {
    let out = run_git(worktree_path, &["status", "--porcelain"])?;
    let mut status = GitStatus::default();
    for line in out.lines() {
        if line.len() < 4 {
            continue;
        }
        let code = &line[0..2];
        let file = line[3..].trim().to_string();
        match code {
            "??" | "A " | " A" | "AM" => status.added.push(file),
            "D " | " D" => status.deleted.push(file),
            _ => status.modified.push(file),
        }
    }
    Ok(status)
}

/// Stages every change (so untracked files are included too) and diffs
/// against HEAD, without committing anything.
pub fn diff(worktree_path: &Path) -> Result<String, GitError> {
    run_git(worktree_path, &["add", "-A"])?;
    run_git(worktree_path, &["diff", "--cached"])
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

    fn init_repo(path: &Path) {
        std::fs::create_dir_all(path).unwrap();
        run_git(path, &["init", "-q", "-b", "main"]).unwrap();
        run_git(path, &["config", "user.email", "test@example.com"]).unwrap();
        run_git(path, &["config", "user.name", "Test"]).unwrap();
        std::fs::write(path.join("README.md"), "scratch repo\n").unwrap();
        run_git(path, &["add", "-A"]).unwrap();
        run_git(path, &["commit", "-q", "-m", "init"]).unwrap();
    }

    #[test]
    fn create_worktree_checks_out_a_real_branch_on_disk() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let worktree = dir.path().join("worktree");
        create_worktree(&repo, &worktree, "task/demo", "main").unwrap();

        assert!(worktree.join("README.md").is_file());
        let branches = run_git(&repo, &["branch", "--list", "task/demo"]).unwrap();
        assert!(branches.contains("task/demo"));
    }

    #[test]
    fn remove_worktree_deletes_the_directory_and_branch() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let worktree = dir.path().join("worktree");
        create_worktree(&repo, &worktree, "task/demo", "main").unwrap();
        remove_worktree(&repo, &worktree, "task/demo").unwrap();

        assert!(!worktree.exists());
        let branches = run_git(&repo, &["branch", "--list", "task/demo"]).unwrap();
        assert!(!branches.contains("task/demo"));
    }

    #[test]
    fn status_and_diff_detect_new_and_modified_files() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let worktree = dir.path().join("worktree");
        create_worktree(&repo, &worktree, "task/demo", "main").unwrap();

        std::fs::write(worktree.join("README.md"), "scratch repo\nupdated\n").unwrap();
        std::fs::write(worktree.join("NOTES.md"), "new file\n").unwrap();

        let st = status(&worktree).unwrap();
        assert!(st.modified.iter().any(|f| f == "README.md"));
        assert!(st.added.iter().any(|f| f == "NOTES.md"));
        assert!(!st.is_clean());

        let diff_text = diff(&worktree).unwrap();
        assert!(diff_text.contains("NOTES.md"));
        assert!(diff_text.contains("README.md"));
        assert!(diff_text.contains("updated"));
    }

    #[test]
    fn status_is_clean_with_no_changes() {
        let dir = tempfile::tempdir().unwrap();
        let repo = dir.path().join("repo");
        init_repo(&repo);

        let worktree = dir.path().join("worktree");
        create_worktree(&repo, &worktree, "task/demo", "main").unwrap();

        assert!(status(&worktree).unwrap().is_clean());
    }
}
