import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Agent, ProjectDetail as ProjectDetailData, Task, TaskNode } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";
import { Panel } from "../Panel";
import { RespondField } from "../RespondField";
import { TaskDetailModal } from "../TaskDetailModal";

const STATUS_COLOR: Record<ProjectDetailData["status"], string> = {
  active: "text-emerald-400",
  paused: "text-amber-400",
  archived: "text-slate-500",
};

export function ProjectDetail({ project }: { project: ProjectDetailData }) {
  const [confirmArchive, setConfirmArchive] = useState(false);

  const { data: agents } = useQuery({
    queryKey: ["agents", project.id],
    queryFn: () => api.getAgents(project.id),
  });
  const idleAgents = (agents ?? []).filter((a) => a.status === "idle" && a.role !== "lead");

  const invalidateKeys = [["projects"], ["project", project.id]];
  const pause = useAction(api.pauseProject, { successMessage: `${project.name} paused`, invalidateKeys });
  const resume = useAction(api.resumeProject, { successMessage: `${project.name} resumed`, invalidateKeys });
  const archive = useAction(api.archiveProject, { successMessage: `${project.name} archived`, invalidateKeys });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-lg font-semibold text-slate-100">{project.name}</h1>
          <div className="text-xs text-slate-500">{project.slug}</div>
        </div>
        <div className="flex items-center gap-3">
          <span className={STATUS_COLOR[project.status]}>{project.status}</span>
          {project.status === "active" && (
            <ActionButton label="Pause" onClick={() => pause.mutate(project.id)} pending={pause.isPending} />
          )}
          {project.status === "paused" && (
            <ActionButton label="Resume" onClick={() => resume.mutate(project.id)} pending={resume.isPending} />
          )}
          {project.status !== "archived" && (
            <ActionButton label="Archive" tone="danger" onClick={() => setConfirmArchive(true)} />
          )}
        </div>
      </div>

      <Panel title="Tasks">
        {project.tasks.length === 0 ? (
          <div className="text-sm text-slate-500">No tasks yet.</div>
        ) : (
          <div className="flex flex-col gap-3">
            {project.tasks.map((node) => (
              <TaskTree key={node.task.id} node={node} idleAgents={idleAgents} />
            ))}
          </div>
        )}
      </Panel>

      <Panel title="Decisions">
        {project.decisions.length === 0 ? (
          <div className="text-sm text-slate-500">No decisions raised.</div>
        ) : (
          <ul className="flex flex-col gap-1">
            {project.decisions.map((d) => (
              <li key={d.id} className="text-sm text-slate-300">
                {d.question}{" "}
                <span className={`text-xs ${d.status === "open" ? "text-amber-400" : "text-slate-500"}`}>
                  ({d.status})
                </span>
              </li>
            ))}
          </ul>
        )}
      </Panel>

      <ProjectSettings project={project} />

      <ConfirmDialog
        open={confirmArchive}
        title={`Archive ${project.name}?`}
        description="Archived projects are removed from active execution entirely -- no further task assignment or work continues, even for tasks already in flight."
        confirmLabel="Archive"
        danger
        pending={archive.isPending}
        onConfirm={() => archive.mutate(project.id, { onSuccess: () => setConfirmArchive(false) })}
        onCancel={() => setConfirmArchive(false)}
      />
    </div>
  );
}

