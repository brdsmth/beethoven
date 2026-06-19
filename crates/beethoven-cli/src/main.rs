use anyhow::{anyhow, bail, Result};
use beethoven_core::{AgentStatus, Decision, DecisionStatus, InboxItem, InboxStatus, Runtime, Task, TaskStatus};
use beethoven_events::{Event, EventScope, EventType};
use chrono::{DateTime, Utc};
use clap::{Parser, Subcommand};
use std::collections::HashMap;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

const DEFAULT_MAX_CONCURRENT: usize = 4;
const DEFAULT_MAX_ATTEMPTS: u32 = 3;

#[derive(Parser)]
#[command(name = "beethoven", about = "Personal agentic software organization")]
struct Cli {
    /// Override Beethoven's state directory (default: $BEETHOVEN_HOME or ~/.beethoven).
    #[arg(long, global = true)]
    home: Option<PathBuf>,

    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Run one orchestration tick: assign ready tasks, execute assigned tasks.
    Run {
        #[arg(long)]
        project: Option<String>,
        /// Max tasks within this project executing the model concurrently.
        #[arg(long, default_value_t = DEFAULT_MAX_CONCURRENT)]
        max_concurrent: usize,
        /// Automatic retries (review needs-changes, crash recovery) before
        /// giving up and escalating to the inbox instead.
        #[arg(long, default_value_t = DEFAULT_MAX_ATTEMPTS)]
        max_attempts: u32,
    },
    /// Run continuously: recover from any crashed prior run, then tick every
    /// project on an interval until interrupted (Ctrl-C or `beethoven stop`).
    Start {
        /// Seconds between orchestration ticks.
        #[arg(long, default_value_t = 5)]
        interval: u64,
        /// Max tasks executing the model concurrently, across all projects.
        #[arg(long, default_value_t = DEFAULT_MAX_CONCURRENT)]
        max_concurrent: usize,
        /// Automatic retries (review needs-changes, crash recovery) before
        /// giving up and escalating to the inbox instead.
        #[arg(long, default_value_t = DEFAULT_MAX_ATTEMPTS)]
        max_attempts: u32,
    },
    /// Stop an already-running daemon started with `beethoven start`.
    Stop,
    /// Inspect the daemon: whether it's running, recent start/stop history,
    /// and per-project/per-agent activity.
    Runtime {
        #[command(subcommand)]
        action: RuntimeAction,
    },
    /// Tail the event log, optionally scoped to one project.
    Logs {
        #[arg(long)]
        project: Option<String>,
        #[arg(long, default_value_t = 50)]
        limit: usize,
    },
    /// Show goal/task/inbox counts and active agents for a project.
    Status {
        #[arg(long)]
        project: Option<String>,
    },
    /// List open inbox items for a project, or answer one.
    Inbox {
        #[arg(long)]
        project: Option<String>,
        #[command(subcommand)]
        action: Option<InboxAction>,
    },
    /// Print the full event log (every project, in order).
    Timeline,
    /// Goal management.
    Goal {
        #[command(subcommand)]
        action: GoalAction,
    },
    /// Project management.
    Project {
        #[command(subcommand)]
        action: ProjectAction,
    },
    /// Initiative management (PM domain: discovery -> plan -> epics/decisions).
    Initiative {
        #[command(subcommand)]
        action: InitiativeAction,
    },
    /// Requirement management (gathered during an initiative's discovery phase).
    Requirement {
        #[command(subcommand)]
        action: RequirementAction,
    },
    /// List or resolve decisions escalated by the PM planner.
    Decision {
        #[arg(long)]
        project: Option<String>,
        #[command(subcommand)]
        action: Option<DecisionAction>,
    },
}

#[derive(Subcommand)]
enum GoalAction {
    /// Create a new goal. Use "- item" lines in the description to seed tasks deterministically.
    Create {
        title: String,
        #[arg(short, long, default_value = "")]
        description: String,
        #[arg(long)]
        project: Option<String>,
    },
    /// Run the Project Manager planner against a goal, generating tasks.
    Plan { goal_id: String },
}

#[derive(Subcommand)]
enum InboxAction {
    /// Answer an open question or decision, unblocking its task.
    Answer { inbox_id: String, response: String },
}

