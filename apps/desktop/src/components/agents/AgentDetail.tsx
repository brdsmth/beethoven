import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import { formatRelativeTime, humanizeEventType } from "../../lib/format";
import { ActionButton } from "../ActionButton";
import { Panel } from "../Panel";
import type { Agent } from "../../lib/types";

export function AgentDetail({ agent }: { agent: Agent }) {
  const { data: events } = useQuery({
    queryKey: ["timeline", { agentId: agent.id }],
    queryFn: () => api.getTimeline({ agentId: agent.id }),
  });
  const { data: workspaces } = useQuery({
    queryKey: ["workspaces"],
    queryFn: api.getWorkspaces,
  });
  const workspace = workspaces?.find((w) => w.agent_id === agent.id);

  const invalidateKeys = [["agents", agent.project_id]];
  const pause = useAction(api.pauseAgent, { successMessage: `${agent.name} paused`, invalidateKeys });
  const resume = useAction(api.resumeAgent, { successMessage: `${agent.name} resumed`, invalidateKeys });

  return (
    <Panel title={agent.name}>
      <div className="flex items-center justify-end gap-2">
        {agent.status === "idle" && (
          <ActionButton label="Pause" onClick={() => pause.mutate(agent.id)} pending={pause.isPending} />
        )}
        {agent.status === "paused" && (
          <ActionButton label="Resume" onClick={() => resume.mutate(agent.id)} pending={resume.isPending} />
        )}
      </div>

      <div className="mt-3 grid grid-cols-2 gap-3 text-sm">
        <div>
          <div className="text-xs uppercase tracking-wide text-slate-500">Status</div>
          <div className="text-slate-100">{agent.status}</div>
        </div>
        <div>
          <div className="text-xs uppercase tracking-wide text-slate-500">Current Task</div>
          <div className="text-slate-100">{agent.current_task_title ?? "—"}</div>
        </div>
        <div>
          <div className="text-xs uppercase tracking-wide text-slate-500">Last Heartbeat</div>
          <div className="text-slate-100">
            {agent.last_heartbeat ? formatRelativeTime(agent.last_heartbeat) : "—"}
          </div>
        </div>
        {workspace && (
          <>
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Branch</div>
              <div className="text-slate-100">{workspace.branch}</div>
            </div>
            <div className="col-span-2">
              <div className="text-xs uppercase tracking-wide text-slate-500">Workspace</div>
              <div className="break-all text-xs text-slate-300">{workspace.git_worktree}</div>
            </div>
            <div className="col-span-2">
              <div className="text-xs uppercase tracking-wide text-slate-500">tmux</div>
              <div className="text-xs text-slate-300">tmux attach -t {workspace.tmux_session}</div>
            </div>
          </>
        )}
      </div>

      <div className="mt-4">
        <div className="mb-2 text-xs uppercase tracking-wide text-slate-500">Recent Activity</div>
        {!events || events.length === 0 ? (
          <div className="text-sm text-slate-500">No recent activity.</div>
        ) : (
          <ul className="flex flex-col gap-1">
            {events.slice(0, 10).map((e) => (
              <li key={e.id} className="flex items-center justify-between text-sm">
                <span className="text-slate-300">{humanizeEventType(e.event_type)}</span>
                <span className="text-xs text-slate-500">{formatRelativeTime(e.timestamp)}</span>
              </li>
            ))}
          </ul>
        )}
      </div>
    </Panel>
  );
}
