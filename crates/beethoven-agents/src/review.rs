#[derive(Debug, Clone, PartialEq, Eq)]
pub enum ReviewOutcome {
    Approved,
    NeedsChanges(String),
    Blocked(String),
    RaisedQuestion(String),
}

pub struct ReviewService;

impl ReviewService {
    /// Stub reviewer: deterministic keyword/diff heuristics stand in for a
    /// real review agent until a model runtime drives this too.
    pub fn review(task_title: &str, diff: &str) -> ReviewOutcome {
        if diff.trim().is_empty() {
            return ReviewOutcome::NeedsChanges("no changes detected in the diff; retry".to_string());
        }

        let lower = task_title.to_lowercase();
        if lower.contains("risky") || lower.contains("migration") {
            return ReviewOutcome::Blocked(format!("flagged as risky by review: {task_title}"));
        }
        if lower.contains("clarify") {
            return ReviewOutcome::RaisedQuestion(format!("review needs clarification on: {task_title}"));
        }

        ReviewOutcome::Approved
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn approves_a_normal_task_with_a_non_empty_diff() {
        assert_eq!(ReviewService::review("Write docs", "+some change"), ReviewOutcome::Approved);
    }

    #[test]
    fn requests_changes_when_the_diff_is_empty() {
        assert!(matches!(
            ReviewService::review("Write docs", "   "),
            ReviewOutcome::NeedsChanges(_)
        ));
    }

    #[test]
    fn blocks_risky_or_migration_titles() {
        assert!(matches!(
            ReviewService::review("Run a risky migration", "+diff"),
            ReviewOutcome::Blocked(_)
        ));
        assert!(matches!(
            ReviewService::review("Database migration cleanup", "+diff"),
            ReviewOutcome::Blocked(_)
        ));
    }

    #[test]
    fn raises_a_question_for_titles_needing_clarification() {
        assert!(matches!(
            ReviewService::review("Please clarify the auth flow", "+diff"),
            ReviewOutcome::RaisedQuestion(_)
        ));
    }
}
