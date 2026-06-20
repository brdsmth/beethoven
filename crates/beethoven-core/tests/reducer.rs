use beethoven_core::{AgentStatus, DecisionStatus, InboxKind, InboxStatus, ProjectStatus, RuntimeState, TaskStatus};
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
            json!({"id": "tmpl-worker", "role": "worker", "label": "Worker", "prompt": "...", "permissions": [], "tools": []}),
        ),
        Event::for_project(
            "p1",
            EventType::AgentSpawned,
            "system",
            json!({"id": "a1", "template_id": "tmpl-worker", "role": "worker", "role_description": "Backend work", "parent_agent_id": None::<String>, "name": "Proj Backend"}),
        ),
        Event::for_project(
            "p1",
            EventType::TaskCreated,
            "human",
            json!({
                "id": "root1",
                "parent_task_id": None::<String>,
                "title": "Root",
                "description": "",
                "status": TaskStatus::Ready,
                "success_criteria": "",
                "target_score": 70.0,
            }),
        ),
        Event::for_project(
            "p1",
            EventType::TaskCreated,
            "pm",
            json!({
                "id": "t1",
                "parent_task_id": "root1",
                "title": "Do it",
                "description": "",
                "status": TaskStatus::Ready,
                "success_criteria": "",
                "target_score": 70.0,
            }),
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
fn full_lifecycle_completes_a_task_and_makes_its_parent_eligible_to_complete() {
    let mut events = setup_events();
    assert!(!RuntimeState::replay(&events).parent_task_is_complete("root1"));

    events.push(Event::for_project(
        "p1",
        EventType::TaskCompleted,
        "a1",
        json!({"task_id": "t1"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Done);
    assert_eq!(state.tasks["t1"].project_id, "p1");
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Completed);
    assert_eq!(state.agent_instances["a1"].current_task_id, None);

    // The reducer never auto-cascades completion up the tree on its own --
    // that's a Runtime-level concern -- but it does report root1 as now
    // *eligible* to complete once its only child is Done.
    assert!(state.parent_task_is_complete("root1"));
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
    assert_eq!(
        s1.parent_task_is_complete("root1"),
        s2.parent_task_is_complete("root1")
    );
    assert_eq!(s1.agent_instances["a1"].status, s2.agent_instances["a1"].status);
}

#[test]
fn spawning_a_subtask_returns_the_parent_to_ready_and_frees_its_agent() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskCreated,
        "a1",
        json!({
            "id": "t2",
            "parent_task_id": "t1",
            "title": "Subtask",
            "description": "",
            "status": TaskStatus::Ready,
            "success_criteria": "",
            "target_score": 70.0,
        }),
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
fn two_projects_do_not_mix_tasks_or_inbox() {
    let mut events = setup_events(); // project p1, root task root1, task t1

    // A second, independent project with its own agent instance and task.
    events.push(Event::global(
        EventType::ProjectCreated,
        "human",
        json!({"id": "p2", "name": "Other", "slug": "other", "repository_path": "/repo2", "default_branch": "main"}),
    ));
    events.push(Event::for_project(
        "p2",
        EventType::AgentSpawned,
        "system",
        json!({"id": "a2", "template_id": "tmpl-worker", "role": "worker", "role_description": "Backend work", "parent_agent_id": None::<String>, "name": "Other Backend"}),
    ));
    events.push(Event::for_project(
        "p2",
        EventType::TaskCreated,
        "pm",
        json!({
            "id": "t2",
            "parent_task_id": None::<String>,
            "title": "Other task",
            "description": "",
            "status": TaskStatus::Ready,
            "success_criteria": "",
            "target_score": 70.0,
        }),
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
    assert_eq!(state.inbox["i2"].project_id, "p2");

    let p1_tasks: Vec<_> = state.tasks.values().filter(|t| t.project_id == "p1").collect();
    let p2_tasks: Vec<_> = state.tasks.values().filter(|t| t.project_id == "p2").collect();
    assert_eq!(p1_tasks.len(), 2); // root1 + t1
    assert_eq!(p2_tasks.len(), 1);

    let p1_inbox: Vec<_> = state.inbox.values().filter(|i| i.project_id == "p1").collect();
    let p2_inbox: Vec<_> = state.inbox.values().filter(|i| i.project_id == "p2").collect();
    assert_eq!(p1_inbox.len(), 0);
    assert_eq!(p2_inbox.len(), 1);
}

#[test]
fn a_multi_level_task_tree_links_children_via_parent_task_id_with_no_separate_container_entity() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskCreated,
        "pm",
        json!({
            "id": "mid1",
            "parent_task_id": "root1",
            "title": "Support PDF attachments",
            "description": "",
            "status": TaskStatus::Ready,
            "success_criteria": "",
            "target_score": 70.0,
        }),
    ));
    events.push(Event::for_project(
        "p1",
        EventType::TaskCreated,
        "pm",
        json!({
            "id": "leaf1",
            "parent_task_id": "mid1",
            "title": "Implement: Support PDF attachments",
            "description": "",
            "status": TaskStatus::Ready,
            "success_criteria": "",
            "target_score": 70.0,
        }),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["root1"].project_id, "p1");
    assert_eq!(state.tasks["mid1"].project_id, "p1");
    assert_eq!(state.tasks["leaf1"].project_id, "p1");
    assert_eq!(state.tasks["mid1"].parent_task_id, Some("root1".to_string()));
    assert_eq!(state.tasks["leaf1"].parent_task_id, Some("mid1".to_string()));
}

#[test]
fn decision_escalation_links_the_inbox_item_and_resolving_it_marks_the_decision_resolved() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::DecisionCreated,
        "pm",
        json!({"id": "dec1", "task_id": "root1", "question": "What auth provider?", "context": "What auth provider?"}),
    ));
    events.push(Event::for_project(
        "p1",
        EventType::DecisionEscalated,
        "pm",
        json!({"decision_id": "dec1", "inbox_id": "i-dec", "prompt": "What auth provider?"}),
    ));

    let state = RuntimeState::replay(&events);
    assert_eq!(state.decisions["dec1"].status, DecisionStatus::Open);
    assert_eq!(state.decisions["dec1"].task_id, Some("root1".to_string()));
    assert_eq!(state.inbox["i-dec"].kind, InboxKind::Decision);
    assert_eq!(state.inbox["i-dec"].decision_id, Some("dec1".to_string()));
    assert_eq!(state.inbox["i-dec"].task_id, None);

    events.push(Event::for_project(
        "p1",
        EventType::DecisionResolved,
        "pm",
        json!({"inbox_id": "i-dec", "response": "Use OAuth"}),
    ));
    let state = RuntimeState::replay(&events);

    assert_eq!(state.decisions["dec1"].status, DecisionStatus::Resolved);
    assert_eq!(state.inbox["i-dec"].status, InboxStatus::Resolved);
    assert_eq!(state.inbox["i-dec"].response, Some("Use OAuth".to_string()));
}

