import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { Panel } from "../components/Panel";

export function InitiativesPage() {
  const { data } = useQuery({ queryKey: ["initiatives"], queryFn: api.getInitiatives });

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Initiatives</h1>
      <Panel title="All Initiatives">
        {!data || data.length === 0 ? (
          <div className="text-sm text-slate-500">No initiatives yet.</div>
        ) : (
          <ul className="flex flex-col gap-3">
            {data.map((initiative) => (
              <li key={initiative.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <Link
                    to={`/projects/${initiative.project_id}`}
                    className="font-medium text-slate-100 hover:underline"
                  >
                    {initiative.title}
                  </Link>
                  <span className="text-xs uppercase text-slate-500">{initiative.status}</span>
                </div>
                <p className="mt-1 text-sm text-slate-400">{initiative.description}</p>
                <div className="mt-2 flex flex-wrap gap-4 text-xs text-slate-500">
                  <span>{initiative.project_name}</span>
                  <span>{initiative.requirement_count} requirements</span>
                  <span>{initiative.epic_count} epics</span>
                  <span>{initiative.task_count} tasks</span>
                  {initiative.open_decision_count > 0 && (
                    <span className="text-amber-400">{initiative.open_decision_count} open decisions</span>
                  )}
                </div>
              </li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
