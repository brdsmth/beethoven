use beethoven_tauri::watcher::spawn_change_watcher;
use beethoven_tauri::AppState;
use tauri::Manager;

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
            app.manage(AppState { store, home });
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
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
