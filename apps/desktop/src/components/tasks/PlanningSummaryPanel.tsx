import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Panel } from "../Panel";

/// "What was planned, why it was planned, how it was planned" -- the most
/// recent planning run for this project, with its quality metrics and full
/// human-readable summary. Read-only; these numbers never influence
/// scheduling, only what's shown.
export function PlanningSummaryPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({
    queryKey: ["planningHistory", projectId],
    queryFn: () => api.getPlanningHistory(projectId),
  });
  const latest = data && data.length > 0 ? data[data.length - 1] : null;

  return (
    <Panel title="Latest Planning Run">
      {!latest ? (
        <div className="text-sm text-slate-500">No planning runs yet.</div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="text-sm text-slate-300">
            Planning Run #{latest.sequence} -- {latest.planned_task_title}
          </div>
          <div className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            <Stat label="Tasks" value={latest.task_count} />
            <Stat label="Leaf Tasks" value={latest.leaf_task_count} />
            <Stat label="Avg Task Length" value={`${Math.round(latest.average_task_length)} chars`} />
            <Stat label="Dependencies" value={latest.dependency_count} />
            <Stat label="Decisions" value={latest.decision_count} />
            <Stat label="Duration" value={`${latest.planning_duration_ms}ms`} />
          </div>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded bg-slate-950 p-2 text-xs text-slate-400">
            {latest.summary}
          </pre>
        </div>
      )}
    </Panel>
  );
}

function Stat({ label, value }: { label: string; value: string | number }) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className="text-lg font-semibold text-slate-100">{value}</div>
    </div>
  );
}
