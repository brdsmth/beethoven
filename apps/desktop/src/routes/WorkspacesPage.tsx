import { useQuery } from "@tanstack/react-query";
import { revealItemInDir } from "@tauri-apps/plugin-opener";
import { api } from "../lib/api";
import { pushToast } from "../lib/toast";
import { ActionButton } from "../components/ActionButton";
import { Panel } from "../components/Panel";

export function WorkspacesPage() {
  const { data } = useQuery({ queryKey: ["workspaces"], queryFn: api.getWorkspaces });

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Workspaces</h1>
      <Panel title="Active Workspaces">
        {!data || data.length === 0 ? (
          <div className="text-sm text-slate-500">No active workspaces.</div>
        ) : (
          <ul className="flex flex-col gap-3">
            {data.map((w) => (
              <li key={w.task_id} className="rounded-md border border-slate-800 p-3 text-sm">
                <div className="flex items-center justify-between">
                  <span className="font-medium text-slate-100">{w.task_title}</span>
                  <span className="text-xs text-slate-500">{w.project_slug}</span>
                </div>
                <div className="mt-1 text-xs text-slate-500">
                  {w.agent_name} · {w.branch}
                </div>
                <div className="mt-1 break-all text-xs text-slate-400">{w.git_worktree}</div>
                <div className="mt-2 flex flex-wrap items-center gap-2">
                  <ActionButton label="Open Workspace" onClick={() => revealItemInDir(w.git_worktree)} />
                  <ActionButton
                    label="Copy tmux attach"
                    onClick={() => {
                      navigator.clipboard.writeText(`tmux attach -t ${w.tmux_session}`);
                      pushToast("Copied to clipboard", "success");
                    }}
                  />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
