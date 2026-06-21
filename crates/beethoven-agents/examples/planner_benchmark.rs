//! A fixed set of representative goals, run through whatever `PlanningService::plan`
//! currently is, with the output captured as a comparable report.
//!
//! Purpose: before Phase 2.5A (or any future planner change) lands, every planner
//! version gets evaluated against the *same* inputs, so "did planning quality
//! improve" has an actual answer instead of "it changed, and we eyeballed four
//! examples." `planner-benchmark-baseline.json` (checked into git, alongside this
//! file) is today's stub bullet-parser's output across all fixtures -- diff a
//! fresh run's report against it after any planner change.
//!
//! Run with: `cargo run --example planner_benchmark -p beethoven-agents`
//! Optional: `-- --out path/to/report.json` to write somewhere other than the
//! default `crates/beethoven-agents/examples/planner-benchmark-latest.json`.

use beethoven_agents::PlanningService;
use serde::Serialize;

/// (name, title, description) -- `name` is just a stable slug for the report,
/// independent of `title` so renaming a goal's title later doesn't silently
/// break a diff against an older report.
///
/// Deliberately not all bullet-formatted: today's planner only ever does
/// anything with `- `/`* ` prefixed lines, so most of these (realistic prose,
/// the way an actual goal description reads) exercise its fallback path --
/// that poverty is exactly the baseline Phase 2.5A needs to beat. A handful
/// are bullet-formatted to exercise today's actual happy path, and two are
/// deliberately ambiguous/contradictory to give a future model-driven planner
/// something to flag as a Decision instead of silently decomposing.
const FIXTURES: &[(&str, &str, &str)] = &[
    (
        "oauth-authentication",
        "Build OAuth Authentication",
        "Add OAuth-based login to the app, supporting Google and GitHub as providers, \
         with secure token storage and session management.",
    ),
    (
        "gmail-integration",
        "Build Gmail Integration",
        "Integrate with Gmail so users can connect their account and the app can read \
         and send email on their behalf, respecting OAuth scopes and rate limits.",
    ),
    (
        "procurement-system",
        "Build Procurement System",
        "Build an internal procurement system where employees can request purchases, \
         managers approve them, and finance can track spend against budgets.",
    ),
    (
        "used-sailboat-marketplace",
        "Build Used Sailboat Marketplace",
        "Build a marketplace where people can list used sailboats for sale, search and \
         filter listings, and message sellers.",
    ),
    (
        "realtime-notifications",
        "Add Real-Time Notifications",
        "Add a notification system that alerts users in real time when something \
         relevant happens, with both in-app and email delivery, and a way to manage \
         notification preferences.",
    ),
    (
        "support-ticketing",
        "Build a Customer Support Ticketing System",
        "Build a system where customers can submit support tickets, support agents can \
         triage and respond, and managers can see SLA compliance metrics.",
    ),
    (
        "multi-tenant-billing",
        "Add Multi-Tenant Billing",
        "Add subscription billing so each tenant organization can be charged based on \
         usage, with invoicing, payment retries, and a self-serve billing portal.",
    ),
    (
        "recipe-sharing-app",
        "Build a Recipe Sharing App",
        "Build an app where users can post recipes with photos and ingredients, search \
         by ingredient or cuisine, and save favorites.",
    ),
    (
        "two-factor-auth",
        "Add Two-Factor Authentication",
        "Add two-factor authentication using TOTP and SMS as a second factor, with \
         backup codes and the ability for users to manage their enrolled devices.",
    ),
    (
        "job-board",
        "Build a Job Board",
        "Build a job board where companies can post job listings and candidates can \
         search, filter, and apply, with employers able to track applicants through a \
         hiring pipeline.",
    ),
    (
        "mysql-to-postgres-migration",
        "Migrate the Database from MySQL to Postgres",
        "Migrate the production database from MySQL to Postgres with zero downtime, \
         including schema translation, data migration, and dual-write verification \
         before cutover.",
    ),
    (
        "internal-analytics-dashboard",
        "Build an Internal Analytics Dashboard",
        "Build a dashboard for internal stakeholders to see key business metrics, with \
         filtering by date range and team, and the ability to export charts.",
    ),
    (
        "realtime-collaborative-editing",
        "Add Real-Time Collaborative Editing",
        "Add real-time collaborative editing to documents so multiple users can edit \
         the same document simultaneously and see each other's cursors and changes \
         live.",
    ),
    (
        "ambiguous-new-market",
        "Expand Into a New Market",
        // Deliberately underspecified: no market named, no definition of what
        // changes. A good planner should raise a Decision, not guess.
        "Expand the product to support a new market.",
    ),
    (
        "contradictory-offline-realtime",
        "Support Full Offline Mode and Instant Real-Time Sync",
        // Deliberately self-contradictory: "no network calls" and "instant
        // updates from other users" can't both hold. A good planner should
        // surface the conflict rather than silently pick one.
        "The app must work fully offline with no network calls at all, and it must \
         also show real-time updates from other users instantly.",
    ),
    (
        "oauth-bulleted-happy-path",
        "Build OAuth Authentication",
        "- Add login with Google\n\
         - Add login with GitHub\n\
         - Store tokens securely\n\
         - What should happen to existing email/password accounts when OAuth is added?",
    ),
    (
        "billing-bulleted-no-questions",
        "Add Multi-Tenant Billing",
        "- Add a billing plan model with monthly and annual tiers\n\
         - Add Stripe as a payment provider\n\
         - Add an invoice history page\n\
         - Add automatic retry for failed payments",
    ),
];

