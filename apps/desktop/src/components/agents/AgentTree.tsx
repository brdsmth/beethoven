import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Agent } from "../../lib/types";
import { ActionButton } from "../ActionButton";

const STATUS_COLOR: Record<Agent["status"], string> = {
  created: "text-slate-500",
  idle: "text-slate-500",
  assigned: "text-sky-400",
  executing: "text-emerald-400",
  completed: "text-slate-500",
  paused: "text-amber-400",
  archived: "text-slate-600",
};

const ROLE_BADGE: Record<Agent["role"], string> = {
  lead: "Lead",
  worker: "Worker",
  reviewer: "Reviewer",
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
  if (agents.length === 0) {
    return <div className="text-sm text-slate-500">No agents yet.</div>;
  }

  const lead = agents.find((a) => a.role === "lead");
  const children = agents
    .filter((a) => a.parent_agent_id === lead?.id)
    .sort((a, b) => b.created_at.localeCompare(a.created_at));

  return (
    <div className="flex flex-col gap-1">
      {lead && <AgentRow agent={lead} selected={lead.id === selectedAgentId} onSelect={onSelect} />}
      <div className="ml-3 flex flex-col gap-1 border-l border-slate-800 pl-4">
        {children.map((child) => (
          <AgentRow key={child.id} agent={child} selected={child.id === selectedAgentId} onSelect={onSelect} />
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
  const invalidateKeys = [["agents", agent.project_id]];
  const pause = useAction(api.pauseAgent, { successMessage: `${agent.name} paused`, invalidateKeys });
  const resume = useAction(api.resumeAgent, { successMessage: `${agent.name} resumed`, invalidateKeys });
  const archive = useAction(api.archiveAgent, { successMessage: `${agent.name} archived`, invalidateKeys });

  const canArchive = agent.role !== "lead" && (agent.status === "idle" || agent.status === "completed" || agent.status === "created");

  return (
    <div
      className={`flex items-center justify-between rounded-md px-3 py-2 text-left text-sm ${
        selected ? "bg-indigo-500/20" : "hover:bg-slate-800/60"
      }`}
    >
      <button onClick={() => onSelect(agent.id)} className="flex-1 text-left">
        <span className="font-medium text-slate-200">{agent.name}</span>
        <span className="ml-2 rounded bg-slate-800 px-1.5 py-0.5 text-xs text-slate-400">{ROLE_BADGE[agent.role]}</span>
        <span className="ml-2 text-xs text-slate-500">{agent.role_description}</span>
      </button>
      <div className="flex items-center gap-3 text-xs">
        {agent.current_task_title && <span className="text-slate-400">{agent.current_task_title}</span>}
        <span className={STATUS_COLOR[agent.status]}>{agent.status}</span>
        {agent.status === "idle" && (
          <ActionButton label="Pause" onClick={() => pause.mutate(agent.id)} pending={pause.isPending} />
        )}
        {agent.status === "paused" && (
          <ActionButton label="Resume" onClick={() => resume.mutate(agent.id)} pending={resume.isPending} />
        )}
        {canArchive && (
          <ActionButton label="Archive" onClick={() => archive.mutate(agent.id)} pending={archive.isPending} />
        )}
      </div>
    </div>
  );
}