#[derive(Subcommand)]
enum ProjectAction {
    /// Create a project, mapped to exactly one repository.
    Create {
        name: String,
        #[arg(long)]
        repo: PathBuf,
        #[arg(long, default_value = "main")]
        default_branch: String,
    },
    /// List all projects; "*" marks the active one.
    List,
    /// Show details and counts for one project.
    Show { name: String },
    /// Switch the active project.
    Use { name: String },
    /// Print the active project's slug.
    Current,
}

#[derive(Subcommand)]
enum InitiativeAction {
    /// Create a new initiative, starting in the discovery phase.
    Create {
        title: String,
        #[arg(short, long, default_value = "")]
        description: String,
        #[arg(long)]
        project: Option<String>,
    },
    /// List initiatives for a project.
    List {
        #[arg(long)]
        project: Option<String>,
    },
    /// Show an initiative's requirements, epics, and decisions.
    Show { initiative_id: String },
    /// Run the deterministic PM planner against an initiative's requirements,
    /// generating epics (-> tasks) and decisions (-> inbox escalations).
    Plan { initiative_id: String },
}

#[derive(Subcommand)]
enum RequirementAction {
    /// Add a requirement to an initiative's discovery phase.
    Create { initiative_id: String, description: String },
    /// List requirements gathered for an initiative.
    List { initiative_id: String },
}

#[derive(Subcommand)]
enum DecisionAction {
    /// Resolve an open decision, unblocking dependent initiative completion.
    Resolve { decision_id: String, response: String },
}

#[derive(Subcommand)]
enum RuntimeAction {
    /// Show whether the daemon is running, its last start/stop, and
    /// per-project task/agent activity.
    Status,
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();
    let home = beethoven_db::resolve_home(cli.home);

    beethoven_db::ensure_beethoven_home(&home)?;
    let store = beethoven_db::SqliteEventStore::connect(&beethoven_db::default_db_path(&home)).await?;
    let mut runtime = Runtime::load(
        Box::new(store),
        beethoven_db::projects_root(&home),
        beethoven_agents::default_registry(),
    )
    .await?;

