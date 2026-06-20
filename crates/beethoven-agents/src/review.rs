#[derive(Debug, Clone, PartialEq)]
pub struct ReviewResult {
    pub score: f32,
    pub summary: String,
    pub recommendations: Vec<String>,
}

#[derive(Debug, Clone, PartialEq)]
pub enum ReviewOutcome {
    Scored(ReviewResult),
    Blocked(String),
    RaisedQuestion(String),
}

pub struct ReviewService;

impl ReviewService {
    /// Stub reviewer: deterministic keyword/diff heuristics stand in for a
    /// real review agent until a model runtime drives this too. Returns a
    /// score rather than a bare approve/reject verdict -- the caller
    /// compares it against the task's `target_score` to decide whether to
    /// retry, which *is* Beethoven's loop (no separate Loop entity needed).
    pub fn review(task_title: &str, diff: &str) -> ReviewOutcome {
        if diff.trim().is_empty() {
            return ReviewOutcome::Scored(ReviewResult {
                score: 0.0,
                summary: "no changes detected in the diff".to_string(),
                recommendations: vec!["make a change before requesting review".to_string()],
            });
        }

        let lower = task_title.to_lowercase();
        if lower.contains("risky") || lower.contains("migration") {
            return ReviewOutcome::Blocked(format!("flagged as risky by review: {task_title}"));
        }
        if lower.contains("clarify") {
            return ReviewOutcome::RaisedQuestion(format!("review needs clarification on: {task_title}"));
        }

        ReviewOutcome::Scored(ReviewResult {
            score: 90.0,
            summary: "no issues found".to_string(),
            recommendations: Vec::new(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn approves_a_normal_task_with_a_non_empty_diff() {
        assert!(matches!(
            ReviewService::review("Write docs", "+some change"),
            ReviewOutcome::Scored(r) if r.score >= 70.0
        ));
    }

    #[test]
    fn requests_changes_when_the_diff_is_empty() {
        assert!(matches!(
            ReviewService::review("Write docs", "   "),
            ReviewOutcome::Scored(r) if r.score < 70.0
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
