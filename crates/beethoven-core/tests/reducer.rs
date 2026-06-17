use beethoven_core::{AgentStatus, GoalStatus, InboxStatus, RuntimeState, TaskStatus};
use beethoven_events::{Event, EventType};
use serde_json::json;

fn setup_events() -> Vec<Event> {
    vec![
        Event::global(
            EventType::ProjectCreated,
            "human",
            json!({"id": "p1", "name": "Proj", "slug": "proj", "repository_path": "/repo", "default_branch": "main"}),
        ),
        Event::global(
            EventType::AgentTemplateCreated,
            "system",
            json!({"id": "tmpl-backend", "kind": "backend", "role": "Backend Engineer", "prompt": "...", "permissions": [], "tools": []}),
        ),
        Event::for_project(
            "p1",
            EventType::AgentCreated,
            "system",
            json!({"id": "a1", "template_id": "tmpl-backend", "kind": "backend", "name": "Proj Backend"}),
        ),
        Event::for_project("p1", EventType::AgentStarted, "system", json!({"id": "a1"})),
        Event::for_project(
            "p1",
            EventType::GoalCreated,
            "human",
            json!({"id": "g1", "title": "Goal", "description": ""}),
        ),
        Event::for_project(
            "p1",
            EventType::TaskCreated,
            "pm",
            json!({"id": "t1", "goal_id": "g1", "parent_task_id": None::<String>, "title": "Do it", "description": ""}),
        ),
        Event::for_project(
            "p1",
            EventType::TaskAssigned,
            "pm",
            json!({"task_id": "t1", "agent_id": "a1"}),
        ),
        Event::for_project("p1", EventType::TaskStarted, "a1", json!({"task_id": "t1"})),
    ]
}

#[test]
fn full_lifecycle_completes_task_and_auto_completes_goal() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskCompleted,
        "a1",
        json!({"task_id": "t1"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Done);
    assert_eq!(state.tasks["t1"].project_id, "p1");
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agent_instances["a1"].current_task_id, None);
    assert_eq!(state.goals["g1"].status, GoalStatus::Completed);
}

#[test]
fn replay_is_deterministic() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskCompleted,
        "a1",
        json!({"task_id": "t1"}),
    ));

    let s1 = RuntimeState::replay(&events);
    let s2 = RuntimeState::replay(&events);

    assert_eq!(s1.tasks["t1"].status, s2.tasks["t1"].status);
    assert_eq!(s1.goals["g1"].status, s2.goals["g1"].status);
    assert_eq!(s1.agent_instances["a1"].status, s2.agent_instances["a1"].status);
}

#[test]
fn spawning_a_subtask_returns_the_parent_to_ready_and_frees_its_agent() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskCreated,
        "a1",
        json!({"id": "t2", "goal_id": "g1", "parent_task_id": "t1", "title": "Subtask", "description": ""}),
    ));

    let state = RuntimeState::replay(&events);

    let parent = &state.tasks["t1"];
    assert_eq!(parent.status, TaskStatus::Ready);
    assert_eq!(parent.assigned_agent_id, None);
    assert_eq!(parent.attempt, 2);

    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agent_instances["a1"].current_task_id, None);

    let subtask = &state.tasks["t2"];
    assert_eq!(subtask.parent_task_id, Some("t1".to_string()));
    assert_eq!(subtask.project_id, "p1");
    assert_eq!(subtask.status, TaskStatus::Ready);
}

#[test]
fn question_raised_blocks_task_and_frees_agent() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskBlocked,
        "a1",
        json!({"task_id": "t1", "reason": "need clarification"}),
    ));
    events.push(Event::for_project(
        "p1",
        EventType::QuestionRaised,
        "a1",
        json!({"inbox_id": "i1", "task_id": "t1", "agent_id": "a1", "prompt": "need clarification"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Blocked);
    assert_eq!(state.tasks["t1"].assigned_agent_id, None);
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
    assert_eq!(state.inbox["i1"].status, InboxStatus::Open);
    assert_eq!(state.inbox["i1"].project_id, "p1");
}

#[test]
fn answering_a_question_unblocks_the_task_and_bumps_its_attempt() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskBlocked,
        "a1",
        json!({"task_id": "t1", "reason": "need clarification"}),
    ));
    events.push(Event::for_project(
        "p1",
        EventType::QuestionRaised,
        "a1",
        json!({"inbox_id": "i1", "task_id": "t1", "agent_id": "a1", "prompt": "need clarification"}),
    ));
    events.push(Event::for_project(
        "p1",
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
    events.push(Event::for_project(
        "p1",
        EventType::PRCreated,
        "a1",
        json!({"task_id": "t1", "branch": "task/do-it", "url": "stub://pr/t1"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].branch, Some("task/do-it".to_string()));
    assert_eq!(state.tasks["t1"].pr_url, Some("stub://pr/t1".to_string()));
}

#[test]
fn two_projects_do_not_mix_tasks_goals_or_inbox() {
    let mut events = setup_events(); // project p1, goal g1, task t1

    // A second, independent project with its own agent instance, goal, and task.
    events.push(Event::global(
        EventType::ProjectCreated,
        "human",
        json!({"id": "p2", "name": "Other", "slug": "other", "repository_path": "/repo2", "default_branch": "main"}),
    ));
    events.push(Event::for_project(
        "p2",
        EventType::AgentCreated,
        "system",
        json!({"id": "a2", "template_id": "tmpl-backend", "kind": "backend", "name": "Other Backend"}),
    ));
    events.push(Event::for_project("p2", EventType::AgentStarted, "system", json!({"id": "a2"})));
    events.push(Event::for_project(
        "p2",
        EventType::GoalCreated,
        "human",
        json!({"id": "g2", "title": "Other Goal", "description": ""}),
    ));
    events.push(Event::for_project(
        "p2",
        EventType::TaskCreated,
        "pm",
        json!({"id": "t2", "goal_id": "g2", "parent_task_id": None::<String>, "title": "Other task", "description": ""}),
    ));
    events.push(Event::for_project(
        "p2",
        EventType::QuestionRaised,
        "a2",
        json!({"inbox_id": "i2", "task_id": "t2", "agent_id": "a2", "prompt": "p2 only"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].project_id, "p1");
    assert_eq!(state.tasks["t2"].project_id, "p2");
    assert_eq!(state.goals["g1"].project_id, "p1");
    assert_eq!(state.goals["g2"].project_id, "p2");
    assert_eq!(state.inbox["i2"].project_id, "p2");

    let p1_tasks: Vec<_> = state.tasks.values().filter(|t| t.project_id == "p1").collect();
    let p2_tasks: Vec<_> = state.tasks.values().filter(|t| t.project_id == "p2").collect();
    assert_eq!(p1_tasks.len(), 1);
    assert_eq!(p2_tasks.len(), 1);

    let p1_inbox: Vec<_> = state.inbox.values().filter(|i| i.project_id == "p1").collect();
    let p2_inbox: Vec<_> = state.inbox.values().filter(|i| i.project_id == "p2").collect();
    assert_eq!(p1_inbox.len(), 0);
    assert_eq!(p2_inbox.len(), 1);
}
