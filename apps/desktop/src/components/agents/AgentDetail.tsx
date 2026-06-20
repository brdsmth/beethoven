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

  const { data: agentTemplates } = useQuery({ queryKey: ["agentTemplates"], queryFn: api.getAgentTemplates });
  const template = agentTemplates?.find((t) => t.id === agent.template_id);

  const { data: projectAgents } = useQuery({
    queryKey: ["agents", agent.project_id],
    queryFn: () => api.getAgents(agent.project_id),
  });
  const parentAgentName = projectAgents?.find((a) => a.id === agent.parent_agent_id)?.name ?? agent.parent_agent_id;

  const invalidateKeys = [["agents", agent.project_id]];
  const pause = useAction(api.pauseAgent, { successMessage: `${agent.name} paused`, invalidateKeys });
  const resume = useAction(api.resumeAgent, { successMessage: `${agent.name} resumed`, invalidateKeys });

  return (
    <div className="flex flex-col gap-4">
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
            <div className="text-xs uppercase tracking-wide text-slate-500">Role Description</div>
            <div className="text-slate-100">{agent.role_description}</div>
          </div>
          {agent.parent_agent_id && (
            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Parent Agent</div>
              <div className="text-slate-100">{parentAgentName}</div>
            </div>
          )}
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

      <Panel title="Configuration">
        {!template ? (
          <div className="text-sm text-slate-500">No template configured for this agent role.</div>
        ) : (
          <div className="flex flex-col gap-3 text-sm">
            <div className="grid grid-cols-2 gap-3">
              <div>
                <div className="text-xs uppercase tracking-wide text-slate-500">Role</div>
                <div className="text-slate-100">{template.label}</div>
              </div>
              <div>
                <div className="text-xs uppercase tracking-wide text-slate-500">Runtime Profile</div>
                <div className="text-slate-100">
                  {template.runtime_profile
                    ? `${template.runtime_profile.runtime_id} / ${template.runtime_profile.model}`
                    : "unresolved"}
                </div>
              </div>
            </div>

            {template.permissions.length > 0 && (
              <div>
                <div className="text-xs uppercase tracking-wide text-slate-500">Permissions</div>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {template.permissions.map((p) => (
                    <span key={p} className="rounded bg-slate-800 px-2 py-0.5 text-xs text-slate-300">
                      {p}
                    </span>
                  ))}
                </div>
              </div>
            )}

            {template.tools.length > 0 && (
              <div>
                <div className="text-xs uppercase tracking-wide text-slate-500">Tools</div>
                <div className="mt-1 flex flex-wrap gap-1.5">
                  {template.tools.map((t) => (
                    <span key={t} className="rounded bg-slate-800 px-2 py-0.5 text-xs text-slate-300">
                      {t}
                    </span>
                  ))}
                </div>
              </div>
            )}

            <div>
              <div className="text-xs uppercase tracking-wide text-slate-500">Prompt</div>
              <pre className="mt-1 max-h-64 overflow-auto rounded bg-slate-950 p-2 text-xs text-slate-400">
                {template.prompt}
              </pre>
            </div>
          </div>
        )}
      </Panel>
    </div>
  );
}
