import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Agent, ProjectDetail as ProjectDetailData, Task } from "../../lib/types";
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
  const idleAgents = (agents ?? []).filter((a) => a.status === "idle");

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

      <Panel title="Initiatives">
        {project.initiatives.length === 0 ? (
          <div className="text-sm text-slate-500">No initiatives.</div>
        ) : (
          <div className="flex flex-col gap-4">
            {project.initiatives.map((initiative) => (
              <div key={initiative.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <div className="font-medium text-slate-100">{initiative.title}</div>
                  <span className="text-xs uppercase text-slate-500">{initiative.status}</span>
                </div>
                <p className="mt-1 text-sm text-slate-400">{initiative.description}</p>

                {initiative.epics.length > 0 && (
                  <div className="mt-3">
                    <div className="text-xs uppercase tracking-wide text-slate-500">Epics</div>
                    <div className="mt-1 flex flex-col gap-2">
                      {initiative.epics.map((epic) => (
                        <div key={epic.id}>
                          <div className="text-sm text-slate-300">{epic.title}</div>
                          {epic.tasks.length > 0 && (
                            <ul className="mt-1 flex flex-col gap-1.5 pl-3">
                              {epic.tasks.map((task) => (
                                <TaskRow key={task.id} task={task} idleAgents={idleAgents} />
                              ))}
                            </ul>
                          )}
                        </div>
                      ))}
                    </div>
                  </div>
                )}

                {initiative.decisions.length > 0 && (
                  <div className="mt-3">
                    <div className="text-xs uppercase tracking-wide text-slate-500">Decisions</div>
                    <ul className="mt-1 flex flex-col gap-1">
                      {initiative.decisions.map((d) => (
                        <li key={d.id} className="text-sm text-slate-300">
                          {d.title}{" "}
                          <span
                            className={`text-xs ${d.status === "open" ? "text-amber-400" : "text-slate-500"}`}
                          >
                            ({d.status})
                          </span>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </Panel>

      <Panel title="Goals">
        {project.goals.length === 0 ? (
          <div className="text-sm text-slate-500">No goals.</div>
        ) : (
          <div className="flex flex-col gap-4">
            {project.goals.map((goal) => (
              <div key={goal.id} className="rounded-md border border-slate-800 p-3">
                <div className="flex items-center justify-between">
                  <div className="font-medium text-slate-100">{goal.title}</div>
                  <span className="text-xs uppercase text-slate-500">{goal.status}</span>
                </div>
                <ul className="mt-2 flex flex-col gap-1.5">
                  {goal.tasks.map((task) => (
                    <TaskRow key={task.id} task={task} idleAgents={idleAgents} />
                  ))}
                </ul>
              </div>
            ))}
          </div>
        )}
      </Panel>

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
    <li className="flex flex-col gap-1.5 rounded-md border border-slate-800 px-3 py-2">
      <div className="flex items-center justify-between text-sm">
        <span className="text-slate-300">{task.title}</span>
        <span className="text-xs text-slate-500">{task.status}</span>
      </div>
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
    </li>
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