    match cli.command {
        Command::Run {
            project,
            max_concurrent,
            max_attempts,
        } => {
            let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
            let mut reports = runtime
                .tick_projects(std::slice::from_ref(&project_id), max_concurrent, max_attempts)
                .await?;
            let report = reports.remove(&project_id).unwrap_or_default();
            println!(
                "assigned={} completed={} needs_changes={} escalated={} subtasks_spawned={}",
                report.assigned, report.completed, report.needs_changes, report.escalated, report.subtasks_spawned
            );
        }
        Command::Start {
            interval,
            max_concurrent,
            max_attempts,
        } => run_daemon(&mut runtime, &home, interval, max_concurrent, max_attempts).await?,
        Command::Stop => stop_daemon(&home).await?,
        Command::Runtime { action } => match action {
            RuntimeAction::Status => print_runtime_status(&runtime, &home).await?,
        },
        Command::Logs { project, limit } => print_logs(&runtime, project.as_deref(), limit).await?,
        Command::Status { project } => {
            let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
            print_status(&runtime, &project_id);
        }
        Command::Timeline => print_timeline(&runtime).await?,
        Command::Goal { action } => match action {
            GoalAction::Create {
                title,
                description,
                project,
            } => {
                let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
                let goal = runtime.create_goal(&project_id, &title, &description).await?;
                println!("Created goal {} \"{}\"", goal.id, goal.title);
            }
            GoalAction::Plan { goal_id } => {
                let tasks = runtime.plan_goal(&goal_id).await?;
                println!("Planned {} task(s):", tasks.len());
                for t in tasks {
                    println!("  {} {}", t.id, t.title);
                }
            }
        },
        Command::Inbox { project, action } => match action {
            None => {
                let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
                print_inbox(&runtime, &project_id);
            }
            Some(InboxAction::Answer { inbox_id, response }) => {
                let item = runtime.answer_inbox(&inbox_id, &response).await?;
                println!("Resolved inbox item {} ({:?})", item.id, item.kind);
            }
        },
        Command::Project { action } => match action {
            ProjectAction::Create {
                name,
                repo,
                default_branch,
            } => {
                let project = runtime.create_project(&name, repo, &default_branch).await?;
                if beethoven_db::read_current_project(&home).is_none() {
                    beethoven_db::write_current_project(&home, &project.id)?;
                }
                println!("Created project {} ({})", project.slug, project.id);
            }
            ProjectAction::List => {
                let active = beethoven_db::read_current_project(&home);
                let mut projects: Vec<_> = runtime.state.projects.values().collect();
                projects.sort_by_key(|p| p.created_at);
                for p in projects {
                    let marker = if active.as_deref() == Some(p.id.as_str()) { "*" } else { " " };
                    println!("{marker} {}", p.slug);
                }
            }
            ProjectAction::Show { name } => {
                let project = runtime
                    .find_project(&name)
                    .ok_or_else(|| anyhow!("no such project: {name}"))?
                    .clone();
                println!("Name: {}", project.name);
                println!("Slug: {}", project.slug);
                println!("Repository: {}", project.repository_path.display());
                println!("Default branch: {}", project.default_branch);
                println!("Status: {:?}", project.status);

                let goals = runtime.state.goals.values().filter(|g| g.project_id == project.id).count();
                let tasks = runtime.state.tasks.values().filter(|t| t.project_id == project.id).count();
                let instances = runtime
                    .state
                    .agent_instances
                    .values()
                    .filter(|a| a.project_id == project.id)
                    .count();
                println!("Goals: {goals}  Tasks: {tasks}  Agent instances: {instances}");
            }
            ProjectAction::Use { name } => {
                let project = runtime
                    .find_project(&name)
                    .ok_or_else(|| anyhow!("no such project: {name}"))?;
                beethoven_db::write_current_project(&home, &project.id)?;
                println!("Switched to project {}", project.slug);
            }
            ProjectAction::Current => match beethoven_db::read_current_project(&home)
                .and_then(|id| runtime.find_project(&id).map(|p| p.slug.clone()))
            {
                Some(slug) => println!("{slug}"),
                None => println!("No active project. Use `beethoven project use <name>`."),
            },
        },
        Command::Initiative { action } => match action {
            InitiativeAction::Create {
                title,
                description,
                project,
            } => {
                let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
                let initiative = runtime.create_initiative(&project_id, &title, &description).await?;
                println!("Created initiative {} \"{}\"", initiative.id, initiative.title);
            }
            InitiativeAction::List { project } => {
                let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
                let mut initiatives: Vec<_> = runtime
                    .state
                    .initiatives
                    .values()
                    .filter(|i| i.project_id == project_id)
                    .collect();
                initiatives.sort_by_key(|i| i.created_at);
                for i in initiatives {
                    println!("{} [{:?}] {}", i.id, i.status, i.title);
                }
            }
            InitiativeAction::Show { initiative_id } => print_initiative(&runtime, &initiative_id)?,
            InitiativeAction::Plan { initiative_id } => {
                let outcome = runtime.plan_initiative(&initiative_id).await?;
                println!("{}", outcome.plan.summary);
                for e in &outcome.epics {
                    println!("  epic     {} {}", e.id, e.title);
                }
                for t in &outcome.tasks {
                    println!("  task     {} {}", t.id, t.title);
                }
                for d in &outcome.decisions {
                    println!("  decision {} {} (escalated to inbox)", d.id, d.title);
                }
            }
        },
        Command::Requirement { action } => match action {
            RequirementAction::Create {
                initiative_id,
                description,
            } => {
                let requirement = runtime.create_requirement(&initiative_id, &description).await?;
                println!("Created requirement {} \"{}\"", requirement.id, requirement.description);
            }
            RequirementAction::List { initiative_id } => {
                let mut requirements: Vec<_> = runtime
                    .state
                    .requirements
                    .values()
                    .filter(|r| r.initiative_id == initiative_id)
                    .collect();
                requirements.sort_by_key(|r| r.created_at);
                for r in requirements {
                    println!("{} {}", r.id, r.description);
                }
            }
        },
        Command::Decision { project, action } => match action {
            None => {
                let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
                print_decisions(&runtime, &project_id);
            }
            Some(DecisionAction::Resolve { decision_id, response }) => {
                let decision = runtime.resolve_decision(&decision_id, &response).await?;
                println!("Resolved decision {} ({:?})", decision.id, decision.status);
            }
        },
    }

    Ok(())
}

