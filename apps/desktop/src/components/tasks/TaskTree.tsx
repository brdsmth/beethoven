import { useState } from "react";
import { api } from "../../lib/api";
import { useAction } from "../../lib/hooks";
import type { Agent, Task, TaskNode } from "../../lib/types";
import { ActionButton } from "../ActionButton";
import { ConfirmDialog } from "../ConfirmDialog";
import { RespondField } from "../RespondField";
import { TaskGraphModal } from "./TaskGraphModal";
import { displayStatus } from "./taskDisplay";

/// A task plus its descendant tree -- containers (tasks with children) just
/// render a header and recurse; leaves render the full `TaskRow` actions,
/// since only leaves are ever delegated to an agent. The one task-tree
/// renderer in the app: ProjectDetail's Task Graph tab and the top-level
/// Tasks page both use this, so a task never has more or fewer actions
/// available depending on which surface you happened to view it from.
export function TaskTree({ node, idleAgents }: { node: TaskNode; idleAgents: Agent[] }) {
  if (node.children.length === 0) {
    return <TaskRow task={node.task} idleAgents={idleAgents} />;
  }

  const { label, dot, border, color } = displayStatus(node.task);

  return (
    <div className="rounded-md border border-slate-800 p-3">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 overflow-hidden">
          <span className={`h-1.5 w-1.5 flex-none rounded-full ${dot}`} />
          <div className="truncate font-medium text-slate-100">{node.task.title}</div>
        </div>
        <span className={`flex-none rounded border px-1.5 py-0.5 text-[10px] uppercase ${border} ${color}`}>{label}</span>
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
  const [graphOpen, setGraphOpen] = useState(false);

  const invalidateKeys = [["project", task.project_id], ["tasks", task.project_id]];
  const plan = useAction(api.planTask, {
    successMessage: (_taskId, outcome) =>
      `Planned ${outcome.tasks.length} task(s), ${outcome.decisions.length} decision(s)`,
    invalidateKeys,
  });
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
  const blockers = task.dependencies.filter((d) => d.status !== "done");
  const { label, color, dot, border } = displayStatus(task);

  const metaLine =
    blockers.length > 0
      ? `Blocked by: ${blockers.map((b) => b.title).join(", ")}`
      : task.blocked_reason ??
        (task.assigned_agent_name ? `${task.assigned_agent_name} · attempt ${task.attempt}` : null);

  return (
    <div className="flex flex-col gap-2 rounded-md border border-slate-800 px-3 py-2 text-sm">
      <div className="flex items-center gap-2.5">
        <span className={`h-1.5 w-1.5 flex-none rounded-full ${dot}`} />
        <div className="flex flex-1 flex-col overflow-hidden">
          <span className="truncate text-slate-200">{task.title}</span>
          {metaLine && (
            <span className="truncate text-xs text-slate-500" title={metaLine}>
              {metaLine}
            </span>
          )}
        </div>
        <span className={`flex-none rounded border px-1.5 py-0.5 text-[10px] uppercase ${border} ${color}`}>{label}</span>
        <div className="flex flex-none flex-wrap items-center gap-2">
          {task.status === "ready" && (
            <ActionButton
              label="Plan"
              title="Break this down into tasks (and, for a model-driven planner, success criteria)."
              onClick={() => plan.mutate(task.id)}
              pending={plan.isPending}
            />
          )}
          <ActionButton label="Graph" onClick={() => setGraphOpen(true)} />
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

      {graphOpen && <TaskGraphModal taskId={task.id} taskTitle={task.title} onClose={() => setGraphOpen(false)} />}

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