function ProjectSettings({ project }: { project: ProjectDetailData }) {
  const editable = project.status === "paused";

  const [repositoryPath, setRepositoryPath] = useState(project.repository_path);
  const [defaultBranch, setDefaultBranch] = useState(project.default_branch);

  const invalidateKeys = [["projects"], ["project", project.id]];
  const updatePath = useAction(
    ({ projectId, repositoryPath }: { projectId: string; repositoryPath: string }) =>
      api.updateProjectRepositoryPath(projectId, repositoryPath),
    { successMessage: "Repository path updated", invalidateKeys },
  );
  const updateBranch = useAction(
    ({ projectId, defaultBranch }: { projectId: string; defaultBranch: string }) =>
      api.updateProjectDefaultBranch(projectId, defaultBranch),
    { successMessage: "Default branch updated", invalidateKeys },
  );

  const { data: agentTemplates } = useQuery({ queryKey: ["agentTemplates"], queryFn: api.getAgentTemplates });

  return (
    <Panel title="Settings">
      <div className="flex flex-col gap-4">
        {!editable && <div className="text-xs text-slate-500">Pause the project to edit.</div>}

        <div className="flex flex-col gap-1">
          <label className="text-xs uppercase tracking-wide text-slate-500">Repository Path</label>
          {editable ? (
            <div className="flex items-center gap-2">
              <input
                value={repositoryPath}
                onChange={(e) => setRepositoryPath(e.target.value)}
                className="flex-1 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-sm text-slate-200"
              />
              <ActionButton
                label="Save"
                onClick={() => updatePath.mutate({ projectId: project.id, repositoryPath })}
                pending={updatePath.isPending}
              />
            </div>
          ) : (
            <div className="text-sm text-slate-300">{project.repository_path}</div>
          )}
        </div>

        <div className="flex flex-col gap-1">
          <label className="text-xs uppercase tracking-wide text-slate-500">Default Branch</label>
          {editable ? (
            <div className="flex items-center gap-2">
              <input
                value={defaultBranch}
                onChange={(e) => setDefaultBranch(e.target.value)}
                className="flex-1 rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-sm text-slate-200"
              />
              <ActionButton
                label="Save"
                onClick={() => updateBranch.mutate({ projectId: project.id, defaultBranch })}
                pending={updateBranch.isPending}
              />
            </div>
          ) : (
            <div className="text-sm text-slate-300">{project.default_branch}</div>
          )}
        </div>

        <div>
          <div className="text-xs uppercase tracking-wide text-slate-500">Runtime</div>
          {!agentTemplates || agentTemplates.length === 0 ? (
            <div className="mt-1 text-sm text-slate-500">No agent templates configured.</div>
          ) : (
            <ul className="mt-2 flex flex-col gap-1.5">
              {agentTemplates.map((t) => (
                <li key={t.id} className="flex items-center justify-between text-sm">
                  <span className="text-slate-300">{t.label}</span>
                  <span className="text-xs text-slate-500">
                    {t.runtime_profile ? `${t.runtime_profile.runtime_id} / ${t.runtime_profile.model}` : "unresolved"}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Panel>
  );
}

/// A task plus its descendant tree -- containers (tasks with children) just
/// render a header and recurse; leaves render the full `TaskRow` actions,
/// since only leaves are ever delegated to an agent.
function TaskTree({ node, idleAgents }: { node: TaskNode; idleAgents: Agent[] }) {
  if (node.children.length === 0) {
    return <TaskRow task={node.task} idleAgents={idleAgents} />;
  }

  return (
    <div className="rounded-md border border-slate-800 p-3">
      <div className="flex items-center justify-between">
        <div className="font-medium text-slate-100">{node.task.title}</div>
        <span className="text-xs uppercase text-slate-500">{node.task.status}</span>
      </div>
      {node.task.description && <p className="mt-1 text-sm text-slate-400">{node.task.description}</p>}
      <div className="mt-3 flex flex-col gap-2 pl-3">
        {node.children.map((child) => (
          <TaskTree key={child.task.id} node={child} idleAgents={idleAgents} />
        ))}
      </div>
    </div>
  );
}

function TaskRow({ task, idleAgents }: { task: Task; idleAgents: Agent[] }) {
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [reassignOpen, setReassignOpen] = useState(false);
  const [viewDetail, setViewDetail] = useState(false);

  const invalidateKeys = [["project", task.project_id], ["tasks", task.project_id]];
  const retry = useAction(api.retryTask, { successMessage: "Task retried", invalidateKeys });
  const cancel = useAction(api.cancelTask, { successMessage: "Task cancelled", invalidateKeys });
  const reassign = useAction(
    ({ taskId, newAgentId }: { taskId: string; newAgentId: string }) => api.reassignTask(taskId, newAgentId),
    { successMessage: "Task reassigned", invalidateKeys: [...invalidateKeys, ["agents", task.project_id]] },
  );
  const approve = useAction(api.approveReview, { successMessage: "Review approved", invalidateKeys });
  const requestChanges = useAction(
    ({ taskId, reason }: { taskId: string; reason: string }) => api.requestReviewChanges(taskId, reason),
    { successMessage: "Changes requested", invalidateKeys },
  );

  const canCancel = task.status !== "done" && task.status !== "cancelled";
  const canReassign = (task.status === "assigned" || task.status === "blocked") && idleAgents.length > 0;

  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-slate-800 px-3 py-2">
      <div className="flex items-center justify-between text-sm">
        <span className="text-slate-300">{task.title}</span>
        <span className="text-xs text-slate-500">{task.status}</span>
      </div>
      {task.status === "blocked" && task.blocked_reason && (
        <div className="text-xs text-rose-400">{task.blocked_reason}</div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        <ActionButton label="View Diff" onClick={() => setViewDetail(true)} />
        {task.status === "blocked" && (
          <ActionButton label="Retry" onClick={() => retry.mutate(task.id)} pending={retry.isPending} />
        )}
        {canCancel && <ActionButton label="Cancel" tone="danger" onClick={() => setConfirmCancel(true)} />}
        {canReassign && (
          <ActionButton label="Reassign" onClick={() => setReassignOpen((open) => !open)} />
        )}
        {task.status === "blocked" && (
          <>
            <ActionButton label="Approve" onClick={() => approve.mutate(task.id)} pending={approve.isPending} />
            <RespondField
              label="Request Changes"
              onSubmit={(reason) => requestChanges.mutate({ taskId: task.id, reason })}
              pending={requestChanges.isPending}
            />
          </>
        )}
      </div>

      {reassignOpen && (
        <AgentPicker
          agents={idleAgents}
          pending={reassign.isPending}
          onSelect={(agentId) => {
            reassign.mutate({ taskId: task.id, newAgentId: agentId }, { onSuccess: () => setReassignOpen(false) });
          }}
        />
      )}

      {viewDetail && <TaskDetailModal taskId={task.id} onClose={() => setViewDetail(false)} />}

      <ConfirmDialog
        open={confirmCancel}
        title={`Cancel "${task.title}"?`}
        description="Stops further execution. Work already mid-attempt will not be resurrected to Done when it completes."
        confirmLabel="Cancel Task"
        danger
        pending={cancel.isPending}
        onConfirm={() => cancel.mutate(task.id, { onSuccess: () => setConfirmCancel(false) })}
        onCancel={() => setConfirmCancel(false)}
      />
    </div>
  );
}

function AgentPicker({
  agents,
  onSelect,
  pending,
}: {
  agents: Agent[];
  onSelect: (agentId: string) => void;
  pending?: boolean;
}) {
  const [agentId, setAgentId] = useState(agents[0]?.id ?? "");

  return (
    <div className="flex items-center gap-2">
      <select
        value={agentId}
        onChange={(e) => setAgentId(e.target.value)}
        className="rounded-md border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
      >
        {agents.map((a) => (
          <option key={a.id} value={a.id}>
            {a.name}
          </option>
        ))}
      </select>
      <ActionButton label="Confirm Reassign" onClick={() => agentId && onSelect(agentId)} pending={pending} />
    </div>
  );
}
