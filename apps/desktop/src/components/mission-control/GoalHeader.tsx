import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../../lib/api";

export function GoalHeader({ projectId }: { projectId: string }) {
  const { data } = useQuery({
    queryKey: ["goalHeader", projectId],
    queryFn: () => api.getGoalHeader(projectId),
  });

  if (!data) return null;

  const percent = Math.round(data.percent_complete);

  return (
    <div className="flex items-center justify-between rounded-lg border border-slate-800 bg-slate-900/60 p-5">
      <div>
        <div className="text-xs uppercase tracking-wide text-slate-500">{data.project_name}</div>
        {data.active_label ? (
          <div className="mt-1 text-2xl font-semibold text-slate-100">{data.active_label}</div>
        ) : (
          <div className="mt-1 flex items-center gap-2">
            <span className="text-2xl font-semibold text-slate-500">No active objective</span>
            <Link
              to={`/projects/${projectId}?tab=task_graph`}
              className="text-sm text-indigo-400 hover:underline"
            >
              New Goal →
            </Link>
          </div>
        )}
      </div>
      <div className="flex flex-col items-end gap-1">
        <div className="text-2xl font-bold text-indigo-300">{percent}% Complete</div>
        <div className="h-2 w-48 overflow-hidden rounded-full bg-slate-800">
          <div className="h-full bg-indigo-500" style={{ width: `${percent}%` }} />
        </div>
      </div>
    </div>
  );
}
