import type { Agent } from "../../lib/types";

const STATUS_COLOR: Record<Agent["status"], string> = {
  created: "text-slate-500",
  idle: "text-slate-500",
  assigned: "text-sky-400",
  planning: "text-sky-400",
  executing: "text-emerald-400",
  review: "text-amber-400",
  completed: "text-slate-500",
  blocked: "text-rose-400",
  waiting: "text-amber-400",
  escalated: "text-rose-400",
};

export function AgentTree({
  agents,
  selectedAgentId,
  onSelect,
}: {
  agents: Agent[];
  selectedAgentId: string | null;
  onSelect: (id: string) => void;
}) {
  const pm = agents.filter((a) => a.kind === "project_manager");
  const workers = agents.filter((a) => a.kind !== "project_manager");

  if (agents.length === 0) {
    return <div className="text-sm text-slate-500">No agents yet.</div>;
  }

  return (
    <div className="flex flex-col gap-1">
      {pm.map((p) => (
        <AgentRow key={p.id} agent={p} selected={p.id === selectedAgentId} onSelect={onSelect} />
      ))}
      <div className="ml-3 flex flex-col gap-1 border-l border-slate-800 pl-4">
        {workers.map((w) => (
          <AgentRow key={w.id} agent={w} selected={w.id === selectedAgentId} onSelect={onSelect} />
        ))}
      </div>
    </div>
  );
}

function AgentRow({
  agent,
  selected,
  onSelect,
}: {
  agent: Agent;
  selected: boolean;
  onSelect: (id: string) => void;
}) {
  return (
    <button
      onClick={() => onSelect(agent.id)}
      className={`flex items-center justify-between rounded-md px-3 py-2 text-left text-sm ${
        selected ? "bg-indigo-500/20" : "hover:bg-slate-800/60"
      }`}
    >
      <div>
        <span className="font-medium text-slate-200">{agent.name}</span>
        <span className="ml-2 text-xs text-slate-500">{agent.kind}</span>
      </div>
      <div className="flex items-center gap-3 text-xs">
        {agent.current_task_title && <span className="text-slate-400">{agent.current_task_title}</span>}
        <span className={STATUS_COLOR[agent.status]}>{agent.status}</span>
      </div>
    </button>
  );
}
