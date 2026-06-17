use beethoven_core::{AgentStatus, GoalStatus, InboxStatus, RuntimeState, TaskStatus};
use beethoven_events::{Event, EventType};
use serde_json::json;

fn ev(event_type: EventType, actor_id: &str, payload: serde_json::Value) -> Event {
    Event::new(event_type, actor_id, payload)
}

fn setup_events() -> Vec<Event> {
    vec![
        ev(
            EventType::GoalCreated,
            "human",
            json!({"id": "g1", "title": "Goal", "description": ""}),
        ),
        ev(EventType::AgentCreated, "system", json!({"id": "a1", "kind": "backend"})),
        ev(EventType::AgentStarted, "system", json!({"id": "a1"})),
        ev(
            EventType::TaskCreated,
            "pm",
            json!({"id": "t1", "goal_id": "g1", "parent_task_id": None::<String>, "title": "Do it", "description": ""}),
        ),
        ev(EventType::TaskAssigned, "pm", json!({"task_id": "t1", "agent_id": "a1"})),
        ev(EventType::TaskStarted, "a1", json!({"task_id": "t1"})),
    ]
}

#[test]
fn full_lifecycle_completes_task_and_auto_completes_goal() {
    let mut events = setup_events();
    events.push(ev(EventType::TaskCompleted, "a1", json!({"task_id": "t1"})));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Done);
    assert_eq!(state.agents["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agents["a1"].current_task_id, None);
    assert_eq!(state.goals["g1"].status, GoalStatus::Completed);
}

#[test]
fn replay_is_deterministic() {
    let mut events = setup_events();
    events.push(ev(EventType::TaskCompleted, "a1", json!({"task_id": "t1"})));

    let s1 = RuntimeState::replay(&events);
    let s2 = RuntimeState::replay(&events);

    assert_eq!(s1.tasks["t1"].status, s2.tasks["t1"].status);
    assert_eq!(s1.goals["g1"].status, s2.goals["g1"].status);
    assert_eq!(s1.agents["a1"].status, s2.agents["a1"].status);
}

#[test]
fn spawning_a_subtask_returns_the_parent_to_ready_and_frees_its_agent() {
    // Regression: the agent that spawned a subtask used to stay stuck "Executing" forever.
    let mut events = setup_events();
    events.push(ev(
        EventType::TaskCreated,
        "a1",
        json!({"id": "t2", "goal_id": "g1", "parent_task_id": "t1", "title": "Subtask", "description": ""}),
    ));

    let state = RuntimeState::replay(&events);

    let parent = &state.tasks["t1"];
    assert_eq!(parent.status, TaskStatus::Ready);
    assert_eq!(parent.assigned_agent_id, None);
    assert_eq!(parent.attempt, 2);

    assert_eq!(state.agents["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agents["a1"].current_task_id, None);

    let subtask = &state.tasks["t2"];
    assert_eq!(subtask.parent_task_id, Some("t1".to_string()));
    assert_eq!(subtask.status, TaskStatus::Ready);
}

#[test]
fn question_raised_blocks_task_and_frees_agent() {
    let mut events = setup_events();
    events.push(ev(
        EventType::TaskBlocked,
        "a1",
        json!({"task_id": "t1", "reason": "need clarification"}),
    ));
    events.push(ev(
        EventType::QuestionRaised,
        "a1",
        json!({"inbox_id": "i1", "task_id": "t1", "agent_id": "a1", "prompt": "need clarification"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Blocked);
    assert_eq!(state.tasks["t1"].assigned_agent_id, None);
    assert_eq!(state.agents["a1"].status, AgentStatus::Idle);
    assert_eq!(state.inbox["i1"].status, InboxStatus::Open);
}

#[test]
fn answering_a_question_unblocks_the_task_and_bumps_its_attempt() {
    // Regression: without bumping `attempt`, a re-executed task would re-raise
    // the exact same question forever instead of proceeding past it.
    let mut events = setup_events();
    events.push(ev(
        EventType::TaskBlocked,
        "a1",
        json!({"task_id": "t1", "reason": "need clarification"}),
    ));
    events.push(ev(
        EventType::QuestionRaised,
        "a1",
        json!({"inbox_id": "i1", "task_id": "t1", "agent_id": "a1", "prompt": "need clarification"}),
    ));
    events.push(ev(
        EventType::QuestionAnswered,
        "pm",
        json!({"inbox_id": "i1", "response": "use OAuth"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Ready);
    assert_eq!(state.tasks["t1"].attempt, 2);
    assert_eq!(state.inbox["i1"].status, InboxStatus::Resolved);
    assert_eq!(state.inbox["i1"].response, Some("use OAuth".to_string()));
}

#[test]
fn pr_created_records_branch_and_url_on_the_task() {
    let mut events = setup_events();
    events.push(ev(
        EventType::PRCreated,
        "a1",
        json!({"task_id": "t1", "branch": "task/do-it", "url": "stub://pr/t1"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].branch, Some("task/do-it".to_string()));
    assert_eq!(state.tasks["t1"].pr_url, Some("stub://pr/t1".to_string()));
}