/// Writes the daemon's pidfile (refusing to start if a live process already
/// owns it), runs the daemon loop, then removes the pidfile on the way out
/// regardless of how the loop ended.
async fn run_daemon(runtime: &mut Runtime, home: &Path, interval: u64, max_concurrent: usize, max_attempts: u32) -> Result<()> {
    let pid_file = beethoven_db::pid_path(home);
    if let Some(existing_pid) = beethoven_db::read_pid(&pid_file) {
        if beethoven_db::is_pid_alive(existing_pid) {
            bail!("a daemon is already running (pid {existing_pid}); use `beethoven stop` first");
        }
    }
    std::fs::write(&pid_file, std::process::id().to_string())?;

    let result = run_daemon_loop(runtime, interval, max_concurrent, max_attempts).await;
    let _ = std::fs::remove_file(&pid_file);
    result
}

/// Recovers any tasks left mid-flight by a crashed prior run, then ticks
/// every active project (concurrently -- see `Runtime::tick_projects`) on a
/// fixed interval until interrupted via Ctrl-C or `beethoven stop` (SIGTERM).
async fn run_daemon_loop(runtime: &mut Runtime, interval: u64, max_concurrent: usize, max_attempts: u32) -> Result<()> {
    let recovery = runtime.recover(max_attempts).await?;
    if !recovery.recovered.is_empty() {
        println!(
            "Recovered {} task(s) interrupted by a previous crash: {}",
            recovery.recovered.len(),
            recovery.recovered.join(", ")
        );
    }
    if !recovery.escalated.is_empty() {
        println!(
            "Gave up on {} task(s) after repeated crashes (escalated to inbox): {}",
            recovery.escalated.len(),
            recovery.escalated.join(", ")
        );
    }

    runtime.mark_runtime_started().await?;
    println!(
        "Beethoven daemon started (tick every {interval}s, max {max_concurrent} concurrent tasks). \
         Press Ctrl-C, or run `beethoven stop` from another shell, to stop."
    );

    let shutdown = Arc::new(AtomicBool::new(false));
    {
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            if tokio::signal::ctrl_c().await.is_ok() {
                shutdown.store(true, Ordering::SeqCst);
            }
        });
    }
    #[cfg(unix)]
    {
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            if let Ok(mut sigterm) = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()) {
                sigterm.recv().await;
                shutdown.store(true, Ordering::SeqCst);
            }
        });
    }

    while !shutdown.load(Ordering::SeqCst) {
        let project_ids: Vec<String> = runtime.state.projects.keys().cloned().collect();
        if !project_ids.is_empty() {
            match runtime.tick_projects(&project_ids, max_concurrent, max_attempts).await {
                Ok(reports) => {
                    for (project_id, report) in reports {
                        let did_work = report.assigned > 0
                            || report.completed > 0
                            || report.needs_changes > 0
                            || report.escalated > 0
                            || report.subtasks_spawned > 0;
                        if did_work {
                            let slug =
                                runtime.state.projects.get(&project_id).map(|p| p.slug.clone()).unwrap_or(project_id);
                            println!(
                                "[{slug}] assigned={} completed={} needs_changes={} escalated={} subtasks_spawned={}",
                                report.assigned,
                                report.completed,
                                report.needs_changes,
                                report.escalated,
                                report.subtasks_spawned
                            );
                        }
                    }
                }
                Err(e) => eprintln!("tick failed: {e}"),
            }
        }

        if shutdown.load(Ordering::SeqCst) {
            break;
        }
        tokio::time::sleep(Duration::from_secs(interval)).await;
    }

    runtime.mark_runtime_stopped().await?;
    println!("Beethoven daemon stopped.");
    Ok(())
}

/// Signals an already-running daemon (found via its pidfile) to shut down,
/// the same clean way Ctrl-C does in its own terminal, and waits for it to
/// actually exit.
async fn stop_daemon(home: &Path) -> Result<()> {
    let pid_file = beethoven_db::pid_path(home);
    let Some(pid) = beethoven_db::read_pid(&pid_file) else {
        bail!("no daemon is running (no pidfile at {})", pid_file.display());
    };
    if !beethoven_db::is_pid_alive(pid) {
        let _ = std::fs::remove_file(&pid_file);
        bail!("no daemon is running (removed stale pidfile for pid {pid})");
    }

    std::process::Command::new("kill").arg("-TERM").arg(pid.to_string()).status()?;
    println!("Sent stop signal to daemon (pid {pid}), waiting for it to exit...");

    for _ in 0..50 {
        if !beethoven_db::is_pid_alive(pid) {
            println!("Daemon stopped.");
            return Ok(());
        }
        tokio::time::sleep(Duration::from_millis(200)).await;
    }
    bail!("daemon (pid {pid}) did not exit within 10s of being sent SIGTERM");
}

