import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { formatRelativeTime } from "../lib/format";
import { ActionButton } from "../components/ActionButton";
import { Panel } from "../components/Panel";
import { TaskDetailModal } from "../components/TaskDetailModal";

export function PromptsPage() {
  const { data } = useQuery({ queryKey: ["recentPrompts"], queryFn: () => api.getRecentPrompts() });
  const [openTaskId, setOpenTaskId] = useState<string | null>(null);

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Prompts</h1>
      <Panel title="Recent Prompts">
        {!data || data.length === 0 ? (
          <div className="text-sm text-slate-500">No prompts captured yet.</div>
        ) : (
          <ul className="flex flex-col gap-3">
            {data.map((p) => (
              <li key={p.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <span className="font-medium text-slate-100">{p.task_title}</span>
                  <span className="text-xs text-slate-500">{formatRelativeTime(p.created_at)}</span>
                </div>
                <div className="mt-1 text-xs text-slate-500">{p.project_slug}</div>
                <pre className="mt-2 max-h-40 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                  {p.content}
                </pre>
                <div className="mt-2">
                  <ActionButton label="View Task" onClick={() => setOpenTaskId(p.task_id)} />
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      {openTaskId && <TaskDetailModal taskId={openTaskId} onClose={() => setOpenTaskId(null)} />}
    </div>
  );
}