#[test]
fn project_pause_resume_archive_transitions() {
    let mut events = setup_events();
    events.push(Event::global(EventType::ProjectPaused, "human", json!({"id": "p1"})));
    let state = RuntimeState::replay(&events);
    assert_eq!(state.projects["p1"].status, ProjectStatus::Paused);

    events.push(Event::global(EventType::ProjectResumed, "human", json!({"id": "p1"})));
    let state = RuntimeState::replay(&events);
    assert_eq!(state.projects["p1"].status, ProjectStatus::Active);

    events.push(Event::global(EventType::ProjectArchived, "human", json!({"id": "p1"})));
    let state = RuntimeState::replay(&events);
    assert_eq!(state.projects["p1"].status, ProjectStatus::Archived);
}

#[test]
fn task_retried_returns_blocked_task_to_ready_and_bumps_attempt() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::TaskBlocked,
        "a1",
        json!({"task_id": "t1", "reason": "flaky"}),
    ));
    events.push(Event::for_project("p1", EventType::TaskRetried, "human", json!({"task_id": "t1"})));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Ready);
    assert_eq!(state.tasks["t1"].attempt, 2);
}

#[test]
fn task_cancelled_frees_its_agent() {
    let mut events = setup_events();
    events.push(Event::for_project("p1", EventType::TaskCancelled, "human", json!({"task_id": "t1"})));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Cancelled);
    assert_eq!(state.tasks["t1"].assigned_agent_id, None);
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agent_instances["a1"].current_task_id, None);
}

#[test]
fn task_reassigned_moves_ownership_to_the_new_agent() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::AgentSpawned,
        "system",
        json!({"id": "a2", "template_id": "tmpl-worker", "role": "worker", "role_description": "Backend work", "parent_agent_id": None::<String>, "name": "Proj Backend 2"}),
    ));
    events.push(Event::for_project(
        "p1",
        EventType::TaskReassigned,
        "human",
        json!({"task_id": "t1", "old_agent_id": "a1", "new_agent_id": "a2"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Assigned);
    assert_eq!(state.tasks["t1"].assigned_agent_id, Some("a2".to_string()));
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
    assert_eq!(state.agent_instances["a1"].current_task_id, None);
    assert_eq!(state.agent_instances["a2"].status, AgentStatus::Assigned);
    assert_eq!(state.agent_instances["a2"].current_task_id, Some("t1".to_string()));
}

#[test]
fn review_changes_requested_returns_task_to_ready_and_frees_its_agent() {
    let mut events = setup_events();
    events.push(Event::for_project(
        "p1",
        EventType::ReviewChangesRequested,
        "human",
        json!({"task_id": "t1", "reason": "missing tests"}),
    ));

    let state = RuntimeState::replay(&events);

    assert_eq!(state.tasks["t1"].status, TaskStatus::Ready);
    assert_eq!(state.tasks["t1"].assigned_agent_id, None);
    assert_eq!(state.tasks["t1"].attempt, 2);
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
}

#[test]
fn agent_paused_and_resumed_transitions() {
    let mut events = setup_events();
    events.push(Event::for_project("p1", EventType::AgentPaused, "human", json!({"id": "a1"})));
    let state = RuntimeState::replay(&events);
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Paused);

    events.push(Event::for_project("p1", EventType::AgentResumed, "human", json!({"id": "a1"})));
    let state = RuntimeState::replay(&events);
    assert_eq!(state.agent_instances["a1"].status, AgentStatus::Idle);
}