async fn print_runtime_status(runtime: &Runtime, home: &Path) -> Result<()> {
    let pid_file = beethoven_db::pid_path(home);
    match beethoven_db::read_pid(&pid_file) {
        Some(pid) if beethoven_db::is_pid_alive(pid) => println!("Daemon: running (pid {pid})"),
        Some(pid) => println!("Daemon: not running (stale pidfile for pid {pid})"),
        None => println!("Daemon: not running"),
    }

    let events = runtime.list_events().await?;
    let last_started = events.iter().rev().find(|e| e.event_type == EventType::RuntimeStarted);
    let last_stopped = events.iter().rev().find(|e| e.event_type == EventType::RuntimeStopped);
    match last_started {
        Some(e) => println!("Last started: {}", e.timestamp.to_rfc3339()),
        None => println!("Last started: never"),
    }
    match last_stopped {
        Some(e) => println!("Last stopped: {}", e.timestamp.to_rfc3339()),
        None => println!("Last stopped: never"),
    }

    let mut last_heartbeat: HashMap<String, DateTime<Utc>> = HashMap::new();
    for e in &events {
        if e.event_type == EventType::AgentHeartbeat {
            if let Some(agent_id) = e.payload.get("agent_id").and_then(|v| v.as_str()) {
                last_heartbeat
                    .entry(agent_id.to_string())
                    .and_modify(|ts| *ts = (*ts).max(e.timestamp))
                    .or_insert(e.timestamp);
            }
        }
    }

    let mut projects: Vec<_> = runtime.state.projects.values().collect();
    projects.sort_by_key(|p| p.created_at);
    for project in projects {
        let tasks: Vec<&Task> = runtime.state.tasks.values().filter(|t| t.project_id == project.id).collect();
        let ready = tasks.iter().filter(|t| t.status == TaskStatus::Ready).count();
        let in_progress = tasks.iter().filter(|t| t.status == TaskStatus::InProgress).count();
        let blocked = tasks.iter().filter(|t| t.status == TaskStatus::Blocked).count();
        let done = tasks.iter().filter(|t| t.status == TaskStatus::Done).count();
        println!(
            "\n[{}] tasks: ready={ready} in_progress={in_progress} blocked={blocked} done={done} (of {})",
            project.slug,
            tasks.len()
        );

        let mut agents: Vec<_> =
            runtime.state.agent_instances.values().filter(|a| a.project_id == project.id).collect();
        agents.sort_by_key(|a| a.name.clone());
        for agent in agents {
            let heartbeat = last_heartbeat.get(&agent.id).map(|ts| ts.to_rfc3339()).unwrap_or_else(|| "never".to_string());
            println!("  {:<24} {:?} last_heartbeat={heartbeat}", agent.name, agent.status);
        }
    }

    Ok(())
}

fn format_event(e: &Event) -> Result<String> {
    let scope = match &e.scope {
        EventScope::Global => "global".to_string(),
        EventScope::Project(id) => format!("project:{id}"),
    };
    Ok(format!(
        "{} {:<10} {:<21} actor={:<36} {}",
        e.timestamp.to_rfc3339(),
        scope,
        e.event_type.as_str(),
        e.actor_id.as_deref().unwrap_or("-"),
        serde_json::to_string(&e.payload)?
    ))
}

async fn print_logs(runtime: &Runtime, project_filter: Option<&str>, limit: usize) -> Result<()> {
    let project_id_filter = match project_filter {
        Some(slug_or_id) => Some(
            runtime
                .find_project(slug_or_id)
                .map(|p| p.id.clone())
                .ok_or_else(|| anyhow!("no such project: {slug_or_id}"))?,
        ),
        None => None,
    };

    let events = runtime.list_events().await?;
    let filtered: Vec<&Event> = events
        .iter()
        .filter(|e| match &project_id_filter {
            Some(pid) => e.scope.project_id() == Some(pid.as_str()),
            None => true,
        })
        .collect();

    let start = filtered.len().saturating_sub(limit);
    for e in &filtered[start..] {
        println!("{}", format_event(e)?);
    }
    Ok(())
}

