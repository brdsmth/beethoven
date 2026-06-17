use anyhow::{anyhow, bail, Result};
use beethoven_core::{AgentStatus, Decision, DecisionStatus, InboxItem, InboxStatus, Runtime, Task, TaskStatus};
use beethoven_events::EventScope;
use clap::{Parser, Subcommand};
use std::path::{Path, PathBuf};

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

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();
    let home = beethoven_db::resolve_home(cli.home);

    beethoven_db::ensure_beethoven_home(&home)?;
    let store = beethoven_db::SqliteEventStore::connect(&beethoven_db::default_db_path(&home)).await?;
    let mut runtime = Runtime::load(
        Box::new(store),
        beethoven_db::projects_root(&home),
        Box::new(beethoven_agents::ClaudeCodeRuntime::default()),
    )
    .await?;

    match cli.command {
        Command::Run { project } => {
            let project_id = resolve_project(project.as_deref(), &runtime, &home)?;
            let report = runtime.run_tick(&project_id).await?;
            println!(
                "assigned={} completed={} needs_changes={} escalated={} subtasks_spawned={}",
                report.assigned, report.completed, report.needs_changes, report.escalated, report.subtasks_spawned
            );
        }
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
    for e in events {
        let scope = match &e.scope {
            EventScope::Global => "global".to_string(),
            EventScope::Project(id) => format!("project:{id}"),
        };
        println!(
            "{} {:<10} {:<21} actor={:<36} {}",
            e.timestamp.to_rfc3339(),
            scope,
            e.event_type.as_str(),
            e.actor_id.as_deref().unwrap_or("-"),
            serde_json::to_string(&e.payload)?
        );
    }
    Ok(())
}