#[derive(Debug, Serialize)]
struct TaskResult {
    title: String,
    reason: String,
}

#[derive(Debug, Serialize)]
struct DecisionResult {
    question: String,
    reason: String,
}

#[derive(Debug, Serialize)]
struct GoalResult {
    name: String,
    title: String,
    description: String,
    task_count: usize,
    decision_count: usize,
    /// True when the planner produced exactly its no-input fallback (a
    /// single "Implement: {title}" task) rather than anything derived from
    /// the description -- the precise failure mode Phase 2.5A exists to fix.
    fell_back_to_generic_task: bool,
    tasks: Vec<TaskResult>,
    decisions: Vec<DecisionResult>,
}

#[derive(Debug, Serialize)]
struct AggregateMetrics {
    goal_count: usize,
    total_tasks: usize,
    total_decisions: usize,
    average_tasks_per_goal: f64,
    fallback_count: usize,
}

#[derive(Debug, Serialize)]
struct BenchmarkReport {
    /// Update this when the planner implementation changes -- it's the
    /// label a future diff actually keys off of, not the filename.
    planner: &'static str,
    generated_at: String,
    goals: Vec<GoalResult>,
    metrics: AggregateMetrics,
}

fn run() -> BenchmarkReport {
    let mut goals = Vec::with_capacity(FIXTURES.len());
    let mut total_tasks = 0usize;
    let mut total_decisions = 0usize;
    let mut fallback_count = 0usize;

    for (name, title, description) in FIXTURES {
        let outcome = PlanningService::plan(title, description);

        let fell_back_to_generic_task =
            outcome.task_drafts.len() == 1 && outcome.decision_drafts.is_empty() && outcome.task_drafts[0].title == format!("Implement: {title}");
        if fell_back_to_generic_task {
            fallback_count += 1;
        }

        total_tasks += outcome.task_drafts.len();
        total_decisions += outcome.decision_drafts.len();

        goals.push(GoalResult {
            name: name.to_string(),
            title: title.to_string(),
            description: description.to_string(),
            task_count: outcome.task_drafts.len(),
            decision_count: outcome.decision_drafts.len(),
            fell_back_to_generic_task,
            tasks: outcome
                .task_drafts
                .iter()
                .map(|d| TaskResult { title: d.title.clone(), reason: d.reason.clone() })
                .collect(),
            decisions: outcome
                .decision_drafts
                .iter()
                .map(|d| DecisionResult { question: d.question.clone(), reason: d.reason.clone() })
                .collect(),
        });
    }

    let goal_count = goals.len();
    BenchmarkReport {
        planner: "stub-bullet-parser",
        generated_at: chrono::Utc::now().to_rfc3339(),
        goals,
        metrics: AggregateMetrics {
            goal_count,
            total_tasks,
            total_decisions,
            average_tasks_per_goal: if goal_count == 0 { 0.0 } else { total_tasks as f64 / goal_count as f64 },
            fallback_count,
        },
    }
}

fn print_human_readable(report: &BenchmarkReport) {
    println!("Planner Benchmark -- planner: {}", report.planner);
    println!("Generated: {}\n", report.generated_at);

    for goal in &report.goals {
        println!("=== {} ({}) ===", goal.title, goal.name);
        if goal.fell_back_to_generic_task {
            println!("  [FALLBACK] no decomposition -- single generic task generated");
        }
        for t in &goal.tasks {
            println!("  task     : {}", t.title);
            println!("             reason: {}", t.reason);
        }
        for d in &goal.decisions {
            println!("  decision : {}", d.question);
            println!("             reason: {}", d.reason);
        }
        println!();
    }

    println!("--- Aggregate ---");
    println!("Goals:                 {}", report.metrics.goal_count);
    println!("Total tasks:            {}", report.metrics.total_tasks);
    println!("Total decisions:        {}", report.metrics.total_decisions);
    println!("Average tasks/goal:     {:.2}", report.metrics.average_tasks_per_goal);
    println!(
        "Fell back to generic:   {}/{}",
        report.metrics.fallback_count, report.metrics.goal_count
    );
}

fn main() {
    let report = run();
    print_human_readable(&report);

    let mut out_path = std::path::PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .join("examples")
        .join("planner-benchmark-latest.json");
    let mut args = std::env::args().skip(1);
    while let Some(arg) = args.next() {
        if arg == "--out" {
            if let Some(path) = args.next() {
                out_path = std::path::PathBuf::from(path);
            }
        }
    }

    let json = serde_json::to_string_pretty(&report).expect("BenchmarkReport always serializes");
    std::fs::write(&out_path, json).expect("failed to write benchmark report");
    println!("\nWrote {}", out_path.display());
}
