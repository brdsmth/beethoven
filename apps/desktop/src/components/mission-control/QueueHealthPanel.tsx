import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { computeProjectHealth } from "../../lib/projectHealth";
import { Panel } from "../Panel";

export function QueueHealthPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({
    queryKey: ["project", projectId],
    queryFn: () => api.getProject(projectId),
  });

  const health = data ? computeProjectHealth(data) : null;

  return (
    <Panel title="Queue Health">
      {health ? (
        <div className="grid grid-cols-3 gap-3 text-sm">
          <Stat label="Ready" value={health.readyTasks} />
          <Stat label="Running" value={health.runningTasks} />
          <Stat label="Blocked" value={health.blockedTasks} tone={health.blockedTasks > 0 ? "warn" : "neutral"} />
        </div>
      ) : (
        <div className="text-sm text-slate-500">Loading…</div>
      )}
    </Panel>
  );
}

function Stat({ label, value, tone = "neutral" }: { label: string; value: number; tone?: "warn" | "neutral" }) {
  const color = tone === "warn" && value > 0 ? "text-amber-400" : "text-slate-100";
  return (
    <div>
      <div className="text-xs uppercase tracking-wide text-slate-500">{label}</div>
      <div className={`text-lg font-semibold ${color}`}>{value}</div>
    </div>
  );
}
