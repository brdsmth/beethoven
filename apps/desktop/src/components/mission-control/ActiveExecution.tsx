import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";
import { useElapsedLabel } from "../../lib/hooks";
import { Panel } from "../Panel";
import type { ActiveExecution as ActiveExecutionItem } from "../../lib/types";

const CAP = 10;

export function ActiveExecution() {
  const { data } = useQuery({ queryKey: ["activeExecution"], queryFn: api.getActiveExecution });
  const items = data ?? [];
  const visible = items.slice(0, CAP);

  return (
    <Panel title="Running Agents">
      {items.length === 0 ? (
        <div className="text-sm text-slate-500">No tasks in progress.</div>
      ) : (
        <div className="flex flex-col gap-2">
          <ul className="flex flex-col gap-2">
            {visible.map((item) => (
              <ActiveExecutionRow key={item.task_id} item={item} />
            ))}
          </ul>
          {items.length > CAP && (
            <Link to="/projects" className="text-xs text-slate-500 hover:text-slate-300">
              view all {items.length} running agents →
            </Link>
          )}
        </div>
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
        <div className="text-xs font-semibold text-indigo-300">Running</div>
        <div className="text-xs text-slate-500">{elapsed}</div>
      </div>
    </li>
  );
}
