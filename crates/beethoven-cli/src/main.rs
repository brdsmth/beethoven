use anyhow::Result;
use beethoven_core::{Agent, Goal, InboxItem, InboxStatus, Runtime, Task};
use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(name = "beethoven", about = "Personal agentic software organization")]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    /// Run one orchestration tick: assign ready tasks, execute assigned tasks.
    Run,
    /// Show goals, tasks, agents, and open inbox counts.
    Status,
    /// List open inbox items, or answer one.
    Inbox {
        #[command(subcommand)]
        action: Option<InboxAction>,
    },
    /// Print the full event log in order.
    Timeline,
    /// Goal management.
    Goal {
        #[command(subcommand)]
        action: GoalAction,
    },
}

#[derive(Subcommand)]
enum GoalAction {
    /// Create a new goal. Use "- item" lines in the description to seed tasks deterministically.
    Create {
        title: String,
        #[arg(short, long, default_value = "")]
        description: String,
    },
    /// Run the Project Manager planner against a goal, generating tasks.
    Plan { goal_id: String },
}

#[derive(Subcommand)]
enum InboxAction {
    /// Answer an open question or decision, unblocking its task.
    Answer { inbox_id: String, response: String },
}

#[tokio::main]
async fn main() -> Result<()> {
    let cli = Cli::parse();

    beethoven_db::ensure_beethoven_home()?;
    let store = beethoven_db::SqliteEventStore::connect(&beethoven_db::default_db_path()).await?;
    let workspaces_dir = beethoven_db::beethoven_home().join("workspaces");
    let mut runtime = Runtime::load(Box::new(store), workspaces_dir).await?;

    match cli.command {
        Command::Run => {
            let report = runtime.run_tick().await?;
            println!(
                "assigned={} completed={} escalated={} subtasks_spawned={}",
                report.assigned, report.completed, report.escalated, report.subtasks_spawned
            );
        }
        Command::Status => print_status(&runtime),
        Command::Timeline => print_timeline(&runtime).await?,
        Command::Goal { action } => match action {
            GoalAction::Create { title, description } => {
                let goal = runtime.create_goal(&title, &description).await?;
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
        Command::Inbox { action } => match action {
            None => print_inbox(&runtime),
            Some(InboxAction::Answer { inbox_id, response }) => {
                let item = runtime.answer_inbox(&inbox_id, &response).await?;
                println!("Resolved inbox item {} ({:?})", item.id, item.kind);
            }
        },
    }

    Ok(())
}

fn print_status(runtime: &Runtime) {
    let mut goals: Vec<&Goal> = runtime.state.goals.values().collect();
    goals.sort_by_key(|g| g.created_at);
    println!("Goals ({}):", goals.len());
    for g in goals {
        println!("  [{:?}] {} \"{}\"", g.status, g.id, g.title);
    }

    let mut tasks: Vec<&Task> = runtime.state.tasks.values().collect();
    tasks.sort_by_key(|t| t.created_at);
    println!("\nTasks ({}):", tasks.len());
    for t in tasks {
        let parent = t
            .parent_task_id
            .as_deref()
            .map(|p| format!(" (subtask of {p})"))
            .unwrap_or_default();
        println!("  [{:?}] {} \"{}\"{}", t.status, t.id, t.title, parent);
    }

    let mut agents: Vec<&Agent> = runtime.state.agents.values().collect();
    agents.sort_by_key(|a| a.created_at);
    println!("\nAgents ({}):", agents.len());
    for a in agents {
        println!("  [{:?}] {} {:?}", a.status, a.id, a.kind);
    }

    let open_inbox = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.status == InboxStatus::Open)
        .count();
    println!("\nOpen inbox items: {open_inbox}");
}

fn print_inbox(runtime: &Runtime) {
    let mut items: Vec<&InboxItem> = runtime
        .state
        .inbox
        .values()
        .filter(|i| i.status == InboxStatus::Open)
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

async fn print_timeline(runtime: &Runtime) -> Result<()> {
    let events = runtime.list_events().await?;
    for e in events {
        println!(
            "{} {:<18} actor={:<36} {}",
            e.timestamp.to_rfc3339(),
            e.event_type.as_str(),
            e.actor_id,
            serde_json::to_string(&e.payload)?
        );
    }
    Ok(())
}
