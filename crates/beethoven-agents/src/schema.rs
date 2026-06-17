use serde::{Deserialize, Serialize};

pub const PM_PLANNING_JSON_SCHEMA: &str = r#"{"type":"object","properties":{"requirements":{"type":"array","items":{"type":"string"}},"epics":{"type":"array","items":{"type":"string"}},"tasks":{"type":"array","items":{"type":"string"}}},"required":["requirements","epics","tasks"]}"#;

pub const REVIEW_JSON_SCHEMA: &str = r#"{"type":"object","properties":{"decision":{"type":"string","enum":["approve","changes_requested","question"]},"notes":{"type":"string"}},"required":["decision","notes"]}"#;

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PmPlanResponse {
    pub requirements: Vec<String>,
    pub epics: Vec<String>,
    pub tasks: Vec<String>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ReviewDecision {
    Approve,
    ChangesRequested,
    Question,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ReviewResponse {
    pub decision: ReviewDecision,
    pub notes: String,
}

/// The single place a model's `structured_output` gets deserialized into a
/// typed response. Consumers must go through this rather than hand-rolling
/// `serde_json::from_value` -- there is never a reason to parse prose.
pub fn parse_structured_output<T: serde::de::DeserializeOwned>(value: &serde_json::Value) -> Result<T, serde_json::Error> {
    serde_json::from_value(value.clone())
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    #[test]
    fn parses_a_pm_plan_response_from_structured_output() {
        let value = json!({"requirements": ["r1"], "epics": ["e1"], "tasks": ["t1"]});
        let parsed: PmPlanResponse = parse_structured_output(&value).unwrap();
        assert_eq!(parsed.requirements, vec!["r1"]);
        assert_eq!(parsed.epics, vec!["e1"]);
        assert_eq!(parsed.tasks, vec!["t1"]);
    }

    #[test]
    fn parses_a_review_response_from_structured_output() {
        let value = json!({"decision": "changes_requested", "notes": "missing tests"});
        let parsed: ReviewResponse = parse_structured_output(&value).unwrap();
        assert_eq!(parsed.decision, ReviewDecision::ChangesRequested);
        assert_eq!(parsed.notes, "missing tests");
    }
}
