use beethoven_tauri::watcher::spawn_change_watcher;
use beethoven_tauri::AppState;
use tauri::Manager;
use tokio::sync::Mutex;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    tauri::Builder::default()
        .plugin(tauri_plugin_opener::init())
        .setup(|app| {
            let home = beethoven_db::resolve_home(None);
            beethoven_db::ensure_beethoven_home(&home)?;
            let db_path = beethoven_db::default_db_path(&home);
            let store = tauri::async_runtime::block_on(beethoven_db::SqliteEventStore::connect(
                &db_path,
            ))?;
            let watcher_store = store.clone();
            let runtime = tauri::async_runtime::block_on(beethoven_core::Runtime::load(
                Box::new(store.clone()),
                beethoven_db::projects_root(&home),
                Box::new(beethoven_agents::ClaudeCodeRuntime::default()),
            ))?;
            app.manage(AppState {
                store,
                home,
                runtime: Mutex::new(runtime),
                daemon: Mutex::new(None),
            });
            spawn_change_watcher(app.handle().clone(), watcher_store);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            beethoven_tauri::commands::get_projects,
            beethoven_tauri::commands::get_project,
            beethoven_tauri::commands::get_agents,
            beethoven_tauri::commands::get_tasks,
            beethoven_tauri::commands::get_inbox,
            beethoven_tauri::commands::get_timeline,
            beethoven_tauri::commands::runtime_status,
            beethoven_tauri::commands::get_active_execution,
            beethoven_tauri::commands::get_goal_header,
            beethoven_tauri::commands::get_workflow_map,
            beethoven_tauri::commands::get_home_dir,
            beethoven_tauri::commands::answer_inbox_item,
            beethoven_tauri::commands::resolve_decision,
            beethoven_tauri::commands::pause_project,
            beethoven_tauri::commands::resume_project,
            beethoven_tauri::commands::archive_project,
            beethoven_tauri::commands::retry_task,
            beethoven_tauri::commands::cancel_task,
            beethoven_tauri::commands::reassign_task,
            beethoven_tauri::commands::approve_review,
            beethoven_tauri::commands::request_review_changes,
            beethoven_tauri::commands::pause_agent,
            beethoven_tauri::commands::resume_agent,
            beethoven_tauri::commands::runtime_start,
            beethoven_tauri::commands::runtime_stop,
            beethoven_tauri::commands::runtime_restart,
            beethoven_tauri::commands::get_initiatives,
            beethoven_tauri::commands::get_decisions,
            beethoven_tauri::commands::get_workspaces,
            beethoven_tauri::commands::get_recent_diffs,
            beethoven_tauri::commands::get_task_detail,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
