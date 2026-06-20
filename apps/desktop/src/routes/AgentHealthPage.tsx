import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { api } from "../lib/api";
import { formatRelativeTime } from "../lib/format";
import { Panel } from "../components/Panel";
import type { AgentHealth, AgentHealthStatus } from "../lib/types";

const HEALTH_COLOR: Record<AgentHealthStatus, string> = {
  healthy: "text-emerald-400",
  slow: "text-amber-400",
  stalled: "text-rose-400",
};

const HEALTH_ORDER: Record<AgentHealthStatus, number> = {
  stalled: 0,
  slow: 1,
  healthy: 2,
};

export function AgentHealthPage() {
  const { data } = useQuery({ queryKey: ["agentHealth"], queryFn: api.getAgentHealth });

  const groups = groupByProject(data ?? []);

  return (
    <div className="flex flex-col gap-4">
      <h1 className="text-lg font-semibold text-slate-100">Agent Health</h1>
      {!data || data.length === 0 ? (
        <Panel title="Agent Health">
          <div className="text-sm text-slate-500">No active agents to monitor.</div>
        </Panel>
      ) : (
        groups.map(([projectSlug, agents]) => (
          <Panel key={projectSlug} title={projectSlug}>
            <ul className="flex flex-col gap-3">
              {agents.map((a) => (
                <li
                  key={a.agent_id}
                  className="flex items-center justify-between rounded-md border border-slate-800 p-3"
                >
                  <div>
                    <div className="flex items-center gap-2">
                      <span className="font-medium text-slate-100">{a.agent_name}</span>
                      <span className="text-xs text-slate-500">{a.role_description}</span>
                    </div>
                    {a.current_task_title && (
                      <Link
                        to={`/projects/${a.project_id}`}
                        className="mt-1 block text-xs text-slate-400 hover:underline"
                      >
                        {a.current_task_title}
                      </Link>
                    )}
                  </div>
                  <div className="flex items-center gap-3 text-xs">
                    <span className="text-slate-500">
                      {a.last_heartbeat ? `heartbeat ${formatRelativeTime(a.last_heartbeat)}` : "no heartbeat"}
                    </span>
                    <span className={`font-semibold uppercase ${HEALTH_COLOR[a.health]}`}>{a.health}</span>
                  </div>
                </li>
              ))}
            </ul>
          </Panel>
        ))
      )}
    </div>
  );
}

function groupByProject(agents: AgentHealth[]): [string, AgentHealth[]][] {
  const byProject = new Map<string, AgentHealth[]>();
  for (const agent of agents) {
    const list = byProject.get(agent.project_slug) ?? [];
    list.push(agent);
    byProject.set(agent.project_slug, list);
  }
  for (const list of byProject.values()) {
    list.sort((a, b) => HEALTH_ORDER[a.health] - HEALTH_ORDER[b.health]);
  }
  return Array.from(byProject.entries()).sort(([a], [b]) => a.localeCompare(b));
}