fn resolve_project(explicit: Option<&str>, runtime: &Runtime, home: &Path) -> Result<String> {
    if let Some(slug_or_id) = explicit {
        return runtime
            .find_project(slug_or_id)
            .map(|p| p.id.clone())
            .ok_or_else(|| anyhow!("no such project: {slug_or_id}"));
    }

    let active = beethoven_db::read_current_project(home).ok_or_else(|| {
        anyhow!("no project specified and no active project set; use --project or `beethoven project use <name>`")
    })?;
    if runtime.find_project(&active).is_none() {
        bail!("active project {active} no longer exists; use `beethoven project use <name>`");
    }
    Ok(active)
}

fn print_status(runtime: &Runtime, project_id: &str) {
    let project = runtime.state.projects.get(project_id).expect("resolved project exists");
    println!("Project: {}\n", project.name);

    let goals = runtime.state.goals.values().filter(|g| g.project_id == project_id).count();
    let tasks: Vec<&Task> = runtime.state.tasks.values().filter(|t| t.project_id == project_id).collect();
    let blocked = tasks.iter().filter(|t| t.status == TaskStatus::Blocked).count();
    let inbox = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.project_id == project_id && i.status == InboxStatus::Open)
        .count();
    let active_agents = runtime
        .state
        .agent_instances
        .values()
        .filter(|a| a.project_id == project_id && matches!(a.status, AgentStatus::Assigned | AgentStatus::Executing))
        .count();

    println!("Goals: {goals}");
    println!("Tasks: {}", tasks.len());
    println!("Blocked: {blocked}");
    println!("Inbox: {inbox}");
    println!();
    println!("Agents Active: {active_agents}");
}

fn print_inbox(runtime: &Runtime, project_id: &str) {
    let mut items: Vec<&InboxItem> = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.project_id == project_id && i.status == InboxStatus::Open)
        .collect();
    items.sort_by_key(|i| i.created_at);

    if items.is_empty() {
        println!("Inbox is empty.");
        return;
    }
    for i in items {
        println!("[{:?}] {} {}", i.kind, i.id, i.prompt);
    }
}

fn print_initiative(runtime: &Runtime, initiative_id: &str) -> Result<()> {
    let initiative = runtime
        .state
        .initiatives
        .get(initiative_id)
        .ok_or_else(|| anyhow!("no such initiative: {initiative_id}"))?;
    println!("Title: {}", initiative.title);
    println!("Description: {}", initiative.description);
    println!("Status: {:?}", initiative.status);

    println!("\nRequirements:");
    let mut requirements: Vec<_> = runtime
        .state
        .requirements
        .values()
        .filter(|r| r.initiative_id == initiative_id)
        .collect();
    requirements.sort_by_key(|r| r.created_at);
    for r in requirements {
        println!("  {} {}", r.id, r.description);
    }

    println!("\nEpics:");
    let mut epics: Vec<_> = runtime.state.epics.values().filter(|e| e.initiative_id == initiative_id).collect();
    epics.sort_by_key(|e| e.created_at);
    for e in epics {
        println!("  {} {}", e.id, e.title);
    }

    println!("\nDecisions:");
    let mut decisions: Vec<_> = runtime
        .state
        .decisions
        .values()
        .filter(|d| d.initiative_id == initiative_id)
        .collect();
    decisions.sort_by_key(|d| d.created_at);
    for d in decisions {
        println!("  {} [{:?}] {}", d.id, d.status, d.title);
    }

    Ok(())
}

fn print_decisions(runtime: &Runtime, project_id: &str) {
    let mut items: Vec<&Decision> = runtime
        .state
        .decisions
        .values()
        .filter(|d| d.project_id == project_id && d.status == DecisionStatus::Open)
        .collect();
    items.sort_by_key(|d| d.created_at);

    if items.is_empty() {
        println!("No open decisions.");
        return;
    }
    for d in items {
        println!("{} {}", d.id, d.title);
    }
}

async fn print_timeline(runtime: &Runtime) -> Result<()> {
    let events = runtime.list_events().await?;
    for e in &events {
        println!("{}", format_event(e)?);
    }
    Ok(())
}
