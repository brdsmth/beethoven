import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useElapsedLabel } from "../../lib/hooks";
import { Panel } from "../Panel";
import type { ActiveExecution as ActiveExecutionItem } from "../../lib/types";

export function ActiveExecution({ projectId, title = "Running Work" }: { projectId?: string; title?: string }) {
  const { data } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });
  const items = (data ?? []).filter((item) => !projectId || item.project_id === projectId);

  return (
    <Panel title={title}>
      {items.length === 0 ? (
        <div className="text-sm text-slate-500">No tasks in progress.</div>
      ) : (
        <ul className="flex flex-col gap-2">
          {items.map((item) => (
            <ActiveExecutionRow key={item.task_id} item={item} />
          ))}
        </ul>
      )}
    </Panel>
  );
}

function ActiveExecutionRow({ item }: { item: ActiveExecutionItem }) {
  const elapsed = useElapsedLabel(item.started_at);
  return (
    <li className="flex items-center justify-between rounded-md border border-slate-800 bg-slate-900/60 px-3 py-2 text-sm">
      <div>
        <div className="font-medium text-slate-200">{item.task_title}</div>
        <div className="text-xs text-slate-500">
          {item.agent_name} · {item.project_slug}
        </div>
      </div>
      <div className="text-right">
        <div className="text-xs font-semibold text-emerald-400">Running</div>
        <div className="text-xs text-slate-500">{elapsed}</div>
      </div>
    </li>
  );
}
