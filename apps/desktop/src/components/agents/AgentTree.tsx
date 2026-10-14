import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Agent } from "../../lib/types";
import { ActionButton } from "../ActionButton";

export const AGENT_STATUS_COLOR: Record<Agent["status"], string> = {
  created: "text-slate-500",
  idle: "text-slate-500",
  assigned: "text-sky-400",
  executing: "text-indigo-300",
  completed: "text-emerald-400",
  paused: "text-amber-400",
  archived: "text-slate-600",
};

export const AGENT_STATUS_DOT: Record<Agent["status"], string> = {
  created: "bg-slate-500",
  idle: "bg-slate-500",
  assigned: "bg-sky-400",
  executing: "bg-indigo-400",
  completed: "bg-emerald-400",
  paused: "bg-amber-400",
  archived: "bg-slate-600",
};

export const AGENT_STATUS_BORDER: Record<Agent["status"], string> = {
  created: "border-slate-700",
  idle: "border-slate-700",
  assigned: "border-sky-800",
  executing: "border-indigo-800",
  completed: "border-emerald-800",
  paused: "border-amber-800",
  archived: "border-slate-700",
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
  const metaLine = agent.current_task_title ?? agent.role_description ?? null;

  return (
    <div
      className={`flex items-center gap-2.5 rounded-md px-2.5 py-1.5 text-sm ${
        selected ? "bg-indigo-500/20" : "hover:bg-slate-800/60"
      }`}
    >
      <span className={`h-1.5 w-1.5 flex-none rounded-full ${AGENT_STATUS_DOT[agent.status]}`} />
      <button onClick={() => onSelect(agent.id)} className="flex flex-1 flex-col overflow-hidden text-left">
        <span className="flex items-center gap-2">
          <span className="truncate font-medium text-slate-200">{agent.name}</span>
          <span className="flex-none rounded bg-slate-800 px-1.5 py-0.5 text-[10px] text-slate-400">{ROLE_BADGE[agent.role]}</span>
        </span>
        {metaLine && <span className="truncate text-xs text-slate-500">{metaLine}</span>}
      </button>
      <span
        className={`flex-none rounded border px-1.5 py-0.5 text-[10px] uppercase ${AGENT_STATUS_BORDER[agent.status]} ${AGENT_STATUS_COLOR[agent.status]}`}
      >
        {agent.status}
      </span>
      <div className="flex flex-none items-center gap-1.5">
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
