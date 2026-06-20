import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { Panel } from "../Panel";
import type { AgentHealthStatus } from "../../lib/types";

const HEALTH_COLOR: Record<AgentHealthStatus, string> = {
  healthy: "text-emerald-400",
  slow: "text-amber-400",
  stalled: "text-rose-400",
};

export function AgentHealthPanel({ projectId }: { projectId: string }) {
  const { data } = useQuery({ queryKey: ["agentHealth"], queryFn: api.getAgentHealth });

  const agents = (data ?? []).filter((a) => a.project_id === projectId);
  const attention = agents.filter((a) => a.health !== "healthy");
  const healthyCount = agents.length - attention.length;

  return (
    <Panel title="Agent Health">
      {agents.length === 0 ? (
        <div className="text-sm text-slate-500">No active agents.</div>
      ) : (
        <div className="flex flex-col gap-2">
          {attention.length === 0 ? (
            <div className="text-sm text-emerald-400">All agents healthy</div>
          ) : (
            <ul className="flex flex-col gap-2">
              {attention.map((a) => (
                <li key={a.agent_id} className="flex items-center justify-between text-sm">
                  <span className="text-slate-200">{a.agent_name}</span>
                  <span className={`text-xs font-semibold uppercase ${HEALTH_COLOR[a.health]}`}>{a.health}</span>
                </li>
              ))}
            </ul>
          )}
          {healthyCount > 0 && <div className="text-xs text-slate-500">{healthyCount} healthy</div>}
        </div>
      )}
    </Panel>
  );
}
