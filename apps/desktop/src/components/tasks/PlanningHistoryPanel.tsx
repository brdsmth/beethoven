import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { formatRelativeTime } from "../../lib/format";
import { Panel } from "../Panel";

/// The full "Planner Timeline" -- every planning run for this project,
/// newest first, with what it generated and when.
export function PlanningHistoryPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({
    queryKey: ["planningHistory", projectId],
    queryFn: () => api.getPlanningHistory(projectId),
  });
  const runs = [...(data ?? [])].reverse();

  return (
    <Panel title="Planning History">
      {runs.length === 0 ? (
        <div className="text-sm text-slate-500">No planning runs yet.</div>
      ) : (
        <ul className="flex flex-col gap-2">
          {runs.map((run) => (
            <li key={run.run_id} className="rounded-md border border-slate-800 p-2 text-sm">
              <div className="flex items-center justify-between">
                <span className="font-medium text-slate-200">Planning Run #{run.sequence}</span>
                <span className="text-xs text-slate-500">{formatRelativeTime(run.started_at)}</span>
              </div>
              <div className="mt-1 text-xs text-slate-500">
                Generated: {run.task_count} Task(s), {run.dependency_count} Dependency(ies), {run.decision_count}{" "}
                Decision(s)
              </div>
            </li>
          ))}
        </ul>
      )}
    </Panel>
  );
}
